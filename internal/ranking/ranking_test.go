package ranking

import (
	"testing"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

func TestComputeOrdersByScore(t *testing.T) {
	r := New(DefaultWeights)

	stats := []model.LiveStat{
		{UniqueID: "a", Nickname: "Apos", MessageCount: 50, GiftCount: 0, GiftTotal: 0},
		{UniqueID: "b", Nickname: "Doador", MessageCount: 5, GiftCount: 3, GiftTotal: 10},
		{UniqueID: "c", Nickname: "Top", MessageCount: 20, GiftCount: 5, GiftTotal: 100},
	}
	anomalies := map[string]int{"b": 1}

	results := r.Compute(stats, anomalies)
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	// Top donor should rank first.
	if results[0].UserRank.UniqueID != "c" {
		t.Fatalf("expected top user c, got %s", results[0].UserRank.UniqueID)
	}
	// User b has an anomaly penalty but still some gift score, above pure chatter a.
	if results[1].UserRank.UniqueID != "b" {
		t.Fatalf("expected second user b, got %s", results[1].UserRank.UniqueID)
	}
	if results[2].UserRank.UniqueID != "a" {
		t.Fatalf("expected third user a, got %s", results[2].UserRank.UniqueID)
	}
}

func TestRiskLevel(t *testing.T) {
	r := New(DefaultWeights)
	stats := []model.LiveStat{
		{UniqueID: "clean", Nickname: "Clean"},
		{UniqueID: "warned", Nickname: "Warned", MessageCount: 3},
		{UniqueID: "bad", Nickname: "Bad", MessageCount: 3},
	}
	anomalies := map[string]int{"warned": 1, "bad": 4}

	results := r.Compute(stats, anomalies)
	byID := map[string]ScoreResult{}
	for _, res := range results {
		byID[res.UserRank.UniqueID] = res
	}
	if byID["clean"].UserRank.RiskLevel != model.RiskLevelNone && byID["clean"].UserRank.RiskLevel != model.RiskLevelLow {
		t.Fatalf("clean user risk = %s, want none/low", byID["clean"].UserRank.RiskLevel)
	}
	if byID["warned"].UserRank.RiskLevel != model.RiskLevelMedium {
		t.Fatalf("warned user risk = %s, want medium", byID["warned"].UserRank.RiskLevel)
	}
	if byID["bad"].UserRank.RiskLevel != model.RiskLevelCritical {
		t.Fatalf("bad user risk = %s, want critical", byID["bad"].UserRank.RiskLevel)
	}
}

func TestBuildLiveRanking(t *testing.T) {
	r := New(DefaultWeights)
	stats := []model.LiveStat{{UniqueID: "u1", Nickname: "User1", MessageCount: 10}}
	lr := r.BuildLiveRanking("streamer1", stats, nil)
	if lr.LiveName != "streamer1" {
		t.Fatalf("liveName = %s, want streamer1", lr.LiveName)
	}
	if lr.TotalUsers != 1 || len(lr.UserRanks) != 1 {
		t.Fatalf("unexpected ranking length: total=%d ranks=%d", lr.TotalUsers, len(lr.UserRanks))
	}
	if lr.UserRanks[0].Score < 0 {
		t.Fatalf("score should never be negative, got %f", lr.UserRanks[0].Score)
	}
}