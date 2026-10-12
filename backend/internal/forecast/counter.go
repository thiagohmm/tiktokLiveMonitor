// Package forecast counts live activity per minute and hands it to storage
// without ever blocking the capture path.
package forecast

import (
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	channelSize    = 4096
	flushInterval  = time.Minute
	freshFor       = 2 * time.Minute
	minLiveMinutes = 15
	inputWindow    = 30
)

// FreshFor is how long a stored forecast stays usable after computed_at.
func FreshFor() time.Duration { return freshFor }

// MinLiveMinutes is the cold-start threshold before a number is shown.
// FORECAST_MIN_MINUTES overrides it (the local compose uses a shorter window).
func MinLiveMinutes() int {
	raw := strings.TrimSpace(os.Getenv("FORECAST_MIN_MINUTES"))
	if raw == "" {
		return minLiveMinutes
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return minLiveMinutes
	}
	return n
}

// Enabled reports whether minute collection and forecast publication are on.
// The default is off, so a deploy that does not set the flag keeps the
// previous capture behavior.
func Enabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FORECAST_ENABLED"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// MetricsSink persists one completed minute. Implementations must be safe to
// call from the counter goroutine only.
type MetricsSink interface {
	AddMinuteMetrics(orgID, liveID string, bucket time.Time, messages, likes, shares, giftUnits, giftDiamonds int) error
}

// Delta is one observed burst. Zero counts are ignored.
type Delta struct {
	Messages     int
	Likes        int
	Shares       int
	GiftUnits    int
	GiftDiamonds int
}

func (d Delta) empty() bool {
	return d.Messages == 0 && d.Likes == 0 && d.Shares == 0 && d.GiftUnits == 0 && d.GiftDiamonds == 0
}

type sample struct {
	orgID  string
	liveID string
	bucket time.Time
	delta  Delta
}

type minuteKey struct {
	orgID  string
	liveID string
	bucket int64
}

type totals struct {
	messages     int
	likes        int
	shares       int
	giftUnits    int
	giftDiamonds int
}

// Counter aggregates events in memory and upserts each finished minute.
// Add drops the sample when the buffer is full; it never blocks the caller.
type Counter struct {
	sink     MetricsSink
	ch       chan sample
	stop     chan struct{}
	done     chan struct{}
	mu       sync.Mutex
	closed   bool
	started  bool
	dropped  int
	pending  map[minuteKey]totals
	interval time.Duration
}

// NewCounter builds a counter. Start runs the flush loop.
func NewCounter(sink MetricsSink) *Counter {
	return &Counter{
		sink:     sink,
		ch:       make(chan sample, channelSize),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
		pending:  make(map[minuteKey]totals),
		interval: flushInterval,
	}
}

// Start begins the flush loop. Stop waits until the pending minutes are written.
func (c *Counter) Start() {
	c.mu.Lock()
	if c.started || c.closed {
		c.mu.Unlock()
		return
	}
	c.started = true
	c.mu.Unlock()
	go c.loop()
}

// Stop rejects new samples, flushes what is still in memory and returns.
// Calling Stop without Start only rejects later samples.
func (c *Counter) Stop() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	started := c.started
	c.mu.Unlock()
	if !started {
		return
	}
	close(c.stop)
	<-c.done
}

// Add records activity for the current UTC minute.
func (c *Counter) Add(orgID, liveID string, delta Delta) {
	c.add(time.Now().UTC().Truncate(time.Minute), orgID, liveID, delta)
}

func (c *Counter) add(bucket time.Time, orgID, liveID string, delta Delta) {
	orgID = strings.TrimSpace(orgID)
	liveID = strings.TrimSpace(liveID)
	if orgID == "" || liveID == "" || delta.empty() {
		return
	}
	sample := sample{orgID: orgID, liveID: liveID, bucket: bucket.UTC(), delta: delta}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	select {
	case c.ch <- sample:
	default:
		c.dropped++
	}
}

func (c *Counter) loop() {
	defer close(c.done)
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-c.stop:
			c.drain()
			c.flush(time.Now().UTC().Truncate(time.Minute).Add(time.Minute))
			return
		case sample := <-c.ch:
			c.accumulate(sample)
		case now := <-ticker.C:
			c.flush(now.UTC())
		}
	}
}

func (c *Counter) drain() {
	for {
		select {
		case sample := <-c.ch:
			c.accumulate(sample)
		default:
			return
		}
	}
}

func (c *Counter) accumulate(sample sample) {
	key := minuteKey{orgID: sample.orgID, liveID: sample.liveID, bucket: sample.bucket.Unix()}
	total := c.pending[key]
	total.messages += sample.delta.Messages
	total.likes += sample.delta.Likes
	total.shares += sample.delta.Shares
	total.giftUnits += sample.delta.GiftUnits
	total.giftDiamonds += sample.delta.GiftDiamonds
	c.pending[key] = total
}

// flush writes every bucket strictly before now and leaves the open minute
// in memory. A sink error keeps the bucket for the next attempt.
func (c *Counter) flush(now time.Time) {
	c.mu.Lock()
	dropped := c.dropped
	c.dropped = 0
	c.mu.Unlock()
	if dropped > 0 {
		log.Printf("[Forecast] %d amostras descartadas; a captura da live não foi interrompida", dropped)
	}
	cutoff := now.UTC().Truncate(time.Minute).Unix()
	for key, total := range c.pending {
		if key.bucket >= cutoff {
			continue
		}
		err := c.sink.AddMinuteMetrics(
			key.orgID, key.liveID, time.Unix(key.bucket, 0).UTC(),
			total.messages, total.likes, total.shares, total.giftUnits, total.giftDiamonds,
		)
		if err != nil {
			log.Printf("[Forecast] falha ao gravar minuto da live %s: %v", key.liveID, err)
			continue
		}
		delete(c.pending, key)
	}
}
