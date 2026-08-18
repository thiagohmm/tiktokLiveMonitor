// Package controller contains request handlers that orchestrate services and models.
package controller

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/ai"
	"github.com/thiagohmm/tiktok-live-monitor/internal/moderation"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
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
	return &AppController{
		aiManager: aiManager,
		modEngine: modEngine,
		monitor:   mon,
		repo:      repo,
	}
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
func (c *AppController) FetchAvailableGifts() (interface{}, error) {
	return c.monitor.FetchAvailableGifts()
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
	uniqueID, _ := data["uniqueId"].(string)
	nickname, _ := data["nickname"].(string)
	giftName, _ := data["giftName"].(string)
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

// --- AI Actions ---

// AskAI asks the AI a question with live context.
func (c *AppController) AskAI(ctx context.Context, question string) (string, error) {
	return service.AskAI(ctx, question, c.aiManager, c.repo)
}

// --- Event Handlers ---

// HandleGiftEvent processes a gift event and stores it.
func (c *AppController) HandleGiftEvent(data monitor.EventData) {
	uniqueID := data["uniqueId"].(string)
	nickname := data["nickname"].(string)
	giftName := data["giftName"].(string)
	repeatCount := 1
	if rc, ok := data["repeatCount"]; ok {
		if rcInt, ok := rc.(int); ok {
			repeatCount = rcInt
		}
	}
	giftType := 0
	if gt, ok := data["giftType"]; ok {
		if gtInt, ok := gt.(int); ok {
			giftType = gtInt
		}
	}
	state := c.monitor.GetState()
	if _, err := c.repo.AddGift(state.Username, uniqueID, nickname, giftName, repeatCount, giftType); err != nil {
		log.Printf("[Controller] Error storing gift: %v", err)
	}
}

// HandleChatMessageEvent processes a chat message event and stores it.
func (c *AppController) HandleChatMessageEvent(data monitor.EventData) {
	uniqueID := data["uniqueId"].(string)
	nickname := data["nickname"].(string)
	comment := data["comment"].(string)
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
	default:
		return 0, false
	}
}
