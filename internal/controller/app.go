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

	"github.com/thiagohmm/tiktok-live-monitor/internal/ai"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"github.com/thiagohmm/tiktok-live-monitor/internal/moderation"
	"github.com/thiagohmm/tiktok-live-monitor/internal/monitor"
	"github.com/thiagohmm/tiktok-live-monitor/internal/service"
)

// AppController orchestrates all application services.
type AppController struct {
	aiManager   *ai.Manager
	modEngine   *moderation.Engine
	monitor     *monitor.Monitor
	repo        model.Repository
	monCtx      context.Context
	monCancel   context.CancelFunc
	monCancelMu sync.Mutex
}

// NewAppController creates a new application controller.
func NewAppController(
	aiManager *ai.Manager,
	modEngine *moderation.Engine,
	mon *monitor.Monitor,
	repo model.Repository,
) *AppController {
	mon.SetRepo(repo)
	c := &AppController{
		aiManager: aiManager,
		modEngine: modEngine,
		monitor:   mon,
		repo:      repo,
	}
	mon.LLMCorrelate = c.correlateGiftQuestionLLM
	return c
}

func (c *AppController) correlateGiftQuestionLLM(ctx context.Context, gift monitor.GiftPayload, candidates []monitor.QuestionEntry) *monitor.QuestionEntry {
	if c.aiManager == nil {
		return nil
	}
	return service.CorrelateGiftQuestion(ctx, c.aiManager, gift, candidates)
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

// GetStartupStatus returns the moderation warmup status.
func (c *AppController) GetStartupStatus() moderation.StartupStatus {
	return c.modEngine.GetStartupStatus()
}

// ClearModerationCache clears the AI moderation cache.
func (c *AppController) ClearModerationCache() {
	c.modEngine.ClearCache()
}

// WarmupModeration warms up the moderation pipeline.
func (c *AppController) WarmupModeration(ctx context.Context, touchLLM, force bool) error {
	_, err := c.modEngine.WarmupLearning(ctx, touchLLM, force)
	return err
}

// ProbeReady checks if the LLM worker is healthy.
func (c *AppController) ProbeReady(ctx context.Context) (bool, error) {
	return c.aiManager.ProbeReady(ctx)
}

// RegisterWorker registers a remote AI worker.
func (c *AppController) RegisterWorker(host string, port int) {
	c.aiManager.RegisterWorker(host, port)
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

// AddFeedback adds user feedback for moderation training.
func (c *AppController) AddFeedback(comment, category, expected string) (int64, error) {
	return c.repo.AddFeedback(comment, category, expected)
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

// --- AI Actions ---

// AskAI asks the AI a question with live context.
func (c *AppController) AskAI(ctx context.Context, question string) (string, error) {
	return service.AskAI(ctx, question, c.aiManager, c.repo)
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
	if err := c.repo.AddUserMessageDedup(uniqueID, nickname, comment); err != nil {
		log.Printf("[Controller] Error storing user message: %v", err)
	}
}

// GetMonitor returns the underlying monitor for event registration.
func (c *AppController) GetMonitor() *monitor.Monitor {
	return c.monitor
}

// Stop shuts down the AI manager.
func (c *AppController) Stop() {
	c.aiManager.Stop()
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
		return name
	}
	for _, nest := range []string{"giftDetails", "extendedGiftInfo", "gift"} {
		if name := nestedString(data[nest], "giftName", "name", "describe"); name != "" {
			return name
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
