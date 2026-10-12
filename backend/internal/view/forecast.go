package view

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

// handleForecast returns the engagement forecast of the caller's organization.
// A failure here is a normal API error: it does not affect readiness.
func (s *HTTPServer) handleForecast(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	t, ok := requestTenant(w, r)
	if !ok {
		return
	}
	view, err := s.controller.EngagementForecast(t.OrgID, r.URL.Query().Get("live"))
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	writeJSON(w, view)
}

// publishForecasts pushes the latest forecast to each organization that has
// a live open. A database error is logged and the loop continues.
func (s *HTTPServer) publishForecasts(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.publishForecastTick()
		}
	}
}

func (s *HTTPServer) publishForecastTick() {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("[View] previsão: panic isolado: %v", rec)
		}
	}()
	manager := s.controller.GetMonitorManager()
	if manager == nil {
		return
	}
	for _, live := range manager.ActiveLives() {
		if live.OrgID == "" || live.LiveID == "" {
			continue
		}
		view, err := s.controller.EngagementForecast(live.OrgID, live.Live)
		if err != nil {
			log.Printf("[View] previsão da live %s: %v", live.Live, err)
			continue
		}
		if view.Status == model.ForecastDisabled || view.Status == model.ForecastIdle {
			continue
		}
		s.publishSSE(live.OrgID, "engagement-forecast", view)
	}
}
