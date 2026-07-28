// Package monitor wraps the TikTok Live WebSocket connection and emits typed events.
package monitor

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/steampoweredtaco/gotiktoklive"
)

// maxRetries and baseBackoff for TikTok client creation (rate limit recovery).
const (
	maxRetries   = 5
	baseBackoff  = 2 * time.Second
)

// Event types emitted to handlers.
const (
	EventChatMessage          = "new-chat-message"
	EventGiftUser             = "new-gift-user"
	EventAnyGift              = "any-gift-received"
	EventPinnedComment        = "pinned-comment"
	EventFlaggedMessage       = "flagged-message"
	EventGiftQuestionCorr     = "gift-question-correlation"
	EventKeywordMention       = "keyword-mention"
	EventMarkUserRed          = "mark-user-red"
	EventConnectionStatus     = "connection-status"
	EventLiveUserConnected    = "live-user-connected"
	EventNewFollower          = "new-follower"
	EventNewSocialEvent       = "new-social-event"
)

// EventData is a generic event payload sent to handlers.
type EventData map[string]interface{}

// EventHandler receives typed events from the monitor.
type EventHandler func(eventType string, data EventData)

// UserInfo holds extracted user identification data.
type UserInfo struct {
	UniqueID   string
	Nickname   string
	IsFollower *bool
}

// ChatMessage represents a buffered chat message.
type ChatMessage struct {
	UniqueID   string
	Nickname   string
	Comment    string
	Timestamp  int64
	IsFollower *bool
}

// QuestionEntry is a question tracked for gift correlation.
type QuestionEntry struct {
	UniqueID   string
	Nickname   string
	Comment    string
	Timestamp  int64
	IsFollower *bool
}

// GiftPayload carries gift event data for correlation.
type GiftPayload struct {
	GiftName   string
	UniqueID   string
	Nickname   string
	RepeatCount int
	RepeatEnd  bool
	GiftType   int
	IsFollower *bool
}

// Settings for the monitor.
type Settings struct {
	ModerationEnabled   bool     `json:"moderationEnabled"`
	AIModerationEnabled bool     `json:"aiModerationEnabled"`
	LogLevel            string   `json:"logLevel"`
	TargetGifts         []string `json:"targetGifts"`
}

// State represents the monitor's current state.
type State struct {
	Connected bool     `json:"connected"`
	Username  string   `json:"username"`
	Settings  Settings `json:"settings"`
}

const (
	chatBufferMax              = 500
	questionBufferMax          = 300
	questionCorrelationWindow  = 3 * time.Minute
	correlationForwardCount    = 2
	correlationForwardDelay    = 4 * time.Second
	pinnedMessagesMax          = 200
	repeatWindowMs             = 60000
	repeatsRequired            = 3
)

// Monitor manages the TikTok Live connection and event dispatching.
type Monitor struct {
	mu               sync.Mutex
	tiktok           *gotiktoklive.TikTok
	live             *gotiktoklive.Live
	currentUsername  string
	chatBuffer       []ChatMessage
	questionBuffer   []QuestionEntry
	pinnedUsers      map[string]bool
	processedPins    map[string]bool
	repeatAlerted    map[string]bool
	handlers         []EventHandler
	settings         Settings

	// Correlation callbacks
	CorrelateGiftQuestion func(gift GiftPayload)
}

