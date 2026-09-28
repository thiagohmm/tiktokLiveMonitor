// Package controller contains request handlers that orchestrate services and models.
package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/database"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"github.com/thiagohmm/tiktok-live-monitor/internal/monitor"
	"github.com/thiagohmm/tiktok-live-monitor/internal/ranking"
	"github.com/thiagohmm/tiktok-live-monitor/internal/report"
)

// MessageCache is an in-memory write-behind buffer for user messages.
type MessageCache interface {
	Add(ref model.LiveRef, uniqueID, username, message string)
	Snapshot() []model.UserMessage
}

// AppController orchestrates all application services. Every tenant-facing
// method takes the organization id (orgID) of the caller: settings, lives,
// history and events of one organization are never visible to another.
type AppController struct {
	monitor        *monitor.Monitor
	monitorManager *monitor.Manager
	repo           model.Repository
	msgCache       MessageCache
	reportGen      *report.Generator
	ranker         *ranking.Ranker
	flagSeen       map[string]struct{}
	flagSeenMu     sync.Mutex
	settingsMu     sync.Mutex
	settings       map[string]monitor.Settings
	goals          goalState
	attachments    *MonitorAttachmentStore
	pixQueue       *PixQueueService
}

// SetMonitorManager replaces the concurrent monitor manager.
func (c *AppController) SetMonitorManager(manager *monitor.Manager) {
	if manager != nil {
		c.monitorManager = manager
	}
}

// maxMonitorsFromEnv reads the global live cap (env MAX_MONITORS, default 20).
func maxMonitorsFromEnv() int {
	if v := strings.TrimSpace(os.Getenv("MAX_MONITORS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 20
}

// NewAppController creates a new application controller. mon only carries
// events derived by the controller itself (external moderation flags); every
// live runs in the monitor manager.
func NewAppController(
	mon *monitor.Monitor,
	repo model.Repository,
) *AppController {
	mon.SetRepo(repo)
	return &AppController{
		monitor:        mon,
		monitorManager: monitor.NewManager(repo, maxMonitorsFromEnv()),
		repo:           repo,
		reportGen:      report.New(repo),
		ranker:         ranking.New(ranking.DefaultWeights),
		flagSeen:       make(map[string]struct{}),
		settings:       make(map[string]monitor.Settings),
		goals: goalState{
			lastUnits: make(map[int64]int),
		},
		attachments: NewMonitorAttachmentStore(),
	}
}

// --- Monitor Actions ---

// StartMonitoring starts monitoring username for the organization, within the
// organization's live quota.
func (c *AppController) StartMonitoring(ctx context.Context, orgID, username string) error {
	orgID = strings.TrimSpace(orgID)
	if orgID == "" {
		return model.ErrOrgRequired
	}
	maxLives := 0
	org, err := c.repo.GetOrganization(orgID)
	switch {
	case err == nil:
		if !org.Active {
			return fmt.Errorf("organização desativada")
		}
		maxLives = org.MaxLives
	case !errors.Is(err, model.ErrOrgNotFound):
		return fmt.Errorf("load organization: %w", err)
	}
	return c.monitorManager.StartMonitoring(ctx, orgID, username, c.GetSettings(orgID), maxLives)
}

// StopMonitoring stops every live of every organization (shutdown).
func (c *AppController) StopMonitoring() {
	c.monitorManager.StopAll()
}

// StopMonitoringLive stops one live of the organization (every live when
// username is empty).
func (c *AppController) StopMonitoringLive(orgID, username string) {
	c.monitorManager.StopMonitoring(orgID, username)
}

// GetLiveStates returns the state of every live monitored by the organization.
func (c *AppController) GetLiveStates(orgID string) []monitor.LiveState {
	return c.monitorManager.States(orgID)
}

// GetState returns the state of the organization's first monitored live.
func (c *AppController) GetState(orgID string) monitor.State {
	state := c.monitorManager.CurrentState(orgID)
	state.Settings = c.GetSettings(orgID)
	return state
}

// ResolveLive returns live when given, otherwise the organization's current
// live (empty when it monitors none).
func (c *AppController) ResolveLive(orgID, live string) string {
	if live = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(live), "@")); live != "" {
		return live
	}
	return c.monitorManager.CurrentState(orgID).Username
}

