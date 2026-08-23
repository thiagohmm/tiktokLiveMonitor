// Package service contains business logic that orchestrates repositories and domain services.
package service

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/ai"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"github.com/thiagohmm/tiktok-live-monitor/internal/monitor"
)

// AskAI builds context from repository data and asks the AI a question.
func AskAI(ctx context.Context, question string, aiManager *ai.Manager, repo model.Repository, cachedMessages []model.UserMessage) (string, error) {
	giftSummary, err := repo.GetGiftSummary()
	if err != nil {
		log.Printf("[Service-AI] Error fetching gift summary: %v", err)
	}

	allMessages, err := repo.GetAllUserMessages()
	if err != nil {
		log.Printf("[Service-AI] Error fetching user messages: %v", err)
	}

	if len(cachedMessages) > 0 {
		allMessages = mergeMessages(allMessages, cachedMessages)
	}

	mentionedUsers := findMentionedUsers(question, allMessages)
	contextText := buildAIContext(allMessages, giftSummary, mentionedUsers)

	systemPrompt := fmt.Sprintf(`Você é o assistente de uma live do TikTok. Responda a pergunta EXCLUSIVAMENTE com base nos dados abaixo.

REGRAS:
1. Vá direto ao ponto. Nunca cumprimente e nunca diga que é um assistente.
2. Se a pergunta menciona um usuário, relate TUDO dele da seção de perfil completo: presença, mensagens e presentes.
3. Se os dados não contêm a informação pedida, responda apenas: "Não encontrei dados sobre isso na live."

DADOS DA LIVE:
%s

Responda em português do Brasil, de forma direta e concisa.`, contextText)

	req := ai.CompletionRequest{
		SystemContent: systemPrompt,
		UserContent:   question,
		MaxTokens:     512,
	}

	return aiManager.Complete(ctx, req)
}

func buildAIContext(allMessages map[string][]model.UserMessage, giftSummary map[string]map[string]int, mentionedUsers []string) string {
	var sb strings.Builder

	const maxMessages = 50
	const maxGiftUsers = 10
	const maxGiftTypesPerUser = 5
	const maxMessageLen = 200
	const maxUserMessages = 100

	if len(mentionedUsers) > 0 {
		sb.WriteString("PERFIL COMPLETO DO USUÁRIO MENCIONADO NA PERGUNTA:\n")
		for _, uid := range mentionedUsers {
			msgs := allMessages[uid]
			nickname := ""
			if len(msgs) > 0 {
				nickname = msgs[0].Username
			}
			if nickname == "" {
				nickname = uid
			}
			// msgs comes ordered by timestamp DESC: first = most recent, last = oldest.
			firstSeen, lastSeen := "", ""
			if len(msgs) > 0 {
				lastSeen = msgs[0].Timestamp
				firstSeen = msgs[len(msgs)-1].Timestamp
			}
			sb.WriteString(fmt.Sprintf("\nUSUÁRIO: %s (uid: %s)\n", nickname, uid))
			sb.WriteString(fmt.Sprintf("  Presença na live: primeira atividade %s | última atividade %s | %d mensagens\n",
				orDash(firstSeen), orDash(lastSeen), len(msgs)))

			sb.WriteString("  Mensagens (ordem cronológica, da mais antiga para a mais recente):\n")
			for i := len(msgs) - 1; i >= 0; i-- {
				if len(msgs)-1-i >= maxUserMessages {
					sb.WriteString(fmt.Sprintf("    ... (e mais %d mensagens anteriores)\n", len(msgs)-maxUserMessages))
					break
				}
				text := msgs[i].Message
				if len(text) > maxMessageLen {
					text = text[:maxMessageLen]
				}
				sb.WriteString(fmt.Sprintf("    - [%s] %s\n", orDash(msgs[i].Timestamp), text))
			}

			if gifts, ok := giftSummary[uid]; ok && len(gifts) > 0 {
				parts := make([]string, 0, len(gifts))
				for gname, count := range gifts {
					parts = append(parts, fmt.Sprintf("%s x%d", gname, count))
				}
				sb.WriteString(fmt.Sprintf("  Presentes enviados: %s\n", strings.Join(parts, ", ")))
			} else {
				sb.WriteString("  Presentes enviados: nenhum\n")
			}
		}
		sb.WriteString("\n")
	}

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

// mergeMessages merges buffered (write-behind cache) messages into the per-user
// message map coming from the database, deduplicating by (user, message).
func mergeMessages(base map[string][]model.UserMessage, extra []model.UserMessage) map[string][]model.UserMessage {
	byUser := make(map[string][]model.UserMessage, len(base)+len(extra))
	for uid, msgs := range base {
		key := strings.ToLower(uid)
		for _, m := range msgs {
			if m.UniqueID != "" && strings.ToLower(m.UniqueID) != key {
				key = strings.ToLower(m.UniqueID)
				break
			}
		}
		byUser[key] = msgs
	}
	for _, m := range extra {
		key := strings.ToLower(m.UniqueID)
		dup := false
		for _, existing := range byUser[key] {
			if strings.ToLower(existing.Message) == strings.ToLower(m.Message) {
				dup = true
				break
			}
		}
		if !dup {
			byUser[key] = append(byUser[key], m)
		}
	}
	return byUser
}

// findMentionedUsers returns the uniqueIDs of registered users mentioned by name in the question.
func findMentionedUsers(question string, allMessages map[string][]model.UserMessage) []string {
	q := strings.ToLower(strings.TrimSpace(question))
	if q == "" {
		return nil
	}
	var out []string
	for uid, msgs := range allMessages {
		names := map[string]struct{}{}
		if uid != "" {
			names[strings.ToLower(uid)] = struct{}{}
		}
		for _, m := range msgs {
			if m.Username != "" {
				names[strings.ToLower(m.Username)] = struct{}{}
			}
		}
		for name := range names {
			if len(name) >= 2 && strings.Contains(q, name) {
				out = append(out, uid)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
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
