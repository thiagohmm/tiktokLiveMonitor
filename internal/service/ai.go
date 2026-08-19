// Package service contains business logic that orchestrates repositories and domain services.
package service

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/ai"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"github.com/thiagohmm/tiktok-live-monitor/internal/monitor"
)

// AskAI builds context from repository data and asks the AI a question.
func AskAI(ctx context.Context, question string, aiManager *ai.Manager, repo model.Repository) (string, error) {
	giftSummary, err := repo.GetGiftSummary()
	if err != nil {
		log.Printf("[Service-AI] Error fetching gift summary: %v", err)
	}

	allMessages, err := repo.GetAllUserMessages()
	if err != nil {
		log.Printf("[Service-AI] Error fetching user messages: %v", err)
	}

	contextText := buildAIContext(allMessages, giftSummary)

	systemPrompt := fmt.Sprintf(`Você é um assistente que responde perguntas sobre uma live do TikTok.
Use os dados abaixo para responder. Busque informações nos dados de mensagens e presentes.
Se a pergunta não estiver relacionada aos dados disponíveis, diga que não encontrou informações.

%s

Responda em português do Brasil de forma direta e concisa.`, contextText)

	req := ai.CompletionRequest{
		SystemContent: systemPrompt,
		UserContent:   question,
		MaxTokens:     512,
	}

	return aiManager.Complete(ctx, req)
}

func buildAIContext(allMessages map[string][]model.UserMessage, giftSummary map[string]map[string]int) string {
	var sb strings.Builder

	const maxMessages = 50
	const maxGiftUsers = 10
	const maxGiftTypesPerUser = 5
	const maxMessageLen = 200

	if len(allMessages) > 0 {
		sb.WriteString("MENSAGENS RECENTES NA LIVE:\n")
		msgCount := 0
		for uid, msgs := range allMessages {
			if msgCount >= maxMessages {
				break
			}
			sb.WriteString(fmt.Sprintf("\n  %s:\n", uid))
			for _, m := range msgs {
				if msgCount >= maxMessages {
					break
				}
				text := m.Message
				if len(text) > maxMessageLen {
					text = text[:maxMessageLen]
				}
				sb.WriteString(fmt.Sprintf("    - %s\n", text))
				msgCount++
			}
		}
	} else {
		sb.WriteString("NENHUMA MENSAGEM DE USUÁRIO REGISTRADA.\n")
	}

	sb.WriteString("\n")

	if len(giftSummary) > 0 {
		sb.WriteString("PRESENTES ENVIADOS NA LIVE:\n")
		giftIdx := 0
		for uid, giftsMap := range giftSummary {
			if giftIdx >= maxGiftUsers {
				break
			}
			var parts []string
			for gname, count := range giftsMap {
				if len(parts) >= maxGiftTypesPerUser {
					break
				}
				parts = append(parts, fmt.Sprintf("%s x%d", gname, count))
			}
			sb.WriteString(fmt.Sprintf("- %s: %s\n", uid, strings.Join(parts, ", ")))
			giftIdx++
		}
	} else {
		sb.WriteString("NENHUM PRESENTE REGISTRADO NA LIVE.\n")
	}

	return sb.String()
}

// CorrelateGiftQuestion asks the LLM which recent question best matches a target gift.
func CorrelateGiftQuestion(ctx context.Context, aiManager *ai.Manager, gift monitor.GiftPayload, candidates []monitor.QuestionEntry) *monitor.QuestionEntry {
	if aiManager == nil || len(candidates) == 0 {
		return nil
	}
	if len(candidates) > 8 {
		candidates = candidates[len(candidates)-8:]
	}

	systemPrompt := strings.Join([]string{
		"Você correlaciona evento de presente no chat com uma pergunta recente.",
		"Escolha APENAS uma opção da lista de perguntas candidatas.",
		"Responda com UMA ÚNICA token:",
		"- NAO (sem correlação clara)",
		"- IDX_<N> (ex: IDX_2 para escolher a opção 2)",
		"Use IDX apenas quando a ligação for clara por autor, menção, contexto ou sequência temporal próxima.",
		"Sem explicações.",
	}, "\n")

	var sb strings.Builder
	fmt.Fprintf(&sb, "Presente: %q\n", gift.GiftName)
	fmt.Fprintf(&sb, "Quem enviou presente: %q\n", coalesceUser(gift.Nickname, gift.UniqueID))
	fmt.Fprintf(&sb, "UID de quem enviou presente: %q\n\nCandidatas:\n", gift.UniqueID)
	now := time.Now().UnixMilli()
	for i, c := range candidates {
		ageSec := int64(0)
		if c.Timestamp > 0 && now >= c.Timestamp {
			ageSec = (now - c.Timestamp) / 1000
		}
		fmt.Fprintf(&sb, "%d. user=%q uid=%q ageSec=%d text=%q\n",
			i+1, coalesceUser(c.Nickname, c.UniqueID), c.UniqueID, ageSec, c.Comment)
	}

	raw, err := aiManager.Complete(ctx, ai.CompletionRequest{
		SystemContent: systemPrompt,
		UserContent:   sb.String(),
		MaxTokens:     12,
	})
	if err != nil {
		log.Printf("[Service-AI] gift-question correlation: %v", err)
		return nil
	}
	normalized := strings.ToUpper(strings.TrimSpace(raw))
	if normalized == "" || strings.HasPrefix(normalized, "NAO") {
		return nil
	}
	idx := parseIdxToken(normalized)
	if idx < 1 || idx > len(candidates) {
		return nil
	}
	pick := candidates[idx-1]
	return &pick
}

func coalesceUser(nickname, uniqueID string) string {
	if nickname != "" {
		return nickname
	}
	if uniqueID != "" {
		return uniqueID
	}
	return "desconhecido"
}

func parseIdxToken(raw string) int {
	raw = strings.TrimSpace(raw)
	raw = strings.ReplaceAll(raw, "-", "_")
	raw = strings.ReplaceAll(raw, " ", "_")
	const prefix = "IDX_"
	i := strings.Index(raw, prefix)
	if i < 0 {
		return 0
	}
	n := 0
	for _, r := range raw[i+len(prefix):] {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}