// GetSettings returns the organization's settings (defaults when none were saved).
func (c *AppController) GetSettings(orgID string) monitor.Settings {
	c.settingsMu.Lock()
	defer c.settingsMu.Unlock()
	if s, ok := c.settings[orgID]; ok {
		return s
	}
	s := defaultSettings()
	if raw, err := c.repo.GetSetting(database.OrgSettingsKey(orgID)); err == nil && raw != "" {
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			log.Printf("[Controller] Failed to parse settings of organization %s: %v", orgID, err)
			s = defaultSettings()
		}
	}
	if s.TargetGifts == nil {
		s.TargetGifts = []string{}
	}
	c.settings[orgID] = s
	return s
}

func defaultSettings() monitor.Settings {
	return monitor.Settings{
		ModerationEnabled:    true,
		LogLevel:             "info",
		TargetGifts:          []string{},
		TargetGiftPriorities: map[string]bool{},
		TargetGiftTags:       map[string]string{},
	}
}

// SetSettings updates the organization's settings on its monitors and
// persists them so the configuration survives app restarts.
func (c *AppController) SetSettings(orgID string, settings monitor.Settings) error {
	orgID = strings.TrimSpace(orgID)
	if orgID == "" {
		return model.ErrOrgRequired
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}
	if err := c.repo.SetSetting(database.OrgSettingsKey(orgID), string(data)); err != nil {
		return fmt.Errorf("persist settings: %w", err)
	}
	c.settingsMu.Lock()
	c.settings[orgID] = settings
	c.settingsMu.Unlock()
	c.monitorManager.SetSettings(orgID, settings)
	return nil
}

