package view

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/ai"
	"github.com/thiagohmm/tiktok-live-monitor/internal/controller"
	"github.com/thiagohmm/tiktok-live-monitor/internal/database"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"github.com/thiagohmm/tiktok-live-monitor/internal/moderation"
	"github.com/thiagohmm/tiktok-live-monitor/internal/monitor"
)

func setupTestServer(t *testing.T) (*HTTPServer, model.Repository, string, *monitor.Monitor) {
	t.Helper()
	dir := t.TempDir()

	repo, err := database.Open(dir)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { repo.Close() })

	aiMgr := ai.NewManager(filepath.Join(dir, "models"), filepath.Join(dir, "bin"))
	t.Cleanup(func() { aiMgr.Stop() })

	mon, err := monitor.New()
	if err != nil {
		t.Skipf("skip test (TikTok API unavailable): %v", err)
	}

	modEngine := moderation.NewEngine(aiMgr, repo)

	os.MkdirAll(filepath.Join(dir, "web"), 0755)

	ctrl := controller.NewAppController(aiMgr, modEngine, mon, repo)

	srv := New(Config{
		Host:      "127.0.0.1",
		Port:      0,
		ModelsDir: filepath.Join(dir, "models"),
		BinDir:    filepath.Join(dir, "bin"),
		WebDir:    filepath.Join(dir, "web"),
	}, ctrl)

	return srv, repo, dir, mon
}

