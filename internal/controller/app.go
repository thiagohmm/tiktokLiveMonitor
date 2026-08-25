// Package controller contains request handlers that orchestrate services and models.
package controller

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/alerts"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"github.com/thiagohmm/tiktok-live-monitor/internal/monitor"
	"github.com/thiagohmm/tiktok-live-monitor/internal/ranking"
	"github.com/thiagohmm/tiktok-live-monitor/internal/report"
	"github.com/thiagohmm/tiktok-live-monitor/internal/suggestions"
)

// MessageCache is an in-memory write-behind buffer for user messages.
type MessageCache interface {
	Add(liveName, uniqueID, username, message string)
	Snapshot() []model.UserMessage
}

// AppController orchestrates all application services.
type AppController struct {
	monitor       *monitor.Monitor
	repo          model.Repository
	msgCache      MessageCache
	reportGen     *report.Generator
	suggEngine    *suggestions.Engine
	alertNotifier *alerts.Notifier
	ranker        *ranking.Ranker
	monCtx        context.Context
	monCancel     context.CancelFunc
	monCancelMu   sync.Mutex
	flagSeen      map[string]struct{}
	flagSeenMu    sync.Mutex
}

// NewAppController creates a new application controller.
func NewAppController(
	mon *monitor.Monitor,
	repo model.Repository,
) *AppController {
	mon.SetRepo(repo)
	c := &AppController{
		monitor:       mon,
		repo:          repo,
		reportGen:     report.New(repo),
		suggEngine:    suggestions.New(repo),
		alertNotifier: alerts.New(alerts.FromEnvironment()),
		ranker:        ranking.New(ranking.DefaultWeights),
		flagSeen:      make(map[string]struct{}),
	}
	c.registerFeatureHandlers()
	return c
}

// registerFeatureHandlers wires the alerts, suggestions and ranking features
// to the monitor event stream.
func (c *AppController) registerFeatureHandlers() {
	mon := c.monitor
	if mon == nil {
		return
	}
	// Handlers run synchronously in the bridge reader goroutine (see
	// Monitor.emit): a blocking handler stalls the WHOLE event pipeline, so
	// every handler here must be async.
	mon.OnEvent(func(eventType string, data monitor.EventData) {
		switch eventType {
		case monitor.EventFlaggedMessage:
			go c.handleAnomalyEvent(data) // Send() does blocking HTTP calls
		case monitor.EventConnectionStatus:
			go c.handleConnectionEvent(data)
		case monitor.EventAnyGift:
			go c.handleHighValueGiftEvent(data)
		}
	})
	// Suggestions run on every chat message in a goroutine so the event
	// pipeline (chat, flagged-message and correlation events) is not blocked.
	mon.OnEvent(func(eventType string, data monitor.EventData) {
		if eventType != monitor.EventChatMessage {
			return
		}
		go c.handleSuggestionEvent(data)
	})
}

func (c *AppController) handleAnomalyEvent(data monitor.EventData) {
	if !c.alertNotifier.Enabled() {
		return
	}
	nickname := eventString(data, "nickname")
	uniqueID := eventString(data, "uniqueId", "userId")
	comment := eventString(data, "comment")
	reason := eventString(data, "reason")
	category := eventString(data, "category")
	state := c.monitor.GetState()
	c.alertNotifier.Send(c.monCtx, model.AlertEvent{
		Type:      "anomaly",
		Title:     "Comportamento detectado: " + category,
		Message:   fmt.Sprintf("%s (%s): %s — %s", nickname, uniqueID, comment, reason),
		Severity:  model.AlertSeverityWarning,
		UniqueID:  uniqueID,
		Nickname:  nickname,
		LiveName:  state.Username,
		Timestamp: time.Now().Format(time.RFC3339),
	})
}

