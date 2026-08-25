// Package suggestions inspects incoming live messages/questions and prepares
// short, non-auto-published reply suggestions via the Python AI agent.
package suggestions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
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

// Engine inspects messages and produces reply suggestions via the Python agent.
type Engine struct {
	mu           sync.Mutex
	repo         model.Repository
	agentBaseURL string
	client       *http.Client
	// QuestionLengthMin filters out too-short questions.
	QuestionLengthMin int
	// HistoryWindow is how many recent messages are considered context.
	HistoryWindow int
}

// New creates a suggestions Engine. Call SetAgentBaseURL before Suggest can
// generate replies.
func New(repo model.Repository) *Engine {
	return &Engine{
		repo:              repo,
		QuestionLengthMin: 8,
		HistoryWindow:     40,
		client:            &http.Client{Timeout: 45 * time.Second},
	}
}

// SetAgentBaseURL configures the Python agent HTTP base URL (e.g. http://127.0.0.1:9001).
func (e *Engine) SetAgentBaseURL(baseURL string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.agentBaseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
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

	e.mu.Lock()
	baseURL := e.agentBaseURL
	e.mu.Unlock()
	if baseURL == "" {
		return candidate, false
	}

	suggested, reason, err := e.callAgent(ctx, baseURL, candidate)
	if err != nil {
		log.Printf("[Suggestions] agent error: %v", err)
		return candidate, false
	}
	if suggested == "" {
		return candidate, false
	}
	candidate.Suggested = suggested
	if reason == "" {
		reason = "pergunta identificada como relevante"
	}
	candidate.Reason = reason
	return candidate, true
}

type suggestRequest struct {
	Question string `json:"question"`
	Nickname string `json:"nickname,omitempty"`
	UniqueID string `json:"uniqueId,omitempty"`
}

type suggestResponse struct {
	Suggested string `json:"suggested"`
	Reason    string `json:"reason"`
	Error     string `json:"error"`
}

func (e *Engine) callAgent(ctx context.Context, baseURL string, cand Candidate) (string, string, error) {
	payload, err := json.Marshal(suggestRequest{
		Question: cand.Message,
		Nickname: cand.Nickname,
		UniqueID: cand.UniqueID,
	})
	if err != nil {
		return "", "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/suggest", bytes.NewReader(payload))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")

	client := e.client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", "", err
	}
	if resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("agent /suggest status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out suggestResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return "", "", err
	}
	if out.Error != "" {
		return "", "", fmt.Errorf("agent /suggest: %s", out.Error)
	}
	return strings.TrimSpace(out.Suggested), strings.TrimSpace(out.Reason), nil
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
