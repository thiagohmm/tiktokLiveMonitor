package database

import (
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

const (
	// messageCacheMaxPerUser bounds how many unique messages are buffered
	// in memory per user (mirrors the DB pruning rule).
	messageCacheMaxPerUser = 10
	// messageCacheMaxPending triggers an out-of-band flush when the total
	// buffered messages across all users grows past this size.
	messageCacheMaxPending = 50
	// defaultMessageCacheFlushPeriod is how often the background flusher
	// writes batches to the database.
	defaultMessageCacheFlushPeriod = 2 * time.Second
)

type messageCacheEntry struct {
	liveName  string
	message   string // normalized: lower-case, trimmed
	username  string
	timestamp time.Time
}

// MessageCache is an in-memory write-behind cache for user messages.
//
// Add() performs no I/O: it dedups in memory, keeps only the newest
// messageCacheMaxPerUser unique messages per user and buffers them.
// A background goroutine (started via Start) flushes batches to the
// database every FlushPeriod, or immediately when the buffer exceeds
// messageCacheMaxPending entries. Stop flushes everything remaining.
//
// Data loss window: messages buffered but not yet flushed are lost on a
// process crash (bounded by FlushPeriod).
type MessageCache struct {
	db     *DB
	period time.Duration

	mu       sync.Mutex
	pending  map[string]map[string]messageCacheEntry // uniqueId(lower) -> message(lower) -> entry
	flushing bool

	done     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewMessageCache creates a cache flushing to the given DB.
func NewMessageCache(db *DB) *MessageCache {
	return &MessageCache{
		db:      db,
		period:  defaultMessageCacheFlushPeriod,
		pending: make(map[string]map[string]messageCacheEntry),
		done:    make(chan struct{}),
	}
}

// SetFlushPeriod overrides the background flush interval (mainly for tests).
func (c *MessageCache) SetFlushPeriod(d time.Duration) {
	if d <= 0 {
		return
	}
	c.mu.Lock()
	c.period = d
	c.mu.Unlock()
}

// Add buffers a user message in memory. It never performs I/O.
func (c *MessageCache) Add(liveName, uniqueID, username, message string) {
	uniqueID = strings.ToLower(strings.TrimSpace(uniqueID))
	message = strings.ToLower(strings.TrimSpace(message))
	username = strings.TrimSpace(username)
	liveName = strings.TrimSpace(liveName)
	if uniqueID == "" || message == "" {
		return
	}
	if username == "" {
		username = uniqueID
	}

	c.mu.Lock()
	m := c.pending[uniqueID]
	if m == nil {
		m = make(map[string]messageCacheEntry)
		c.pending[uniqueID] = m
	}
	total := 0
	for _, pm := range c.pending {
		total += len(pm)
	}
	if _, ok := m[message]; !ok {
		m[message] = messageCacheEntry{
			liveName:  liveName,
			message:   message,
			username:  username,
			timestamp: time.Now(),
		}
		total++
		c.pruneMemory(uniqueID)
	}
	c.mu.Unlock()

	if total >= messageCacheMaxPending {
		go c.Flush()
	}
}

// pendingLen returns the total number of buffered messages (for tests).
func (c *MessageCache) pendingLen() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	total := 0
	for _, m := range c.pending {
		total += len(m)
	}
	return total
}

// pruneMemory keeps only the messageCacheMaxPerUser most recent unique
// messages for the given user. Caller must hold c.mu.
func (c *MessageCache) pruneMemory(uniqueID string) {
	m := c.pending[uniqueID]
	if len(m) <= messageCacheMaxPerUser {
		return
	}
	entries := make([]messageCacheEntry, 0, len(m))
	for _, e := range m {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].timestamp.After(entries[j].timestamp)
	})
	pruned := make(map[string]messageCacheEntry, messageCacheMaxPerUser)
	for _, e := range entries[:messageCacheMaxPerUser] {
		pruned[e.message] = e
	}
	c.pending[uniqueID] = pruned
}

// Flush writes all buffered messages to the database in one batch.
func (c *MessageCache) Flush() {
	c.mu.Lock()
	if c.flushing {
		c.mu.Unlock()
		return
	}
	c.flushing = true
	entries := make([]UserMessageEntry, 0, len(c.pending))
	for uid, m := range c.pending {
		for _, e := range m {
			entries = append(entries, UserMessageEntry{
				LiveName:  e.liveName,
				UniqueID:  uid,
				Username:  e.username,
				Message:   e.message,
				Timestamp: e.timestamp,
			})
		}
	}
	c.pending = make(map[string]map[string]messageCacheEntry)
	c.flushing = false
	c.mu.Unlock()

	if len(entries) == 0 {
		return
	}
	if err := c.db.BatchAddUserMessages(entries); err != nil {
		// Buffered entries are lost on failure; they will usually be
		// replaced by live traffic. Log loudly either way.
		log.Printf("[MessageCache] flush failed (%d messages lost): %v", len(entries), err)
	}
}

// Snapshot returns a copy of the buffered messages that have not been
// flushed to the database yet (most recent additions).
func (c *MessageCache) Snapshot() []model.UserMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []model.UserMessage
	for uid, m := range c.pending {
		for _, e := range m {
			out = append(out, model.UserMessage{
				LiveName:  e.liveName,
				UniqueID:  uid,
				Username:  e.username,
				Message:   e.message,
				Timestamp: e.timestamp.Format("2006-01-02 15:04:05"),
			})
		}
	}
	return out
}

// Start launches the background flusher.
func (c *MessageCache) Start() {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.mu.Lock()
		period := c.period
		c.mu.Unlock()
		ticker := time.NewTicker(period)
		defer ticker.Stop()
		for {
			select {
			case <-c.done:
				return
			case <-ticker.C:
				c.Flush()
			}
		}
	}()
}

// Stop halts the background flusher and performs a final flush so no
// buffered messages are dropped on shutdown.
func (c *MessageCache) Stop() {
	c.stopOnce.Do(func() { close(c.done) })
	c.wg.Wait()
	c.Flush()
}
