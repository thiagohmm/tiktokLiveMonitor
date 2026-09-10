package database

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("TEST_DATABASE_URL não configurado: testes exigem PostgreSQL descartável")
	}
	dsn, cleanup, err := CreateTestDatabase(baseDSN)
	if err != nil {
		t.Fatalf("create test database: %v", err)
	}
	t.Cleanup(cleanup)
	db, err := OpenPostgres(dsn)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// testRef builds a live reference for tests that do not need a real session row.
// The id is stable per streamer name so two different names never collide.
func testRef(name string) model.LiveRef {
	return model.LiveRef{ID: name + "-session", Name: name}
}

// seedSession inserts a closed session row directly, for tests that need a
// specific id/day without going through the resume rules.
func seedSession(t *testing.T, db *DB, id, liveName, day string) {
	t.Helper()
	if err := db.ExecSQL(
		`INSERT INTO live_sessions (id, live_name, day, started_at, last_seen_at, ended_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id, liveName, day, day+" 18:00:00", day+" 23:00:00", day+" 23:00:00",
	); err != nil {
		t.Fatalf("seed session %s: %v", id, err)
	}
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
		if err := db.ExecSQL(
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

	err := db.AddUserMessageDedup(testRef("live1"), "user1", "User One", "Hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = db.AddUserMessageDedup(testRef("live1"), "user1", "User One", "Hello")
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

	err := db.AddUserMessageDedup(testRef("live1"), "user1", "User One", "Hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = db.AddUserMessageDedup(testRef("live1"), "USER1", "User One", "HELLO")
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
		err := db.AddUserMessageDedup(testRef("live1"), "user1", "User One", "msg")
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

	err := db.AddUserMessageDedup(testRef("live1"), "", "User", "msg")
	if err != nil {
		t.Fatalf("expected nil for empty uniqueID, got: %v", err)
	}

	err = db.AddUserMessageDedup(testRef("live1"), "user1", "User", "")
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

	err := db.AddUserMessageDedup(testRef("live1"), "user1", "User One", "msg1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	err = db.AddUserMessageDedup(testRef("live1"), "user2", "User Two", "msg2")
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

	err := db.LogAnomaly(testRef("live1"), "bad msg", true, "SPAM", "user1")
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
		err := db.LogAnomaly(testRef("live1"), "msg", false, "OK", "user1")
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

	err := db.LogAnomaly(testRef("live1"), "msg", true, "SPAM", "user1")
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
		err := db.LogAnomaly(testRef("live1"), "msg", false, "OK", "user1")
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

	id, err := db.AddGift(testRef("live1"), "user1", "User One", "Rose", 1, 0)
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
		_, err := db.AddGift(testRef("live1"), "user1", "User One", "Rose", 1, 0)
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

	_, err := db.AddGift(testRef("live1"), "user1", "User One", "Rose", 1, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = db.AddGift(testRef("live1"), "user2", "User Two", "Tiger", 2, 1)
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

	_, err := db.AddGift(testRef("live1"), "user1", "User One", "Rose", 3, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = db.AddGift(testRef("live1"), "user1", "User One", "Rose", 2, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = db.AddGift(testRef("live1"), "user2", "User Two", "Tiger", 1, 1)
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
		_, err := db.AddGift(testRef("live1"), "user1", "User One", "Rose", 1, 0)
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

	err := db.ExecSQL("INSERT INTO anomaly_logs (live_id, live_name, day, comment, is_anomaly, category) VALUES ('s1', 'live1', '2020-01-01', 'old', TRUE, 'SPAM')")
	if err != nil {
		t.Fatalf("insert old: %v", err)
	}

	// day is stored in UTC; seed 'today' with CURRENT_DATE so the test
	// is independent of the host timezone.
	err = db.ExecSQL("INSERT INTO anomaly_logs (live_id, live_name, day, comment, is_anomaly, category) VALUES ('s1', 'live1', CURRENT_DATE, 'new', FALSE, 'OK')")
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

func countTable(t *testing.T, db *DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.queryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count query: %v", err)
	}
	return n
}

// TestDeleteLiveSessionOnlyDeletesThatSession reproduces the reported bug: two
// lives of the SAME streamer on the same day, plus one of another streamer.
// Deleting one session must not touch the others.
func TestDeleteLiveSessionOnlyDeletesThatSession(t *testing.T) {
	db := openTestDB(t)

	seedSession(t, db, "sess-a", "liveA", "2026-08-24")
	seedSession(t, db, "sess-b", "liveA", "2026-08-24")
	seedSession(t, db, "sess-c", "liveB", "2026-08-24")

	sessions := []struct{ id, name string }{
		{"sess-a", "liveA"}, {"sess-b", "liveA"}, {"sess-c", "liveB"},
	}
	for _, s := range sessions {
		ref := model.LiveRef{ID: s.id, Name: s.name}
		if _, err := db.AddGift(ref, "u1", "User", "rose", 1, 0); err != nil {
			t.Fatalf("add gift %s: %v", s.id, err)
		}
		if err := db.AddUserMessageDedup(ref, "u1", "User", "oi"); err != nil {
			t.Fatalf("add message %s: %v", s.id, err)
		}
		if err := db.LogAnomaly(ref, "spam", true, "SPAM", "u1"); err != nil {
			t.Fatalf("log anomaly %s: %v", s.id, err)
		}
		if err := db.AddShare(ref, "u1", "User"); err != nil {
			t.Fatalf("add share %s: %v", s.id, err)
		}
		if err := db.AddLike(ref, "u1", "User", 3); err != nil {
			t.Fatalf("add like %s: %v", s.id, err)
		}
		if err := db.UpsertRoomLikeTotal(ref, 100); err != nil {
			t.Fatalf("upsert room total %s: %v", s.id, err)
		}
		if _, err := db.AddGiftGoal(model.GiftGoal{
			LiveID: s.id, LiveName: s.name, Title: "meta",
			TargetUnits: 10, Status: model.GoalStatusActive,
		}); err != nil {
			t.Fatalf("add goal %s: %v", s.id, err)
		}
	}

	// 7 rows of the session + the session row itself.
	deleted, err := db.DeleteLiveSession("sess-a")
	if err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if deleted != 8 {
		t.Fatalf("expected 8 deleted rows, got %d", deleted)
	}

	checks := []struct {
		query string
		arg   string
		want  int
	}{
		{"SELECT COUNT(*) FROM gifts WHERE live_id = ?", "sess-a", 0},
		{"SELECT COUNT(*) FROM gifts WHERE live_id = ?", "sess-b", 1},
		{"SELECT COUNT(*) FROM user_messages WHERE live_id = ?", "sess-b", 1},
		{"SELECT COUNT(*) FROM anomaly_logs WHERE live_id = ?", "sess-b", 1},
		{"SELECT COUNT(*) FROM shares WHERE live_id = ?", "sess-b", 1},
		{"SELECT COUNT(*) FROM likes WHERE live_id = ?", "sess-b", 1},
		{"SELECT COUNT(*) FROM room_like_totals WHERE live_id = ?", "sess-b", 1},
		{"SELECT COUNT(*) FROM gift_goals WHERE live_id = ?", "sess-b", 1},
		{"SELECT COUNT(*) FROM live_sessions WHERE id = ?", "sess-a", 0},
		// Same streamer, same day: untouched.
		{"SELECT COUNT(*) FROM gifts WHERE live_id = ?", "sess-c", 1},
		{"SELECT COUNT(*) FROM room_like_totals WHERE live_id = ?", "sess-c", 1},
		{"SELECT COUNT(*) FROM live_sessions WHERE id = ?", "sess-c", 1},
	}
	for _, c := range checks {
		if n := countTable(t, db, c.query, c.arg); n != c.want {
			t.Fatalf("%s arg=%s: expected %d, got %d", c.query, c.arg, c.want, n)
		}
	}

	if _, err := db.DeleteLiveSession("  "); err == nil {
		t.Fatal("expected error for empty id")
	}
}

func TestDeleteLiveSessionCoversEveryLiveIDTable(t *testing.T) {
	db := openTestDB(t)

	// A lista do delete vem de liveIDColumns; aqui ela é confrontada com o banco,
	// então esquecer uma tabela nova reprova o teste na direção que importa.
	covered := make(map[string]bool, len(liveIDColumns))
	for _, table := range liveIDTableNames() {
		covered[table] = true
	}

	rows, err := db.query(
		`SELECT table_name FROM information_schema.columns
		 WHERE table_schema = 'public' AND column_name = 'live_id'`)
	if err != nil {
		t.Fatalf("query live_id columns: %v", err)
	}
	defer closeRows(rows)

	found := 0
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		found++
		if !covered[name] {
			t.Fatalf("table %q has live_id but is missing from DeleteLiveSession", name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if found != len(covered) {
		t.Fatalf("expected %d tables with live_id, got %d", len(covered), found)
	}
}

func TestLiveIDSchemaHardening(t *testing.T) {
	db := openTestDB(t)

	// Reentrancy: the backfill, SET NOT NULL and the primary key swap must all
	// be safe to run again on boot.
	if err := db.migratePostgres(); err != nil {
		t.Fatalf("re-running migration: %v", err)
	}

	rows, err := db.query(
		`SELECT table_name, is_nullable FROM information_schema.columns
		 WHERE table_schema = 'public' AND column_name = 'live_id'`)
	if err != nil {
		t.Fatalf("query live_id columns: %v", err)
	}
	for rows.Next() {
		var table, nullable string
		if err := rows.Scan(&table, &nullable); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if nullable != "NO" {
			t.Fatalf("%s.live_id must be NOT NULL, got is_nullable=%s", table, nullable)
		}
	}
	closeRows(rows)

	// room_like_totals is now scoped by session.
	var pkColumns int
	if err := db.queryRow(
		`SELECT COUNT(*) FROM information_schema.table_constraints tc
		 JOIN information_schema.key_column_usage kcu
		   ON kcu.constraint_name = tc.constraint_name AND kcu.table_schema = tc.table_schema
		 WHERE tc.table_schema = 'public' AND tc.table_name = 'room_like_totals'
		   AND tc.constraint_type = 'PRIMARY KEY' AND kcu.column_name = 'live_id'`,
	).Scan(&pkColumns); err != nil {
		t.Fatalf("query room_like_totals pk: %v", err)
	}
	if pkColumns != 1 {
		t.Fatalf("expected primary key on room_like_totals(live_id), got %d cols", pkColumns)
	}
}

func TestBeginLiveSessionLifecycle(t *testing.T) {
	db := openTestDB(t)
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)

	first, err := db.BeginLiveSession("live1", now)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if first.ID == "" || first.LiveName != "live1" {
		t.Fatalf("unexpected session: %#v", first)
	}
	if first.Day != "2026-08-24" {
		t.Fatalf("expected day 2026-08-24, got %s", first.Day)
	}

	// Backend restart inside the same live resumes the open session.
	resumed, err := db.BeginLiveSession("live1", now.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.ID != first.ID {
		t.Fatalf("expected resume of %s, got %s", first.ID, resumed.ID)
	}

	// Closing is idempotent and keeps the first ended_at.
	if err := db.EndLiveSession(first.ID, now.Add(3*time.Hour)); err != nil {
		t.Fatalf("end: %v", err)
	}
	if err := db.EndLiveSession(first.ID, now.Add(4*time.Hour)); err != nil {
		t.Fatalf("end twice must not fail: %v", err)
	}
	ended, err := db.GetLiveSession(first.ID)
	if err != nil {
		t.Fatalf("get ended: %v", err)
	}
	wantEnded := now.Add(3 * time.Hour).Format(time.RFC3339)
	if ended.EndedAt != wantEnded {
		t.Fatalf("expected ended_at %s, got %s", wantEnded, ended.EndedAt)
	}

	// Stop + start on the SAME day is a different live (this is the case the
	// old <10h reuse rule merged into one).
	second, err := db.BeginLiveSession("live1", now.Add(4*time.Hour))
	if err != nil {
		t.Fatalf("begin after end: %v", err)
	}
	if second.ID == first.ID {
		t.Fatal("a stopped live must not be resumed by the next start")
	}

	// A new day starts a new session and closes the leftover open one.
	nextDay := now.Add(25 * time.Hour)
	third, err := db.BeginLiveSession("live1", nextDay)
	if err != nil {
		t.Fatalf("begin next day: %v", err)
	}
	if third.ID == second.ID {
		t.Fatal("expected a new session on a new day")
	}
	prev, err := db.GetLiveSession(second.ID)
	if err != nil {
		t.Fatalf("get previous: %v", err)
	}
	if prev.EndedAt == "" {
		t.Fatal("expected the leftover open session to be closed")
	}

	// An idle session (untouched past the reuse window) is not resumed, but is
	// closed and stays in the listing.
	idleAt := now.Add(-11 * time.Hour)
	if err := db.ExecSQL(
		`INSERT INTO live_sessions (id, live_name, day, started_at, last_seen_at, ended_at)
		 VALUES (?, ?, ?, ?, ?, NULL)`,
		"idle-session", "live9", idleAt.Format("2006-01-02"), idleAt, idleAt,
	); err != nil {
		t.Fatalf("seed idle session: %v", err)
	}
	afterIdle, err := db.BeginLiveSession("live9", now)
	if err != nil {
		t.Fatalf("begin after idle: %v", err)
	}
	if afterIdle.ID == "idle-session" {
		t.Fatal("an idle session past the reuse window must not be resumed")
	}
	idle, err := db.GetLiveSession("idle-session")
	if err != nil {
		t.Fatalf("get idle: %v", err)
	}
	if idle.EndedAt == "" {
		t.Fatal("expected the idle session to be closed")
	}

	// Invalid input.
	if _, err := db.BeginLiveSession("   ", now); err == nil {
		t.Fatal("expected error for empty live name")
	}
	if _, err := db.GetLiveSession(""); err == nil {
		t.Fatal("expected error for empty id")
	}
	if _, err := db.GetLiveSession("does-not-exist"); err != model.ErrLiveSessionNotFound {
		t.Fatalf("expected ErrLiveSessionNotFound, got %v", err)
	}
}

func TestLatestLiveSessionPrefersOpen(t *testing.T) {
	db := openTestDB(t)
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)

	seedSession(t, db, "closed", "live1", "2026-08-23")
	seedSession(t, db, "closed-2", "live1", "2026-08-24")
	if err := db.ExecSQL(
		`INSERT INTO live_sessions (id, live_name, day, started_at, last_seen_at, ended_at)
		 VALUES (?, ?, ?, ?, ?, NULL)`,
		"open", "live1", "2026-08-22", now.Add(-time.Hour), now,
	); err != nil {
		t.Fatalf("seed open session: %v", err)
	}

	got, err := db.LatestLiveSession("live1")
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if got.ID != "open" {
		t.Fatalf("expected the open session, got %s", got.ID)
	}

	// Without an open session, the most recent one wins.
	if err := db.EndLiveSession("open", now); err != nil {
		t.Fatalf("end open: %v", err)
	}
	got, err = db.LatestLiveSession("live1")
	if err != nil {
		t.Fatalf("latest after end: %v", err)
	}
	if got.ID != "closed-2" {
		t.Fatalf("expected closed-2, got %s", got.ID)
	}

	// Never creates a session.
	if _, err := db.LatestLiveSession("unknown-live"); err != model.ErrLiveSessionNotFound {
		t.Fatalf("expected ErrLiveSessionNotFound, got %v", err)
	}
	if n := countTable(t, db, "SELECT COUNT(*) FROM live_sessions WHERE live_name = ?", "unknown-live"); n != 0 {
		t.Fatalf("expected no session to be created, got %d", n)
	}
}

// sameUTCDay mirrors the production rule: timestamps are stored and read in UTC.

func TestTargetGiftHistoryFlow(t *testing.T) {
	db := openTestDB(t)
	receivedAt := time.Date(2026, 8, 17, 15, 30, 0, 0, time.UTC)

	id, err := db.AddTargetGiftHistory(testRef("live1"), "user1", "User One", "Rosa", receivedAt, false)
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
	id, err := db.AddTargetGiftHistory(testRef("live1"), "user1", "User One", "Rosa", time.Now(), false)
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

	pendingID, err := db.AddTargetGiftHistory(testRef("live1"), "user1", "User One", "Rosa", now, false)
	if err != nil {
		t.Fatalf("add pending: %v", err)
	}
	answeredID, err := db.AddTargetGiftHistory(testRef("live1"), "user2", "User Two", "Dino", now.Add(time.Second), false)
	if err != nil {
		t.Fatalf("add answered: %v", err)
	}
	if err := db.MarkTargetGiftAnswered(answeredID, model.TargetGiftResponseManual, now.Add(2*time.Second)); err != nil {
		t.Fatalf("mark answered: %v", err)
	}
	if _, err := db.AddTargetGiftHistory(testRef("live2"), "user3", "User Three", "Rosa", now, false); err != nil {
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

func TestTargetGiftPriorityToggle(t *testing.T) {
	db := openTestDB(t)
	now := time.Now()

	id, err := db.AddTargetGiftHistory(testRef("live1"), "user1", "User One", "Rosa", now, false)
	if err != nil {
		t.Fatalf("add history: %v", err)
	}

	// Invalid ids.
	if err := db.SetTargetGiftPriority(0, true, now); err == nil {
		t.Fatal("expected error for invalid id")
	}
	if err := db.SetTargetGiftPriority(999999, true, now); err == nil {
		t.Fatal("expected error for unknown id")
	}

	// Promote: flag + stamp persist across re-queries.
	if err := db.SetTargetGiftPriority(id, true, now.Add(time.Second)); err != nil {
		t.Fatalf("promote: %v", err)
	}
	items, err := db.GetPendingTargetGiftHistory("live1", 10)
	if err != nil {
		t.Fatalf("get pending: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if !items[0].IsPriority || items[0].PriorityAt == nil {
		t.Fatalf("expected promoted item, got %+v", items[0])
	}

	// Demote: flag and stamp cleared.
	if err := db.SetTargetGiftPriority(id, false, now.Add(2*time.Second)); err != nil {
		t.Fatalf("demote: %v", err)
	}
	items, err = db.GetPendingTargetGiftHistory("live1", 10)
	if err != nil {
		t.Fatalf("get pending after demote: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].IsPriority || items[0].PriorityAt != nil {
		t.Fatalf("expected demoted item, got %+v", items[0])
	}
}

func TestTargetGiftPriorityQueueOrder(t *testing.T) {
	db := openTestDB(t)
	now := time.Now()

	// A(t1) e B(t2) normais; C(t3) normal.
	idA, err := db.AddTargetGiftHistory(testRef("live1"), "userA", "User A", "Rosa", now, false)
	if err != nil {
		t.Fatalf("add A: %v", err)
	}
	idB, err := db.AddTargetGiftHistory(testRef("live1"), "userB", "User B", "Dino", now.Add(time.Second), false)
	if err != nil {
		t.Fatalf("add B: %v", err)
	}
	idC, err := db.AddTargetGiftHistory(testRef("live1"), "userC", "User C", "Lion", now.Add(2*time.Second), false)
	if err != nil {
		t.Fatalf("add C: %v", err)
	}

	queueIDs := func() []int64 {
		t.Helper()
		items, err := db.GetPendingTargetGiftHistory("live1", 10)
		if err != nil {
			t.Fatalf("get pending: %v", err)
		}
		ids := make([]int64, 0, len(items))
		for _, item := range items {
			ids = append(ids, item.ID)
		}
		return ids
	}

	want := func(ids ...int64) {
		t.Helper()
		got := queueIDs()
		if len(got) != len(ids) {
			t.Fatalf("expected %v, got %v", ids, got)
		}
		for i := range ids {
			if got[i] != ids[i] {
				t.Fatalf("expected %v, got %v", ids, got)
			}
		}
	}

	// Fila normal: FIFO por received_at.
	want(idA, idB, idC)

	// Promover C: vai para o topo (único fura fila).
	if err := db.SetTargetGiftPriority(idC, true, now.Add(3*time.Second)); err != nil {
		t.Fatalf("promote C: %v", err)
	}
	want(idC, idA, idB)

	// Promover A depois: fura fila, mas fica ABAIXO de C (FIFO entre fura fila).
	if err := db.SetTargetGiftPriority(idA, true, now.Add(4*time.Second)); err != nil {
		t.Fatalf("promote A: %v", err)
	}
	want(idC, idA, idB)

	// Despromover C: volta para a posição normal por received_at (fim da fila).
	if err := db.SetTargetGiftPriority(idC, false, now.Add(5*time.Second)); err != nil {
		t.Fatalf("demote C: %v", err)
	}
	want(idA, idB, idC)
}

func TestTargetGiftPriorityAnsweredRejected(t *testing.T) {
	db := openTestDB(t)
	now := time.Now()

	id, err := db.AddTargetGiftHistory(testRef("live1"), "user1", "User One", "Rosa", now, false)
	if err != nil {
		t.Fatalf("add history: %v", err)
	}
	if err := db.MarkTargetGiftAnswered(id, model.TargetGiftResponseManual, now.Add(time.Second)); err != nil {
		t.Fatalf("mark answered: %v", err)
	}

	// Presente já respondido não pode furar fila.
	if err := db.SetTargetGiftPriority(id, true, now.Add(2*time.Second)); err == nil {
		t.Fatal("expected error promoting answered entry")
	}

	items, err := db.GetPendingTargetGiftHistory("live1", 10)
	if err != nil {
		t.Fatalf("get pending: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected empty queue, got %+v", items)
	}
}

// TestTargetGiftHistoryInsertPriority covers the "fura fila por tipo de
// presente" path: entries created with priority=true join the queue head
// already promoted (priority_at = received_at).
func TestTargetGiftHistoryInsertPriority(t *testing.T) {
	db := openTestDB(t)
	now := time.Now()

	normalID, err := db.AddTargetGiftHistory(testRef("live1"), "user1", "User One", "Rosa", now.Add(time.Second), false)
	if err != nil {
		t.Fatalf("add normal: %v", err)
	}
	priorityID, err := db.AddTargetGiftHistory(testRef("live1"), "user2", "User Two", "Dino", now, true)
	if err != nil {
		t.Fatalf("add priority: %v", err)
	}

	items, err := db.GetPendingTargetGiftHistory("live1", 10)
	if err != nil {
		t.Fatalf("get pending: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %+v", items)
	}
	// O prioritário (recebido antes) fica à frente do normal.
	if items[0].ID != priorityID || items[1].ID != normalID {
		t.Fatalf("expected [priority, normal], got %+v", items)
	}
	if !items[0].IsPriority || items[0].PriorityAt == nil {
		t.Fatalf("expected priority stamp, got %+v", items[0])
	}
	if items[1].IsPriority || items[1].PriorityAt != nil {
		t.Fatalf("expected normal entry, got %+v", items[1])
	}
}

func TestPinnedCommentFlow(t *testing.T) {
	db := openTestDB(t)
	at := time.Date(2026, 8, 17, 15, 30, 0, 0, time.UTC)
	follower := true

	id, err := db.AddPinnedComment(testRef("live1"), "user1", "User One", "comentário fixado", "pin-1", &follower, at)
	if err != nil {
		t.Fatalf("add pinned: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive id, got %d", id)
	}

	same, err := db.AddPinnedComment(testRef("live1"), "user1", "User One", "comentário fixado", "pin-1", &follower, at.Add(time.Minute))
	if err != nil {
		t.Fatalf("dedup pinned: %v", err)
	}
	if same != id {
		t.Fatalf("expected same id %d, got %d", id, same)
	}

	if _, err := db.AddPinnedComment(testRef("live1"), "user2", "User Two", "outro", "pin-2", nil, at.Add(2*time.Minute)); err != nil {
		t.Fatalf("add second: %v", err)
	}
	if _, err := db.AddPinnedComment(testRef("live2"), "user3", "User Three", "outra live", "pin-1", nil, at); err != nil {
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
	if _, err := db.AddPinnedComment(testRef("live1"), "user1", "User", "  ", "", nil, time.Now()); err == nil {
		t.Fatal("expected error for empty comment")
	}
}

func TestListLives(t *testing.T) {
	db := openTestDB(t)

	// Same streamer on two different days, plus a second streamer on the later
	// day. Sessions are the unit the admin table lists and deletes.
	seedSession(t, db, "a1", "liveA", "2026-08-20")
	seedSession(t, db, "a2", "liveA", "2026-08-24")
	seedSession(t, db, "b1", "liveB", "2026-08-24")

	seedEvent := func(liveID, liveName, table, ts string) {
		t.Helper()
		var err error
		switch table {
		case "user_messages":
			err = db.ExecSQL(
				"INSERT INTO user_messages (live_id, live_name, uniqueId, username, message, timestamp) VALUES (?, ?, ?, ?, ?, ?)",
				liveID, liveName, "u1", "User", "oi", ts,
			)
		case "gifts":
			err = db.ExecSQL(
				"INSERT INTO gifts (live_id, live_name, uniqueId, nickname, gift_name, timestamp) VALUES (?, ?, ?, ?, ?, ?)",
				liveID, liveName, "u1", "User", "rose", ts,
			)
		case "likes":
			err = db.ExecSQL(
				"INSERT INTO likes (live_id, live_name, uniqueId, nickname, like_count, timestamp) VALUES (?, ?, ?, ?, ?, ?)",
				liveID, liveName, "u1", "User", 1, ts,
			)
		case "gift_goals":
			err = db.ExecSQL(
				"INSERT INTO gift_goals (live_id, live_name, title, target_units, status) VALUES (?, ?, ?, ?, ?)",
				liveID, liveName, "meta", 10, "active",
			)
		}
		if err != nil {
			t.Fatalf("seed %s: %v", table, err)
		}
	}

	seedEvent("a1", "liveA", "gifts", "2026-08-20 19:00:00")
	seedEvent("a1", "liveA", "user_messages", "2026-08-20 19:05:00")
	seedEvent("a1", "liveA", "gifts", "2026-08-20 21:30:00")
	seedEvent("a2", "liveA", "gifts", "2026-08-24 20:00:00")
	seedEvent("b1", "liveB", "gifts", "2026-08-24 18:00:00")
	// Likes count as events; goals do not.
	seedEvent("a2", "liveA", "likes", "2026-08-24 20:10:00")
	seedEvent("a2", "liveA", "gift_goals", "2026-08-24 20:20:00")

	lives, err := db.ListLives(10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lives) != 3 {
		t.Fatalf("expected 3 session rows, got %d: %#v", len(lives), lives)
	}

	// Most recent day first; each row carries its session id.
	if lives[0].Day != "2026-08-24" {
		t.Fatalf("unexpected first row: %#v", lives[0])
	}
	byID := map[string]model.Live{}
	for _, l := range lives {
		if l.ID == "" {
			t.Fatalf("expected session id in %#v", l)
		}
		if l.StartedAt == "" {
			t.Fatalf("expected startedAt in %#v", l)
		}
		byID[l.ID] = l
	}

	a1, ok := byID["a1"]
	if !ok {
		t.Fatalf("missing session a1: %#v", lives)
	}
	if a1.Name != "liveA" || a1.Day != "2026-08-20" || a1.Events != 3 {
		t.Fatalf("unexpected a1: %#v", a1)
	}
	if a1.EndedAt == "" {
		t.Fatalf("expected endedAt for a closed session: %#v", a1)
	}

	a2 := byID["a2"]
	// gift + like = 2 events; the goal must not be counted.
	if a2.Events != 2 {
		t.Fatalf("expected 2 events for a2 (goal excluded), got %d", a2.Events)
	}
}

func TestListLivesIncludesOpenSession(t *testing.T) {
	db := openTestDB(t)

	// An idle session that was never closed must stay visible, otherwise it
	// could never be deleted from the UI.
	idleAt := time.Now().UTC().Add(-30 * time.Hour)
	if err := db.ExecSQL(
		`INSERT INTO live_sessions (id, live_name, day, started_at, last_seen_at, ended_at)
		 VALUES (?, ?, ?, ?, ?, NULL)`,
		"idle", "live1", idleAt.Format("2006-01-02"), idleAt, idleAt,
	); err != nil {
		t.Fatalf("seed idle session: %v", err)
	}

	lives, err := db.ListLives(10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lives) != 1 || lives[0].ID != "idle" {
		t.Fatalf("expected the idle open session to be listed, got %#v", lives)
	}
	if lives[0].EndedAt != "" {
		t.Fatalf("expected no endedAt, got %q", lives[0].EndedAt)
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
		seedSession(t, db, fmt.Sprintf("s%d", i), fmt.Sprintf("live%d", i), "2026-08-24")
	}
	lives, err := db.ListLives(2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lives) != 2 {
		t.Fatalf("expected 2 rows with limit, got %d", len(lives))
	}
}

func TestDeleteLiveSessionEmptyID(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.DeleteLiveSession("   "); err == nil {
		t.Fatal("expected error for empty session id")
	}
}

func TestGiftGoalCRUD(t *testing.T) {
	db := openTestDB(t)

	id, err := db.AddGiftGoal(model.GiftGoal{
		LiveID:      testRef("live1").ID,
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

	goals, err := db.GetGiftGoals(testRef("live1"))
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
	other, err := db.GetGiftGoals(testRef("live2"))
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
	goals, err = db.GetGiftGoals(testRef("live1"))
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

}

func TestGiftGoalValidation(t *testing.T) {
	db := openTestDB(t)
	cases := []struct {
		name string
		g    model.GiftGoal
	}{
		{"empty title", model.GiftGoal{LiveID: "s1", LiveName: "live1", Title: " ", TargetUnits: 10}},
		{"zero target", model.GiftGoal{LiveID: "s1", LiveName: "live1", Title: "meta", TargetUnits: 0}},
		{"empty live", model.GiftGoal{LiveID: "s1", LiveName: "", Title: "meta", TargetUnits: 10}},
		{"empty live id", model.GiftGoal{LiveName: "live1", Title: "meta", TargetUnits: 10}},
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

	units, count, err := db.GetGiftUnits(testRef("live1"), "")
	if err != nil {
		t.Fatalf("empty live: %v", err)
	}
	if units != 0 || count != 0 {
		t.Fatalf("expected 0/0, got %d/%d", units, count)
	}

	if _, err := db.AddGift(testRef("live1"), "u1", "User One", "Rosa", 5, 0); err != nil {
		t.Fatalf("add gift: %v", err)
	}
	if _, err := db.AddGift(testRef("live1"), "u2", "User Two", "Dino", 12, 0); err != nil {
		t.Fatalf("add gift: %v", err)
	}
	if _, err := db.AddGift(testRef("live2"), "u3", "User Three", "Rosa", 99, 0); err != nil {
		t.Fatalf("add gift other live: %v", err)
	}

	units, count, err = db.GetGiftUnits(testRef("live1"), "")
	if err != nil {
		t.Fatalf("get units: %v", err)
	}
	if units != 17 || count != 2 {
		t.Fatalf("expected 17 units / 2 events, got %d / %d", units, count)
	}

	// Filtering by gift name counts only that gift.
	units, count, err = db.GetGiftUnits(testRef("live1"), "Rosa")
	if err != nil {
		t.Fatalf("get units (Rosa): %v", err)
	}
	if units != 5 || count != 1 {
		t.Fatalf("expected 5 units / 1 event for Rosa, got %d / %d", units, count)
	}

	// Unknown gift name returns zero.
	units, count, err = db.GetGiftUnits(testRef("live1"), "Rocket")
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
		id, err := db.insertID(
			"INSERT INTO user_messages (live_id, live_name, uniqueId, username, message, timestamp) VALUES (?, ?, ?, ?, ?, ?)",
			testRef(liveName).ID, liveName, uid, user, fmt.Sprintf("msg %d", i), from.Add(time.Duration(i)*time.Minute),
		)
		if err != nil {
			t.Fatalf("seed message %d: %v", i, err)
		}
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
		if err := db.AddShare(testRef("live1"), "user1", "User One"); err != nil {
			t.Fatalf("add share %d: %v", i, err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := db.AddShare(testRef("live1"), "USER1", "User One"); err != nil {
			t.Fatalf("add share (uppercase) %d: %v", i, err)
		}
	}
	if err := db.AddShare(testRef("live1"), "user2", "User Two"); err != nil {
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

	if err := db.AddLike(testRef("live1"), "user1", "User One", 3); err != nil {
		t.Fatalf("add like: %v", err)
	}
	if err := db.AddLike(testRef("live2"), "USER1", "User One", 5); err != nil {
		t.Fatalf("add like (uppercase): %v", err)
	}
	if err := db.AddLike(testRef("live1"), "user2", "User Two", 7); err != nil {
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
