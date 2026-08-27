package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/monitor"
)

type correlateRequest struct {
	Gift       monitor.GiftPayload     `json:"gift"`
	Candidates []monitor.QuestionEntry `json:"candidates"`
	IsTarget   bool                    `json:"isTarget"`
}

type correlateResponse struct {
	Match      *monitor.QuestionEntry `json:"match"`
	Method     string                `json:"method"`
	Confidence string                `json:"confidence"`
}

// NewCorrelateFunc returns the monitor.LLMCorrelate callback backed by the
// Python agent's POST /correlate-gift endpoint. The agent (which consumes the
// same live event stream) checks the gift sender's recent messages and picks
// the question that best fits the gift sent. It returns (nil, "", "") when the
// agent is unavailable or finds no match, so the monitor falls back to its
// deterministic heuristic.
func NewCorrelateFunc(baseURL string) func(ctx context.Context, gift monitor.GiftPayload, candidates []monitor.QuestionEntry) (*monitor.QuestionEntry, string, string) {
	client := &http.Client{Timeout: 9 * time.Second}
	return func(ctx context.Context, gift monitor.GiftPayload, candidates []monitor.QuestionEntry) (*monitor.QuestionEntry, string, string) {
		payload, err := json.Marshal(correlateRequest{Gift: gift, Candidates: candidates, IsTarget: true})
		if err != nil {
			return nil, "", ""
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/correlate-gift", bytes.NewReader(payload))
		if err != nil {
			return nil, "", ""
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("[Agent] correlate-gift indisponível: %v", err)
			return nil, "", ""
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			log.Printf("[Agent] correlate-gift respondeu status %d", resp.StatusCode)
			return nil, "", ""
		}
		var out correlateResponse
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			log.Printf("[Agent] correlate-gift resposta inválida: %v", err)
			return nil, "", ""
		}
		if out.Match == nil {
			return nil, out.Method, out.Confidence
		}
		method := out.Method
		if method == "" {
			method = "llm"
		}
		confidence := out.Confidence
		if confidence == "" {
			confidence = "medium"
		}
		return out.Match, method, confidence
	}
}