func newTikTokClientWithRetry() (*gotiktoklive.TikTok, error) {
	signerURL := os.Getenv("TIKTOK_SIGNER_URL")
	if signerURL == "" {
		return nil, fmt.Errorf("TIKTOK_SIGNER_URL não definida. É necessário um serviço de assinatura TikTok. Configure via: export TIKTOK_SIGNER_URL=https://seu-signer.com")
	}

	opts := []gotiktoklive.TikTokLiveOption{
		gotiktoklive.SigningUrl(signerURL),
		gotiktoklive.DisableSigningLimitsValidation,
	}
	if apiKey := os.Getenv("TIKTOK_API_KEY"); apiKey != "" {
		opts = append(opts, gotiktoklive.SigningApiKey(apiKey))
	}

	fmt.Printf("[Monitor] Using signer: %s\n", signerURL)

	var lastErr error
	for i := range maxRetries {
		if i > 0 {
			backoff := baseBackoff * (1 << i)
			fmt.Printf("[Monitor] Signer not ready, retrying in %v (attempt %d/%d)...\n", backoff, i+1, maxRetries)
			time.Sleep(backoff)
		}
		tk, err := gotiktoklive.NewTikTok(opts...)
		if err == nil {
			return tk, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("falha ao conectar ao signer %s: %w", signerURL, lastErr)
}

// New creates a new Monitor with default settings.
func New() (*Monitor, error) {
	tk, err := newTikTokClientWithRetry()
	if err != nil {
		return nil, fmt.Errorf("create tiktok client: %w", err)
	}

	return &Monitor{
		tiktok:        tk,
		handlers:      make([]EventHandler, 0),
		pinnedUsers:   make(map[string]bool),
		processedPins: make(map[string]bool),
		repeatAlerted: make(map[string]bool),
		settings: Settings{
			ModerationEnabled:   true,
			AIModerationEnabled: true,
			LogLevel:            "info",
			TargetGifts:         []string{"perfume", "coração", "dino"},
		},

	}, nil
}

// OnEvent registers an event handler.
func (m *Monitor) OnEvent(handler EventHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handlers = append(m.handlers, handler)
}

// GetState returns the current monitor state.
func (m *Monitor) GetState() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return State{
		Connected: m.live != nil,
		Username:  m.currentUsername,
		Settings:  m.settings,
	}
}

// GetSettings returns current settings.
func (m *Monitor) GetSettings() Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings
}

// SetSettings updates monitor settings.
func (m *Monitor) SetSettings(s Settings) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fmt.Printf("[Monitor] Updating settings: %+v\n", s)
	m.settings = s
}

// StartMonitoring connects to a TikTok user's livestream.
func (m *Monitor) StartMonitoring(ctx context.Context, username string) error {
	m.mu.Lock()
	if m.live != nil {
		m.live.Close()
		m.live = nil
	}
	m.currentUsername = username
	m.chatBuffer = nil
	m.questionBuffer = nil
	m.pinnedUsers = make(map[string]bool)
	m.processedPins = make(map[string]bool)
	m.repeatAlerted = make(map[string]bool)
	m.mu.Unlock()

	live, err := m.tiktok.TrackUser(username)
	if err != nil {
		m.emit(EventConnectionStatus, EventData{
			"success": false,
			"error":   fmt.Sprintf("Falha ao conectar: %v", err),
		})
		return fmt.Errorf("track user %s: %w", username, err)
	}

	m.mu.Lock()
	m.live = live
	m.mu.Unlock()

	go m.processEvents(ctx, live)

	m.emit(EventConnectionStatus, EventData{
		"success":  true,
		"username": username,
	})
	return nil
}

// StopMonitoring disconnects from the current livestream.
func (m *Monitor) StopMonitoring() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.live != nil {
		m.live.Close()
		m.live = nil
	}
	m.emitLocked(EventConnectionStatus, EventData{
		"success": false,
		"error":   "Desconectado pelo usuário",
	})
}

// GetChatBuffer returns a snapshot of recent chat messages.
func (m *Monitor) GetChatBuffer() []ChatMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ChatMessage, len(m.chatBuffer))
	copy(out, m.chatBuffer)
	return out
}

// GetQuestionBuffer returns a snapshot of recent questions.
func (m *Monitor) GetQuestionBuffer() []QuestionEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]QuestionEntry, len(m.questionBuffer))
	copy(out, m.questionBuffer)
	return out
}

func (m *Monitor) processEvents(ctx context.Context, live *gotiktoklive.Live) {
	defer func() {
		m.mu.Lock()
		if m.live == live {
			m.live = nil
		}
		m.mu.Unlock()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-live.Events:
			if !ok {
				return
			}
			m.handleEvent(event)
		}
	}
}

