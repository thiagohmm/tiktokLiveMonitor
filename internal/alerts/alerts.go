// Package alerts sends external webhook notifications (Discord, Telegram, WhatsApp)
// for notable live events.
package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

// Provider identifies a webhook destination.
type Provider string

const (
	ProviderDiscord  Provider = "discord"
	ProviderTelegram Provider = "telegram"
	ProviderWhatsApp Provider = "whatsapp"
)

// Config holds webhook configuration for each provider.
type Config struct {
	DiscordWebhook  string
	TelegramChatID  string
	TelegramToken   string
	WhatsAppURL     string
	HTTPTimeout     time.Duration
	NotifyOnLowGift bool
}

// Notifier dispatches alert events to configured webhook providers.
type Notifier struct {
	mu     sync.Mutex
	cfg    Config
	client *http.Client
}

// New creates a Notifier from the given config.
func New(cfg Config) *Notifier {
	if cfg.HTTPTimeout <= 0 {
		cfg.HTTPTimeout = 10 * time.Second
	}
	if cfg.NotifyOnLowGift == false {
		cfg.NotifyOnLowGift = true
	}
	return &Notifier{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.HTTPTimeout},
	}
}

// FromEnvironment builds a Config from the process environment.
func FromEnvironment() Config {
	return Config{
		DiscordWebhook:  os.Getenv("ALERT_DISCORD_WEBHOOK"),
		TelegramChatID:  os.Getenv("ALERT_TELEGRAM_CHAT_ID"),
		TelegramToken:   os.Getenv("ALERT_TELEGRAM_TOKEN"),
		WhatsAppURL:     os.Getenv("ALERT_WHATSAPP_URL"),
		HTTPTimeout:     time.Second * 10,
		NotifyOnLowGift: true,
	}
}

// Enabled reports whether at least one provider is configured.
func (n *Notifier) Enabled() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.cfg.DiscordWebhook != "" ||
		(n.cfg.TelegramChatID != "" && n.cfg.TelegramToken != "") ||
		n.cfg.WhatsAppURL != ""
}

// SetConfig replaces the active configuration.
func (n *Notifier) SetConfig(cfg Config) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.cfg = cfg
}

// GetConfig returns a copy of the active configuration (secrets redacted).
func (n *Notifier) GetConfig() Config {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := n.cfg
	if out.TelegramToken != "" {
		out.TelegramToken = "***"
	}
	return out
}

// Send delivers an alert event to all configured providers.
func (n *Notifier) Send(ctx context.Context, event model.AlertEvent) {
	if !n.Enabled() {
		return
	}
	n.mu.Lock()
	cfg := n.cfg
	n.mu.Unlock()

	payload := buildPayload(event)

	if cfg.DiscordWebhook != "" {
		n.sendDiscord(ctx, cfg.DiscordWebhook, payload)
	}
	if cfg.TelegramChatID != "" && cfg.TelegramToken != "" {
		url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", cfg.TelegramToken)
		n.sendTelegram(ctx, url, payload)
	}
	if cfg.WhatsAppURL != "" {
		n.sendWhatsApp(ctx, cfg.WhatsAppURL, event)
	}
}

// buildPayload assembles a unified payload for text-based providers.
func buildPayload(event model.AlertEvent) map[string]any {
	emoji := severityEmoji(string(event.Severity))
	return map[string]any{
		"alert":    event.Type,
		"title":    event.Title,
		"message":  event.Message,
		"severity": string(event.Severity),
		"user":     coalesce(event.Nickname, event.UniqueID, "desconhecido"),
		"live":     event.LiveName,
		"time":     event.Timestamp,
		"emoji":    emoji,
	}
}

func (n *Notifier) sendDiscord(ctx context.Context, webhook string, payload map[string]any) {
	embed := map[string]any{
		"title":       fmt.Sprintf("%s %s", payload["emoji"], payload["title"]),
		"description": payload["message"],
		"color":       severityColor(payload["severity"].(string)),
		"footer":      map[string]string{"text": fmt.Sprintf("Live: %s", payload["live"])},
		"timestamp":   payload["time"],
	}
	body, _ := json.Marshal(map[string]any{
		"content": fmt.Sprintf("%s **%s**", payload["emoji"], payload["title"]),
		"embeds":  []map[string]any{embed},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

func (n *Notifier) sendTelegram(ctx context.Context, url string, payload map[string]any) {
	text := fmt.Sprintf("%s *%s*\n%s\n_Live: %s_",
		payload["emoji"], payload["title"], payload["message"], payload["live"])
	body, _ := json.Marshal(map[string]any{
		"chat_id":    payload["live"],
		"text":       text,
		"parse_mode": "Markdown",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

func (n *Notifier) sendWhatsApp(ctx context.Context, url string, event model.AlertEvent) {
	to := coalesce(event.UniqueID, event.Nickname, "")
	body, _ := json.Marshal(map[string]any{
		"messages": []map[string]any{{
			"to":   to,
			"type": "text",
			"text": map[string]any{"body": fmt.Sprintf("%s %s: %s", severityEmoji(string(event.Severity)), event.Title, event.Message)},
		}},
		"type": "personal",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

func severityEmoji(severity string) string {
	switch strings.ToLower(severity) {
	case string(model.AlertSeverityCritical):
		return "🔴"
	case string(model.AlertSeverityError):
		return "🟠"
	case string(model.AlertSeverityWarning):
		return "🟡"
	default:
		return "ℹ️"
	}
}

func severityColor(severity string) int {
	switch strings.ToLower(severity) {
	case string(model.AlertSeverityCritical):
		return 0xdc2626
	case string(model.AlertSeverityError):
		return 0xea580c
	case string(model.AlertSeverityWarning):
		return 0xca8a04
	default:
		return 0x22c55e
	}
}

func coalesce(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}