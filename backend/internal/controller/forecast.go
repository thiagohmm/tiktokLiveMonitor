package controller

import (
	"log"
	"strings"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/forecast"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

// forecastReader is the slice of the database the forecast panel needs.
// A repository that does not implement it yields "unavailable" and never
// fails the rest of the API.
type forecastReader interface {
	OpenLiveSession(orgID, liveName string) (model.LiveSession, bool, error)
	CountMinuteBuckets(orgID, liveID string) (int, error)
	ReadForecast(orgID, liveID string) (string, time.Time, []model.ForecastHorizon, error)
}

// SetForecastCounter attaches the minute counter. Nil leaves collection off.
func (c *AppController) SetForecastCounter(counter *forecast.Counter) {
	c.forecast = counter
}

func (c *AppController) noteForecast(ref model.LiveRef, delta forecast.Delta) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("[Controller] previsão ignorada: %v", rec)
		}
	}()
	if c.forecast == nil || !ref.Valid() {
		return
	}
	c.forecast.Add(ref.OrgID, ref.ID, delta)
}

// EngagementForecast resolves the open live of the organization and the
// latest model output. Storage errors are returned to the caller so the HTTP
// layer can log them; they are not tied to process health.
func (c *AppController) EngagementForecast(orgID, liveName string) (model.EngagementForecast, error) {
	if !forecast.Enabled() {
		return model.EngagementForecast{Status: model.ForecastDisabled}, nil
	}
	liveName = c.ResolveLive(orgID, liveName)
	if liveName == "" {
		return model.EngagementForecast{Status: model.ForecastIdle}, nil
	}
	reader, ok := c.repo.(forecastReader)
	if !ok {
		return model.EngagementForecast{Status: model.ForecastUnavailable, LiveName: liveName}, nil
	}
	session, open, err := reader.OpenLiveSession(orgID, liveName)
	if err != nil {
		return model.EngagementForecast{}, err
	}
	if !open {
		return model.EngagementForecast{Status: model.ForecastIdle, LiveName: liveName}, nil
	}
	minutes, err := reader.CountMinuteBuckets(orgID, session.ID)
	if err != nil {
		return model.EngagementForecast{}, err
	}
	version, computed, horizons, err := reader.ReadForecast(orgID, session.ID)
	if err != nil {
		return model.EngagementForecast{}, err
	}
	return buildEngagementForecast(liveName, session.ID, minutes, computed, version, horizons, time.Now().UTC()), nil
}

func buildEngagementForecast(liveName, liveID string, minutes int, computed time.Time, version string, horizons []model.ForecastHorizon, now time.Time) model.EngagementForecast {
	view := model.EngagementForecast{
		Status:   model.ForecastIdle,
		LiveName: strings.TrimSpace(liveName),
		LiveID:   strings.TrimSpace(liveID),
	}
	if view.LiveName == "" || view.LiveID == "" {
		return model.EngagementForecast{Status: model.ForecastIdle}
	}
	if minutes < forecast.MinLiveMinutes() {
		view.Status = model.ForecastCollecting
		return view
	}
	fresh := !computed.IsZero() && now.Sub(computed) <= forecast.FreshFor() && len(horizons) > 0
	if !fresh {
		view.Status = model.ForecastUnavailable
		return view
	}
	view.Status = model.ForecastReady
	view.ComputedAt = computed.UTC().Format(time.RFC3339)
	view.ModelVersion = version
	view.Horizons = horizons
	return view
}