// FetchAvailableGifts fetches the available gifts of one of the organization's lives.
func (c *AppController) FetchAvailableGifts(orgID, live string) ([]string, error) {
	live = c.ResolveLive(orgID, live)
	if live == "" {
		return []string{}, nil
	}
	gifts, err := c.monitorManager.FetchAvailableGifts(orgID, live)
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
// existing flagged-message pipeline (UI + anomaly log). The flag must carry
// the organization (orgId) and live (liveName) it belongs to.
func (c *AppController) ReportExternalFlag(data monitor.EventData) {
	orgID := eventString(data, "orgId")
	if orgID == "" {
		return
	}
	settings := c.GetSettings(orgID)
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

	key := orgID + "|" + strings.ToLower(uniqueID) + "|" + foldComment(comment)
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

	liveName := c.eventLiveName(data)
	c.monitor.Emit(monitor.EventFlaggedMessage, monitor.EventData{
		"orgId":     orgID,
		"liveName":  liveName,
		"uniqueId":  uniqueID,
		"nickname":  nickname,
		"comment":   comment,
		"reason":    reason,
		"category":  category,
		"timestamp": eventString(data, "timestamp"),
	})
	ref, err := c.eventLiveRef(data)
	if err != nil {
		log.Printf("[Controller] Error resolving live session for external flag (%s): %v", liveName, err)
		return
	}
	if err := c.repo.LogAnomaly(ref, comment, true, category, uniqueID); err != nil {
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

// GetRecentModerations returns the organization's recent moderation history.
func (c *AppController) GetRecentModerations(orgID string, limit int) ([]model.AnomalyLog, error) {
	return c.repo.GetRecentModerations(orgID, limit)
}

// DeleteModeration deletes one of the organization's moderation records.
func (c *AppController) DeleteModeration(orgID string, id int64) (int64, error) {
	return c.repo.DeleteModeration(orgID, id)
}

// ClearHistory clears the organization's moderation history.
func (c *AppController) ClearHistory(orgID string) (int64, error) {
	return c.repo.ClearHistory(orgID)
}

// GetLives returns one row per live session of the organization.
func (c *AppController) GetLives(orgID string, limit int) ([]model.Live, error) {
	return c.repo.ListLives(orgID, limit)
}

// GetLiveSession returns one live session of the organization. A session of
// another organization is reported as not found.
func (c *AppController) GetLiveSession(orgID, id string) (model.LiveSession, error) {
	session, err := c.repo.GetLiveSession(id)
	if err != nil {
		return model.LiveSession{}, err
	}
	if session.OrgID != strings.TrimSpace(orgID) {
		return model.LiveSession{}, model.ErrLiveSessionNotFound
	}
	return session, nil
}

// DeleteLive removes all stored data of one live session (id) of the
// organization, never the whole history of the streamer.
func (c *AppController) DeleteLive(orgID, id string) (int64, error) {
	if _, err := c.GetLiveSession(orgID, id); err != nil {
		return 0, err
	}
	return c.repo.DeleteLiveSession(id)
}

// AssignLegacyLives moves sessions of the legacy organization into a customer
// organization. A live the legacy organization is still monitoring is refused:
// its monitor would keep writing into a session that no longer belongs to it.
// When the legacy settings are copied, the destination's cached settings are
// dropped and pushed to its running monitors.
func (c *AppController) AssignLegacyLives(a model.LiveAssignment) (model.LiveAssignResult, error) {
	monitored := make(map[string]struct{})
	for _, st := range c.GetLiveStates(model.DefaultOrgID) {
		monitored[strings.ToLower(strings.TrimSpace(st.Live))] = struct{}{}
	}
	if len(monitored) > 0 {
		names := append([]string{}, a.LiveNames...)
		for _, id := range a.SessionIDs {
			if s, err := c.repo.GetLiveSession(id); err == nil {
				names = append(names, s.LiveName)
			}
		}
		for _, n := range names {
			if _, ok := monitored[strings.ToLower(strings.TrimSpace(n))]; ok {
				return model.LiveAssignResult{}, model.ErrLiveBeingMonitored
			}
		}
	}

	result, err := c.repo.AssignLegacyLives(a)
	if err != nil {
		return model.LiveAssignResult{}, err
	}
	if result.SettingsCopied {
		orgID := strings.TrimSpace(a.OrgID)
		c.settingsMu.Lock()
		delete(c.settings, orgID)
		c.settingsMu.Unlock()
		c.monitorManager.SetSettings(orgID, c.GetSettings(orgID))
	}
	return result, nil
}

// GetRecentGifts returns recent gifts of one of the organization's lives.
func (c *AppController) GetRecentGifts(orgID, liveName string, limit int) ([]model.Gift, error) {
	return c.repo.GetRecentGifts(orgID, liveName, limit)
}

// GetGiftsByUser returns the gifts a participant sent in the organization's lives.
func (c *AppController) GetGiftsByUser(orgID, userID string) ([]model.Gift, error) {
	return c.repo.GetGiftsByUser(orgID, userID)
}

// ClearGifts clears the organization's gift records.
func (c *AppController) ClearGifts(orgID string) (int64, error) {
	return c.repo.ClearGifts(orgID)
}

// RecordTargetGiftReceived stores a pending target gift history entry and returns its id.
func (c *AppController) RecordTargetGiftReceived(data monitor.EventData) (int64, error) {
	ref, err := c.eventLiveRef(data)
	if err != nil {
		return 0, err
	}
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

	id, err := c.repo.AddTargetGiftHistory(ref, uniqueID, nickname, giftName, receivedAt, eventBool(data, "isPriority"))
	if err == nil {
		data["receivedAt"] = receivedAt.UTC().Format(time.RFC3339Nano)
	}
	return id, err
}

// eventBool reads a boolean flag from event data, tolerating JSON numbers.
func eventBool(data monitor.EventData, key string) bool {
	switch v := data[key].(type) {
	case bool:
		return v
	case float64:
		return v != 0
	case int:
		return v != 0
	default:
		return false
	}
}

// AnswerTargetGift marks one of the organization's target gift entries as answered.
func (c *AppController) AnswerTargetGift(orgID string, id int64, responseType string) error {
	return c.repo.MarkTargetGiftAnswered(orgID, id, responseType, time.Now())
}

// SetTargetGiftPriority promotes (priority=true) or demotes (priority=false)
// a pending target gift of the organization in the queue.
func (c *AppController) SetTargetGiftPriority(orgID string, id int64, priority bool) (*time.Time, error) {
	at := time.Now().UTC().Truncate(time.Microsecond)
	if err := c.repo.SetTargetGiftPriority(orgID, id, priority, at); err != nil {
		return nil, err
	}
	if !priority {
		return nil, nil
	}
	return &at, nil
}

// GetRecentTargetGiftHistory returns recent target gift history of one of the
// organization's lives (the current one when live is empty).
func (c *AppController) GetRecentTargetGiftHistory(orgID, live string, limit int) ([]model.TargetGiftHistory, error) {
	return c.repo.GetRecentTargetGiftHistory(orgID, c.ResolveLive(orgID, live), limit)
}

// GetPendingTargetGiftHistory returns unanswered target gifts of one of the
// organization's lives (the current one when live is empty).
func (c *AppController) GetPendingTargetGiftHistory(orgID, live string, limit int) ([]model.TargetGiftHistory, error) {
	liveName := c.ResolveLive(orgID, live)
	if liveName == "" {
		return []model.TargetGiftHistory{}, nil
	}
	return c.repo.GetPendingTargetGiftHistory(orgID, liveName, limit)
}

// RecordPinnedComment stores a pinned comment from a live event.
func (c *AppController) RecordPinnedComment(data monitor.EventData) (int64, error) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("[Controller] panic storing pinned comment: %v", rec)
		}
	}()

	ref, err := c.eventLiveRef(data)
	if err != nil {
		return 0, err
	}
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
	return c.repo.AddPinnedComment(ref, uniqueID, nickname, comment, pinID, eventBoolPtr(data, "isFollower"), at)
}