func (c *AppController) handleConnectionEvent(data monitor.EventData) {
	if !c.alertNotifier.Enabled() {
		return
	}
	connected, _ := data["connected"].(bool)
	status := eventString(data, "status")
	if connected {
		return
	}
	state := c.monitor.GetState()
	c.alertNotifier.Send(c.monCtx, model.AlertEvent{
		Type:      "disconnected",
		Title:     "Live desconectada",
		Message:   fmt.Sprintf("A transmissão foi desconectada. Status: %s", status),
		Severity:  model.AlertSeverityError,
		LiveName:  state.Username,
		Timestamp: time.Now().Format(time.RFC3339),
	})
}

func (c *AppController) handleHighValueGiftEvent(data monitor.EventData) {
	if !c.alertNotifier.Enabled() {
		return
	}
	value := eventInt(data, "value", 0)
	if value <= 0 {
		return
	}
	nickname := eventString(data, "nickname")
	uniqueID := eventString(data, "uniqueId", "userId")
	giftName := resolveGiftName(data)
	state := c.monitor.GetState()
	c.alertNotifier.Send(c.monCtx, model.AlertEvent{
		Type:  "high-value-gift",
		Title: fmt.Sprintf("Presente de alto valor (%d)", value),
		Message: fmt.Sprintf("%s (%s) enviou %s",
			coalesceStr(nickname, uniqueID, "desconhecido"), uniqueID, giftName),
		Severity:  model.AlertSeverityInfo,
		UniqueID:  uniqueID,
		Nickname:  nickname,
		LiveName:  state.Username,
		Timestamp: time.Now().Format(time.RFC3339),
	})
}

func (c *AppController) handleSuggestionEvent(data monitor.EventData) {
	if c.suggEngine == nil {
		return
	}
	nickname := eventString(data, "nickname")
	uniqueID := eventString(data, "uniqueId", "userId")
	comment := eventString(data, "comment")
	state := c.monitor.GetState()
	if state.Username == "" {
		return
	}
	cand, ok := c.suggEngine.Suggest(c.monCtx, state.Username, uniqueID, nickname, comment)
	if !ok {
		return
	}
	// Emit a suggestion event without auto-publishing.
	c.monitor.Emit(suggestions.EventSuggested, monitor.EventData{
		"uniqueId":  cand.UniqueID,
		"nickname":  cand.Nickname,
		"question":  cand.Message,
		"response":  cand.Suggested,
		"reason":    cand.Reason,
		"timestamp": cand.Timestamp,
	})
}

// handleModerationEvent was removed: message moderation (rules + RAG + LLM)
// now lives entirely in the Python agent, which reports flags back through
// POST /api/moderation/flag → ReportExternalFlag.