func (m *Monitor) handleEvent(event gotiktoklive.Event) {
	switch e := event.(type) {
	case gotiktoklive.ChatEvent:
		m.handleChat(e)
	case gotiktoklive.GiftEvent:
		m.handleGift(e)
	case gotiktoklive.UserEvent:
		m.handleUserEvent(e)
	case gotiktoklive.RoomEvent:
		m.handleRoomEvent(e)
	case gotiktoklive.DisconnectEvent:
		m.emit(EventConnectionStatus, EventData{
			"success": false,
			"error":   "Conexão encerrada pelo TikTok",
		})
	}
}

func (m *Monitor) handleChat(e gotiktoklive.ChatEvent) {
	comment := strings.TrimSpace(e.Comment)
	if comment == "" {
		return
	}
	// Skip history messages.
	if e.IsHistory() {
		return
	}

	// Check if this is a pinned message.
	if strings.HasPrefix(comment, "<pinned>: ") {
		comment = strings.TrimPrefix(comment, "<pinned>: ")
		m.handlePinnedChat(e, comment)
		return
	}

	user := extractUser(e.User, e.UserIdentity)
	now := time.Now().UnixMilli()

	m.emit(EventChatMessage, EventData{
		"uniqueId":   user.UniqueID,
		"nickname":   user.Nickname,
		"comment":    comment,
		"timestamp":  now,
		"isFollower": user.IsFollower,
	})

	// Detect repeated messages.
	commentLower := strings.ToLower(comment)
	senderKey := normalizeID(user.UniqueID)

	m.mu.Lock()
	// Check repeats.
	repeats := 0
	for _, msg := range m.chatBuffer {
		if normalizeID(msg.UniqueID) == senderKey &&
			strings.ToLower(strings.TrimSpace(msg.Comment)) == commentLower &&
			(now - msg.Timestamp) < repeatWindowMs {
			repeats++
		}
	}

	seqKey := fmt.Sprintf(`["%s","%s"]`, senderKey, commentLower)
	if repeats >= repeatsRequired-1 {
		if !m.repeatAlerted[seqKey] {
			m.repeatAlerted[seqKey] = true
			m.mu.Unlock()
			m.emit(EventFlaggedMessage, EventData{
				"uniqueId":   user.UniqueID,
				"nickname":   user.Nickname,
				"isFollower": user.IsFollower,
				"comment":    comment,
				"reason":     "Mensagem repetida",
				"category":   "REPETICAO",
			})
			m.mu.Lock()
		}
	} else {
		delete(m.repeatAlerted, seqKey)
	}

	// Store in chat buffer.
	msg := ChatMessage{
		UniqueID:   user.UniqueID,
		Nickname:   user.Nickname,
		Comment:    comment,
		Timestamp:  now,
		IsFollower: user.IsFollower,
	}
	m.chatBuffer = append(m.chatBuffer, msg)
	if len(m.chatBuffer) > chatBufferMax {
		m.chatBuffer = m.chatBuffer[1:]
	}

	// Track questions.
	if looksLikeQuestion(comment) {
		m.questionBuffer = append(m.questionBuffer, QuestionEntry{
			UniqueID:   user.UniqueID,
			Nickname:   user.Nickname,
			Comment:    comment,
			Timestamp:  now,
			IsFollower: user.IsFollower,
		})
	}
	m.pruneQuestions(now)
	m.mu.Unlock()

	// Detect keyword mentions.
	if keyword := m.detectKeyword(comment); keyword != "" {
		m.mu.Lock()
		m.pinnedUsers[senderKey] = true
		m.mu.Unlock()
		m.emit(EventKeywordMention, EventData{
			"uniqueId":   user.UniqueID,
			"nickname":   user.Nickname,
			"comment":    comment,
			"keyword":    keyword,
			"timestamp":  now,
			"isFollower": user.IsFollower,
		})
		m.emit(EventMarkUserRed, EventData{"uniqueId": senderKey})
	}
}

