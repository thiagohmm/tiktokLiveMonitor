package monitor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

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

type EventData map[string]interface{}

type EventHandler func(eventType string, data EventData)

type UserInfo struct {
	UniqueID   string
	Nickname   string
	IsFollower *bool
}

type ChatMessage struct {
	UniqueID   string
	Nickname   string
	Comment    string
	Timestamp  int64
	IsFollower *bool
}

type QuestionEntry struct {
	UniqueID   string
	Nickname   string
	Comment    string
	Timestamp  int64
	IsFollower *bool
}

type GiftPayload struct {
	GiftName    string
	UniqueID    string
	Nickname    string
	RepeatCount int
	RepeatEnd   bool
	GiftType    int
	IsFollower  *bool
}

type Settings struct {
	ModerationEnabled   bool     `json:"moderationEnabled"`
	AIModerationEnabled bool     `json:"aiModerationEnabled"`
	LogLevel            string   `json:"logLevel"`
	TargetGifts         []string `json:"targetGifts"`
}

type State struct {
	Connected bool     `json:"connected"`
	Username  string   `json:"username"`
	Settings  Settings `json:"settings"`
}

const (
	chatBufferMax             = 500
	questionBufferMax         = 300
	questionCorrelationWindow = 3 * time.Minute
	correlationForwardCount   = 2
	correlationForwardDelay   = 4 * time.Second
	pinnedMessagesMax         = 200
	repeatWindowMs            = 60000
	repeatsRequired           = 3
)

type Monitor struct {
	mu               sync.Mutex
	cmd              *exec.Cmd
	stdin            io.WriteCloser
	stdout           io.ReadCloser
	currentUsername  string
	chatBuffer       []ChatMessage
	questionBuffer   []QuestionEntry
	pinnedUsers      map[string]bool
	processedPins    map[string]bool
	repeatAlerted    map[string]bool
	handlers         []EventHandler
	settings         Settings
	connected        bool

	CorrelateGiftQuestion func(gift GiftPayload)
}

func New() (*Monitor, error) {
	return &Monitor{
		handlers:      make([]EventHandler, 0),
		pinnedUsers:   make(map[string]bool),
		processedPins: make(map[string]bool),
		repeatAlerted: make(map[string]bool),
		settings: Settings{
			ModerationEnabled:   true,
			AIModerationEnabled: true,
			LogLevel:            "info",
			TargetGifts:         []string{"perfume", "coracao", "dino"},
		},
	}, nil
}

func (m *Monitor) OnEvent(handler EventHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handlers = append(m.handlers, handler)
}

func (m *Monitor) GetState() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return State{
		Connected: m.connected,
		Username:  m.currentUsername,
		Settings:  m.settings,
	}
}

func (m *Monitor) GetSettings() Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings
}

func (m *Monitor) SetSettings(s Settings) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fmt.Printf("[Monitor] Updating settings: %+v\n", s)
	m.settings = s
}

func (m *Monitor) startBridge() error {
	bridgePath := filepath.Join("internal", "monitor", "bridge.js")
	cmd := exec.Command("node", bridgePath)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start bridge: %w", err)
	}

	m.cmd = cmd
	m.stdin = stdin
	m.stdout = stdout

	go m.readBridge()

	return nil
}

func (m *Monitor) stopBridge() {
	if m.cmd != nil {
		m.cmd.Process.Kill()
		m.cmd.Wait()
		m.cmd = nil
	}
}

func (m *Monitor) sendBridge(cmd map[string]interface{}) error {
	data, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(m.stdin, "%s\n", data)
	return err
}

type bridgeMsg struct {
	Type string                 `json:"type"`
	Data map[string]interface{} `json:"data"`
}

func (m *Monitor) readBridge() {
	scanner := bufio.NewScanner(m.stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		var msg bridgeMsg
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		m.handleBridgeEvent(msg.Type, msg.Data)
	}
	log.Println("[Monitor] Bridge process ended")
}

func (m *Monitor) handleBridgeEvent(eventType string, data EventData) {
	switch eventType {
	case "connection-status":
		success, _ := data["success"].(bool)
		m.mu.Lock()
		m.connected = success
		if username, ok := data["username"].(string); ok {
			m.currentUsername = username
		}
		m.mu.Unlock()
		m.emit(eventType, data)

	case "new-chat-message":
		m.mu.Lock()
		m.handleChatMessage(data)
		m.mu.Unlock()
		m.emit(eventType, data)

	case "any-gift-received":
		m.emit(eventType, data)

	case "new-gift-user":
		m.handleTargetGift(data)

	case "pinned-comment":
		m.mu.Lock()
		user := extractFromData(data)
		key := normalizeID(user.UniqueID)
		m.pinnedUsers[key] = true
		m.mu.Unlock()
		m.emit(eventType, data)
		if user.UniqueID != "" {
			m.emit(EventMarkUserRed, EventData{"uniqueId": key})
		}

	case "live-user-connected", "new-follower", "new-social-event":
		m.emit(eventType, data)

	case "error":
		log.Printf("[Bridge] Error: %v", data["message"])
	}
}