func coalesceStr(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// --- Monitor Actions ---

// StartMonitoring starts monitoring the given username.
func (c *AppController) StartMonitoring(ctx context.Context, username string) error {
	c.monCancelMu.Lock()
	monCtx, cancel := context.WithCancel(context.Background())
	c.monCtx = monCtx
	c.monCancel = cancel
	c.monCancelMu.Unlock()

	return c.monitor.StartMonitoring(monCtx, username)
}

// StopMonitoring stops the current monitoring session.
func (c *AppController) StopMonitoring() {
	c.monCancelMu.Lock()
	defer c.monCancelMu.Unlock()
	if c.monCancel != nil {
		c.monCancel()
	}
	c.monitor.StopMonitoring()
}

// GetState returns the current monitor state.
func (c *AppController) GetState() monitor.State {
	return c.monitor.GetState()
}

// GetSettings returns the current monitor settings.
func (c *AppController) GetSettings() monitor.Settings {
	return c.monitor.GetSettings()
}

// SetSettings updates the monitor settings.
func (c *AppController) SetSettings(settings monitor.Settings) {
	c.monitor.SetSettings(settings)
}

// FetchAvailableGifts fetches the available gifts from TikTok.
func (c *AppController) FetchAvailableGifts() ([]string, error) {
	gifts, err := c.monitor.FetchAvailableGifts()
	if err != nil {
		return nil, err
	}
	if gifts == nil {
		gifts = []string{}
	}
	return gifts, nil
}

// --- Moderation Actions ---

// ReportExternalFlag ingests a moderation flag from the Python agent and
// surfaces it through the existing flagged-message pipeline (alerts + UI),
// logging the anomaly. This is plumbing only — no AI logic lives here.
func (c *AppController) ReportExternalFlag(data monitor.EventData) {
	settings := c.monitor.GetSettings()
	if !settings.ModerationEnabled {
		return
	}
	comment := eventString(data, "comment")
	category := eventString(data, "category")
	if comment == "" || category == "" {
		return
	}
	uniqueID := eventString(data, "uniqueId", "userId")
	nickname := eventString(data, "nickname")
	reason := eventString(data, "reason")
	if reason == "" {
		reason = category
	}

	key := strings.ToLower(uniqueID) + "|" + foldComment(comment)
	c.flagSeenMu.Lock()
	if _, ok := c.flagSeen[key]; ok {
		c.flagSeenMu.Unlock()
		return
	}
	c.flagSeen[key] = struct{}{}
	if len(c.flagSeen) > 500 {
		for k := range c.flagSeen {
			delete(c.flagSeen, k)
			break
		}
	}
	c.flagSeenMu.Unlock()

	state := c.monitor.GetState()
	c.monitor.Emit(monitor.EventFlaggedMessage, monitor.EventData{
		"uniqueId":  uniqueID,
		"nickname":  nickname,
		"comment":   comment,
		"reason":    reason,
		"category":  category,
		"timestamp": eventString(data, "timestamp"),
	})
	if err := c.repo.LogAnomaly(state.Username, comment, true, category, uniqueID); err != nil {
		log.Printf("[Controller] Error logging external flag: %v", err)
	}
}

func foldComment(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "ç", "c")
	var b strings.Builder
	for _, r := range s {
		if r >= 0x0300 && r <= 0x036F {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// --- Repository Actions ---

// GetRecentModerations returns recent moderation history.
func (c *AppController) GetRecentModerations(limit int) ([]model.AnomalyLog, error) {
	return c.repo.GetRecentModerations(limit)
}

// DeleteModeration deletes a moderation record by ID.
func (c *AppController) DeleteModeration(id int64) (int64, error) {
	return c.repo.DeleteModeration(id)
}

// ClearHistory clears all moderation history.
func (c *AppController) ClearHistory() (int64, error) {
	return c.repo.ClearHistory()
}

// GetLives returns derived lives (grouped by live and day) for the admin tab.
func (c *AppController) GetLives(limit int) ([]model.Live, error) {
	return c.repo.ListLives(limit)
}

// DeleteLive removes all stored data for a live.
func (c *AppController) DeleteLive(liveName string) (int64, error) {
	return c.repo.DeleteLive(liveName)
}

// GetRecentGifts returns recent gifts for the current live.
func (c *AppController) GetRecentGifts(liveName string, limit int) ([]model.Gift, error) {
	return c.repo.GetRecentGifts(liveName, limit)
}

// GetGiftsByUser returns gifts for a specific user.
func (c *AppController) GetGiftsByUser(userID string) ([]model.Gift, error) {
	return c.repo.GetGiftsByUser(userID)
}

// ClearGifts clears all gift records.
func (c *AppController) ClearGifts() (int64, error) {
	return c.repo.ClearGifts()
}

// RecordTargetGiftReceived stores a pending target gift history entry and returns its id.
func (c *AppController) RecordTargetGiftReceived(data monitor.EventData) (int64, error) {
	state := c.monitor.GetState()
	liveName := state.Username
	uniqueID := eventString(data, "uniqueId", "userId")
	nickname := eventString(data, "nickname")
	giftName := resolveGiftName(data)
	if uniqueID == "" || giftName == "" {
		return 0, fmt.Errorf("uniqueId and giftName are required")
	}
	if nickname == "" {
		nickname = uniqueID
	}

	receivedAt := time.Now()
	if ts, ok := toInt64(data["timestamp"]); ok && ts > 0 {
		// TikTok payloads use milliseconds.
		if ts > 1_000_000_000_000 {
			receivedAt = time.UnixMilli(ts)
		} else {
			receivedAt = time.Unix(ts, 0)
		}
	}

	return c.repo.AddTargetGiftHistory(liveName, uniqueID, nickname, giftName, receivedAt)
}

// AnswerTargetGift marks a target gift history entry as answered.
func (c *AppController) AnswerTargetGift(id int64, responseType string) error {
	return c.repo.MarkTargetGiftAnswered(id, responseType, time.Now())
}

// GetRecentTargetGiftHistory returns recent target gift history for the current live.
func (c *AppController) GetRecentTargetGiftHistory(limit int) ([]model.TargetGiftHistory, error) {
	state := c.monitor.GetState()
	return c.repo.GetRecentTargetGiftHistory(state.Username, limit)
}

// GetPendingTargetGiftHistory returns unanswered target gifts for the current live.
func (c *AppController) GetPendingTargetGiftHistory(limit int) ([]model.TargetGiftHistory, error) {
	state := c.monitor.GetState()
	if strings.TrimSpace(state.Username) == "" {
		return []model.TargetGiftHistory{}, nil
	}
	return c.repo.GetPendingTargetGiftHistory(state.Username, limit)
}

// RecordPinnedComment stores a pinned comment from a live event.
func (c *AppController) RecordPinnedComment(data monitor.EventData) (int64, error) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("[Controller] panic storing pinned comment: %v", rec)
		}
	}()

	state := c.monitor.GetState()
	uniqueID := eventString(data, "uniqueId", "userId")
	nickname := eventString(data, "nickname")
	comment := eventString(data, "comment")
	if comment == "" {
		comment = "[sem texto identificado]"
	}
	if nickname == "" {
		nickname = uniqueID
	}
	pinID := eventString(data, "pinId")
	at := time.Now()
	if ts, ok := toInt64(data["timestamp"]); ok && ts > 0 {
		if ts > 1_000_000_000_000 {
			at = time.UnixMilli(ts)
		} else {
			at = time.Unix(ts, 0)
		}
	}
	return c.repo.AddPinnedComment(state.Username, uniqueID, nickname, comment, pinID, eventBoolPtr(data, "isFollower"), at)
}

