package view

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/controller"
	"github.com/thiagohmm/tiktok-live-monitor/internal/database"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"github.com/thiagohmm/tiktok-live-monitor/internal/monitor"
)

// testRef builds a live reference for tests that do not need a real session row.
func testRef(name string) model.LiveRef {
	return model.LiveRef{ID: name + "-session", Name: name}
}

// testDB exposes the concrete repository for test-only SQL.
func testDB(t *testing.T, repo model.Repository) *database.DB {
	t.Helper()
	db, ok := repo.(*database.DB)
	if !ok {
		t.Fatalf("unexpected repository type %T", repo)
	}
	return db
}

// openTestSession opens a session whose id is testRef(name).ID, so rows seeded
// with testRef(name) belong to the live under test.
func openTestSession(t *testing.T, repo model.Repository, name string) {
	t.Helper()
	now := time.Now().UTC()
	if err := testDB(t, repo).ExecSQL(
		`INSERT INTO live_sessions (id, live_name, day, started_at, last_seen_at) VALUES (?, ?, ?, ?, ?)`,
		testRef(name).ID, name, now.Format("2006-01-02"), now, now,
	); err != nil {
		t.Fatalf("open test session %s: %v", name, err)
	}
}

func setupTestServer(t *testing.T) (*HTTPServer, model.Repository, string, *monitor.Monitor) {
	t.Helper()
	dir := t.TempDir()

	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("TEST_DATABASE_URL não configurado: testes exigem PostgreSQL descartável")
	}
	dsn, cleanup, err := database.CreateTestDatabase(baseDSN)
	if err != nil {
		t.Fatalf("create test database: %v", err)
	}
	t.Cleanup(cleanup)
	repo, err := database.OpenPostgres(dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { repo.Close() })

	mon, err := monitor.New()
	if err != nil {
		t.Skipf("skip test (TikTok API unavailable): %v", err)
	}

	ctrl := controller.NewAppController(mon, repo)

	// StartMonitoring is what opens the session in production; the harness sets a
	// live and opens its session so the event/goal handlers resolve one.
	mon.SetCurrentLive("live1")
	openTestSession(t, repo, "live1")

	srv := New(Config{
		Host: "127.0.0.1",
		Port: 0,
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

	err := db.LogAnomaly(testRef("live1"), "test msg", true, "SPAM", "user1")
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

	err := db.LogAnomaly(testRef("live1"), "test msg", true, "SPAM", "user1")
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
		_ = db.LogAnomaly(testRef("live1"), "msg", false, "OK", "user1")
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

func TestHandleGifts(t *testing.T) {
	srv, db, _, mon := setupTestServer(t)

	_, _ = db.AddGift(testRef("live1"), "user1", "User One", "Rose", 3, 0)
	_, _ = db.AddGift(testRef("live1"), "user2", "User Two", "Tiger", 1, 1)

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
		"repeatEnd":   true,
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
	if byName["Rosa"].RepeatCount != 5 {
		t.Fatalf("expected Rosa repeatCount 5, got %d", byName["Rosa"].RepeatCount)
	}
	if byName["Dino"].UniqueID != "unknown" {
		t.Fatalf("expected unknown uniqueId, got %q", byName["Dino"].UniqueID)
	}
}

func TestHandleGiftEventSkipsStreakInProgress(t *testing.T) {
	srv, db, _, mon := setupTestServer(t)
	mon.SetCurrentLive("live1")

	srv.controller.HandleGiftEvent(monitor.EventData{
		"uniqueId":    "user1",
		"nickname":    "User One",
		"giftName":    "Rose",
		"repeatCount": float64(3),
		"giftType":    float64(1),
		"repeatEnd":   false,
	})
	srv.controller.HandleGiftEvent(monitor.EventData{
		"uniqueId":    "user1",
		"nickname":    "User One",
		"giftName":    "Rose",
		"repeatCount": float64(3),
		"giftType":    float64(1),
		"repeatEnd":   true,
	})

	gifts, err := db.GetRecentGifts("live1", 10)
	if err != nil {
		t.Fatalf("get gifts: %v", err)
	}
	if len(gifts) != 1 {
		t.Fatalf("expected 1 stored gift after streak settlement, got %d", len(gifts))
	}
	if gifts[0].RepeatCount != 3 {
		t.Fatalf("expected repeatCount 3, got %d", gifts[0].RepeatCount)
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
		if _, err := db.AddPinnedComment(testRef("live1"), "user1", "User One", "olá", "pin-1", nil, time.Now()); err != nil {
			t.Fatalf("add pinned: %v", err)
		}
		if _, err := db.AddPinnedComment(testRef("live2"), "user2", "User Two", "outra", "pin-2", nil, time.Now()); err != nil {
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

	pendingID, err := db.AddTargetGiftHistory(testRef("live1"), "user1", "User One", "Rosa", time.Now(), false)
	if err != nil {
		t.Fatalf("add pending: %v", err)
	}
	answeredID, err := db.AddTargetGiftHistory(testRef("live1"), "user2", "User Two", "Dino", time.Now(), false)
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

func TestHandleTargetGiftHistoryPriority(t *testing.T) {
	srv, db, _, mon := setupTestServer(t)
	mon.SetCurrentLive("live1")

	id, err := db.AddTargetGiftHistory(testRef("live1"), "user1", "User One", "Rosa", time.Now(), false)
	if err != nil {
		t.Fatalf("add pending: %v", err)
	}

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/target-gift-history/priority", strings.NewReader(body))
		rec := httptest.NewRecorder()
		srv.handleTargetGiftHistoryPriority(rec, req)
		return rec
	}

	// Validações de entrada.
	if rec := post(`{"id": 123"`); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid body: expected 400, got %d", rec.Code)
	}
	if rec := post(`{"id": ` + strconv.FormatInt(id, 10) + `}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing priority: expected 400, got %d", rec.Code)
	}
	if rec := post(`{"id": 0, "priority": true}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("zero id: expected 400, got %d", rec.Code)
	}
	if rec := post(`{"id": -1, "priority": true}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("negative id: expected 400, got %d", rec.Code)
	}
	if rec := post(`{"id": ` + strconv.FormatInt(id, 10) + `, "priority": "true"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("non-boolean priority: expected 400, got %d", rec.Code)
	}

	// Promover um presente pendente.
	rec := post(`{"id": ` + strconv.FormatInt(id, 10) + `, "priority": true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("promote: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var result struct {
		Success    bool       `json:"success"`
		IsPriority bool       `json:"isPriority"`
		PriorityAt *time.Time `json:"priorityAt"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || !result.Success || !result.IsPriority || result.PriorityAt == nil {
		t.Fatalf("expected success=true, got %s", rec.Body.String())
	}

	// Flag persistida (sobrevive a re-query / reconexão).
	items, err := db.GetPendingTargetGiftHistory("live1", 50)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(items) != 1 || !items[0].IsPriority || items[0].PriorityAt == nil {
		t.Fatalf("expected priority flag persisted, got %+v", items)
	}
	persistedAt, err := time.Parse(time.RFC3339Nano, *items[0].PriorityAt)
	if err != nil || !persistedAt.Equal(*result.PriorityAt) {
		t.Fatalf("API stamp %v differs from persisted stamp %v: %v", result.PriorityAt, items[0].PriorityAt, err)
	}

	// Despromover.
	rec = post(`{"id": ` + strconv.FormatInt(id, 10) + `, "priority": false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("demote: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || !result.Success || result.IsPriority || result.PriorityAt != nil {
		t.Fatalf("expected demotion with null priorityAt, got %s", rec.Body.String())
	}
	items, err = db.GetPendingTargetGiftHistory("live1", 50)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(items) != 1 || items[0].IsPriority || items[0].PriorityAt != nil {
		t.Fatalf("expected priority cleared, got %+v", items)
	}

	// Linha inexistente → 404 (promote e demote).
	if rec := post(`{"id": 999999, "priority": true}`); rec.Code != http.StatusNotFound {
		t.Fatalf("missing id promote: expected 404, got %d", rec.Code)
	}
	if rec := post(`{"id": 999999, "priority": false}`); rec.Code != http.StatusNotFound {
		t.Fatalf("missing id demote: expected 404, got %d", rec.Code)
	}

	// Linha já respondida não pode ser promovida → 404.
	if err := db.MarkTargetGiftAnswered(id, model.TargetGiftResponseManual, time.Now()); err != nil {
		t.Fatalf("mark answered: %v", err)
	}
	if rec := post(`{"id": ` + strconv.FormatInt(id, 10) + `, "priority": true}`); rec.Code != http.StatusNotFound {
		t.Fatalf("answered row: expected 404, got %d", rec.Code)
	}

	// Método não permitido.
	req := httptest.NewRequest(http.MethodGet, "/api/target-gift-history/priority", nil)
	rec = httptest.NewRecorder()
	srv.handleTargetGiftHistoryPriority(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: expected 405, got %d", rec.Code)
	}
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

func TestHandleAdminLives(t *testing.T) {
	srv, repo, _, _ := setupTestServer(t)

	session, err := repo.BeginLiveSession("liveA", time.Now())
	if err != nil {
		t.Fatalf("begin session: %v", err)
	}
	if _, err := repo.AddGift(model.LiveRef{ID: session.ID, Name: "liveA"}, "u1", "User", "rose", 1, 0); err != nil {
		t.Fatalf("seed gift: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/lives", nil)
	rec := httptest.NewRecorder()
	srv.handleAdminLives(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var result struct {
		Lives []model.Live `json:"lives"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if len(result.Lives) != 2 {
		t.Fatalf("expected 2 lives (harness + liveA), got %d", len(result.Lives))
	}
	var found *model.Live
	for i := range result.Lives {
		if result.Lives[i].Name == "liveA" {
			found = &result.Lives[i]
		}
	}
	if found == nil {
		t.Fatalf("expected liveA in %#v", result.Lives)
	}
	if found.ID != session.ID {
		t.Fatalf("expected session id %q, got %q", session.ID, found.ID)
	}
	if found.Day == "" {
		t.Fatal("expected non-empty day")
	}
	if found.Events != 1 {
		t.Fatalf("expected 1 event, got %d", found.Events)
	}
}

func TestHandleAdminLivesMethodNotAllowed(t *testing.T) {
	srv, _, _, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/admin/lives", nil)
	rec := httptest.NewRecorder()
	srv.handleAdminLives(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestHandleAdminLivesEmptyDB(t *testing.T) {
	srv, repo, _, _ := setupTestServer(t)

	// The harness opens a session; this test is about the empty case.
	if err := testDB(t, repo).ExecSQL("DELETE FROM live_sessions"); err != nil {
		t.Fatalf("clear sessions: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/lives", nil)
	rec := httptest.NewRecorder()
	srv.handleAdminLives(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var result struct {
		Lives []model.Live `json:"lives"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if result.Lives == nil || len(result.Lives) != 0 {
		t.Fatalf("expected empty lives list, got %#v", result.Lives)
	}
}

// TestHandleAdminLivesSessionDelete reproduces the reported bug through the API:
// two lives of the SAME streamer on the same day, deleting one row must keep the
// other (and its events).
func TestHandleAdminLivesSessionDelete(t *testing.T) {
	srv, repo, _, _ := setupTestServer(t)

	now := time.Now()
	target, err := repo.BeginLiveSession("liveA", now)
	if err != nil {
		t.Fatalf("begin target: %v", err)
	}
	if err := repo.EndLiveSession(target.ID, now.Add(time.Minute)); err != nil {
		t.Fatalf("end target: %v", err)
	}
	kept, err := repo.BeginLiveSession("liveA", now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("begin kept: %v", err)
	}
	if _, err := repo.AddGift(model.LiveRef{ID: target.ID, Name: "liveA"}, "u1", "User", "rose", 1, 0); err != nil {
		t.Fatalf("seed target gift: %v", err)
	}
	if _, err := repo.AddGift(model.LiveRef{ID: kept.ID, Name: "liveA"}, "u2", "User", "rose", 1, 0); err != nil {
		t.Fatalf("seed kept gift: %v", err)
	}

	deleteURL := func(query string) string {
		return "/api/admin/lives/session/delete?" + query
	}

	// missing params -> 400
	for _, query := range []string{
		"",
		"live=liveA&day=" + target.Day,
		"id=" + target.ID + "&day=" + target.Day,
		"id=" + target.ID + "&live=liveA",
	} {
		rec := httptest.NewRecorder()
		srv.handleAdminLivesSessionDelete(rec, httptest.NewRequest(http.MethodPost, deleteURL(query), nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("query %q: expected 400, got %d", query, rec.Code)
		}
	}

	// unknown id -> 404
	rec := httptest.NewRecorder()
	srv.handleAdminLivesSessionDelete(rec, httptest.NewRequest(
		http.MethodPost, deleteURL("id=does-not-exist&live=liveA&day="+target.Day), nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	// id does not match live/day -> 409 (and nothing deleted)
	for _, query := range []string{
		"id=" + target.ID + "&live=liveB&day=" + target.Day,
		"id=" + target.ID + "&live=liveA&day=1999-01-01",
	} {
		rec := httptest.NewRecorder()
		srv.handleAdminLivesSessionDelete(rec, httptest.NewRequest(http.MethodPost, deleteURL(query), nil))
		if rec.Code != http.StatusConflict {
			t.Fatalf("query %q: expected 409, got %d", query, rec.Code)
		}
	}
	if _, err := repo.GetLiveSession(target.ID); err != nil {
		t.Fatalf("session must survive a rejected delete: %v", err)
	}

	// wrong method -> 405
	rec = httptest.NewRecorder()
	srv.handleAdminLivesSessionDelete(rec, httptest.NewRequest(
		http.MethodGet, deleteURL("id="+target.ID+"&live=liveA&day="+target.Day), nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}

	// happy path -> 200, deleting the target session (gift + session row)
	rec = httptest.NewRecorder()
	srv.handleAdminLivesSessionDelete(rec, httptest.NewRequest(
		http.MethodPost, deleteURL("id="+target.ID+"&live=liveA&day="+target.Day), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var result struct {
		Deleted int64  `json:"deleted"`
		ID      string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if result.ID != target.ID {
		t.Fatalf("expected id %q, got %q", target.ID, result.ID)
	}
	if result.Deleted != 2 {
		t.Fatalf("expected 2 deleted rows (gift + session), got %d", result.Deleted)
	}
	if _, err := repo.GetLiveSession(target.ID); err != model.ErrLiveSessionNotFound {
		t.Fatalf("expected target session to be gone, got %v", err)
	}

	// The other live of the same streamer is untouched.
	if _, err := repo.GetLiveSession(kept.ID); err != nil {
		t.Fatalf("expected the kept session to survive: %v", err)
	}
	gifts, err := repo.GetRecentGifts("liveA", 10)
	if err != nil {
		t.Fatalf("gifts: %v", err)
	}
	if len(gifts) != 1 || gifts[0].UniqueID != "u2" {
		t.Fatalf("expected only the kept session's gift, got %#v", gifts)
	}
}

// The retired path must never delete anything: an old client hitting it fails
// safely instead of wiping the streamer's history.
func TestHandleAdminLivesDeleteIsGone(t *testing.T) {
	srv, repo, _, _ := setupTestServer(t)

	session, err := repo.BeginLiveSession("liveA", time.Now())
	if err != nil {
		t.Fatalf("begin session: %v", err)
	}
	if _, err := repo.AddGift(model.LiveRef{ID: session.ID, Name: "liveA"}, "u1", "User", "rose", 1, 0); err != nil {
		t.Fatalf("seed gift: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.handleAdminLivesDelete(rec, httptest.NewRequest(http.MethodPost, "/api/admin/lives/delete?live=liveA", nil))
	if rec.Code != http.StatusGone {
		t.Fatalf("expected 410, got %d", rec.Code)
	}

	if _, err := repo.GetLiveSession(session.ID); err != nil {
		t.Fatalf("expected nothing to be deleted, got %v", err)
	}
	gifts, err := repo.GetRecentGifts("liveA", 10)
	if err != nil {
		t.Fatalf("gifts: %v", err)
	}
	if len(gifts) != 1 {
		t.Fatalf("expected the gift to survive, got %d", len(gifts))
	}
}

func TestReportExternalFlagPipeline(t *testing.T) {
	srv, db, _, mon := setupTestServer(t)
	mon.SetCurrentLive("live1")

	var gotType string
	var gotData monitor.EventData
	mon.OnEvent(func(eventType string, data monitor.EventData) {
		if eventType == monitor.EventFlaggedMessage {
			gotType = eventType
			gotData = data
		}
	})

	t.Run("emit flag", func(t *testing.T) {
		srv.controller.ReportExternalFlag(monitor.EventData{
			"comment":  "vai embora seu lixo",
			"uniqueId": "user1",
			"nickname": "User One",
			"category": "ODIO",
			"reason":   "Odio",
		})
		if gotType != monitor.EventFlaggedMessage {
			t.Fatalf("expected flagged-message event, got %q", gotType)
		}
		if gotData["category"] != "ODIO" {
			t.Fatalf("expected ODIO, got %v", gotData["category"])
		}
		logs, err := db.GetRecentModerations(10)
		if err != nil || len(logs) != 1 {
			t.Fatalf("expected 1 anomaly log, got %d err=%v", len(logs), err)
		}
	})

	t.Run("ignored when moderation disabled", func(t *testing.T) {
		srv.controller.SetSettings(monitor.Settings{ModerationEnabled: false})
		srv.controller.ReportExternalFlag(monitor.EventData{
			"comment":  "outra msg",
			"uniqueId": "user2",
			"category": "SPAM",
		})
		logs, err := db.GetRecentModerations(10)
		if err != nil || len(logs) != 1 {
			t.Fatalf("expected flag ignored, got %d logs err=%v", len(logs), err)
		}
	})
}

// postJSON invokes a handler directly with a POST request carrying a JSON body.
func postJSON(t *testing.T, handler func(http.ResponseWriter, *http.Request), path string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func TestHandleGoals(t *testing.T) {
	srv, repo, _, mon := setupTestServer(t)
	mon.SetCurrentLive("live1")

	t.Run("POST create", func(t *testing.T) {
		body := map[string]interface{}{
			"title":       "Meta da noite",
			"targetUnits": 100,
			"milestones": []map[string]interface{}{
				{"atUnits": 50, "reward": "música especial"},
			},
		}
		rec := postJSON(t, srv.handleGoals, "/api/goals", body)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
		}
		var goal model.GiftGoal
		if err := json.Unmarshal(rec.Body.Bytes(), &goal); err != nil {
			t.Fatalf("decode JSON: %v", err)
		}
		if goal.ID <= 0 || goal.Status != model.GoalStatusActive {
			t.Fatalf("unexpected goal: %+v", goal)
		}
		if len(goal.Milestones) != 1 || goal.Milestones[0].Reward != "música especial" {
			t.Fatalf("unexpected milestones: %+v", goal.Milestones)
		}
	})

	t.Run("GET state with progress", func(t *testing.T) {
		if _, err := repo.AddGift(testRef("live1"), "u1", "User One", "Rosa", 60, 0); err != nil {
			t.Fatalf("seed gift: %v", err)
		}
		req := httptest.NewRequest(http.MethodGet, "/api/goals", nil)
		rec := httptest.NewRecorder()
		srv.handleGoals(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
		}
		var st controller.GoalsState
		if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
			t.Fatalf("decode JSON: %v", err)
		}
		if st.Active == nil {
			t.Fatal("expected active goal")
		}
		if st.Active.Units != 60 || st.Active.Percent != 60 {
			t.Fatalf("expected 60/60, got %d/%.1f", st.Active.Units, st.Active.Percent)
		}
	})

	t.Run("POST update", func(t *testing.T) {
		st, err := srv.controller.GetGoalsState()
		if err != nil || st.Active == nil {
			t.Fatalf("expected active goal: %v", err)
		}
		body := map[string]interface{}{
			"id":          st.Active.Goal.ID,
			"title":       "Meta editada",
			"targetUnits": 200,
		}
		rec := postJSON(t, srv.handleGoals, "/api/goals", body)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
		}
		var goal model.GiftGoal
		if err := json.Unmarshal(rec.Body.Bytes(), &goal); err != nil {
			t.Fatalf("decode JSON: %v", err)
		}
		if goal.Title != "Meta editada" || goal.TargetUnits != 200 {
			t.Fatalf("unexpected goal: %+v", goal)
		}
	})

	t.Run("POST complete", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/goals", nil)
		rec := httptest.NewRecorder()
		srv.handleGoals(rec, req)
		var st controller.GoalsState
		if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
			t.Fatalf("decode JSON: %v", err)
		}
		if st.Active == nil {
			t.Fatalf("expected an active goal to complete: %+v", st)
		}
		req = httptest.NewRequest(http.MethodPost, "/api/goals/complete?id="+strconv.FormatInt(st.Active.Goal.ID, 10), nil)
		rec = httptest.NewRecorder()
		srv.handleGoalComplete(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
		}
		req = httptest.NewRequest(http.MethodGet, "/api/goals", nil)
		rec = httptest.NewRecorder()
		srv.handleGoals(rec, req)
		st = controller.GoalsState{}
		if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
			t.Fatalf("decode JSON: %v", err)
		}
		if st.Active != nil || len(st.History) != 1 || st.History[0].Status != model.GoalStatusCompleted {
			t.Fatalf("expected completed goal in history: %+v", st)
		}
	})

	t.Run("POST cancel", func(t *testing.T) {
		body := map[string]interface{}{"title": "nova meta", "targetUnits": 10}
		createRec := postJSON(t, srv.handleGoals, "/api/goals", body)
		if createRec.Code != http.StatusOK {
			t.Fatalf("create: expected 200, got %d body=%s", createRec.Code, createRec.Body.String())
		}
		var created model.GiftGoal
		if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
			t.Fatalf("decode JSON: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/goals/cancel?id="+strconv.FormatInt(created.ID, 10), nil)
		rec := httptest.NewRecorder()
		srv.handleGoalCancel(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
		}
		req = httptest.NewRequest(http.MethodGet, "/api/goals", nil)
		rec = httptest.NewRecorder()
		srv.handleGoals(rec, req)
		var st controller.GoalsState
		if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
			t.Fatalf("decode JSON: %v", err)
		}
		if st.Active != nil {
			t.Fatalf("expected no active goal, got %+v", st.Active)
		}
		if len(st.History) != 2 || st.History[0].Status != model.GoalStatusCancelled {
			t.Fatalf("expected cancelled goal in history: %+v", st.History)
		}
	})

	t.Run("validation errors", func(t *testing.T) {
		if rec := postJSON(t, srv.handleGoals, "/api/goals", map[string]interface{}{"title": " ", "targetUnits": 10}); rec.Code != http.StatusBadRequest {
			t.Fatalf("empty title: expected 400, got %d", rec.Code)
		}
		if rec := postJSON(t, srv.handleGoals, "/api/goals", map[string]interface{}{"title": "m", "targetUnits": 0}); rec.Code != http.StatusBadRequest {
			t.Fatalf("zero target: expected 400, got %d", rec.Code)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/goals/cancel", nil)
		rec := httptest.NewRecorder()
		srv.handleGoalCancel(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("cancel without active: expected 400, got %d", rec.Code)
		}
		req = httptest.NewRequest(http.MethodGet, "/api/goals/cancel", nil)
		rec = httptest.NewRecorder()
		srv.handleGoalCancel(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("wrong method: expected 405, got %d", rec.Code)
		}
		req = httptest.NewRequest(http.MethodDelete, "/api/goals", nil)
		rec = httptest.NewRecorder()
		srv.handleGoals(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("wrong method: expected 405, got %d", rec.Code)
		}
	})
}

func TestGoalSSEBroadcast(t *testing.T) {
	_ = os.Setenv("PORT", "19855")
	defer os.Unsetenv("PORT")

	srv, _, _, mon := setupTestServer(t)
	mon.SetCurrentLive("live1")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Start(ctx)

	base := "http://127.0.0.1:19855"
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get(base + "/api/readiness")
		if err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("server did not start")
		}
		time.Sleep(50 * time.Millisecond)
	}

	sseReq, err := http.NewRequestWithContext(context.Background(), http.MethodGet, base+"/events", nil)
	if err != nil {
		t.Fatalf("new SSE request: %v", err)
	}
	sseResp, err := http.DefaultClient.Do(sseReq)
	if err != nil {
		t.Fatalf("SSE connect: %v", err)
	}
	defer sseResp.Body.Close()

	events := make(chan string, 64)
	go func() {
		scanner := bufio.NewScanner(sseResp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "event: ") {
				events <- strings.TrimPrefix(line, "event: ")
			}
		}
	}()

	waitEvent := func(name string) bool {
		for {
			select {
			case e, ok := <-events:
				if !ok {
					return false
				}
				if e == name {
					return true
				}
			case <-time.After(5 * time.Second):
				return false
			}
		}
	}

	if !waitEvent("server-state") {
		t.Fatal("expected initial server-state SSE event")
	}

	resp, err := http.Post(base+"/api/goals", "application/json",
		strings.NewReader(`{"title":"meta","targetUnits":100,"milestones":[{"atUnits":50,"reward":"prêmio"}]}`))
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	resp.Body.Close()

	srv.controller.HandleGiftEvent(monitor.EventData{
		"uniqueId": "u1", "nickname": "User", "giftName": "Rose",
		"repeatCount": 150, "repeatEnd": true,
	})

	seen := map[string]bool{}
	for !seen["goal-completed"] {
		select {
		case e, ok := <-events:
			if !ok {
				t.Fatalf("SSE stream closed; saw %v", seen)
			}
			seen[e] = true
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out; saw %v", seen)
		}
	}
	if !seen["goal-update"] {
		t.Fatalf("expected goal-update, saw %v", seen)
	}
	if !seen["goal-unlocked"] {
		t.Fatalf("expected goal-unlocked, saw %v", seen)
	}
}
