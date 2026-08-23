// Package report generates AI-assisted post-live summaries using the local LLM.
package report

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/ai"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

// Generator produces post-live reports.
type Generator struct {
	ai   *ai.Manager
	repo model.Repository
}

// New creates a report Generator.
func New(aiManager *ai.Manager, repo model.Repository) *Generator {
	return &Generator{ai: aiManager, repo: repo}
}

// Prompt builds the prompt that asks the LLM to summarise a live.
func Prompt(r model.LiveReport) string {
	var b strings.Builder
	b.WriteString("Você é um assistente de análise de transmissões ao vivo (TikTok Live).\n")
	b.WriteString(fmt.Sprintf("Nome da live/streamer: %s\n", r.LiveName))
	b.WriteString(fmt.Sprintf("Duração: %d minutos\n", r.DurationMinutes))
	b.WriteString(fmt.Sprintf("Período: de %s até %s\n", r.StartedAt, r.EndedAt))
	b.WriteString(fmt.Sprintf("Total de mensagens: %d\n", r.MessageCount))
	b.WriteString(fmt.Sprintf("Participantes únicos: %d\n", r.ParticipantCount))
	b.WriteString(fmt.Sprintf("Total de presentes: %d (valor estimado %d)\n", r.GiftCount, r.GiftTotal))

	if len(r.TopSupporters) > 0 {
		b.WriteString("\nPrincipais apoiadores:\n")
		for _, s := range r.TopSupporters {
			b.WriteString(fmt.Sprintf("- %s (%s): presentes=%d, mensagens=%d, perguntas=%d, score=%.1f\n",
				s.Nickname, s.UniqueID, s.GiftCount, s.MessageCount, s.QuestionCount, s.Score))
		}
	}

	if len(r.FrequentQuestions) > 0 {
		b.WriteString("\nPerguntas frequentes:\n")
		for _, q := range r.FrequentQuestions {
			b.WriteString("- " + q + "\n")
		}
	}

	if len(r.ModerationIssues) > 0 {
		b.WriteString("\nProblemas de moderação (por categoria):\n")
		for _, i := range r.ModerationIssues {
			b.WriteString(fmt.Sprintf("- %s: %d ocorrência(s)\n", i.Category, i.Count))
		}
	}

	b.WriteString("\n")
	b.WriteString("Produza um relatório conciso em português (br) com até 8 linhas:\n")
	b.WriteString("1) Resumo geral do clima da live.\n")
	b.WriteString("2) Destaques de participação e apoio.\n")
	b.WriteString("3) Perguntas mais recorrentes.\n")
	b.WriteString("4) Observações de moderação e recomendações.\n")
	b.WriteString("Responda apenas com o texto do relatório, sem marcações.\n")
	return b.String()
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

	if g.ai != nil {
		p := Prompt(report)
		resp, err := g.ai.Complete(ctx, ai.CompletionRequest{
			SystemContent: "Você é um analista de transmissões ao vivo.",
			UserContent:   p,
			MaxTokens:     512,
		})
		if err != nil {
			log.Printf("[Report] AI report error: %v", err)
		} else {
			report.Summary = strings.TrimSpace(resp)
		}
	}

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
			AnomalyCount:  s.QuestionCount,
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