func (m *Monitor) handlePinnedChat(e gotiktoklive.ChatEvent, comment string) {
	msgKey := fmt.Sprintf("%d", e.MessageID)
	m.mu.Lock()
	if m.processedPins[msgKey] {
		m.mu.Unlock()
		return
	}
	m.processedPins[msgKey] = true
	if len(m.processedPins) > pinnedMessagesMax {
		// Trim old entries.
		newMap := make(map[string]bool)
		count := 0
		for k := range m.processedPins {
			if count >= 100 {
				break
			}
			newMap[k] = true
			count++
		}
		m.processedPins = newMap
	}
	m.mu.Unlock()

	user := extractUser(e.User, e.UserIdentity)
	now := time.Now().UnixMilli()

	m.emit(EventPinnedComment, EventData{
		"uniqueId":   user.UniqueID,
		"nickname":   coalesce(user.Nickname, user.UniqueID, "Nao identificado"),
		"comment":    coalesceStr(comment, "[sem texto identificado]"),
		"pinId":      fmt.Sprintf("%d", e.MessageID),
		"timestamp":  now,
		"isFollower": user.IsFollower,
	})

	if user.UniqueID != "" {
		key := normalizeID(user.UniqueID)
		m.mu.Lock()
		m.pinnedUsers[key] = true
		m.mu.Unlock()
		m.emit(EventMarkUserRed, EventData{"uniqueId": key})
	}
}

func (m *Monitor) handleRoomEvent(e gotiktoklive.RoomEvent) {
	// Room events can contain pinned messages too.
	if e.Type == "WebcastRoomPinMessage" || strings.Contains(e.Message, "<pinned>") {
		// Already handled via ChatEvent prefix.
	}
}

func (m *Monitor) handleGift(e gotiktoklive.GiftEvent) {
	if e.IsHistory() {
		return
	}

	user := extractUser(e.User, e.UserIdentity)
	uniqueID := normalizeID(user.UniqueID)

	m.mu.Lock()
	isPinned := m.pinnedUsers[uniqueID]
	m.mu.Unlock()

	isTarget := m.isTargetGift(e.Name)

	payload := EventData{
		"uniqueId":   user.UniqueID,
		"nickname":   user.Nickname,
		"giftName":   e.Name,
		"repeatCount": e.RepeatCount,
		"repeatEnd":  e.RepeatEnd,
		"giftType":   e.Type,
		"isRed":      isTarget && isPinned,
		"isFollower": user.IsFollower,
		"timestamp":  time.Now().UnixMilli(),
	}

	// Emit for all gifts.
	m.emit(EventAnyGift, payload)

	// Emit for target gifts only.
	if isTarget && isGiftCountingSettlement(e) {
		m.emit(EventGiftUser, payload)

		// Trigger correlation if callback is set.
		if m.CorrelateGiftQuestion != nil {
			gift := GiftPayload{
				GiftName:    e.Name,
				UniqueID:    user.UniqueID,
				Nickname:    user.Nickname,
				RepeatCount: e.RepeatCount,
				RepeatEnd:   e.RepeatEnd,
				GiftType:    e.Type,
				IsFollower:  user.IsFollower,
			}
			go m.CorrelateGiftQuestion(gift)
		}
	}
}

func (m *Monitor) handleUserEvent(e gotiktoklive.UserEvent) {
	if e.IsHistory() {
		return
	}
	user := extractUser(e.User, nil)

	switch e.Event {
	case gotiktoklive.USER_JOIN:
		m.emit(EventLiveUserConnected, EventData{
			"uniqueId":   user.UniqueID,
			"nickname":   user.Nickname,
			"isFollower": user.IsFollower,
		})
	case gotiktoklive.USER_FOLLOW:
		if user.IsFollower == nil {
			t := true
			user.IsFollower = &t
		}
		m.emit(EventNewFollower, EventData{
			"uniqueId":   user.UniqueID,
			"nickname":   user.Nickname,
			"isFollower": user.IsFollower,
		})
	case gotiktoklive.USER_SHARE:
		m.emit(EventNewSocialEvent, EventData{
			"uniqueId":   user.UniqueID,
			"nickname":   user.Nickname,
			"isFollower": user.IsFollower,
		})
	}
}