// GetRecentPinnedComments returns recent pinned comments for the current live.
func (c *AppController) GetRecentPinnedComments(limit int) ([]model.PinnedComment, error) {
	state := c.monitor.GetState()
	return c.repo.GetRecentPinnedComments(state.Username, limit)
}

// --- Ranking, Report & Profile Actions ---

// GetLiveRanking returns the engagement ranking for the given live.
func (c *AppController) GetLiveRanking(liveName string) (model.LiveRanking, error) {
	out := model.LiveRanking{LiveName: liveName, UpdatedAt: time.Now().Format(time.RFC3339)}
	if strings.TrimSpace(liveName) == "" {
		return out, nil
	}
	stats, err := c.repo.LiveStatsByUser(liveName)
	if err != nil {
		return out, err
	}
	if stats == nil {
		stats = []model.LiveStat{}
	}
	anomaliesByUser := map[string]int{}
	if logs, err := c.repo.GetAnomalyLogsByLiveName(liveName); err == nil {
		for _, l := range logs {
			if l.IsAnomaly {
				anomaliesByUser[l.UniqueID]++
			}
		}
	}
	scored := c.ranker.Compute(stats, anomaliesByUser)
	_ = scored
	out = c.ranker.BuildLiveRanking(liveName, stats, anomaliesByUser)
	return out, nil
}

