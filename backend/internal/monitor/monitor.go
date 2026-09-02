package monitor

import (
	"context"
	"fmt"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"io"
	"math"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

const (
	EventChatMessage       = "new-chat-message"
	EventGiftUser          = "new-gift-user"
	EventAnyGift           = "any-gift-received"
	EventPinnedComment     = "pinned-comment"
	EventFlaggedMessage    = "flagged-message"
	EventGiftQuestionCorr  = "gift-question-correlation"
	EventKeywordMention    = "keyword-mention"
	EventMarkUserRed       = "mark-user-red"
	EventConnectionStatus  = "connection-status"
	EventLiveUserConnected = "live-user-connected"
	EventNewFollower       = "new-follower"
	EventNewSocialEvent    = "new-social-event"
	EventGiftsList         = "gifts-list"
	EventNewLike           = "new-like-event"
)

type EventData map[string]interface{}

type EventHandler func(eventType string, data EventData)

// pendingEmit é um evento aguardando emissão fora do lock.
type pendingEmit struct {
	eventType string
	data      EventData
}

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
	UniqueID   string `json:"uniqueId"`
	Nickname   string `json:"nickname"`
	Comment    string `json:"comment"`
	Timestamp  int64  `json:"timestamp"`
	IsFollower *bool  `json:"isFollower"`
}

type GiftPayload struct {
	GiftName    string `json:"giftName"`
	UniqueID    string `json:"uniqueId"`
	Nickname    string `json:"nickname"`
	RepeatCount int    `json:"repeatCount"`
	RepeatEnd   bool   `json:"repeatEnd"`
	GiftType    int    `json:"giftType"`
	IsFollower  *bool  `json:"isFollower"`
}

type Settings struct {
	ModerationEnabled bool     `json:"moderationEnabled"`
	LogLevel          string   `json:"logLevel"`
	TargetGifts       []string `json:"targetGifts"`
}

type State struct {
	Connected         bool     `json:"connected"`
	Username          string   `json:"username"`
	Settings          Settings `json:"settings"`
	ReconnectAttempts int      `json:"reconnectAttempts,omitempty"`
}

const (
	chatBufferMax             = 500
	questionBufferMax         = 300
	sessionReuseMaxAge        = 10 * time.Hour
	questionCorrelationWindow = 3 * time.Minute
	correlationForwardCount   = 2
	correlationForwardDelay   = 4 * time.Second
	pinnedMessagesMax         = 200
	repeatWindowMs            = 60000
	repeatsRequired           = 3
)

// Ajustáveis para permitir testes com backoff acelerado. Usam atomics porque
// goroutines de produção (supervisor, timers) os leem em paralelo com os
// testes os gravando.
var (
	reconnectBaseDelay atomic.Int64  // nanos
	reconnectMaxDelay  atomic.Int64  // nanos
	reconnectJitterPct atomic.Uint64 // bits de float64
)

func init() {
	reconnectBaseDelay.Store(time.Second.Nanoseconds())
	reconnectMaxDelay.Store((30 * time.Second).Nanoseconds())
	reconnectJitterPct.Store(math.Float64bits(0.2))
}

type Monitor struct {
	mu              sync.Mutex
	cmd             *exec.Cmd
	stdin           io.WriteCloser
	stdout          io.ReadCloser
	currentUsername string
	chatBuffer      []ChatMessage
	questionBuffer  []QuestionEntry
	pinnedUsers     map[string]bool
	processedPins   map[string]bool
	repeatAlerted   map[string]bool
	handlers        []EventHandler
	settings        Settings
	connected       bool
	repo            model.Repository
	giftsCh         chan []string
	availableGifts  []string

	bridgeEnded       chan struct{}
	reconnectKick     chan struct{}
	reconnectAttempts int
	userStopped       bool
	supCancel         context.CancelFunc
	supDone           chan struct{}
	supStopCh         chan struct{}

	// giftStreaks rastreia streaks (combos) de presente aguardando liquidação;
	// ver handleGiftReceived/settleGiftStreak.
	giftStreaks map[string]*giftStreak
}

func New() (*Monitor, error) {
	return &Monitor{
		handlers:      make([]EventHandler, 0),
		pinnedUsers:   make(map[string]bool),
		processedPins: make(map[string]bool),
		repeatAlerted: make(map[string]bool),
		giftsCh:       make(chan []string, 1),
		reconnectKick: make(chan struct{}, 1),
		giftStreaks:   make(map[string]*giftStreak),
		settings: Settings{
			ModerationEnabled: true,
			LogLevel:          "info",
			TargetGifts:       []string{},
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
		Connected:         m.connected,
		Username:          m.currentUsername,
		Settings:          m.settings,
		ReconnectAttempts: m.reconnectAttempts,
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

// SetRepo sets the repository used for loading today's data on connect.
func (m *Monitor) SetRepo(repo model.Repository) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.repo = repo
}

// SetCurrentLive sets the current live username for gift filtering.
func (m *Monitor) SetCurrentLive(username string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.currentUsername = username
}

func (m *Monitor) StartMonitoring(ctx context.Context, username string) error {
	m.mu.Lock()
	m.userStopped = false
	m.reconnectAttempts = 0
	m.mu.Unlock()

	m.mu.Lock()
	needBridge := m.cmd == nil
	m.mu.Unlock()
	if needBridge {
		if err := m.startBridge(); err != nil {
			return fmt.Errorf("start bridge: %w", err)
		}
	}
	m.startSupervisor(ctx)

	m.mu.Lock()
	m.currentUsername = username
	m.chatBuffer = nil
	m.questionBuffer = nil
	m.pinnedUsers = make(map[string]bool)
	m.processedPins = make(map[string]bool)
	m.repeatAlerted = make(map[string]bool)
	for _, st := range m.giftStreaks {
		if st.timer != nil {
			st.timer.Stop()
		}
	}
	m.giftStreaks = make(map[string]*giftStreak)
	m.mu.Unlock()

	if m.repo != nil {
		m.restoreOrPurgeSessionData()
	}

	return m.sendBridge(map[string]interface{}{
		"action":   "connect",
		"username": username,
	})
}

// Close stops the supervisor and kills the bridge child process. It is meant
// for full application shutdown so the node bridge is not orphaned.
func (m *Monitor) Close() {
	m.stopSupervisor()
	m.stopBridge()
}

func (m *Monitor) StopMonitoring() {
	m.mu.Lock()
	m.userStopped = true
	m.connected = false
	m.mu.Unlock()
	m.stopSupervisor()

	m.mu.Lock()
	stdin := m.stdin
	m.mu.Unlock()
	if stdin != nil {
		// Best-effort: the bridge is stopping anyway, so a failed write
		// here is not actionable.
		_ = m.sendBridge(map[string]interface{}{
			"action": "disconnect",
		})
	}
	m.emit(EventConnectionStatus, EventData{
		"success": false,
		"error":   "Desconectado pelo usuário",
	})
}

// Emit dispatches an event to all registered handlers. It is exported so the
// controller layer can surface derived events.
func (m *Monitor) Emit(eventType string, data EventData) {
	m.emit(eventType, data)
}

func (m *Monitor) emit(eventType string, data EventData) {
	m.mu.Lock()
	handlers := append([]EventHandler(nil), m.handlers...)
	m.mu.Unlock()
	for _, h := range handlers {
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
