// Package controller contains request handlers that orchestrate services and models.
package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"github.com/thiagohmm/tiktok-live-monitor/internal/monitor"
	"github.com/thiagohmm/tiktok-live-monitor/internal/ranking"
	"github.com/thiagohmm/tiktok-live-monitor/internal/report"
)

// settingsKey is the settings table key under which the app settings are stored.
const settingsKey = "app"

// MessageCache is an in-memory write-behind buffer for user messages.
type MessageCache interface {
	Add(liveName, uniqueID, username, message string)
	Snapshot() []model.UserMessage
}

// AppController orchestrates all application services.
type AppController struct {
	monitor     *monitor.Monitor
	repo        model.Repository
	msgCache    MessageCache
	reportGen   *report.Generator
	ranker      *ranking.Ranker
	monCancel   context.CancelFunc
	monCancelMu sync.Mutex
	flagSeen    map[string]struct{}
	flagSeenMu  sync.Mutex
	goals       goalState
}

// NewAppController creates a new application controller.
func NewAppController(
	mon *monitor.Monitor,
	repo model.Repository,
) *AppController {
	mon.SetRepo(repo)
	c := &AppController{
		monitor:   mon,
		repo:      repo,
		reportGen: report.New(repo),
		ranker:    ranking.New(ranking.DefaultWeights),
		flagSeen:  make(map[string]struct{}),
		goals: goalState{
			lastUnits: make(map[int64]int),
		},
	}
	// Restore persisted settings (target gifts, moderation toggles, etc.) so
	// they survive app restarts.
	if raw, err := repo.GetSetting(settingsKey); err == nil && raw != "" {
		var s monitor.Settings
		if err := json.Unmarshal([]byte(raw), &s); err == nil {
			mon.SetSettings(s)
		} else {
			log.Printf("[Controller] Failed to parse persisted settings: %v", err)
		}
	}
	return c
}

// --- Monitor Actions ---

// StartMonitoring starts monitoring the given username.
func (c *AppController) StartMonitoring(ctx context.Context, username string) error {
	c.monCancelMu.Lock()
	monCtx, cancel := context.WithCancel(context.Background())
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

// SetSettings updates the monitor settings and persists them so the
// configuration (including target gifts) survives app restarts.
func (c *AppController) SetSettings(settings monitor.Settings) {
	c.monitor.SetSettings(settings)
	if data, err := json.Marshal(settings); err == nil {
		if err := c.repo.SetSetting(settingsKey, string(data)); err != nil {
			log.Printf("[Controller] Failed to persist settings: %v", err)
		}
	}
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

// ReportExternalFlag ingests a moderation flag and surfaces it through the
// existing flagged-message pipeline (UI + anomaly log). Plumbing only.
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

// GetLiveRanking returns the ranking for the given live. mode selects the
// criterion: "tiktok" reproduces the TikTok in-room ranking (pure gift value)
// while any other value keeps the default weighted engagement score.
func (c *AppController) GetLiveRanking(liveName, mode string) (model.LiveRanking, error) {
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
	// O `total` do stream é o contador ACUMULADO da sala desde o início da
	// live (inclui curtidas anteriores à conexão), por isso não pode ser usado
	// para reescalar a contagem por usuário — o faria inflar. Cada usuário é
	// exibido com a soma dos eventos de like efetivamente entregues ao
	// monitor; TotalLikes mostra o contador oficial da sala.
	var roomTotal int64
	if rt, _, err := c.repo.LikeTotals(liveName); err == nil {
		roomTotal = rt
	}
	out.TotalLikes = roomTotal
	if strings.EqualFold(mode, model.ModeTikTok) {
		// TikTok in-room ranking: gift (diamond) value only, no anomaly penalty.
		out = c.ranker.BuildTikTokRanking(liveName, stats)
		out.TotalLikes = roomTotal
		return out, nil
	}
	anomaliesByUser := map[string]int{}
	if logs, err := c.repo.GetAnomalyLogsByLiveName(liveName); err == nil {
		for _, l := range logs {
			if l.IsAnomaly {
				anomaliesByUser[l.UniqueID]++
			}
		}
	}
	out = c.ranker.BuildLiveRanking(liveName, stats, anomaliesByUser)
	out.TotalLikes = roomTotal
	return out, nil
}

// GenerateReport produces the deterministic post-live report.
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
	out.Messages, _ = c.repo.GetUserMessagesRecent(uniqueID, 10)
	out.Gifts, _ = c.repo.GetGiftsByUser(uniqueID)
	out.LastLives, _ = c.repo.RecentLivesForUser(uniqueID, 10)

	// Aggregate totals.
	out.TotalMessages = len(out.Messages)
	out.TotalGifts = len(out.Gifts)
	for _, g := range out.Gifts {
		out.TotalGiftUnits += g.RepeatCount
	}
	if total, err := c.repo.GetUserLikeTotal(uniqueID); err == nil {
		out.TotalLikes = int(total)
	}
	if count, err := c.repo.GetUserShareCount(uniqueID); err == nil {
		out.TotalShares = count
	}

	// Best-effort nickname from stored events (messages store username,
	// gifts/shares store nickname).
	if out.Nickname == "" {
		for _, g := range out.Gifts {
			if g.Nickname != "" {
				out.Nickname = g.Nickname
				break
			}
		}
	}
	if out.Nickname == "" && len(out.Messages) > 0 {
		out.Nickname = out.Messages[0].Username
	}

	// Derive a risk level from the user's anomaly history.
	alerts, err := c.repo.GetAnomalyLogsByUser(uniqueID, 50)
	if err != nil {
		log.Printf("[Controller] Error fetching user alerts: %v", err)
	} else {
		out.Alerts = alerts
		out.RiskLevel = riskForUser(alerts, uniqueID)
	}
	if out.RiskLevel == "" {
		out.RiskLevel = model.RiskLevelNone
	}
	return out, nil
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
	c.checkGoalProgress()
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

// HandleLikeEvent processes a live like (heart) event and stores it.
func (c *AppController) HandleLikeEvent(data monitor.EventData) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("[Controller] panic storing like: %v", rec)
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
	likeCount := eventInt(data, "likeCount", 1)
	if likeCount < 1 {
		likeCount = 1
	}
	liveName := c.monitor.GetState().Username
	if err := c.repo.AddLike(liveName, uniqueID, nickname, likeCount); err != nil {
		log.Printf("[Controller] Error storing like: %v", err)
	}
	// `total` é o contador acumulado de curtidas da SALA (autoritativo).
	// O stream entrega apenas uma amostra dos eventos de like, então esse
	// total é usado para calibrar a contagem por usuário no ranking.
	if roomTotal := int64(eventInt(data, "total", 0)); roomTotal > 0 {
		if err := c.repo.UpsertRoomLikeTotal(liveName, roomTotal); err != nil {
			log.Printf("[Controller] Error storing room like total: %v", err)
		}
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
