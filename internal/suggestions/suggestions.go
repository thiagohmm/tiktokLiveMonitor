// Package suggestions inspects incoming live messages/questions and identifies
// questions worth answering. Reply generation moved to the Python agent
// (docs/plano-unificacao-ia.md, fase 2).
package suggestions

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

// EventSuggested is the monitor event name used to surface a prepared reply.
const EventSuggested = "suggested-response"

// Candidate identifies a message worth answering.
type Candidate struct {
	UniqueID  string
	Nickname  string
	Message   string
	Timestamp string
	LiveName  string
	Suggested string
	Reason    string
	Answered  bool
}

// Engine inspects messages and identifies questions worth answering.
type Engine struct {
	mu   sync.Mutex
	repo model.Repository
	// QuestionLengthMin/Max filter out too-short or too-long questions.
	QuestionLengthMin int
	// QuestionLengthMax int
	// HistoryWindow is how many recent messages are considered context.
	HistoryWindow int
}

// New creates a suggestions Engine.
func New(repo model.Repository) *Engine {
	return &Engine{
		repo:              repo,
		QuestionLengthMin: 8,
		HistoryWindow:     40,
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

	// Reply generation moved to the Python agent; only report the candidate.
	return candidate, false
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
