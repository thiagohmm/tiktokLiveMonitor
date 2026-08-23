package database

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestMessageCacheAddAndFlush(t *testing.T) {
	db := openTestDB(t)
	cache := NewMessageCache(db)

	// 15 unique messages from user A -> only the 10 most recent survive.
	for i := 0; i < 15; i++ {
		cache.Add("live1", "userA", "nickA", fmt.Sprintf("message %d", i))
		time.Sleep(time.Millisecond) // ensure increasing timestamps
	}
	// 3 unique messages from user B.
	for i := 0; i < 3; i++ {
		cache.Add("live1", "userB", "nickB", fmt.Sprintf("question %d", i))
	}

	if got := cache.pendingLen(); got != 13 {
		t.Fatalf("expected 13 buffered messages (10 + 3), got %d", got)
	}

	cache.Flush()
	if got := cache.pendingLen(); got != 0 {
		t.Fatalf("expected empty buffer after flush, got %d", got)
	}

	msgsA, err := db.GetUserMessages("userA")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgsA) != 10 {
		t.Fatalf("expected 10 messages for userA, got %d", len(msgsA))
	}
	// Newest (timestamp DESC) must be "message 14".
	if msgsA[0].Message != "message 14" {
		t.Fatalf("expected most recent 'message 14', got %q", msgsA[0].Message)
	}
	for _, m := range msgsA {
		for i := 0; i < 5; i++ {
			if m.Message == fmt.Sprintf("message %d", i) {
				t.Fatalf("message %d should have been pruned", i)
			}
		}
	}

	msgsB, err := db.GetUserMessages("userB")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgsB) != 3 {
		t.Fatalf("expected 3 messages for userB, got %d", len(msgsB))
	}
}

func TestMessageCacheDedup(t *testing.T) {
	db := openTestDB(t)
	cache := NewMessageCache(db)

	cache.Add("live1", "userA", "nickA", "same message")
	cache.Add("live1", "USERA", "nickA", "  SAME MESSAGE  ") // case/whitespace insensitive
	cache.Add("live1", "userA", "nickA", "")                // ignored

	if got := cache.pendingLen(); got != 1 {
		t.Fatalf("expected 1 buffered message, got %d", got)
	}
	cache.Flush()

	msgs, err := db.GetUserMessages("userA")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 unique message, got %d", len(msgs))
	}
}

func TestMessageCacheFlushIdempotent(t *testing.T) {
	db := openTestDB(t)
	cache := NewMessageCache(db)

	cache.Add("live1", "userA", "nickA", "hello")
	cache.Flush()
	cache.Flush() // second flush is a no-op

	msgs, err := db.GetUserMessages("userA")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message after double flush, got %d", len(msgs))
	}
}

func TestMessageCacheStopFlushesRemaining(t *testing.T) {
	db := openTestDB(t)
	cache := NewMessageCache(db)
	cache.SetFlushPeriod(time.Hour) // disable ticker; rely on Stop's final flush
	cache.Start()
	defer cache.Stop()

	cache.Add("live1", "userA", "nickA", "pending message")
	cache.Stop()

	msgs, err := db.GetUserMessages("userA")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message after Stop, got %d", len(msgs))
	}
}

func TestMessageCacheConcurrentAdd(t *testing.T) {
	db := openTestDB(t)
	cache := NewMessageCache(db)

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				// 5 unique messages per goroutine/user pair.
				cache.Add("live1", fmt.Sprintf("user%d", g), fmt.Sprintf("nick%d", g), fmt.Sprintf("msg %d", i%5))
			}
		}(g)
	}
	wg.Wait()

	cache.Flush()

	total := 0
	for g := 0; g < 8; g++ {
		msgs, err := db.GetUserMessages(fmt.Sprintf("user%d", g))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(msgs) != 5 {
			t.Fatalf("user%d: expected 5 unique messages, got %d", g, len(msgs))
		}
		total += len(msgs)
	}
	if total != 40 {
		t.Fatalf("expected 40 messages total, got %d", total)
	}
}

func TestMessageCacheAsyncFlush(t *testing.T) {
	db := openTestDB(t)
	cache := NewMessageCache(db)
	cache.SetFlushPeriod(50 * time.Millisecond)
	cache.Start()
	defer cache.Stop()

	cache.Add("live1", "userA", "nickA", "async hello")

	deadline := time.Now().Add(2 * time.Second)
	for {
		msgs, err := db.GetUserMessages("userA")
		if err == nil && len(msgs) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("message was not flushed within deadline")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