// GenerateReport produces an AI-assisted post-live report.
func (c *AppController) GenerateReport(ctx context.Context, liveName string) (model.LiveReport, error) {
	if c.reportGen == nil {
		return model.LiveReport{}, fmt.Errorf("report generator unavailable")
	}
	return c.reportGen.Generate(ctx, liveName)
}

// GetUserProfile returns the historical profile for a participant.
func (c *AppController) GetUserProfile(uniqueID string) (model.UserProfile, error) {
	out := model.UserProfile{UniqueID: uniqueID}
	if strings.TrimSpace(uniqueID) == "" {
		return out, nil
	}
	out.Messages, _ = c.repo.GetUserMessages(uniqueID)
	out.Gifts, _ = c.repo.GetGiftsByUser(uniqueID)
	out.LastLives, _ = c.repo.RecentLivesForUser(uniqueID, 10)

	// Aggregate totals.
	out.TotalMessages = len(out.Messages)
	out.TotalGifts = len(out.Gifts)

	// Derive a risk level from recent anomaly logs.
	var risk string
	if logs, err := c.repo.GetAnomalyLogsByLiveName(""); err == nil {
		risk = riskForUser(logs, uniqueID)
	}
	if risk == "" {
		risk = model.RiskLevelNone
	}
	out.RiskLevel = risk
	return out, nil
}

// GetAlertConfig returns the current alert configuration (secrets redacted).
func (c *AppController) GetAlertConfig() alerts.Config {
	return c.alertNotifier.GetConfig()
}

// SetAlertConfig updates the alert configuration.
func (c *AppController) SetAlertConfig(cfg alerts.Config) {
	if c.alertNotifier != nil {
		c.alertNotifier.SetConfig(cfg)
	}
}

// AlertEnabled reports whether external alerts are configured.
func (c *AppController) AlertEnabled() bool {
	return c.alertNotifier != nil && c.alertNotifier.Enabled()
}

// riskForUser classifies a user's risk based on their anomaly log count.
func riskForUser(logs []model.AnomalyLog, uniqueID string) string {
	count := 0
	for _, l := range logs {
		if l.IsAnomaly && l.UniqueID == uniqueID {
			count++
		}
	}
	switch {
	case count >= 4:
		return model.RiskLevelCritical
	case count >= 2:
		return model.RiskLevelMedium
	case count >= 1:
		return model.RiskLevelLow
	default:
		return model.RiskLevelNone
	}
}

// --- Event Handlers ---

// HandleGiftEvent processes a gift event and stores it.
func (c *AppController) HandleGiftEvent(data monitor.EventData) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("[Controller] panic storing gift: %v", rec)
		}
	}()

	uniqueID := eventString(data, "uniqueId", "userId")
	if uniqueID == "" {
		uniqueID = "unknown"
	}
	nickname := eventString(data, "nickname")
	if nickname == "" {
		nickname = uniqueID
	}
	giftName := resolveGiftName(data)
	if isGiftStreakInProgress(data) {
		return
	}
	repeatCount := eventInt(data, "repeatCount", 1)
	if repeatCount < 1 {
		repeatCount = 1
	}
	giftType := eventInt(data, "giftType", 0)
	state := c.monitor.GetState()
	if _, err := c.repo.AddGift(state.Username, uniqueID, nickname, giftName, repeatCount, giftType); err != nil {
		log.Printf("[Controller] Error storing gift: %v", err)
	}
}

// HandleChatMessageEvent processes a chat message event and stores it.
func (c *AppController) HandleChatMessageEvent(data monitor.EventData) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("[Controller] panic storing chat: %v", rec)
		}
	}()

	uniqueID := eventString(data, "uniqueId", "userId")
	if uniqueID == "" {
		uniqueID = "unknown"
	}
	nickname := eventString(data, "nickname")
	if nickname == "" {
		nickname = uniqueID
	}
	comment := eventString(data, "comment")
	if comment == "" {
		return
	}
	liveName := c.monitor.GetState().Username
	if c.msgCache != nil {
		c.msgCache.Add(liveName, uniqueID, nickname, comment)
		return
	}
	if err := c.repo.AddUserMessageDedup(liveName, uniqueID, nickname, comment); err != nil {
		log.Printf("[Controller] Error storing user message: %v", err)
	}
}