func (m *Monitor) handleChatMessage(data EventData) {
	comment, _ := data["comment"].(string)
	comment = strings.TrimSpace(comment)
	if comment == "" {
		return
	}

	user := extractFromData(data)
	now := time.Now().UnixMilli()

	commentLower := strings.ToLower(comment)
	senderKey := normalizeID(user.UniqueID)

	repeats := 0
	for _, msg := range m.chatBuffer {
		if normalizeID(msg.UniqueID) == senderKey &&
			strings.ToLower(strings.TrimSpace(msg.Comment)) == commentLower &&
			(now-msg.Timestamp) < repeatWindowMs {
			repeats++
		}
	}

	seqKey := fmt.Sprintf(`["%s","%s"]`, senderKey, commentLower)
	if repeats >= repeatsRequired-1 {
		if !m.repeatAlerted[seqKey] {
			m.repeatAlerted[seqKey] = true
			m.emit(EventFlaggedMessage, EventData{
				"uniqueId":   user.UniqueID,
				"nickname":   user.Nickname,
				"isFollower": user.IsFollower,
				"comment":    comment,
				"reason":     "Mensagem repetida",
				"category":   "REPETICAO",
			})
		}
	} else {
		delete(m.repeatAlerted, seqKey)
	}

	m.chatBuffer = append(m.chatBuffer, ChatMessage{
		UniqueID:   user.UniqueID,
		Nickname:   user.Nickname,
		Comment:    comment,
		Timestamp:  now,
		IsFollower: user.IsFollower,
	})
	if len(m.chatBuffer) > chatBufferMax {
		m.chatBuffer = m.chatBuffer[1:]
	}

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

	if keyword := m.detectKeyword(comment); keyword != "" {
		m.pinnedUsers[senderKey] = true
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

func (m *Monitor) handleTargetGift(data EventData) {
	user := extractFromData(data)
	uniqueID := normalizeID(user.UniqueID)

	m.mu.Lock()
	isPinned := m.pinnedUsers[uniqueID]
	m.mu.Unlock()

	giftName, _ := data["giftName"].(string)
	isTarget := m.isTargetGift(giftName)

	data["isRed"] = isTarget && isPinned

	m.emit(EventGiftUser, data)

	if isTarget && m.CorrelateGiftQuestion != nil {
		repeatCount, _ := toInt(data["repeatCount"])
		giftType, _ := toInt(data["giftType"])
		repeatEnd, _ := data["repeatEnd"].(bool)

		gift := GiftPayload{
			GiftName:    giftName,
			UniqueID:    user.UniqueID,
			Nickname:    user.Nickname,
			RepeatCount: repeatCount,
			RepeatEnd:   repeatEnd,
			GiftType:    giftType,
			IsFollower:  user.IsFollower,
		}
		go m.CorrelateGiftQuestion(gift)
	}
}

func toInt(v interface{}) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	}
	return 0, false
}

func (m *Monitor) StartMonitoring(ctx context.Context, username string) error {
	if m.cmd == nil {
		if err := m.startBridge(); err != nil {
			return fmt.Errorf("start bridge: %w", err)
		}
	}

	m.mu.Lock()
	m.currentUsername = username
	m.chatBuffer = nil
	m.questionBuffer = nil
	m.pinnedUsers = make(map[string]bool)
	m.processedPins = make(map[string]bool)
	m.repeatAlerted = make(map[string]bool)
	m.mu.Unlock()

	return m.sendBridge(map[string]interface{}{
		"action":   "connect",
		"username": username,
	})
}

func (m *Monitor) StopMonitoring() {
	if m.stdin != nil {
		m.sendBridge(map[string]interface{}{
			"action": "disconnect",
		})
	}
	m.mu.Lock()
	m.connected = false
	m.mu.Unlock()
	m.emit(EventConnectionStatus, EventData{
		"success": false,
		"error":   "Desconectado pelo usuário",
	})
}

func (m *Monitor) emit(eventType string, data EventData) {
	m.mu.Lock()
	defer m.mu.Unlock()
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

func (m *Monitor) PruneQuestions(now int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneQuestions(now)
}

func (m *Monitor) IsPinnedUser(uid string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pinnedUsers[normalizeID(uid)]
}

func (m *Monitor) GetChatBuffer() []ChatMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ChatMessage, len(m.chatBuffer))
	copy(out, m.chatBuffer)
	return out
}

func (m *Monitor) GetQuestionBuffer() []QuestionEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]QuestionEntry, len(m.questionBuffer))
	copy(out, m.questionBuffer)
	return out
}

func extractFromData(data EventData) UserInfo {
	info := UserInfo{}
	if uid, ok := data["uniqueId"].(string); ok {
		info.UniqueID = uid
	}
	if nick, ok := data["nickname"].(string); ok {
		info.Nickname = nick
	}
	if f, ok := data["isFollower"].(bool); ok {
		info.IsFollower = &f
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
