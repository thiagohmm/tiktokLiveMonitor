package ranking

import (
	"testing"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

func TestComputeOrdersByScore(t *testing.T) {
	r := New(DefaultWeights)

	stats := []model.LiveStat{
		{UniqueID: "a", Nickname: "Apos", MessageCount: 50, GiftCount: 0, GiftTotal: 0, GiftValue: 0},
		{UniqueID: "b", Nickname: "Doador", MessageCount: 5, GiftCount: 3, GiftTotal: 10, GiftValue: 10},
		{UniqueID: "c", Nickname: "Top", MessageCount: 20, GiftCount: 5, GiftTotal: 100, GiftValue: 2500},
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
	if lr.Mode != model.ModeEngagement {
		t.Fatalf("mode = %s, want %s", lr.Mode, model.ModeEngagement)
	}
}

func TestComputeTikTokRanksByDiamonds(t *testing.T) {
	r := New(DefaultWeights)
	stats := []model.LiveStat{
		{UniqueID: "chatty", Nickname: "Chatty", MessageCount: 500, LikeCount: 900},
		{UniqueID: "rich", Nickname: "Rich", GiftCount: 2, GiftTotal: 10, GiftValue: 500},
		{UniqueID: "steady", Nickname: "Steady", GiftCount: 10, GiftTotal: 30, GiftValue: 300},
		{UniqueID: "tieB", Nickname: "TieB", GiftCount: 1, GiftTotal: 3, GiftValue: 300},
		{UniqueID: "tieA", Nickname: "TieA", GiftCount: 5, GiftTotal: 15, GiftValue: 300},
	}
	res := r.ComputeTikTok(stats)
	got := []string{}
	for _, x := range res {
		got = append(got, x.UserRank.UniqueID)
	}
	want := []string{"rich", "steady", "tieA", "tieB", "chatty"} // gift count breaks diamond ties
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
	// Top 3 tiers: crown, headband, medal.
	if res[0].UserRank.Tier != model.TierCrown {
		t.Fatalf("#1 tier = %s, want crown", res[0].UserRank.Tier)
	}
	if res[1].UserRank.Tier != model.TierHeadband {
		t.Fatalf("#2 tier = %s, want headband", res[1].UserRank.Tier)
	}
	if res[2].UserRank.Tier != model.TierMedal {
		t.Fatalf("#3 tier = %s, want medal", res[2].UserRank.Tier)
	}
	if res[3].UserRank.Tier != "" {
		t.Fatalf("#4 tier = %s, want empty", res[3].UserRank.Tier)
	}
	// Score mirrors diamond value.
	if res[0].Score != 500 || res[0].UserRank.Diamonds != 500 {
		t.Fatalf("#1 score/diamonds = %f/%d, want 500/500", res[0].Score, res[0].UserRank.Diamonds)
	}
}

func TestBuildTikTokRanking(t *testing.T) {
	r := New(DefaultWeights)
	stats := []model.LiveStat{{UniqueID: "u1", Nickname: "User1", GiftCount: 3, GiftTotal: 42, GiftValue: 210}}
	lr := r.BuildTikTokRanking("streamer1", stats)
	if lr.Mode != model.ModeTikTok {
		t.Fatalf("mode = %s, want %s", lr.Mode, model.ModeTikTok)
	}
	if lr.TotalUsers != 1 || len(lr.UserRanks) != 1 {
		t.Fatalf("unexpected ranking length: total=%d ranks=%d", lr.TotalUsers, len(lr.UserRanks))
	}
	if lr.UserRanks[0].Tier != model.TierCrown {
		t.Fatalf("solo gifter tier = %s, want crown", lr.UserRanks[0].Tier)
	}
}