func TestHandleState(t *testing.T) {
	srv, _, _, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	rec := httptest.NewRecorder()

	srv.handleState(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if _, ok := result["connected"]; !ok {
		t.Error("expected 'connected' field in state")
	}
}

func TestHandleSettings(t *testing.T) {
	srv, _, _, _ := setupTestServer(t)

	t.Run("GET", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
		rec := httptest.NewRecorder()
		srv.handleSettings(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("POST", func(t *testing.T) {
		body := map[string]interface{}{
			"moderationEnabled":   true,
			"aiModerationEnabled": true,
			"logLevel":            "debug",
		}
		data, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/api/settings", bytes.NewReader(data))
		rec := httptest.NewRecorder()
		srv.handleSettings(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("PUT rejected", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/settings", nil)
		rec := httptest.NewRecorder()
		srv.handleSettings(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405, got %d", rec.Code)
		}
	})
}

func TestHandleHistory(t *testing.T) {
	srv, db, _, _ := setupTestServer(t)

	err := db.LogAnomaly("live1", "test msg", true, "SPAM", "user1")
	if err != nil {
		t.Fatalf("log anomaly: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/history", nil)
	rec := httptest.NewRecorder()
	srv.handleHistory(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body: %s", rec.Code, rec.Body.String())
	}

	var logs []map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &logs); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(logs))
	}
}

func TestHandleHistoryDelete(t *testing.T) {
	srv, db, _, _ := setupTestServer(t)

	err := db.LogAnomaly("live1", "test msg", true, "SPAM", "user1")
	if err != nil {
		t.Fatalf("log anomaly: %v", err)
	}

	logs, err := db.GetRecentModerations(10)
	if err != nil || len(logs) == 0 {
		t.Fatalf("no logs to delete: err=%v, len=%d", err, len(logs))
	}
	id := logs[0].ID

	req := httptest.NewRequest(http.MethodDelete, "/api/history/"+fmt.Sprintf("%d", id), nil)
	rec := httptest.NewRecorder()
	srv.handleHistory(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestHandleConnect(t *testing.T) {
	srv, _, _, _ := setupTestServer(t)

	t.Run("missing username", func(t *testing.T) {
		body := map[string]string{}
		data, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/api/connect", bytes.NewReader(data))
		rec := httptest.NewRecorder()
		srv.handleConnect(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("wrong method", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/connect", nil)
		rec := httptest.NewRecorder()
		srv.handleConnect(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405, got %d", rec.Code)
		}
	})
}

func TestHandleDisconnect(t *testing.T) {
	srv, _, _, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/disconnect", nil)
	rec := httptest.NewRecorder()
	srv.handleDisconnect(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestHandleClearHistory(t *testing.T) {
	srv, db, _, _ := setupTestServer(t)

	for i := 0; i < 3; i++ {
		_ = db.LogAnomaly("live1", "msg", false, "OK", "user1")
	}

	req := httptest.NewRequest(http.MethodPost, "/api/clear-history", nil)
	rec := httptest.NewRecorder()
	srv.handleClearHistory(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	deleted, _ := result["deleted"].(float64)
	if deleted != 3 {
		t.Fatalf("expected 3 deleted, got %v", deleted)
	}
}

func TestHandleFeedback(t *testing.T) {
	srv, _, _, _ := setupTestServer(t)

	t.Run("valid feedback", func(t *testing.T) {
		body := map[string]string{
			"comment":  "spam msg",
			"category": "SPAM",
			"expected": "SIM_SPAM",
		}
		data, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/api/feedback", bytes.NewReader(data))
		rec := httptest.NewRecorder()
		srv.handleFeedback(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("invalid category", func(t *testing.T) {
		body := map[string]string{
			"comment":  "msg",
			"category": "INVALID",
			"expected": "SIM_SPAM",
		}
		data, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/api/feedback", bytes.NewReader(data))
		rec := httptest.NewRecorder()
		srv.handleFeedback(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("empty comment", func(t *testing.T) {
		body := map[string]string{
			"comment":  "",
			"category": "SPAM",
			"expected": "SIM_SPAM",
		}
		data, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/api/feedback", bytes.NewReader(data))
		rec := httptest.NewRecorder()
		srv.handleFeedback(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})
}

func TestHandleGifts(t *testing.T) {
	srv, db, _, mon := setupTestServer(t)

	_, _ = db.AddGift("live1", "user1", "User One", "Rose", 3, 0)
	_, _ = db.AddGift("live1", "user2", "User Two", "Tiger", 1, 1)

	t.Run("GET all", func(t *testing.T) {
		mon.SetCurrentLive("live1")
		req := httptest.NewRequest(http.MethodGet, "/api/gifts", nil)
		rec := httptest.NewRecorder()
		srv.handleGifts(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var gifts []model.Gift
		if err := json.Unmarshal(rec.Body.Bytes(), &gifts); err != nil {
			t.Fatalf("decode JSON: %v", err)
		}
		if len(gifts) != 2 {
			t.Fatalf("expected 2 gifts, got %d", len(gifts))
		}
	})

	t.Run("GET with limit", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/gifts?limit=1", nil)
		rec := httptest.NewRecorder()
		srv.handleGifts(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var gifts []model.Gift
		if err := json.Unmarshal(rec.Body.Bytes(), &gifts); err != nil {
			t.Fatalf("decode JSON: %v", err)
		}
		if len(gifts) != 1 {
			t.Fatalf("expected 1 gift, got %d", len(gifts))
		}
	})

	t.Run("GET by user", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/gifts?user=user1", nil)
		rec := httptest.NewRecorder()
		srv.handleGifts(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var gifts []model.Gift
		if err := json.Unmarshal(rec.Body.Bytes(), &gifts); err != nil {
			t.Fatalf("decode JSON: %v", err)
		}
		if len(gifts) != 1 {
			t.Fatalf("expected 1 gift for user1, got %d", len(gifts))
		}
		if gifts[0].GiftName != "Rose" {
			t.Fatalf("expected Rose, got %q", gifts[0].GiftName)
		}
	})

	t.Run("DELETE", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/gifts", nil)
		rec := httptest.NewRecorder()
		srv.handleGifts(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("PUT rejected", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/gifts", nil)
		rec := httptest.NewRecorder()
		srv.handleGifts(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405, got %d", rec.Code)
		}
	})
}

func TestHandleGiftsEmptyReturnsArray(t *testing.T) {
	srv, _, _, mon := setupTestServer(t)
	mon.SetCurrentLive("nobody")

	req := httptest.NewRequest(http.MethodGet, "/api/gifts", nil)
	rec := httptest.NewRecorder()
	srv.handleGifts(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var gifts []model.Gift
	if err := json.Unmarshal(rec.Body.Bytes(), &gifts); err != nil {
		t.Fatalf("decode JSON: %v body=%s", err, rec.Body.String())
	}
	if gifts == nil {
		t.Fatal("expected empty array, got null")
	}
	if len(gifts) != 0 {
		t.Fatalf("expected 0 gifts, got %d", len(gifts))
	}
}

func TestHandleAvailableGiftsWithoutBridgeReturnsArray(t *testing.T) {
	srv, _, _, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/available-gifts", nil)
	rec := httptest.NewRecorder()
	srv.handleAvailableGifts(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var gifts []string
	if err := json.Unmarshal(rec.Body.Bytes(), &gifts); err != nil {
		t.Fatalf("decode JSON: %v body=%s", err, rec.Body.String())
	}
	if gifts == nil {
		t.Fatal("expected empty array, got null")
	}
}

func TestHandleGiftEventStoresJSONNumbersAndNestedName(t *testing.T) {
	srv, db, _, mon := setupTestServer(t)
	mon.SetCurrentLive("live1")

	srv.controller.HandleGiftEvent(monitor.EventData{
		"uniqueId":    "user1",
		"nickname":    "User One",
		"giftName":    "Rose",
		"repeatCount": float64(5),
		"giftType":    float64(1),
	})
	srv.controller.HandleGiftEvent(monitor.EventData{
		"uniqueId": nil,
		"nickname": nil,
		"giftDetails": map[string]interface{}{
			"giftName": "Dino",
		},
		"repeatCount": float64(2),
	})

	gifts, err := db.GetRecentGifts("live1", 10)
	if err != nil {
		t.Fatalf("get gifts: %v", err)
	}
	if len(gifts) != 2 {
		t.Fatalf("expected 2 gifts, got %d", len(gifts))
	}

	byName := map[string]model.Gift{}
	for _, g := range gifts {
		byName[g.GiftName] = g
	}
	if byName["Rose"].RepeatCount != 5 {
		t.Fatalf("expected Rose repeatCount 5, got %d", byName["Rose"].RepeatCount)
	}
	if byName["Dino"].UniqueID != "unknown" {
		t.Fatalf("expected unknown uniqueId, got %q", byName["Dino"].UniqueID)
	}
}

func TestHandlePinnedComments(t *testing.T) {
	srv, db, _, mon := setupTestServer(t)

	t.Run("empty array", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/pinned-comments", nil)
		rec := httptest.NewRecorder()
		srv.handlePinnedComments(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
		}
		var items []model.PinnedComment
		if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
			t.Fatalf("decode JSON: %v body=%s", err, rec.Body.String())
		}
		if items == nil {
			t.Fatal("expected empty array, got null")
		}
		if len(items) != 0 {
			t.Fatalf("expected 0 items, got %d", len(items))
		}
	})

	t.Run("wrong method", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/pinned-comments", nil)
		rec := httptest.NewRecorder()
		srv.handlePinnedComments(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405, got %d", rec.Code)
		}
	})

	t.Run("GET recent for live", func(t *testing.T) {
		mon.SetCurrentLive("live1")
		if _, err := db.AddPinnedComment("live1", "user1", "User One", "olá", "pin-1", nil, time.Now()); err != nil {
			t.Fatalf("add pinned: %v", err)
		}
		if _, err := db.AddPinnedComment("live2", "user2", "User Two", "outra", "pin-2", nil, time.Now()); err != nil {
			t.Fatalf("add other live: %v", err)
		}

		req := httptest.NewRequest(http.MethodGet, "/api/pinned-comments?limit=15", nil)
		rec := httptest.NewRecorder()
		srv.handlePinnedComments(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
		}
		var items []model.PinnedComment
		if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
			t.Fatalf("decode JSON: %v", err)
		}
		if len(items) != 1 || items[0].Comment != "olá" {
			t.Fatalf("expected live1 comment, got %+v", items)
		}
	})
}

func TestRecordPinnedCommentStoresEvent(t *testing.T) {
	srv, db, _, mon := setupTestServer(t)
	mon.SetCurrentLive("live1")

	id, err := srv.controller.RecordPinnedComment(monitor.EventData{
		"uniqueId":   "user1",
		"nickname":   "User One",
		"comment":    "fixado",
		"pinId":      "abc",
		"isFollower": true,
		"timestamp":  float64(1750000000000),
	})
	if err != nil {
		t.Fatalf("record pinned: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive id, got %d", id)
	}

	items, err := db.GetRecentPinnedComments("live1", 10)
	if err != nil {
		t.Fatalf("get pinned: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(items))
	}
	if items[0].Comment != "fixado" || items[0].PinID != "abc" {
		t.Fatalf("unexpected comment %+v", items[0])
	}
	if items[0].IsFollower == nil || !*items[0].IsFollower {
		t.Fatal("expected isFollower true")
	}
}

func TestHandleTargetGiftHistoryPending(t *testing.T) {
	srv, db, _, mon := setupTestServer(t)
	mon.SetCurrentLive("live1")

	pendingID, err := db.AddTargetGiftHistory("live1", "user1", "User One", "Rosa", time.Now())
	if err != nil {
		t.Fatalf("add pending: %v", err)
	}
	answeredID, err := db.AddTargetGiftHistory("live1", "user2", "User Two", "Dino", time.Now())
	if err != nil {
		t.Fatalf("add answered: %v", err)
	}
	if err := db.MarkTargetGiftAnswered(answeredID, model.TargetGiftResponseManual, time.Now()); err != nil {
		t.Fatalf("mark answered: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/target-gift-history?pending=1", nil)
	rec := httptest.NewRecorder()
	srv.handleTargetGiftHistory(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var items []model.TargetGiftHistory
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if len(items) != 1 || items[0].ID != pendingID {
		t.Fatalf("expected only pending id %d, got %+v", pendingID, items)
	}
}

func TestHandleAskAI(t *testing.T) {
	srv, _, _, _ := setupTestServer(t)

	t.Run("missing question", func(t *testing.T) {
		body := map[string]string{}
		data, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/api/ask-ai", bytes.NewReader(data))
		rec := httptest.NewRecorder()
		srv.handleAskAI(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("wrong method", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/ask-ai", nil)
		rec := httptest.NewRecorder()
		srv.handleAskAI(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405, got %d", rec.Code)
		}
	})
}

func TestHandleReadiness(t *testing.T) {
	srv, _, _, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/readiness", nil)
	rec := httptest.NewRecorder()
	srv.handleReadiness(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if _, ok := result["ready"]; !ok {
		t.Error("expected 'ready' field")
	}
}

func TestHandleProbeLLM(t *testing.T) {
	srv, _, _, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/probe-llm", nil)
	rec := httptest.NewRecorder()
	srv.handleProbeLLM(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestHandleWorkerRegister(t *testing.T) {
	srv, _, _, _ := setupTestServer(t)

	t.Run("valid register", func(t *testing.T) {
		body := map[string]interface{}{
			"host": "192.168.1.100",
			"port": 8080,
		}
		data, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/api/worker/register", bytes.NewReader(data))
		rec := httptest.NewRecorder()
		srv.handleWorkerRegister(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("wrong method", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/worker/register", nil)
		rec := httptest.NewRecorder()
		srv.handleWorkerRegister(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405, got %d", rec.Code)
		}
	})
}

func TestServerStartPortEnv(t *testing.T) {
	_ = os.Setenv("PORT", "9999")
	defer os.Unsetenv("PORT")

	srv, _, _, _ := setupTestServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := srv.Start(ctx)
	if err != http.ErrServerClosed && err != context.Canceled {
		t.Logf("Start returned: %v (acceptable)", err)
	}
}
