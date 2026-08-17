package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/tmc/langchaingo/chains"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/openai"
	"github.com/tmc/langchaingo/prompts"
)

// llamaServerDoer injects chat_template_kwargs into the request body,
// which the embedded OpenAI client does not support.
type llamaServerDoer struct {
	client *http.Client
}

func (d *llamaServerDoer) Do(req *http.Request) (*http.Response, error) {
	var payload map[string]any
	body, err := io.ReadAll(req.Body)
	req.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("read request body: %w", err)
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("unmarshal request body: %w", err)
	}
	payload["chat_template_kwargs"] = map[string]bool{"enable_thinking": false}

	newBody := bytes.NewBuffer(nil)
	if err := json.NewEncoder(newBody).Encode(payload); err != nil {
		return nil, fmt.Errorf("re-encode request body: %w", err)
	}

	req.Body = io.NopCloser(newBody)
	req.Header.Set("Content-Type", "application/json")
	return d.client.Do(req)
}

// llamaServerLLM wraps the langchaingo OpenAI chat client for
// llama-server (OpenAI-compatible endpoint), guaranteeing non-empty content.
type llamaServerLLM struct {
	client *openai.LLM
}

func (l *llamaServerLLM) GenerateContent(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
	resp, err := l.client.GenerateContent(ctx, messages, options...)
	if err != nil {
		return nil, err
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("empty response")
	}
	text := strings.TrimSpace(resp.Choices[0].Content)
	if text == "" {
		return nil, fmt.Errorf("empty content in response")
	}
	return &llms.ContentResponse{Choices: []*llms.ContentChoice{{Content: text}}}, nil
}

func (l *llamaServerLLM) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {
	resp, err := l.GenerateContent(ctx, []llms.MessageContent{{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: prompt}}}}, options...)
	if err != nil {
		return "", err
	}
	return resp.Choices[0].Content, nil
}

// NewLangChainClient creates a langchaingo chat model pointing at a
// local (or registered remote) llama-server OpenAI-compatible endpoint.
// The token is a placeholder: the local llama-server ignores auth, but
// the langchaingo OpenAI client requires a non-empty key.
func NewLangChainClient(host string, port int) (llms.Model, error) {
	baseURL := fmt.Sprintf("http://%s:%d/v1", host, port)
	doer := &llamaServerDoer{client: http.DefaultClient}

	ollm, err := openai.New(
		openai.WithToken("local"),
		openai.WithBaseURL(baseURL),
		openai.WithModel("local"),
		openai.WithHTTPClient(doer),
	)
	if err != nil {
		return nil, fmt.Errorf("new langchaingo client: %w", err)
	}
	return &llamaServerLLM{client: ollm}, nil
}

// ModerationChain is a langchaingo LLMChain for the moderation prompt:
// a system message with the moderation instructions and a user message
// with the comment context.
type ModerationChain struct {
	*chains.LLMChain
}

// Complete runs the moderation chain and returns the raw model output.
func (c *ModerationChain) Complete(ctx context.Context, system, message string, maxTokens int) (string, error) {
	out, err := chains.Call(ctx, c.LLMChain, map[string]any{
		"system":  system,
		"message": message,
	}, chains.WithMaxTokens(maxTokens))
	if err != nil {
		return "", err
	}
	text, _ := out[c.OutputKey].(string)
	return strings.TrimSpace(text), nil
}

// NewModerationChain creates the moderation chain with a chat prompt
// template: system instructions + user comment.
func NewModerationChain(model llms.Model) *ModerationChain {
	prompt := prompts.NewChatPromptTemplate([]prompts.MessageFormatter{
		prompts.SystemMessagePromptTemplate{Prompt: prompts.NewPromptTemplate("{{.system}}", []string{"system"})},
		prompts.HumanMessagePromptTemplate{Prompt: prompts.NewPromptTemplate("{{.message}}", []string{"message"})},
	})
	return &ModerationChain{LLMChain: chains.NewLLMChain(model, prompt)}
}