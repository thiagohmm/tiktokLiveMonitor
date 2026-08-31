// Package report generates post-live summaries from deterministic live data.
package report

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

// Generator produces post-live reports.
type Generator struct {
	repo model.Repository
}

// New creates a report Generator.
func New(repo model.Repository) *Generator {
	return &Generator{repo: repo}
}

// Generate builds a report for the given live name.
func (g *Generator) Generate(ctx context.Context, liveName string) (model.LiveReport, error) {
	var report model.LiveReport
	report.LiveName = liveName

	firstSeen, err := g.repo.LiveFirstSeen(liveName)
	if err == nil {
		report.StartedAt = firstSeen
	}
	report.EndedAt = time.Now().Format(time.RFC3339)

	stats, err := g.repo.LiveStatsByUser(liveName)
	if err != nil {
		log.Printf("[Report] Error fetching live stats: %v", err)
	} else {
		report.ParticipantCount = len(stats)
		var gifts int
		for _, s := range stats {
			gifts += s.GiftCount
		}
		report.GiftCount = gifts
		report.MessageCount = totalMessages(stats)
		report.TopSupporters = topSupporters(stats)
	}

	gifts, _ := g.repo.GetRecentGifts(liveName, 1000)
	report.GiftTotal = giftTotal(gifts)
	report.GiftCount = len(gifts)

	byLive, _ := g.repo.GetAllUserMessages()
	report.FrequentQuestions = frequentQuestions(byLive)

	// Anomaly summary for moderation issues.
	var issues []model.AnomalySummary
	if logs, err := g.repo.GetAnomalyLogsByLiveName(liveName); err == nil {
		issues = anomalySummary(logs)
	}
	report.ModerationIssues = issues

	report.DurationMinutes = durationMinutes(report.StartedAt, report.EndedAt)

	return report, nil
}

func totalMessages(stats []model.LiveStat) int {
	n := 0
	for _, s := range stats {
		n += s.MessageCount
	}
	return n
}

func topSupporters(stats []model.LiveStat) []model.UserRank {
	ranks := make([]model.UserRank, 0, len(stats))
	for _, s := range stats {
		ranks = append(ranks, model.UserRank{
			UniqueID:      s.UniqueID,
			Nickname:      s.Nickname,
			GiftScore:     float64(s.GiftTotal),
			GiftCount:     s.GiftCount,
			MessageCount:  s.MessageCount,
			QuestionCount: s.QuestionCount,
			FirstSeen:     s.FirstSeen,
			LastSeen:      s.LastSeen,
		})
	}
	sortUserRanksByScore(ranks)
	if len(ranks) > 10 {
		ranks = ranks[:10]
	}
	return ranks
}

func giftTotal(gifts []model.Gift) int {
	total := 0
	for _, g := range gifts {
		total += g.RepeatCount
	}
	return total
}

func frequentQuestions(messages map[string][]model.UserMessage) []string {
	counts := map[string]int{}
	for _, list := range messages {
		for _, m := range list {
			key := strings.ToLower(strings.TrimSpace(m.Message))
			if key == "" || len(key) < 3 {
				continue
			}
			counts[key]++
		}
	}
	questions := make([]string, 0, len(counts))
	for q, c := range counts {
		if c >= 2 {
			questions = append(questions, q)
		}
	}
	sortStringsDescByCount(questions)
	if len(questions) > 8 {
		questions = questions[:8]
	}
	return questions
}

func anomalySummary(logs []model.AnomalyLog) []model.AnomalySummary {
	counts := map[string]int{}
	for _, l := range logs {
		if l.IsAnomaly {
			counts[l.Category]++
		}
	}
	out := make([]model.AnomalySummary, 0, len(counts))
	for cat, c := range counts {
		out = append(out, model.AnomalySummary{Category: cat, Count: c})
	}
	return out
}

func durationMinutes(started, ended string) int {
	t1, e1 := time.Parse(time.RFC3339, started)
	t2, e2 := time.Parse(time.RFC3339, ended)
	if e1 != nil || e2 != nil {
		return 0
	}
	min := int(t2.Sub(t1).Minutes())
	if min < 0 {
		min = 0
	}
	return min
}

// sortUserRanksByScore reorders UserRank by gift score descending (simple selection sort).
func sortUserRanksByScore(ranks []model.UserRank) {
	for i := 0; i < len(ranks); i++ {
		for j := i + 1; j < len(ranks); j++ {
			if ranks[j].GiftScore > ranks[i].GiftScore {
				ranks[i], ranks[j] = ranks[j], ranks[i]
			}
		}
	}
}

func sortStringsDescByCount(questions []string) {
	for i := 0; i < len(questions); i++ {
		for j := i + 1; j < len(questions); j++ {
			if questions[j] > questions[i] {
				questions[i], questions[j] = questions[j], questions[i]
			}
		}
	}
}
