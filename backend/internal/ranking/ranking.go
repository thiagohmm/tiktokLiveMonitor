// Package ranking computes participant engagement rankings for a live.
package ranking

import (
	"errors"
	"sort"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

var ErrUnparseableTimestamp = errors.New("ranking: unparseable timestamp")

// Weights defines how each signal contributes to a participant's score.
type Weights struct {
	// GiftPointPerGift awards points per received gift.
	GiftPointPerGift float64
	// GiftPointPerUnit awards points per unit of gift value (repeat count).
	GiftPointPerUnit float64
	// MessagePointPerMessage awards points per chat message. It is 0 by default
	// because chat messages are not a ranking criterion (presents, shares,
	// likes and questions are).
	MessagePointPerMessage float64
	// QuestionPointPerQuestion awards extra points per detected question.
	QuestionPointPerQuestion float64
	// SharePointPerShare awards points per live share.
	SharePointPerShare float64
	// LikePointPerLike awards points per like (heart) sent by a participant.
	LikePointPerLike float64
	// AnomalyPenaltyPerEvent subtracts 3 points from the score per repeated
	// message / spam anomaly detected, reducing the ranking position.
	AnomalyPenaltyPerEvent float64
}

// DefaultWeights are the default scoring weights.
// The ranking priority is driven by: presents > shares > likes > questions.
// Chat messages no longer determine who tops the ranking.
var DefaultWeights = Weights{
	GiftPointPerGift:         20,
	GiftPointPerUnit:         5,
	MessagePointPerMessage:   0,
	QuestionPointPerQuestion: 2,
	SharePointPerShare:       10,
	LikePointPerLike:         3,
	AnomalyPenaltyPerEvent:   3,
}

// ScoreResult is the computed score for a single participant.
type ScoreResult struct {
	UserRank   model.UserRank
	GiftScore  float64
	Score      float64
	RiskLevel  string
}

// Ranker computes engagement rankings.
type Ranker struct {
	weights Weights
}

// New creates a Ranker with the given weights (falls back to DefaultWeights).
func New(weights Weights) *Ranker {
	if weights.GiftPointPerGift == 0 &&
		weights.GiftPointPerUnit == 0 &&
		weights.MessagePointPerMessage == 0 &&
		weights.QuestionPointPerQuestion == 0 &&
		weights.SharePointPerShare == 0 {
		weights = DefaultWeights
	}
	return &Ranker{weights: weights}
}

// Compute turns per-user live stats into a ranked slice, highest score first.
func (r *Ranker) Compute(stats []model.LiveStat, anomaliesByUser map[string]int) []ScoreResult {
	results := make([]ScoreResult, 0, len(stats))
	for _, s := range stats {
		giftScore := r.weights.GiftPointPerGift*float64(s.GiftCount) +
			r.weights.GiftPointPerUnit*float64(s.GiftTotal)
		messageScore := r.weights.MessagePointPerMessage*float64(s.MessageCount)
		questionScore := r.weights.QuestionPointPerQuestion*float64(s.QuestionCount)
		likeScore := r.weights.LikePointPerLike * float64(s.LikeCount)
		shareScore := r.weights.SharePointPerShare * float64(s.ShareCount)
		// Repeated messages / spam reduce the score (penalty subtracts, never adds).
		anomalyCount := anomaliesByUser[s.UniqueID]
		anomalyPenalty := r.weights.AnomalyPenaltyPerEvent * float64(anomalyCount)

		score := giftScore + messageScore + questionScore + shareScore + likeScore - anomalyPenalty
		if score < 0 {
			score = 0
		}

		results = append(results, ScoreResult{
			UserRank: model.UserRank{
				UniqueID:      s.UniqueID,
				Nickname:      s.Nickname,
				Score:         score,
				GiftScore:     giftScore,
				MessageCount:  s.MessageCount,
				QuestionCount: s.QuestionCount,
				GiftCount:     s.GiftCount,
				ShareCount:    s.ShareCount,
				LikeCount:     s.LikeCount,
				AnomalyCount:  anomalyCount,
				RiskLevel:     riskLevelFor(anomalyCount, score),
				FirstSeen:     formatTS(s.FirstSeen),
				LastSeen:      formatTS(s.LastSeen),
			},
			GiftScore: giftScore,
			Score:     score,
			RiskLevel: riskLevelFor(anomalyCount, score),
		})
	}

	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].UserRank.Nickname < results[j].UserRank.Nickname
	})
	return results
}

// BuildLiveRanking assembles a LiveRanking from stats, anomalies and a live name.
func (r *Ranker) BuildLiveRanking(liveName string, stats []model.LiveStat, anomaliesByUser map[string]int) model.LiveRanking {
	ranked := r.Compute(stats, anomaliesByUser)
	ranks := make([]model.UserRank, 0, len(ranked))
	for _, res := range ranked {
		ranks = append(ranks, res.UserRank)
	}
	return model.LiveRanking{
		LiveName:   liveName,
		UpdatedAt:  time.Now().UTC().Format(time.RFC3339),
		TotalUsers: len(ranks),
		UserRanks:  ranks,
	}
}

// riskLevelFor derives a risk level from anomaly count and score.
func riskLevelFor(anomalyCount int, score float64) string {
	switch {
	case anomalyCount >= 4:
		return model.RiskLevelCritical
	case anomalyCount >= 2:
		return model.RiskLevelHigh
	case anomalyCount == 1:
		return model.RiskLevelMedium
	case score > 0:
		return model.RiskLevelLow
	default:
		return model.RiskLevelNone
	}
}

// formatTS normalizes a raw stored timestamp to RFC3339 UTC, returning "" when empty.
func formatTS(raw string) string {
	raw = trimRawTS(raw)
	if raw == "" {
		return ""
	}
	if at, err := parseTS(raw); err == nil {
		return at.UTC().Format(time.RFC3339)
	}
	return raw
}

func trimRawTS(raw string) string {
	out := make([]rune, 0, len(raw))
	for _, r := range raw {
		if r >= '0' && r <= '9' || r == '-' || r == ':' || r == 'T' || r == '.' {
			out = append(out, r)
		}
	}
	return string(out)
}

func parseTS(raw string) (time.Time, error) {
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t, nil
		}
	}
	return time.Time{}, ErrUnparseableTimestamp
}