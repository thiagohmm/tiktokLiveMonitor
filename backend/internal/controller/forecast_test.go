package controller

import (
	"testing"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/forecast"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

func TestBuildEngagementForecastStates(t *testing.T) {
	now := time.Date(2026, 10, 12, 16, 0, 0, 0, time.UTC)
	horizons := []model.ForecastHorizon{{Minutes: 5, Messages: 3}, {Minutes: 10, Messages: 6}}
	tests := []struct {
		name     string
		minutes  int
		computed time.Time
		horizons []model.ForecastHorizon
		want     string
	}{
		{name: "collecting", minutes: 14, computed: now, horizons: horizons, want: model.ForecastCollecting},
		{name: "ready", minutes: 15, computed: now.Add(-time.Minute), horizons: horizons, want: model.ForecastReady},
		{name: "stale", minutes: 20, computed: now.Add(-3 * time.Minute), horizons: horizons, want: model.ForecastUnavailable},
		{name: "missing", minutes: 20, horizons: nil, want: model.ForecastUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildEngagementForecast("live", "sess", tt.minutes, tt.computed, "v1", tt.horizons, now)
			if got.Status != tt.want {
				t.Fatalf("status = %s, want %s", got.Status, tt.want)
			}
			if tt.want == model.ForecastReady && (got.ComputedAt == "" || len(got.Horizons) != 2) {
				t.Fatalf("ready payload = %+v", got)
			}
			if tt.want != model.ForecastReady && len(got.Horizons) != 0 {
				t.Fatalf("non-ready payload leaked horizons: %+v", got)
			}
		})
	}
}

func TestNoteForecastFullChannelDoesNotBlock(t *testing.T) {
	counter := forecast.NewCounter(nopSink{})
	counterFill(t, counter)
	c := &AppController{}
	c.SetForecastCounter(counter)
	start := time.Now()
	for i := 0; i < 1000; i++ {
		c.noteForecast(model.LiveRef{ID: "live", OrgID: "org"}, forecast.Delta{Messages: 1})
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("noteForecast blocked for %s", elapsed)
	}
}

type nopSink struct{}

func (nopSink) AddMinuteMetrics(string, string, time.Time, int, int, int, int, int) error {
	return nil
}

func counterFill(t *testing.T, counter *forecast.Counter) {
	t.Helper()
	for i := 0; i < 5000; i++ {
		counter.Add("org", "live", forecast.Delta{Messages: 1})
	}
}
