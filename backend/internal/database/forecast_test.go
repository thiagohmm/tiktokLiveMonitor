package database

import (
	"testing"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

func TestMinuteMetricsAccumulateAndFollowSessionDelete(t *testing.T) {
	db := openTestDB(t)
	ref := testRef(t, db, "forecast-live")
	bucket := time.Date(2026, 10, 12, 18, 0, 0, 0, time.UTC)
	if err := db.AddMinuteMetrics(ref.OrgID, ref.ID, bucket, 1, 2, 3, 4, 5); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := db.AddMinuteMetrics(ref.OrgID, ref.ID, bucket.Add(30*time.Second), 1, 0, 0, 0, 0); err != nil {
		t.Fatalf("add again: %v", err)
	}
	n, err := db.CountMinuteBuckets(ref.OrgID, ref.ID)
	if err != nil || n != 1 {
		t.Fatalf("count = %d, %v", n, err)
	}
	var messages int
	if err := db.queryRow(
		`SELECT messages FROM live_minute_metrics WHERE org_id = ? AND live_id = ?`,
		ref.OrgID, ref.ID,
	).Scan(&messages); err != nil {
		t.Fatal(err)
	}
	if messages != 2 {
		t.Fatalf("messages = %d, want 2", messages)
	}
	other, err := db.CountMinuteBuckets("org-alheia", ref.ID)
	if err != nil || other != 0 {
		t.Fatalf("other org count = %d, %v", other, err)
	}

	if _, err := db.DeleteLiveSession(ref.ID); err != nil {
		t.Fatal(err)
	}
	n, err = db.CountMinuteBuckets(ref.OrgID, ref.ID)
	if err != nil || n != 0 {
		t.Fatalf("after delete count = %d, %v", n, err)
	}
}

func TestReadForecastIsScopedToOrg(t *testing.T) {
	db := openTestDB(t)
	ref := testRef(t, db, "forecast-live")
	computed := time.Now().UTC().Truncate(time.Second)
	horizons := []model.ForecastHorizon{
		{Minutes: 5, Messages: 10, Likes: 20, Shares: 1, GiftUnits: 2, GiftDiamonds: 30},
		{Minutes: 10, Messages: 15, GiftDiamonds: 40},
	}
	if err := db.ReplaceForecast(ref.OrgID, ref.ID, "test-model", computed, horizons); err != nil {
		t.Fatal(err)
	}
	version, at, got, err := db.ReadForecast(ref.OrgID, ref.ID)
	if err != nil {
		t.Fatal(err)
	}
	if version != "test-model" || len(got) != 2 || got[0].Messages != 10 || got[1].GiftDiamonds != 40 {
		t.Fatalf("forecast version=%s rows=%+v", version, got)
	}
	if at.Unix() != computed.Unix() {
		t.Fatalf("computed = %s, want %s", at, computed)
	}
	_, _, hidden, err := db.ReadForecast("org-alheia", ref.ID)
	if err != nil || len(hidden) != 0 {
		t.Fatalf("other org saw %+v (%v)", hidden, err)
	}
	if _, err := db.DeleteLiveSession(ref.ID); err != nil {
		t.Fatal(err)
	}
	_, _, gone, err := db.ReadForecast(ref.OrgID, ref.ID)
	if err != nil || len(gone) != 0 {
		t.Fatalf("after delete %+v (%v)", gone, err)
	}
}