// Helpers

func (m *Monitor) emit(eventType string, data EventData) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.emitLocked(eventType, data)
}

func (m *Monitor) emitLocked(eventType string, data EventData) {
	for _, h := range m.handlers {
		h(eventType, data)
	}
}

func (m *Monitor) pruneQuestions(now int64) {
	cutoff := now - questionCorrelationWindow.Milliseconds()
	filtered := m.questionBuffer[:0]
	for _, q := range m.questionBuffer {
		if q.Timestamp >= cutoff {
			filtered = append(filtered, q)
		}
	}
	m.questionBuffer = filtered
	if len(m.questionBuffer) > questionBufferMax {
		m.questionBuffer = m.questionBuffer[len(m.questionBuffer)-questionBufferMax:]
	}
}

// PruneQuestions is exported for external use (correlation).
func (m *Monitor) PruneQuestions(now int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneQuestions(now)
}

// IsPinnedUser checks if a user is in the pinned set.
func (m *Monitor) IsPinnedUser(uid string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pinnedUsers[normalizeID(uid)]
}

// --- Pure functions ---

func extractUser(u *gotiktoklive.User, identity *gotiktoklive.UserIdentity) UserInfo {
	info := UserInfo{}
	if u != nil {
		info.UniqueID = u.Username
		info.Nickname = u.Nickname
	}
	if identity != nil {
		isFollower := identity.IsFollower
		info.IsFollower = &isFollower
	}
	return info
}

func normalizeID(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func foldText(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.Map(func(r rune) rune {
		if r == 'ç' {
			return 'c'
		}
		// Remove combining diacritical marks.
		if r >= 0x0300 && r <= 0x036F {
			return -1
		}
		return r
	}, s)
	return s
}

func looksLikeQuestion(comment string) bool {
	raw := strings.TrimSpace(comment)
	if raw == "" {
		return false
	}
	if strings.ContainsAny(raw, "?¿") {
		return true
	}
	t := foldText(raw)
	questionStarts := regexp.MustCompile(
		`^(pq|pk|por\s+que|porque|como|quando|onde|aonde|quem|qual|quais|sera\s+que|duvida\b|duvida[:\\-])`,
	)
	if questionStarts.MatchString(t) {
		return true
	}
	questionCues := regexp.MustCompile(
		`\b(tem\s+como|da\s+pra|d[aá]\s+pra|alguem\s+sabe|algm\s+sabe|me\s+tira\s+uma\s+duvida|qual\s+o|qual\s+a)\b`,
	)
	return questionCues.MatchString(t)
}

func (m *Monitor) detectKeyword(comment string) string {
	lower := strings.ToLower(comment)
	for _, target := range m.settings.TargetGifts {
		tLower := strings.ToLower(target)
		if strings.Contains(lower, tLower) {
			return tLower
		}
	}
	return ""
}

func (m *Monitor) isTargetGift(name string) bool {
	lower := strings.ToLower(name)
	compact := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return -1
	}, lower)

	for _, target := range m.settings.TargetGifts {
		tLower := strings.ToLower(target)
		tCompact := strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				return r
			}
			return -1
		}, tLower)

		if strings.Contains(lower, tLower) || strings.Contains(compact, tCompact) {
			return true
		}
	}
	return false
}

func isGiftCountingSettlement(e gotiktoklive.GiftEvent) bool {
	// GiftType 1 with repeatEnd=false is an intermediate streak — skip.
	if e.Type == 1 && !e.RepeatEnd {
		return false
	}
	return true
}

func coalesce(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func coalesceStr(val, fallback string) string {
	if val == "" {
		return fallback
	}
	return val
}


