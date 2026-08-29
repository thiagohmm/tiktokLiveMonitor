package database

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpenAndMigrate(t *testing.T) {
	db := openTestDB(t)
	if db == nil {
		t.Fatal("expected non-nil db")
	}
}

func TestGetFalsePositiveComments(t *testing.T) {
	db := openTestDB(t)

	seed := func(comment, category, expected string) {
		t.Helper()
		if _, err := db.conn.Exec(
			"INSERT INTO false_positives (comment, category, expected) VALUES (?, ?, ?)",
			comment, category, expected,
		); err != nil {
			t.Fatalf("seed feedback %q: %v", comment, err)
		}
	}
	seed("jesus te ama", "PROSELITISMO", "NAO")
	seed("jesus te ama", "PROSELITISMO", "NAO")
	seed("clica no link", "SPAM", "NAO")
	seed("isto é spam mesmo", "SPAM", "SIM_SPAM")

	comments, err := db.GetFalsePositiveComments(10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("expected 2 distinct comments, got %d", len(comments))
	}

	got := make(map[string]bool, len(comments))
	for _, c := range comments {
		got[c] = true
	}
	if !got["jesus te ama"] || !got["clica no link"] {
		t.Fatalf("unexpected comments: %v", comments)
	}
}

func TestAddUserMessageDedup(t *testing.T) {
	db := openTestDB(t)

	err := db.AddUserMessageDedup("live1", "user1", "User One", "Hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = db.AddUserMessageDedup("live1", "user1", "User One", "Hello")
	if err != nil {
		t.Fatalf("unexpected error for duplicate: %v", err)
	}

	msgs, err := db.GetUserMessages("user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
}

func TestAddUserMessageDedupCaseInsensitive(t *testing.T) {
	db := openTestDB(t)

	err := db.AddUserMessageDedup("live1", "user1", "User One", "Hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = db.AddUserMessageDedup("live1", "USER1", "User One", "HELLO")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs, err := db.GetUserMessages("user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message (dedup), got %d", len(msgs))
	}
}

func TestAddUserMessageDedupMax10FIFO(t *testing.T) {
	db := openTestDB(t)

	for i := 0; i < 15; i++ {
		err := db.AddUserMessageDedup("live1", "user1", "User One", "msg")
		if err != nil {
			t.Fatalf("add message %d: %v", i, err)
		}
	}

	msgs, err := db.GetUserMessages("user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) > 10 {
		t.Fatalf("expected at most 10 messages, got %d", len(msgs))
	}
}

func TestAddUserMessageDedupEmpty(t *testing.T) {
	db := openTestDB(t)

	err := db.AddUserMessageDedup("live1", "", "User", "msg")
	if err != nil {
		t.Fatalf("expected nil for empty uniqueID, got: %v", err)
	}

	err = db.AddUserMessageDedup("live1", "user1", "User", "")
	if err != nil {
		t.Fatalf("expected nil for empty message, got: %v", err)
	}

	msgs, err := db.GetUserMessages("user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected 0 messages, got %d", len(msgs))
	}
}

func TestGetUserMessagesEmpty(t *testing.T) {
	db := openTestDB(t)

	_, err := db.GetUserMessages("")
	if err == nil {
		t.Fatal("expected error for empty uniqueId")
	}
}

func TestGetAllUserMessages(t *testing.T) {
	db := openTestDB(t)

	err := db.AddUserMessageDedup("live1", "user1", "User One", "msg1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	err = db.AddUserMessageDedup("live1", "user2", "User Two", "msg2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	all, err := db.GetAllUserMessages()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 users, got %d", len(all))
	}
	if len(all["user1"]) != 1 {
		t.Fatalf("expected 1 msg for user1, got %d", len(all["user1"]))
	}
	if len(all["user2"]) != 1 {
		t.Fatalf("expected 1 msg for user2, got %d", len(all["user2"]))
	}
}

func TestLogAnomaly(t *testing.T) {
	db := openTestDB(t)

	err := db.LogAnomaly("live1", "bad msg", true, "SPAM", "user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	logs, err := db.GetRecentModerations(10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(logs))
	}
	if !logs[0].IsAnomaly {
		t.Fatal("expected IsAnomaly to be true")
	}
	if logs[0].Category != "SPAM" {
		t.Fatalf("expected SPAM, got %q", logs[0].Category)
	}
}

func TestGetRecentModerationsLimit(t *testing.T) {
	db := openTestDB(t)

	for i := 0; i < 5; i++ {
		err := db.LogAnomaly("live1", "msg", false, "OK", "user1")
		if err != nil {
			t.Fatalf("log %d: %v", i, err)
		}
	}

	logs, err := db.GetRecentModerations(3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(logs) != 3 {
		t.Fatalf("expected 3, got %d", len(logs))
	}
}

func TestDeleteModeration(t *testing.T) {
	db := openTestDB(t)

	err := db.LogAnomaly("live1", "msg", true, "SPAM", "user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	logs, _ := db.GetRecentModerations(10)
	id := logs[0].ID

	deleted, err := db.DeleteModeration(id)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted, got %d", deleted)
	}

	logs, _ = db.GetRecentModerations(10)
	if len(logs) != 0 {
		t.Fatalf("expected 0 logs, got %d", len(logs))
	}
}

func TestDeleteModerationInvalid(t *testing.T) {
	db := openTestDB(t)

	_, err := db.DeleteModeration(0)
	if err == nil {
		t.Fatal("expected error for id 0")
	}

	_, err = db.DeleteModeration(-1)
	if err == nil {
		t.Fatal("expected error for negative id")
	}
}

func TestClearHistory(t *testing.T) {
	db := openTestDB(t)

	for i := 0; i < 5; i++ {
		err := db.LogAnomaly("live1", "msg", false, "OK", "user1")
		if err != nil {
			t.Fatalf("log %d: %v", i, err)
		}
	}

	deleted, err := db.ClearHistory()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deleted != 5 {
		t.Fatalf("expected 5 deleted, got %d", deleted)
	}
}

func TestAddGift(t *testing.T) {
	db := openTestDB(t)

	id, err := db.AddGift("live1", "user1", "User One", "Rose", 1, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive id, got %d", id)
	}

	gifts, err := db.GetRecentGifts("live1", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(gifts) != 1 {
		t.Fatalf("expected 1 gift, got %d", len(gifts))
	}
	if gifts[0].GiftName != "Rose" {
		t.Fatalf("expected 'Rose', got %q", gifts[0].GiftName)
	}
}

func TestGetRecentGiftsEmptySlice(t *testing.T) {
	db := openTestDB(t)

	gifts, err := db.GetRecentGifts("missing-live", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gifts == nil {
		t.Fatal("expected empty slice, got nil")
	}
	if len(gifts) != 0 {
		t.Fatalf("expected 0, got %d", len(gifts))
	}
}

func TestGetRecentGiftsLimit(t *testing.T) {
	db := openTestDB(t)

	for i := 0; i < 15; i++ {
		_, err := db.AddGift("live1", "user1", "User One", "Rose", 1, 0)
		if err != nil {
			t.Fatalf("add gift %d: %v", i, err)
		}
	}

	gifts, err := db.GetRecentGifts("live1", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(gifts) != 5 {
		t.Fatalf("expected 5, got %d", len(gifts))
	}
}

func TestGetGiftsByUser(t *testing.T) {
	db := openTestDB(t)

	_, err := db.AddGift("live1", "user1", "User One", "Rose", 1, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = db.AddGift("live1", "user2", "User Two", "Tiger", 2, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gifts, err := db.GetGiftsByUser("user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(gifts) != 1 {
		t.Fatalf("expected 1 gift, got %d", len(gifts))
	}
	if gifts[0].GiftName != "Rose" {
		t.Fatalf("expected 'Rose', got %q", gifts[0].GiftName)
	}
}

func TestGetGiftsByUserEmpty(t *testing.T) {
	db := openTestDB(t)

	_, err := db.GetGiftsByUser("")
	if err == nil {
		t.Fatal("expected error for empty uniqueId")
	}
}

func TestGetGiftSummary(t *testing.T) {
	db := openTestDB(t)

	_, err := db.AddGift("live1", "user1", "User One", "Rose", 3, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = db.AddGift("live1", "user1", "User One", "Rose", 2, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = db.AddGift("live1", "user2", "User Two", "Tiger", 1, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	summary, err := db.GetGiftSummary()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summary) != 2 {
		t.Fatalf("expected 2 users, got %d", len(summary))
	}
	if summary["user1"]["Rose"] != 5 {
		t.Fatalf("expected user1 Rose=5, got %d", summary["user1"]["Rose"])
	}
	if summary["user2"]["Tiger"] != 1 {
		t.Fatalf("expected user2 Tiger=1, got %d", summary["user2"]["Tiger"])
	}
}

func TestGetGiftSummaryEmpty(t *testing.T) {
	db := openTestDB(t)

	summary, err := db.GetGiftSummary()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summary) != 0 {
		t.Fatalf("expected empty summary, got %d entries", len(summary))
	}
}

func TestClearGifts(t *testing.T) {
	db := openTestDB(t)

	for i := 0; i < 5; i++ {
		_, err := db.AddGift("live1", "user1", "User One", "Rose", 1, 0)
		if err != nil {
			t.Fatalf("add gift %d: %v", i, err)
		}
	}

	deleted, err := db.ClearGifts()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deleted != 5 {
		t.Fatalf("expected 5 deleted, got %d", deleted)
	}

	gifts, _ := db.GetRecentGifts("", 10)
	if len(gifts) != 0 {
		t.Fatalf("expected 0 gifts, got %d", len(gifts))
	}
}

func TestCleanupOldAnomalies(t *testing.T) {
	db := openTestDB(t)

	_, err := db.conn.Exec("INSERT INTO anomaly_logs (live_name, day, comment, is_anomaly, category) VALUES ('live1', '2020-01-01', 'old', 1, 'SPAM')")
	if err != nil {
		t.Fatalf("insert old: %v", err)
	}

	// day is stored in UTC; seed 'today' with date('now') so the test
	// is independent of the host timezone.
	_, err = db.conn.Exec("INSERT INTO anomaly_logs (live_name, day, comment, is_anomaly, category) VALUES ('live1', date('now'), 'new', 0, 'OK')")
	if err != nil {
		t.Fatalf("insert new: %v", err)
	}

	var deleted int64
	deleted, err = db.CleanupOldAnomalies()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted, got %d", deleted)
	}
}

func TestDatabaseFilePath(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	db.Close()

	_, err = os.Stat(dir + "/feedback.db")
	if err != nil {
		t.Fatalf("expected feedback.db to exist: %v", err)
	}
}

func countTable(t *testing.T, db *DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.conn.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count query: %v", err)
	}
	return n
}

func TestGetLastSessionActivityEmpty(t *testing.T) {
	db := openTestDB(t)

	_, ok, err := db.GetLastSessionActivity("live1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected no session activity")
	}
}

func TestGetLastSessionActivityUsesLatestAcrossTables(t *testing.T) {
	db := openTestDB(t)

	_, err := db.conn.Exec(
		`INSERT INTO gifts (live_name, uniqueId, nickname, gift_name, timestamp) VALUES (?, ?, ?, ?, ?)`,
		"live1", "user1", "User", "Rose", "2026-08-17 10:00:00",
	)
	if err != nil {
		t.Fatalf("insert gift: %v", err)
	}
	_, err = db.conn.Exec(
		`INSERT INTO anomaly_logs (live_name, day, comment, is_anomaly, category, timestamp) VALUES (?, ?, ?, ?, ?, ?)`,
		"live1", "2026-08-17", "spam", 1, "SPAM", "2026-08-17 11:00:00",
	)
	if err != nil {
		t.Fatalf("insert anomaly: %v", err)
	}
	_, err = db.conn.Exec(
		`INSERT INTO user_messages (uniqueId, username, message, timestamp) VALUES (?, ?, ?, ?)`,
		"user1", "User", "hello", "2026-08-17 12:30:00",
	)
	if err != nil {
		t.Fatalf("insert message: %v", err)
	}
	_, err = db.conn.Exec(
		`INSERT INTO gifts (live_name, uniqueId, nickname, gift_name, timestamp) VALUES (?, ?, ?, ?, ?)`,
		"other", "user2", "Other", "Tiger", "2026-08-17 23:00:00",
	)
	if err != nil {
		t.Fatalf("insert other gift: %v", err)
	}

	last, ok, err := db.GetLastSessionActivity("live1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected session activity")
	}
	want := time.Date(2026, 8, 17, 12, 30, 0, 0, time.UTC)
	if !last.Equal(want) {
		t.Fatalf("expected last activity %v, got %v", want, last)
	}
}

func TestDeleteSessionData(t *testing.T) {
	db := openTestDB(t)

	if _, err := db.AddGift("live1", "user1", "User One", "Rose", 1, 0); err != nil {
		t.Fatalf("add gift live1: %v", err)
	}
	if _, err := db.AddGift("live2", "user2", "User Two", "Tiger", 1, 0); err != nil {
		t.Fatalf("add gift live2: %v", err)
	}
	if err := db.LogAnomaly("live1", "spam", true, "SPAM", "user1"); err != nil {
		t.Fatalf("log live1: %v", err)
	}
	if err := db.LogAnomaly("live2", "ok", false, "OK", "user2"); err != nil {
		t.Fatalf("log live2: %v", err)
	}
	if err := db.AddUserMessageDedup("live1", "user1", "User One", "hello"); err != nil {
		t.Fatalf("add message: %v", err)
	}
	if _, err := db.conn.Exec(
		"INSERT INTO false_positives (comment, category, expected) VALUES (?, ?, ?)",
		"example", "SPAM", "SIM_SPAM",
	); err != nil {
		t.Fatalf("insert feedback: %v", err)
	}
	if _, err := db.AddPinnedComment("live1", "user1", "User One", "fixado live1", "pin-1", nil, time.Now()); err != nil {
		t.Fatalf("add pinned live1: %v", err)
	}
	if _, err := db.AddPinnedComment("live2", "user2", "User Two", "fixado live2", "pin-2", nil, time.Now()); err != nil {
		t.Fatalf("add pinned live2: %v", err)
	}
	if _, err := db.AddTargetGiftHistory("live1", "user1", "User One", "Rosa", time.Now()); err != nil {
		t.Fatalf("add target gift live1: %v", err)
	}
	if _, err := db.AddTargetGiftHistory("live2", "user2", "User Two", "Dino", time.Now()); err != nil {
		t.Fatalf("add target gift live2: %v", err)
	}

	if err := db.DeleteSessionData("live1"); err != nil {
		t.Fatalf("delete session: %v", err)
	}

	if n := countTable(t, db, "SELECT COUNT(*) FROM gifts WHERE live_name = ?", "live1"); n != 0 {
		t.Fatalf("expected 0 gifts for live1, got %d", n)
	}
	if n := countTable(t, db, "SELECT COUNT(*) FROM gifts WHERE live_name = ?", "live2"); n != 1 {
		t.Fatalf("expected 1 gift for live2, got %d", n)
	}
	if n := countTable(t, db, "SELECT COUNT(*) FROM anomaly_logs WHERE live_name = ?", "live1"); n != 0 {
		t.Fatalf("expected 0 anomalies for live1, got %d", n)
	}
	if n := countTable(t, db, "SELECT COUNT(*) FROM anomaly_logs WHERE live_name = ?", "live2"); n != 1 {
		t.Fatalf("expected 1 anomaly for live2, got %d", n)
	}
	if n := countTable(t, db, "SELECT COUNT(*) FROM user_messages"); n != 0 {
		t.Fatalf("expected 0 user messages, got %d", n)
	}
	if n := countTable(t, db, "SELECT COUNT(*) FROM false_positives"); n != 1 {
		t.Fatalf("expected feedback to remain, got %d", n)
	}
	if n := countTable(t, db, "SELECT COUNT(*) FROM pinned_comments WHERE live_name = ?", "live1"); n != 0 {
		t.Fatalf("expected 0 pinned comments for live1, got %d", n)
	}
	if n := countTable(t, db, "SELECT COUNT(*) FROM pinned_comments WHERE live_name = ?", "live2"); n != 1 {
		t.Fatalf("expected 1 pinned comment for live2, got %d", n)
	}
	if n := countTable(t, db, "SELECT COUNT(*) FROM target_gift_history WHERE live_name = ?", "live1"); n != 0 {
		t.Fatalf("expected 0 target gift history for live1, got %d", n)
	}
	if n := countTable(t, db, "SELECT COUNT(*) FROM target_gift_history WHERE live_name = ?", "live2"); n != 1 {
		t.Fatalf("expected 1 target gift history for live2, got %d", n)
	}
}

func TestSessionReuseAgeCases(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name string
		at   time.Time
	}{
		{"same day under 10h", now.Add(-2 * time.Hour)},
		{"same day over 10h", now.Add(-11 * time.Hour)},
		{"different day", now.Add(-25 * time.Hour)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openTestDB(t)
			_, err := db.conn.Exec(
				`INSERT INTO gifts (live_name, uniqueId, nickname, gift_name, timestamp) VALUES (?, ?, ?, ?, ?)`,
				"live1", "user1", "User", "Rose", tt.at.UTC().Format("2006-01-02 15:04:05"),
			)
			if err != nil {
				t.Fatalf("insert gift: %v", err)
			}

			last, ok, err := db.GetLastSessionActivity("live1")
			if err != nil {
				t.Fatalf("last activity: %v", err)
			}
			if !ok {
				t.Fatal("expected last activity")
			}

			wantKeep := sameUTCDay(now, tt.at) && now.Sub(tt.at) < 10*time.Hour
			keep := sameUTCDay(now, last) && now.Sub(last) < 10*time.Hour
			if keep != wantKeep {
				t.Fatalf("keep=%v want %v (last=%v now=%v)", keep, wantKeep, last, now)
			}
			if keep {
				if n := countTable(t, db, "SELECT COUNT(*) FROM gifts WHERE live_name = ?", "live1"); n != 1 {
					t.Fatalf("expected gift to remain, got %d", n)
				}
				return
			}
			if err := db.DeleteSessionData("live1"); err != nil {
				t.Fatalf("delete: %v", err)
			}
			if n := countTable(t, db, "SELECT COUNT(*) FROM gifts WHERE live_name = ?", "live1"); n != 0 {
				t.Fatalf("expected gifts deleted, got %d", n)
			}
		})
	}
}

// sameUTCDay mirrors the production rule: timestamps are stored and read in UTC.
func sameUTCDay(a, b time.Time) bool {
	a = a.UTC()
	b = b.UTC()
	return a.Year() == b.Year() && a.YearDay() == b.YearDay()
}

func TestTargetGiftHistoryFlow(t *testing.T) {
	db := openTestDB(t)
	receivedAt := time.Date(2026, 8, 17, 15, 30, 0, 0, time.UTC)

	id, err := db.AddTargetGiftHistory("live1", "user1", "User One", "Rosa", receivedAt)
	if err != nil {
		t.Fatalf("add history: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive id, got %d", id)
	}

	items, err := db.GetRecentTargetGiftHistory("live1", 10)
	if err != nil {
		t.Fatalf("get history: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].GiftName != "Rosa" {
		t.Fatalf("expected Rosa, got %q", items[0].GiftName)
	}
	if items[0].AnsweredAt != nil || items[0].ResponseType != nil {
		t.Fatalf("expected pending item")
	}

	answeredAt := receivedAt.Add(2 * time.Minute)
	if err := db.MarkTargetGiftAnswered(id, model.TargetGiftResponseManual, answeredAt); err != nil {
		t.Fatalf("mark answered: %v", err)
	}

	items, err = db.GetRecentTargetGiftHistory("live1", 10)
	if err != nil {
		t.Fatalf("get history after answer: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].AnsweredAt == nil || items[0].ResponseType == nil {
		t.Fatal("expected answered item")
	}
	if *items[0].ResponseType != model.TargetGiftResponseManual {
		t.Fatalf("expected manual, got %q", *items[0].ResponseType)
	}

	// Idempotent second mark should not fail.
	if err := db.MarkTargetGiftAnswered(id, model.TargetGiftResponseAutomatic, answeredAt.Add(time.Minute)); err != nil {
		t.Fatalf("second mark: %v", err)
	}
	items, err = db.GetRecentTargetGiftHistory("live1", 10)
	if err != nil {
		t.Fatalf("get history after second mark: %v", err)
	}
	if *items[0].ResponseType != model.TargetGiftResponseManual {
		t.Fatalf("expected original manual response to remain, got %q", *items[0].ResponseType)
	}
}

func TestMarkTargetGiftAnsweredInvalid(t *testing.T) {
	db := openTestDB(t)
	if err := db.MarkTargetGiftAnswered(0, model.TargetGiftResponseManual, time.Now()); err == nil {
		t.Fatal("expected error for invalid id")
	}
	id, err := db.AddTargetGiftHistory("live1", "user1", "User One", "Rosa", time.Now())
	if err != nil {
		t.Fatalf("add history: %v", err)
	}
	if err := db.MarkTargetGiftAnswered(id, "weird", time.Now()); err == nil {
		t.Fatal("expected error for invalid response type")
	}
}

func TestGetPendingTargetGiftHistory(t *testing.T) {
	db := openTestDB(t)
	now := time.Now()

	pendingID, err := db.AddTargetGiftHistory("live1", "user1", "User One", "Rosa", now)
	if err != nil {
		t.Fatalf("add pending: %v", err)
	}
	answeredID, err := db.AddTargetGiftHistory("live1", "user2", "User Two", "Dino", now.Add(time.Second))
	if err != nil {
		t.Fatalf("add answered: %v", err)
	}
	if err := db.MarkTargetGiftAnswered(answeredID, model.TargetGiftResponseManual, now.Add(2*time.Second)); err != nil {
		t.Fatalf("mark answered: %v", err)
	}
	if _, err := db.AddTargetGiftHistory("live2", "user3", "User Three", "Rosa", now); err != nil {
		t.Fatalf("add other live: %v", err)
	}

	tests := []struct {
		name     string
		liveName string
		wantLen  int
		wantID   int64
	}{
		{"pending for live1", "live1", 1, pendingID},
		{"empty live name", "", 0, 0},
		{"other live", "live2", 1, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items, err := db.GetPendingTargetGiftHistory(tt.liveName, 10)
			if err != nil {
				t.Fatalf("get pending: %v", err)
			}
			if items == nil {
				t.Fatal("expected empty slice, got nil")
			}
			if len(items) != tt.wantLen {
				t.Fatalf("expected %d items, got %d", tt.wantLen, len(items))
			}
			if tt.wantID > 0 && (len(items) == 0 || items[0].ID != tt.wantID) {
				t.Fatalf("expected pending id %d, got %+v", tt.wantID, items)
			}
			for _, item := range items {
				if item.AnsweredAt != nil {
					t.Fatalf("expected unanswered item, got answered %+v", item)
				}
			}
		})
	}
}

func TestPinnedCommentFlow(t *testing.T) {
	db := openTestDB(t)
	at := time.Date(2026, 8, 17, 15, 30, 0, 0, time.UTC)
	follower := true

	id, err := db.AddPinnedComment("live1", "user1", "User One", "comentário fixado", "pin-1", &follower, at)
	if err != nil {
		t.Fatalf("add pinned: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive id, got %d", id)
	}

	same, err := db.AddPinnedComment("live1", "user1", "User One", "comentário fixado", "pin-1", &follower, at.Add(time.Minute))
	if err != nil {
		t.Fatalf("dedup pinned: %v", err)
	}
	if same != id {
		t.Fatalf("expected same id %d, got %d", id, same)
	}

	if _, err := db.AddPinnedComment("live1", "user2", "User Two", "outro", "pin-2", nil, at.Add(2*time.Minute)); err != nil {
		t.Fatalf("add second: %v", err)
	}
	if _, err := db.AddPinnedComment("live2", "user3", "User Three", "outra live", "pin-1", nil, at); err != nil {
		t.Fatalf("add other live: %v", err)
	}

	items, err := db.GetRecentPinnedComments("live1", 10)
	if err != nil {
		t.Fatalf("get pinned: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 pinned comments, got %d", len(items))
	}
	if items[0].Comment != "outro" {
		t.Fatalf("expected newest first, got %q", items[0].Comment)
	}
	if items[1].PinID != "pin-1" {
		t.Fatalf("expected pin-1, got %q", items[1].PinID)
	}
	if items[1].IsFollower == nil || !*items[1].IsFollower {
		t.Fatal("expected follower flag")
	}

	empty, err := db.GetRecentPinnedComments("missing", 10)
	if err != nil {
		t.Fatalf("get missing: %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("expected empty slice, got %#v", empty)
	}
}

func TestAddPinnedCommentRequiresComment(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.AddPinnedComment("live1", "user1", "User", "  ", "", nil, time.Now()); err == nil {
		t.Fatal("expected error for empty comment")
	}
}

func TestListLives(t *testing.T) {
	db := openTestDB(t)

	seed := func(table, liveName, ts string) {
		t.Helper()
		switch table {
		case "user_messages":
			_, err := db.conn.Exec(
				"INSERT INTO user_messages (live_name, uniqueId, username, message, timestamp) VALUES (?, ?, ?, ?, ?)",
				liveName, "u1", "User", "oi", ts,
			)
			if err != nil {
				t.Fatalf("seed user_messages: %v", err)
			}
		case "gifts":
			_, err := db.conn.Exec(
				"INSERT INTO gifts (live_name, uniqueId, nickname, gift_name, timestamp) VALUES (?, ?, ?, ?, ?)",
				liveName, "u1", "User", "rose", ts,
			)
			if err != nil {
				t.Fatalf("seed gifts: %v", err)
			}
		}
	}

	// Same live on two different days, plus a second live on the later day.
	seed("gifts", "liveA", "2026-08-20 19:00:00")
	seed("user_messages", "liveA", "2026-08-20 19:05:00")
	seed("gifts", "liveA", "2026-08-20 21:30:00")
	seed("gifts", "liveA", "2026-08-24 20:00:00")
	seed("gifts", "liveB", "2026-08-24 18:00:00")

	lives, err := db.ListLives(10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lives) != 3 {
		t.Fatalf("expected 3 live-day rows, got %d: %#v", len(lives), lives)
	}

	// Most recent day first.
	if lives[0].Name != "liveA" || lives[0].Day != "2026-08-24" {
		t.Fatalf("unexpected first row: %#v", lives[0])
	}
	if lives[0].Events != 1 {
		t.Fatalf("expected 1 event, got %d", lives[0].Events)
	}
	if lives[0].StartedAt == "" || lives[0].EndedAt == "" {
		t.Fatalf("expected timestamps, got %#v", lives[0])
	}

	// liveA on 2026-08-20 aggregates messages and gifts.
	if lives[2].Name != "liveA" || lives[2].Day != "2026-08-20" {
		t.Fatalf("unexpected last row: %#v", lives[2])
	}
	if lives[2].Events != 3 {
		t.Fatalf("expected 3 events, got %d", lives[2].Events)
	}
}

func TestListLivesEmpty(t *testing.T) {
	db := openTestDB(t)
	lives, err := db.ListLives(10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lives == nil || len(lives) != 0 {
		t.Fatalf("expected empty slice, got %#v", lives)
	}
}

func TestListLivesLimit(t *testing.T) {
	db := openTestDB(t)
	for i := 0; i < 5; i++ {
		if _, err := db.conn.Exec(
			"INSERT INTO gifts (live_name, uniqueId, nickname, gift_name) VALUES (?, ?, ?, ?)",
			fmt.Sprintf("live%d", i), "u1", "User", "rose",
		); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	lives, err := db.ListLives(2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lives) != 2 {
		t.Fatalf("expected 2 rows with limit, got %d", len(lives))
	}
}

func TestDeleteLive(t *testing.T) {
	db := openTestDB(t)

	seed := func(table, liveName string) {
		t.Helper()
		var err error
		switch table {
		case "gifts":
			_, err = db.conn.Exec(
				"INSERT INTO gifts (live_name, uniqueId, nickname, gift_name) VALUES (?, ?, ?, ?)",
				liveName, "u1", "User", "rose",
			)
		case "user_messages":
			_, err = db.conn.Exec(
				"INSERT INTO user_messages (live_name, uniqueId, username, message) VALUES (?, ?, ?, ?)",
				liveName, "u1", "User", "oi",
			)
		case "gift_goals":
			_, err = db.conn.Exec(
				"INSERT INTO gift_goals (live_name, title, target_units, status) VALUES (?, ?, ?, ?)",
				liveName, "meta", 100, "active",
			)
		}
		if err != nil {
			t.Fatalf("seed %s: %v", table, err)
		}
	}

	seed("gifts", "liveA")
	seed("user_messages", "liveA")
	seed("gift_goals", "liveA")
	seed("gifts", "liveB")

	deleted, err := db.DeleteLive("liveA")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deleted != 3 {
		t.Fatalf("expected 3 deleted rows, got %d", deleted)
	}

	lives, err := db.ListLives(10)
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(lives) != 1 || lives[0].Name != "liveB" {
		t.Fatalf("expected only liveB, got %#v", lives)
	}

	if _, err := db.DeleteLive("  "); err == nil {
		t.Fatal("expected error for empty live name")
	}
}

func TestGiftGoalCRUD(t *testing.T) {
	db := openTestDB(t)

	id, err := db.AddGiftGoal(model.GiftGoal{
		LiveName:    "live1",
		Title:       "Meta da noite",
		TargetUnits: 500,
		Status:      model.GoalStatusActive,
		Milestones: []model.GoalMilestone{
			{AtUnits: 100, Reward: "música especial"},
			{AtUnits: 300, Reward: "dedicatória"},
		},
	})
	if err != nil {
		t.Fatalf("add goal: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive id, got %d", id)
	}

	goals, err := db.GetGiftGoals("live1")
	if err != nil {
		t.Fatalf("get goals: %v", err)
	}
	if len(goals) != 1 {
		t.Fatalf("expected 1 goal, got %d", len(goals))
	}
	g := goals[0]
	if g.Title != "Meta da noite" || g.TargetUnits != 500 || g.Status != model.GoalStatusActive {
		t.Fatalf("unexpected goal fields: %+v", g)
	}
	if len(g.Milestones) != 2 || g.Milestones[0].Reward != "música especial" {
		t.Fatalf("unexpected milestones: %+v", g.Milestones)
	}
	if g.CreatedAt == "" {
		t.Fatal("expected created_at to be populated")
	}

	// Other live must not see the goal.
	other, err := db.GetGiftGoals("live2")
	if err != nil {
		t.Fatalf("get other live: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("expected 0 goals for live2, got %d", len(other))
	}

	// Save updates status/milestones.
	g.Status = model.GoalStatusCompleted
	g.Milestones[0].Unlocked = true
	at := time.Now().UTC().Format(time.RFC3339)
	g.Milestones[0].UnlockedAt = &at
	if err := db.SaveGiftGoal(g); err != nil {
		t.Fatalf("save goal: %v", err)
	}
	goals, err = db.GetGiftGoals("live1")
	if err != nil {
		t.Fatalf("get goals after save: %v", err)
	}
	if goals[0].Status != model.GoalStatusCompleted {
		t.Fatalf("expected completed, got %q", goals[0].Status)
	}
	if !goals[0].Milestones[0].Unlocked || goals[0].Milestones[0].UnlockedAt == nil {
		t.Fatalf("expected unlocked milestone, got %+v", goals[0].Milestones[0])
	}
	if goals[0].Milestones[1].Unlocked {
		t.Fatal("second milestone should remain locked")
	}

	if err := db.SaveGiftGoal(model.GiftGoal{ID: 0}); err == nil {
		t.Fatal("expected error saving goal without id")
	}

	deleted, err := db.DeleteGiftGoals("live1")
	if err != nil {
		t.Fatalf("delete goals: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted, got %d", deleted)
	}
	goals, err = db.GetGiftGoals("live1")
	if err != nil {
		t.Fatalf("get goals after delete: %v", err)
	}
	if len(goals) != 0 {
		t.Fatalf("expected 0 goals after delete, got %d", len(goals))
	}
}

func TestGiftGoalValidation(t *testing.T) {
	db := openTestDB(t)
	cases := []struct {
		name string
		g    model.GiftGoal
	}{
		{"empty title", model.GiftGoal{LiveName: "live1", Title: " ", TargetUnits: 10}},
		{"zero target", model.GiftGoal{LiveName: "live1", Title: "meta", TargetUnits: 0}},
		{"empty live", model.GiftGoal{LiveName: "", Title: "meta", TargetUnits: 10}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.AddGiftGoal(tc.g); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestGetGiftUnits(t *testing.T) {
	db := openTestDB(t)

	units, count, err := db.GetGiftUnits("live1", "")
	if err != nil {
		t.Fatalf("empty live: %v", err)
	}
	if units != 0 || count != 0 {
		t.Fatalf("expected 0/0, got %d/%d", units, count)
	}

	if _, err := db.AddGift("live1", "u1", "User One", "Rosa", 5, 0); err != nil {
		t.Fatalf("add gift: %v", err)
	}
	if _, err := db.AddGift("live1", "u2", "User Two", "Dino", 12, 0); err != nil {
		t.Fatalf("add gift: %v", err)
	}
	if _, err := db.AddGift("live2", "u3", "User Three", "Rosa", 99, 0); err != nil {
		t.Fatalf("add gift other live: %v", err)
	}

	units, count, err = db.GetGiftUnits("live1", "")
	if err != nil {
		t.Fatalf("get units: %v", err)
	}
	if units != 17 || count != 2 {
		t.Fatalf("expected 17 units / 2 events, got %d / %d", units, count)
	}

	// Filtering by gift name counts only that gift.
	units, count, err = db.GetGiftUnits("live1", "Rosa")
	if err != nil {
		t.Fatalf("get units (Rosa): %v", err)
	}
	if units != 5 || count != 1 {
		t.Fatalf("expected 5 units / 1 event for Rosa, got %d / %d", units, count)
	}

	// Unknown gift name returns zero.
	units, count, err = db.GetGiftUnits("live1", "Rocket")
	if err != nil {
		t.Fatalf("get units (Rocket): %v", err)
	}
	if units != 0 || count != 0 {
		t.Fatalf("expected 0/0 for Rocket, got %d / %d", units, count)
	}
}

func seedMessagesAt(t *testing.T, db *DB, liveName, uid, user string, from time.Time, n int) []int64 {
	t.Helper()
	ids := make([]int64, 0, n)
	for i := 0; i < n; i++ {
		res, err := db.conn.Exec(
			"INSERT INTO user_messages (live_name, uniqueId, username, message, timestamp) VALUES (?, ?, ?, ?, ?)",
			liveName, uid, user, fmt.Sprintf("msg %d", i), from.Add(time.Duration(i)*time.Minute),
		)
		if err != nil {
			t.Fatalf("seed message %d: %v", i, err)
		}
		id, _ := res.LastInsertId()
		ids = append(ids, id)
	}
	return ids
}

func TestGetUserMessagesRecent(t *testing.T) {
	db := openTestDB(t)

	base := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	ids := seedMessagesAt(t, db, "live1", "user1", "User One", base, 12)

	// limit 10: newest first.
	msgs, err := db.GetUserMessagesRecent("user1", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 10 {
		t.Fatalf("expected 10 messages, got %d", len(msgs))
	}
	wantFirst := ids[11] // most recent
	if msgs[0].ID != wantFirst {
		t.Fatalf("expected newest message first (id %d), got %d", wantFirst, msgs[0].ID)
	}
	wantLast := ids[2] // 12 - 10
	if msgs[9].ID != wantLast {
		t.Fatalf("expected oldest of the window last (id %d), got %d", wantLast, msgs[9].ID)
	}

	// limit larger than available returns everything.
	msgs, err = db.GetUserMessagesRecent("user1", 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 12 {
		t.Fatalf("expected 12 messages, got %d", len(msgs))
	}

	// limit <= 0 returns everything.
	msgs, err = db.GetUserMessagesRecent("user1", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 12 {
		t.Fatalf("expected 12 messages with limit 0, got %d", len(msgs))
	}

	// Case-insensitive lookup.
	msgs, err = db.GetUserMessagesRecent("USER1", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 10 {
		t.Fatalf("expected 10 messages (case-insensitive), got %d", len(msgs))
	}

	// Empty uniqueId returns an error.
	if _, err := db.GetUserMessagesRecent("  ", 10); err == nil {
		t.Fatal("expected error for empty uniqueId")
	}
}

func TestGetUserShareCount(t *testing.T) {
	db := openTestDB(t)

	for i := 0; i < 3; i++ {
		if err := db.AddShare("live1", "user1", "User One"); err != nil {
			t.Fatalf("add share %d: %v", i, err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := db.AddShare("live1", "USER1", "User One"); err != nil {
			t.Fatalf("add share (uppercase) %d: %v", i, err)
		}
	}
	if err := db.AddShare("live1", "user2", "User Two"); err != nil {
		t.Fatalf("add share user2: %v", err)
	}

	count, err := db.GetUserShareCount("user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 5 {
		t.Fatalf("expected 5 shares, got %d", count)
	}

	count, err = db.GetUserShareCount("USER1")
	if err != nil {
		t.Fatalf("unexpected error (case-insensitive): %v", err)
	}
	if count != 5 {
		t.Fatalf("expected 5 shares (case-insensitive), got %d", count)
	}

	count, err = db.GetUserShareCount("user2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 share, got %d", count)
	}

	count, err = db.GetUserShareCount("nobody")
	if err != nil {
		t.Fatalf("unexpected error for unknown user: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 shares for unknown user, got %d", count)
	}

	if _, err := db.GetUserShareCount(""); err == nil {
		t.Fatal("expected error for empty uniqueId")
	}
}

func TestGetUserLikeTotal(t *testing.T) {
	db := openTestDB(t)

	if err := db.AddLike("live1", "user1", "User One", 3); err != nil {
		t.Fatalf("add like: %v", err)
	}
	if err := db.AddLike("live2", "USER1", "User One", 5); err != nil {
		t.Fatalf("add like (uppercase): %v", err)
	}
	if err := db.AddLike("live1", "user2", "User Two", 7); err != nil {
		t.Fatalf("add like user2: %v", err)
	}

	total, err := db.GetUserLikeTotal("user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 8 {
		t.Fatalf("expected 8 likes, got %d", total)
	}

	// Case-insensitive lookup.
	total, err = db.GetUserLikeTotal("USER1")
	if err != nil {
		t.Fatalf("unexpected error (case-insensitive): %v", err)
	}
	if total != 8 {
		t.Fatalf("expected 8 likes (case-insensitive), got %d", total)
	}

	total, err = db.GetUserLikeTotal("user2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if total != 7 {
		t.Fatalf("expected 7 likes, got %d", total)
	}

	// Unknown user sums to zero without error.
	total, err = db.GetUserLikeTotal("nobody")
	if err != nil {
		t.Fatalf("unexpected error for unknown user: %v", err)
	}
	if total != 0 {
		t.Fatalf("expected 0 likes for unknown user, got %d", total)
	}

	if _, err := db.GetUserLikeTotal(""); err == nil {
		t.Fatal("expected error for empty uniqueId")
	}
}
