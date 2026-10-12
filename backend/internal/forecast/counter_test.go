package forecast

import (
	"errors"
	"testing"
	"time"
)

type memSink struct {
	rows []stored
	err  error
}

type stored struct {
	orgID, liveID string
	bucket        time.Time
	messages      int
	likes         int
}

func (m *memSink) AddMinuteMetrics(orgID, liveID string, bucket time.Time, messages, likes, shares, giftUnits, giftDiamonds int) error {
	if m.err != nil {
		return m.err
	}
	m.rows = append(m.rows, stored{orgID: orgID, liveID: liveID, bucket: bucket, messages: messages, likes: likes})
	return nil
}

func TestAddDropsWhenFull(t *testing.T) {
	c := NewCounter(&memSink{})
	c.ch = make(chan sample, 1)
	c.Add("org", "live", Delta{Messages: 1})
	start := time.Now()
	for i := 0; i < 1000; i++ {
		c.Add("org", "live", Delta{Messages: 1})
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Add blocked for %s", elapsed)
	}
	c.mu.Lock()
	dropped := c.dropped
	c.mu.Unlock()
	if dropped != 1000 {
		t.Fatalf("dropped = %d, want 1000", dropped)
	}
}

func TestFlushWritesOnlyCompletedMinutes(t *testing.T) {
	sink := &memSink{}
	c := NewCounter(sink)
	bucket := time.Date(2026, 10, 12, 15, 0, 0, 0, time.UTC)
	c.accumulate(sample{orgID: "org", liveID: "live", bucket: bucket, delta: Delta{Messages: 2}})
	c.accumulate(sample{orgID: "org", liveID: "live", bucket: bucket, delta: Delta{Messages: 3}})
	c.accumulate(sample{orgID: "org", liveID: "live", bucket: bucket.Add(time.Minute), delta: Delta{Likes: 4}})

	c.flush(bucket.Add(time.Minute))
	if len(sink.rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(sink.rows))
	}
	if sink.rows[0].messages != 5 || !sink.rows[0].bucket.Equal(bucket) {
		t.Fatalf("row = %+v", sink.rows[0])
	}
	if len(c.pending) != 1 {
		t.Fatalf("pending = %d, want the open minute", len(c.pending))
	}

	c.flush(bucket.Add(2 * time.Minute))
	if len(sink.rows) != 2 || sink.rows[1].likes != 4 {
		t.Fatalf("rows = %+v", sink.rows)
	}
}

func TestFlushKeepsBucketWhenSinkFails(t *testing.T) {
	sink := &memSink{err: errors.New("db down")}
	c := NewCounter(sink)
	bucket := time.Date(2026, 10, 12, 15, 0, 0, 0, time.UTC)
	c.accumulate(sample{orgID: "org", liveID: "live", bucket: bucket, delta: Delta{Shares: 1}})
	c.flush(bucket.Add(time.Minute))
	if len(c.pending) != 1 {
		t.Fatal("failed flush dropped the minute")
	}
	sink.err = nil
	c.flush(bucket.Add(time.Minute))
	if len(sink.rows) != 1 || len(c.pending) != 0 {
		t.Fatalf("retry rows=%d pending=%d", len(sink.rows), len(c.pending))
	}
}

func TestMinLiveMinutesOverride(t *testing.T) {
	if MinLiveMinutes() != minLiveMinutes {
		t.Fatalf("default = %d", MinLiveMinutes())
	}
	t.Setenv("FORECAST_MIN_MINUTES", "2")
	if MinLiveMinutes() != 2 {
		t.Fatalf("override = %d", MinLiveMinutes())
	}
	t.Setenv("FORECAST_MIN_MINUTES", "nope")
	if MinLiveMinutes() != minLiveMinutes {
		t.Fatalf("invalid override = %d", MinLiveMinutes())
	}
}

func TestEnabledDefaultsOff(t *testing.T) {
	t.Setenv("FORECAST_ENABLED", "")
	if Enabled() {
		t.Fatal("empty flag should be off")
	}
	t.Setenv("FORECAST_ENABLED", "1")
	if !Enabled() {
		t.Fatal("1 should enable")
	}
	t.Setenv("FORECAST_ENABLED", "off")
	if Enabled() {
		t.Fatal("off should disable")
	}
}