// HandleShareEvent processes a live share event and stores it.
func (c *AppController) HandleShareEvent(data monitor.EventData) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("[Controller] panic storing share: %v", rec)
		}
	}()

	uniqueID := eventString(data, "uniqueId", "userId")
	if uniqueID == "" {
		uniqueID = "unknown"
	}
	nickname := eventString(data, "nickname")
	if nickname == "" {
		nickname = uniqueID
	}
	liveName := c.monitor.GetState().Username
	if err := c.repo.AddShare(liveName, uniqueID, nickname); err != nil {
		log.Printf("[Controller] Error storing share: %v", err)
	}
}

// GetMonitor returns the underlying monitor for event registration.
func (c *AppController) GetMonitor() *monitor.Monitor {
	return c.monitor
}

// SetMessageCache enables write-behind caching for chat messages.
func (c *AppController) SetMessageCache(mc MessageCache) {
	c.msgCache = mc
}

// Stop shuts down the bridge child process.
func (c *AppController) Stop() {
	if c.monitor != nil {
		c.monitor.Close()
	}
}

func toInt64(v interface{}) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case float64:
		return int64(n), true
	case float32:
		return int64(n), true
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

func isGiftStreakInProgress(data monitor.EventData) bool {
	ended := eventBoolPtr(data, "repeatEnd")
	return ended != nil && !*ended
}

func eventInt(data monitor.EventData, key string, fallback int) int {
	if n, ok := toInt64(data[key]); ok {
		return int(n)
	}
	return fallback
}

func eventString(data monitor.EventData, keys ...string) string {
	for _, key := range keys {
		if s := stringify(data[key]); s != "" {
			return s
		}
	}
	return ""
}

func stringify(v interface{}) string {
	switch n := v.(type) {
	case string:
		return strings.TrimSpace(n)
	case float64:
		return strconv.FormatInt(int64(n), 10)
	case float32:
		return strconv.FormatInt(int64(n), 10)
	case int:
		return strconv.Itoa(n)
	case int64:
		return strconv.FormatInt(n, 10)
	default:
		return ""
	}
}

func eventBoolPtr(data monitor.EventData, key string) *bool {
	v, ok := data[key]
	if !ok || v == nil {
		return nil
	}
	switch n := v.(type) {
	case bool:
		b := n
		return &b
	case float64:
		b := n != 0
		return &b
	case int:
		b := n != 0
		return &b
	case int64:
		b := n != 0
		return &b
	case string:
		s := strings.ToLower(strings.TrimSpace(n))
		if s == "true" || s == "1" {
			b := true
			return &b
		}
		if s == "false" || s == "0" {
			b := false
			return &b
		}
	}
	return nil
}

func nestedString(v interface{}, keys ...string) string {
	m, ok := v.(map[string]interface{})
	if !ok {
		return stringify(v)
	}
	return eventString(monitor.EventData(m), keys...)
}

func resolveGiftName(data monitor.EventData) string {
	if name := eventString(data, "giftName", "name", "describe"); name != "" {
		return translateGiftName(name)
	}
	for _, nest := range []string{"giftDetails", "extendedGiftInfo", "gift"} {
		if name := nestedString(data[nest], "giftName", "name", "describe"); name != "" {
			return translateGiftName(name)
		}
	}
	if id := eventString(data, "giftId"); id != "" {
		return "Presente " + id
	}
	if id := nestedString(data["gift"], "giftId", "gift_id"); id != "" {
		return "Presente " + id
	}
	return "Presente"
}
