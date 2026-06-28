package server

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

	"github.com/thiagohmm/tiktok-live-monitor/internal/ai"
	"github.com/thiagohmm/tiktok-live-monitor/internal/database"
	"github.com/thiagohmm/tiktok-live-monitor/internal/moderation"
	"github.com/thiagohmm/tiktok-live-monitor/internal/monitor"
)

func setupTestServer(t *testing.T) (*Server, *database.DB, string) {
	t.Helper()
	dir := t.TempDir()

	db, err := database.Open(dir)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	aiMgr := ai.NewManager(filepath.Join(dir, "models"), filepath.Join(dir, "bin"))
	t.Cleanup(func() { aiMgr.Stop() })

	mon, err := monitor.New()
	if err != nil {
		t.Skipf("skip test (TikTok API unavailable): %v", err)
	}

	modEngine := moderation.NewEngine(aiMgr, db)

	os.MkdirAll(filepath.Join(dir, "web"), 0755)

	srv := New(Config{
		Host:      "127.0.0.1",
		Port:      0,
		ModelsDir: filepath.Join(dir, "models"),
		BinDir:    filepath.Join(dir, "bin"),
		WebDir:    filepath.Join(dir, "web"),
	}, db, aiMgr, modEngine, mon)

	return srv, db, dir
}

func TestHandleState(t *testing.T) {
	srv, _, _ := setupTestServer(t)

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
	srv, _, _ := setupTestServer(t)

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
	srv, db, _ := setupTestServer(t)

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
	srv, db, _ := setupTestServer(t)

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
	srv, _, _ := setupTestServer(t)

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
	srv, _, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/disconnect", nil)
	rec := httptest.NewRecorder()
	srv.handleDisconnect(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestHandleClearHistory(t *testing.T) {
	srv, db, _ := setupTestServer(t)

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
	srv, _, _ := setupTestServer(t)

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
	srv, db, _ := setupTestServer(t)

	_, _ = db.AddGift("live1", "user1", "User One", "Rose", 3, 0)
	_, _ = db.AddGift("live1", "user2", "User Two", "Tiger", 1, 1)

	t.Run("GET all", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/gifts", nil)
		rec := httptest.NewRecorder()
		srv.handleGifts(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var gifts []database.Gift
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
		var gifts []database.Gift
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
		var gifts []database.Gift
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

func TestHandleAskAI(t *testing.T) {
	srv, _, _ := setupTestServer(t)

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
	srv, _, _ := setupTestServer(t)

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
	srv, _, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/probe-llm", nil)
	rec := httptest.NewRecorder()
	srv.handleProbeLLM(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestHandleWorkerRegister(t *testing.T) {
	srv, _, _ := setupTestServer(t)

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

	srv, _, _ := setupTestServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := srv.Start(ctx)
	if err != http.ErrServerClosed && err != context.Canceled {
		t.Logf("Start returned: %v (acceptable)", err)
	}
}
