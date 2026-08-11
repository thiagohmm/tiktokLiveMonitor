// Package service contains business logic that orchestrates repositories and domain services.
package service

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/thiagohmm/tiktok-live-monitor/internal/ai"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
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