// GetRecentPinnedComments returns recent pinned comments of one of the
// organization's lives (the current one when live is empty).
func (c *AppController) GetRecentPinnedComments(orgID, live string, limit int) ([]model.PinnedComment, error) {
	return c.repo.GetRecentPinnedComments(orgID, c.ResolveLive(orgID, live), limit)
}

// --- Ranking, Report & Profile Actions ---

// GetLiveRanking returns the ranking for one of the organization's lives. mode
// selects the criterion: "tiktok" reproduces the TikTok in-room ranking (pure
// gift value) while any other value keeps the default weighted engagement score.
func (c *AppController) GetLiveRanking(orgID, liveName, mode string) (model.LiveRanking, error) {
	out := model.LiveRanking{LiveName: liveName, UpdatedAt: time.Now().Format(time.RFC3339)}
	if strings.TrimSpace(liveName) == "" {
		return out, nil
	}
	stats, err := c.repo.LiveStatsByUser(orgID, liveName)
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
	if rt, _, err := c.repo.LikeTotals(orgID, liveName); err == nil {
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
	if logs, err := c.repo.GetAnomalyLogsByLiveName(orgID, liveName); err == nil {
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

// GenerateReport produces the deterministic post-live report of one of the
// organization's lives.
func (c *AppController) GenerateReport(ctx context.Context, orgID, liveName string) (model.LiveReport, error) {
	if c.reportGen == nil {
		return model.LiveReport{}, fmt.Errorf("report generator unavailable")
	}
	return c.reportGen.Generate(ctx, orgID, liveName)
}

// GetUserProfile returns the historical profile of a participant within the
// organization's lives.
func (c *AppController) GetUserProfile(orgID, uniqueID string) (model.UserProfile, error) {
	out := model.UserProfile{UniqueID: uniqueID}
	if strings.TrimSpace(uniqueID) == "" {
		return out, nil
	}
	out.Messages, _ = c.repo.GetUserMessagesRecent(orgID, uniqueID, 10)
	out.Gifts, _ = c.repo.GetGiftsByUser(orgID, uniqueID)
	out.LastLives, _ = c.repo.RecentLivesForUser(orgID, uniqueID, 10)

	// Aggregate totals.
	out.TotalMessages = len(out.Messages)
	out.TotalGifts = len(out.Gifts)
	for _, g := range out.Gifts {
		out.TotalGiftUnits += g.RepeatCount
		out.TotalGiftValue += g.RepeatCount * model.GiftValue(g.GiftName)
	}
	if total, err := c.repo.GetUserLikeTotal(orgID, uniqueID); err == nil {
		out.TotalLikes = int(total)
	}
	if count, err := c.repo.GetUserShareCount(orgID, uniqueID); err == nil {
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
	alerts, err := c.repo.GetAnomalyLogsByUser(orgID, uniqueID, 50)
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
	ref, err := c.eventLiveRef(data)
	if err != nil {
		log.Printf("[Controller] Error resolving live session for gift: %v", err)
		return
	}
	if _, err := c.repo.AddGift(ref, uniqueID, nickname, giftName, repeatCount, giftType); err != nil {
		log.Printf("[Controller] Error storing gift: %v", err)
	}
	c.checkGoalProgress(ref)
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
	ref, err := c.eventLiveRef(data)
	if err != nil {
		log.Printf("[Controller] Error resolving live session for message: %v", err)
		return
	}
	if c.msgCache != nil {
		c.msgCache.Add(ref, uniqueID, nickname, comment)
		return
	}
	if err := c.repo.AddUserMessageDedup(ref, uniqueID, nickname, comment); err != nil {
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
	ref, err := c.eventLiveRef(data)
	if err != nil {
		log.Printf("[Controller] Error resolving live session for share: %v", err)
		return
	}
	if err := c.repo.AddShare(ref, uniqueID, nickname); err != nil {
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
	ref, err := c.eventLiveRef(data)
	if err != nil {
		log.Printf("[Controller] Error resolving live session for like: %v", err)
		return
	}
	if err := c.repo.AddLike(ref, uniqueID, nickname, likeCount); err != nil {
		log.Printf("[Controller] Error storing like: %v", err)
	}
	// `total` é o contador acumulado de curtidas da SALA (autoritativo).
	// O stream entrega apenas uma amostra dos eventos de like, então esse
	// total é usado para calibrar a contagem por usuário no ranking.
	if roomTotal := int64(eventInt(data, "total", 0)); roomTotal > 0 {
		if err := c.repo.UpsertRoomLikeTotal(ref, roomTotal); err != nil {
			log.Printf("[Controller] Error storing room like total: %v", err)
		}
	}
}

// GetMonitor returns the controller's own event emitter (derived events).
func (c *AppController) GetMonitor() *monitor.Monitor {
	return c.monitor
}

// GetMonitorManager returns the concurrent monitor manager.
func (c *AppController) GetMonitorManager() *monitor.Manager { return c.monitorManager }

// SetMessageCache enables write-behind caching for chat messages.
func (c *AppController) SetMessageCache(mc MessageCache) {
	c.msgCache = mc
}

// SetPixQueueService enables the Fila PIX (WhatsApp/WAHA + MinIO).
func (c *AppController) SetPixQueueService(svc *PixQueueService) {
	c.pixQueue = svc
}

// GetPixQueueService returns the Fila PIX service, when configured.
func (c *AppController) GetPixQueueService() *PixQueueService { return c.pixQueue }

func (c *AppController) eventLiveName(data monitor.EventData) string {
	if liveName := eventString(data, "liveName"); liveName != "" {
		return liveName
	}
	if orgID := eventString(data, "orgId"); orgID != "" {
		return c.monitorManager.CurrentState(orgID).Username
	}
	return ""
}

// activeLiveRef returns the session of one live monitored by the organization
// (its current live when live is empty).
//
// When the monitor has no session id (a start that could not open one, or a
// test harness that did not go through StartMonitoring) the latest session of
// that streamer in the organization is used, so an event or goal is never
// written without a live.
func (c *AppController) activeLiveRef(orgID, live string) (model.LiveRef, error) {
	orgID = strings.TrimSpace(orgID)
	if orgID == "" {
		return model.LiveRef{}, model.ErrOrgRequired
	}
	state := c.monitorManager.CurrentState(orgID)
	if live = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(live), "@")); live != "" {
		st, ok := c.monitorManager.StateFor(orgID, live)
		if !ok {
			st = monitor.State{Username: live}
		}
		state = st
	}
	if state.Username == "" {
		return model.LiveRef{}, fmt.Errorf("no live is being monitored")
	}
	if state.LiveID != "" {
		return model.LiveRef{ID: state.LiveID, Name: state.Username, OrgID: orgID}, nil
	}
	session, err := c.repo.LatestLiveSession(orgID, state.Username)
	if err != nil {
		return model.LiveRef{}, fmt.Errorf("resolve live session for %s: %w", state.Username, err)
	}
	return model.LiveRef{ID: session.ID, Name: state.Username, OrgID: orgID}, nil
}

// eventLiveRef resolves the session an event belongs to.
//
// Events coming from the bridge carry orgId and liveId (injected by the
// manager). Events arriving without liveId are resolved from the streamer name
// inside the organization — and never create a session, so an unknown source
// cannot spawn a session that owns no live.
func (c *AppController) eventLiveRef(data monitor.EventData) (model.LiveRef, error) {
	orgID := strings.TrimSpace(eventString(data, "orgId"))
	if orgID == "" {
		return model.LiveRef{}, model.ErrOrgRequired
	}
	liveName := strings.TrimSpace(c.eventLiveName(data))
	if liveName == "" {
		return model.LiveRef{}, fmt.Errorf("live name is required")
	}
	if id := strings.TrimSpace(eventString(data, "liveId")); id != "" {
		return model.LiveRef{ID: id, Name: liveName, OrgID: orgID}, nil
	}
	session, err := c.repo.LatestLiveSession(orgID, liveName)
	if err != nil {
		return model.LiveRef{}, fmt.Errorf("resolve live session for %s: %w", liveName, err)
	}
	return model.LiveRef{ID: session.ID, Name: liveName, OrgID: orgID}, nil
}

// Stop shuts down every bridge child process.
func (c *AppController) Stop() {
	c.monitorManager.Close()
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

// Repository exposes the repository (the view resolves organizations with it).
func (c *AppController) Repository() model.Repository { return c.repo }
