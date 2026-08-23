// Package suggestions inspects incoming live messages/questions and prepares
// short, non-auto-published reply suggestions via the local LLM.
package suggestions

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/ai"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

// EventSuggested is the monitor event name used to surface a prepared reply.
const EventSuggested = "suggested-response"

// Candidate identifies a message worth answering.
type Candidate struct {
	UniqueID   string
	Nickname   string
	Message    string
	Timestamp  string
	LiveName   string
	Suggested  string
	Reason     string
	Answered   bool
}

// Engine inspects messages and produces reply suggestions.
type Engine struct {
	mu   sync.Mutex
	ai   *ai.Manager
	repo model.Repository
	// QuestionLengthMin/Max filter out too-short or too-long questions.
	QuestionLengthMin int
	// QuestionLengthMax int
	// HistoryWindow is how many recent messages are considered context.
	HistoryWindow int
}

// New creates a suggestions Engine.
func New(aiManager *ai.Manager, repo model.Repository) *Engine {
	return &Engine{
		ai:              aiManager,
		repo:            repo,
		QuestionLengthMin: 8,
		HistoryWindow:   40,
	}
}

// Suggest inspects a single incoming message and returns a suggestion if the
// message looks like a genuine question worth answering.
func (e *Engine) Suggest(ctx context.Context, liveName, uniqueID, nickname, message string) (Candidate, bool) {
	candidate := Candidate{
		UniqueID:  uniqueID,
		Nickname:  nickname,
		Message:   strings.TrimSpace(message),
		Timestamp: time.Now().Format(time.RFC3339),
		LiveName:  liveName,
	}
	if candidate.Message == "" || e.QuestionLengthMin > 0 && len(candidate.Message) < e.QuestionLengthMin {
		return candidate, false
	}
	if !looksLikeQuestion(candidate.Message) {
		return candidate, false
	}

	if e.ai == nil {
		return candidate, false
	}

	prompt := buildPrompt(candidate.Message)
	resp, err := e.ai.Complete(ctx, ai.CompletionRequest{
		SystemContent: "Você é um moderador de transmissões ao vivo (TikTok Live). Responda de forma curta, cordial e útil as perguntas do público.",
		UserContent:   prompt,
		MaxTokens:     120,
	})
	if err != nil {
		log.Printf("[Suggestions] AI suggestion error: %v", err)
		return candidate, false
	}
	suggested := strings.TrimSpace(resp)
	if suggested == "" || strings.EqualFold(suggested, "NAO") {
		return candidate, false
	}
	candidate.Suggested = suggested
	candidate.Reason = "pergunta identificada como relevante"
	return candidate, true
}

// looksLikeQuestion reports whether a message is plausibly a question.
func looksLikeQuestion(msg string) bool {
	trimmed := strings.TrimSpace(msg)
	if strings.Contains(trimmed, "?") {
		return true
	}
	lower := strings.ToLower(trimmed)
	for _, w := range []string{"como", "por que", "porque", "quando", "onde", "quem", "qual", "quanto", "vale", "custa", "pode", "tem", "gosta", "recomenda"} {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}

// buildPrompt assembles the LLM prompt for a question.
func buildPrompt(question string) string {
	return fmt.Sprintf("Pergunta recebida ao vivo: %q\n\nDê uma resposta curta (até 2 frases), cordial e direta, em português (br).", question)
}