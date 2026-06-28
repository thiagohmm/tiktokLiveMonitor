package database

import (
	"os"
	"testing"
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

func TestAddFeedback(t *testing.T) {
	db := openTestDB(t)

	id, err := db.AddFeedback("spam message", "SPAM", "SIM_SPAM")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive id, got %d", id)
	}

	fbs, err := db.GetRecentFeedbacks(10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fbs) != 1 {
		t.Fatalf("expected 1 feedback, got %d", len(fbs))
	}
	if fbs[0].Comment != "spam message" {
		t.Fatalf("expected 'spam message', got %q", fbs[0].Comment)
	}
	if fbs[0].Category != "SPAM" {
		t.Fatalf("expected 'SPAM', got %q", fbs[0].Category)
	}
}

func TestAddFeedbackValidation(t *testing.T) {
	db := openTestDB(t)

	_, err := db.AddFeedback("", "SPAM", "SIM_SPAM")
	if err == nil {
		t.Fatal("expected error for empty comment")
	}

	_, err = db.AddFeedback("msg", "INVALID", "SIM_SPAM")
	if err == nil {
		t.Fatal("expected error for invalid category")
	}

	_, err = db.AddFeedback("msg", "SPAM", "INVALID")
	if err == nil {
		t.Fatal("expected error for invalid expected")
	}
}

func TestGetRecentFeedbacksLimit(t *testing.T) {
	db := openTestDB(t)

	for i := 0; i < 15; i++ {
		_, err := db.AddFeedback("msg", "SPAM", "SIM_SPAM")
		if err != nil {
			t.Fatalf("add feedback %d: %v", i, err)
		}
	}

	fbs, err := db.GetRecentFeedbacks(5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fbs) != 5 {
		t.Fatalf("expected 5, got %d", len(fbs))
	}

	_, err = db.GetRecentFeedbacks(0)
	if err != nil {
		t.Fatalf("unexpected error for limit 0: %v", err)
	}
}

func TestAddUserMessageDedup(t *testing.T) {
	db := openTestDB(t)

	err := db.AddUserMessageDedup("user1", "User One", "Hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = db.AddUserMessageDedup("user1", "User One", "Hello")
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

	err := db.AddUserMessageDedup("user1", "User One", "Hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = db.AddUserMessageDedup("USER1", "User One", "HELLO")
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
		err := db.AddUserMessageDedup("user1", "User One", "msg")
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

	err := db.AddUserMessageDedup("", "User", "msg")
	if err != nil {
		t.Fatalf("expected nil for empty uniqueID, got: %v", err)
	}

	err = db.AddUserMessageDedup("user1", "User", "")
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

	err := db.AddUserMessageDedup("user1", "User One", "msg1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	err = db.AddUserMessageDedup("user2", "User Two", "msg2")
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

	gifts, err := db.GetRecentGifts(10)
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

func TestGetRecentGiftsLimit(t *testing.T) {
	db := openTestDB(t)

	for i := 0; i < 15; i++ {
		_, err := db.AddGift("live1", "user1", "User One", "Rose", 1, 0)
		if err != nil {
			t.Fatalf("add gift %d: %v", i, err)
		}
	}

	gifts, err := db.GetRecentGifts(5)
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

	gifts, _ := db.GetRecentGifts(10)
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

	err = db.LogAnomaly("live1", "new", false, "OK", "user1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
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

func TestValidExpected(t *testing.T) {
	expected := []string{"NAO", "SIM_PERGUNTA", "SIM_PROSELITISMO", "SIM_ODIO", "SIM_SPAM", "SIM_GOLPE", "SIM_OUTRO"}
	for _, v := range expected {
		if !ValidExpected[v] {
			t.Errorf("expected %q to be valid", v)
		}
	}
	if ValidExpected["INVALID"] {
		t.Error("expected INVALID to be invalid")
	}
}

func TestValidCategory(t *testing.T) {
	categories := []string{"OK", "PERGUNTA", "PROSELITISMO", "ODIO", "SPAM", "GOLPE", "OUTRO"}
	for _, v := range categories {
		if !ValidCategory[v] {
			t.Errorf("expected %q to be valid", v)
		}
	}
	if ValidCategory["INVALID"] {
		t.Error("expected INVALID to be invalid")
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
