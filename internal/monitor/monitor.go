package monitor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
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

// Ajustáveis (var) para permitir testes com backoff acelerado.
var (
	reconnectBaseDelay = time.Second
	reconnectMaxDelay  = 30 * time.Second
	reconnectJitterPct = 0.2
)

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

	// LLMCorrelate is an optional fallback when the heuristic finds no match.
	LLMCorrelate func(ctx context.Context, gift GiftPayload, candidates []QuestionEntry) *QuestionEntry
}

func New() (*Monitor, error) {
	return &Monitor{
		handlers:      make([]EventHandler, 0),
		pinnedUsers:   make(map[string]bool),
		processedPins: make(map[string]bool),
		repeatAlerted: make(map[string]bool),
		giftsCh:       make(chan []string, 1),
		reconnectKick: make(chan struct{}, 1),
		settings: Settings{
			ModerationEnabled:   true,
			AIModerationEnabled: true,
			LogLevel:            "info",
			TargetGifts:         []string{},
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

// resolveBridgePath returns an absolute path to bridge.js.
// It tries multiple strategies so it works whether the binary is run via
// `go run`, as a compiled binary, or from a different working directory.
func resolveBridgePath() (string, error) {
	// 1. Try paths relative to the executable.
	exe, err := os.Executable()
	if err == nil {
		exeDir := filepath.Dir(exe)
		// e.g. /path/to/bin/internal/monitor/bridge.js
		candidate := filepath.Join(exeDir, "internal", "monitor", "bridge.js")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		// e.g. /path/to/bin/bridge.js (when the whole tree is flattened).
		candidate = filepath.Join(exeDir, "bridge.js")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		// e.g. /path/to/bin/../internal/monitor/bridge.js (compiled binary in parent dir).
		parentDir := filepath.Dir(exeDir)
		candidate = filepath.Join(parentDir, "internal", "monitor", "bridge.js")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	// 2. Try relative to current working directory.
	cwd, err := os.Getwd()
	if err == nil {
		candidate := filepath.Join(cwd, "internal", "monitor", "bridge.js")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	// 3. Try relative to source directory (go run).
	_, filename, _, _ := runtime.Caller(0)
	candidate := filepath.Join(filepath.Dir(filename), "..", "monitor", "bridge.js")
	if abs, err := filepath.Abs(candidate); err == nil {
		if _, err := os.Stat(abs); err == nil {
			return abs, nil
		}
	}

	return "", fmt.Errorf("bridge.js not found in any known location")
}

func (m *Monitor) startBridge() error {
	bridgePath, err := resolveBridgePath()
	if err != nil {
		return fmt.Errorf("resolve bridge: %w", err)
	}
	workDir := resolveNodeWorkDir(bridgePath)
	log.Printf("[Monitor] Starting bridge: %s (workdir=%s)", bridgePath, workDir)
	cmd := exec.Command("node", bridgePath)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "NODE_PATH="+filepath.Join(workDir, "node_modules"))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start bridge: %w", err)
	}

	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			log.Printf("[Bridge stderr] %s", scanner.Text())
		}
	}()

	m.mu.Lock()
	m.cmd = cmd
	m.stdin = stdin
	m.stdout = stdout
	m.bridgeEnded = make(chan struct{})
	m.mu.Unlock()

	go m.readBridge()

	return nil
}

func resolveNodeWorkDir(bridgePath string) string {
	candidates := make([]string, 0, 6)
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, cwd)
	}
	dir := filepath.Dir(bridgePath)
	for i := 0; i < 5; i++ {
		candidates = append(candidates, dir)
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(filepath.Join(candidate, "node_modules", "tiktok-live-connector")); err == nil {
			return candidate
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return filepath.Dir(bridgePath)
}

func (m *Monitor) stopBridge() {
	m.mu.Lock()
	if m.cmd == nil {
		m.mu.Unlock()
		return
	}
	cmd := m.cmd
	m.cmd = nil
	m.stdin = nil
	m.stdout = nil
	m.mu.Unlock()

	log.Println("[Monitor] Stopping bridge")
	if err := cmd.Process.Kill(); err == nil {
		cmd.Wait()
	}
}

// backoffDelay computes the delay before reconnect attempt n (1-indexed):
// exponential growth from reconnectBaseDelay capped at reconnectMaxDelay,
// with a random jitter of up to reconnectJitterPct.
func backoffDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	shift := attempt - 1
	if shift > 30 {
		shift = 30
	}
	d := time.Duration(1<<uint(shift)) * reconnectBaseDelay
	if d > reconnectMaxDelay {
		d = reconnectMaxDelay
	}
	jitter := time.Duration(rand.Float64() * reconnectJitterPct * float64(d))
	return d + jitter
}

// startSupervisor starts the reconnect supervisor goroutine (idempotent).
func (m *Monitor) startSupervisor(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.supDone != nil {
		return
	}
	supCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	stopCh := make(chan struct{})
	m.supCancel = cancel
	m.supDone = done
	m.supStopCh = stopCh
	go m.runSupervisor(supCtx, stopCh, done)
}

// stopSupervisor cancels the supervisor and waits for it to exit.
func (m *Monitor) stopSupervisor() {
	m.mu.Lock()
	cancel := m.supCancel
	done := m.supDone
	stopCh := m.supStopCh
	m.supCancel = nil
	m.supDone = nil
	m.supStopCh = nil
	m.mu.Unlock()

	if cancel == nil {
		return
	}
	select {
	case <-stopCh:
	default:
		close(stopCh)
	}
	cancel()
	<-done
}

// runSupervisor watches the bridge process and reconnect kicks. When the
// bridge dies or reports a lost connection, it restarts the bridge and
// re-sends "connect" with exponential backoff and jitter.
func (m *Monitor) runSupervisor(ctx context.Context, stopCh, done chan struct{}) {
	defer close(done)

	for {
		m.mu.Lock()
		ended := m.bridgeEnded
		username := m.currentUsername
		stopped := m.userStopped
		m.mu.Unlock()

		if stopped {
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-stopCh:
			return
		case <-ended:
			// bridge process ended unexpectedly
		case <-m.reconnectKick:
			// bridge alive but connection dropped
		}

		m.mu.Lock()
		stopped = m.userStopped
		m.reconnectAttempts++
		attempt := m.reconnectAttempts
		username = m.currentUsername
		m.mu.Unlock()

		if stopped || username == "" {
			return
		}

		delay := backoffDelay(attempt)
		log.Printf("[Monitor] Reconnecting to %s (attempt %d, next in %s)", username, attempt, delay)
		m.emit(EventConnectionStatus, EventData{
			"success":       false,
			"error":         fmt.Sprintf("Conexão perdida. Reconectando (tentativa %d, próxima em %s)...", attempt, delay.Round(time.Second)),
			"retries":       attempt,
			"nextRetryInMs": delay.Milliseconds(),
		})

		select {
		case <-ctx.Done():
			return
		case <-stopCh:
			return
		case <-time.After(delay):
		}

		m.mu.Lock()
		stopped = m.userStopped
		m.mu.Unlock()
		if stopped {
			return
		}

		m.stopBridge()
		if err := m.startBridge(); err != nil {
			log.Printf("[Monitor] Failed to restart bridge: %v", err)
			continue // loops back: waits and retries with longer backoff
		}
		if err := m.sendBridge(map[string]interface{}{
			"action":   "connect",
			"username": username,
		}); err != nil {
			log.Printf("[Monitor] Failed to send connect after bridge restart: %v", err)
		}
	}
}

func (m *Monitor) sendBridge(cmd map[string]interface{}) error {
	data, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	m.mu.Lock()
	stdin := m.stdin
	m.mu.Unlock()
	if stdin == nil {
		return fmt.Errorf("bridge stdin is not available")
	}
	_, err = fmt.Fprintf(stdin, "%s\n", data)
	return err
}

type bridgeMsg struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

func dataToEvent(raw interface{}) EventData {
	switch v := raw.(type) {
	case map[string]interface{}:
		return v
	case string:
		return EventData{"uniqueId": v}
	default:
		return EventData{}
	}
}

func (m *Monitor) readBridge() {
	m.mu.Lock()
	ended := m.bridgeEnded
	m.mu.Unlock()
	if ended != nil {
		defer close(ended)
	}

	scanner := bufio.NewScanner(m.stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		var msg bridgeMsg
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			log.Printf("[Monitor] Bridge JSON unmarshal error: %v (line: %s)", err, line[:min(len(line), 80)])
			continue
		}
		m.handleBridgeEvent(msg.Type, dataToEvent(msg.Data))
	}
	log.Println("[Monitor] Bridge process ended")
}

func (m *Monitor) handleBridgeEvent(eventType string, data EventData) {
	switch eventType {
	case "connection-status":
		success, _ := data["success"].(bool)
		m.mu.Lock()
		m.connected = success
		if success {
			m.reconnectAttempts = 0
		}
		stopped := m.userStopped
		m.mu.Unlock()
		log.Printf("[Monitor] Bridge connection-status: success=%v username=%v", success, data["username"])
		m.emit(eventType, data)
		if !success && !stopped {
			select {
			case m.reconnectKick <- struct{}{}:
			default:
			}
		}

	case "new-chat-message":
		m.mu.Lock()
		pending := m.handleChatMessage(data)
		m.mu.Unlock()
		for _, p := range pending {
			m.emit(p.eventType, p.data)
		}
		m.emit(eventType, data)

	case "any-gift-received":
		m.emit(eventType, data)

	case "new-gift-user":
		m.handleTargetGift(data)

	case EventNewLike:
		m.emit(EventNewLike, data)

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

	case "mark-user-red":
		m.emit(eventType, data)

	case "error":
		log.Printf("[Bridge] Error: %v", data["message"])

	case EventGiftsList:
		names := parseGiftNames(data)
		m.cacheAvailableGifts(names)
		select {
		case m.giftsCh <- names:
		default:
		}
		if len(names) > 0 {
			m.emit(EventGiftsList, EventData{"gifts": names})
		}
	}
}

// handleChatMessage muta o estado interno e retorna eventos pendentes.
// DEVE ser chamada com m.mu travado. NUNCA chame m.emit aqui dentro
// (emit trava m.mu e causaria deadlock — sync.Mutex não é reentrante).
func (m *Monitor) handleChatMessage(data EventData) []pendingEmit {
	var pending []pendingEmit

	comment := asString(data["comment"])
	comment = strings.TrimSpace(comment)
	if comment == "" {
		return pending
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
			pending = append(pending, pendingEmit{EventFlaggedMessage, EventData{
				"uniqueId":   user.UniqueID,
				"nickname":   user.Nickname,
				"isFollower": user.IsFollower,
				"comment":    comment,
				"reason":     "Mensagem repetida",
				"category":   "REPETICAO",
			}})
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
		pending = append(pending, pendingEmit{EventKeywordMention, EventData{
			"uniqueId":   user.UniqueID,
			"nickname":   user.Nickname,
			"comment":    comment,
			"keyword":    keyword,
			"timestamp":  now,
			"isFollower": user.IsFollower,
		}})
		pending = append(pending, pendingEmit{EventMarkUserRed, EventData{"uniqueId": senderKey}})
	}

	return pending
}

func (m *Monitor) handleTargetGift(data EventData) {
	user := extractFromData(data)
	uniqueID := normalizeID(user.UniqueID)

	m.mu.Lock()
	isPinned := m.pinnedUsers[uniqueID]
	m.mu.Unlock()

	giftName := asString(data["giftName"])
	if giftName == "" {
		giftName = asString(data["name"])
	}
	isTarget := m.isTargetGift(giftName)

	data["isRed"] = isTarget && isPinned

	if !isTarget || !m.isGiftCountingSettlement(data) {
		return
	}

	m.emit(EventGiftUser, data)

	repeatCount, _ := toInt(data["repeatCount"])
	giftType, _ := toInt(data["giftType"])
	repeatEnd := truthy(data["repeatEnd"])

	gift := GiftPayload{
		GiftName:    giftName,
		UniqueID:    user.UniqueID,
		Nickname:    user.Nickname,
		RepeatCount: repeatCount,
		RepeatEnd:   repeatEnd,
		GiftType:    giftType,
		IsFollower:  user.IsFollower,
	}
	go m.correlateGiftWithQuestion(gift)
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
	m.mu.Lock()
	m.userStopped = false
	m.reconnectAttempts = 0
	m.mu.Unlock()

	if m.cmd == nil {
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
	m.mu.Unlock()

	if m.repo != nil {
		m.restoreOrPurgeSessionData()
	}

	return m.sendBridge(map[string]interface{}{
		"action":   "connect",
		"username": username,
	})
}

// sessionReusable is true when last activity is on the same UTC calendar day
// and less than sessionReuseMaxAge before now.
// Timestamps are stored and read in UTC, so compare days in UTC.
func sessionReusable(last, now time.Time) bool {
	if last.IsZero() {
		return false
	}
	last = last.UTC()
	now = now.UTC()
	if last.Year() != now.Year() || last.YearDay() != now.YearDay() {
		return false
	}
	return now.Sub(last) < sessionReuseMaxAge
}

// restoreOrPurgeSessionData reloads today's session if it is still reusable;
// otherwise it deletes gifts, messages and anomaly logs for this live.
func (m *Monitor) restoreOrPurgeSessionData() {
	m.mu.Lock()
	liveName := m.currentUsername
	m.mu.Unlock()

	last, ok, err := m.repo.GetLastSessionActivity(liveName)
	if err != nil {
		log.Printf("[Monitor] Error reading last session activity: %v", err)
		return
	}
	if ok && sessionReusable(last, time.Now()) {
		m.loadTodayData()
		return
	}
	if err := m.repo.DeleteSessionData(liveName); err != nil {
		log.Printf("[Monitor] Error deleting stale session data: %v", err)
		return
	}
	log.Printf("[Monitor] Purged session data for %s", liveName)
}

// loadTodayData loads today's user messages and anomaly logs from the database
// to restore the chat buffer and pinned users when reconnecting to the same live.
func (m *Monitor) loadTodayData() {
	now := time.Now().UnixMilli()

	m.mu.Lock()
	currentUsername := m.currentUsername
	m.mu.Unlock()

	todayMsgs, err := m.repo.GetTodayUserMessages()
	if err != nil {
		log.Printf("[Monitor] Error loading today's messages: %v", err)
	} else if len(todayMsgs) > 0 {
		m.mu.Lock()
		for _, um := range todayMsgs {
			m.chatBuffer = append(m.chatBuffer, ChatMessage{
				UniqueID:  um.UniqueID,
				Nickname:  um.Username,
				Comment:   um.Message,
				Timestamp: now,
			})
			if looksLikeQuestion(um.Message) {
				m.questionBuffer = append(m.questionBuffer, QuestionEntry{
					UniqueID:  um.UniqueID,
					Nickname:  um.Username,
					Comment:   um.Message,
					Timestamp: now,
				})
			}
		}
		if len(m.chatBuffer) > chatBufferMax {
			m.chatBuffer = m.chatBuffer[len(m.chatBuffer)-chatBufferMax:]
		}
		if len(m.questionBuffer) > questionBufferMax {
			m.questionBuffer = m.questionBuffer[len(m.questionBuffer)-questionBufferMax:]
		}
		m.mu.Unlock()
		log.Printf("[Monitor] Loaded %d messages from today", len(todayMsgs))
	}

	todayAnomalies, err := m.repo.GetTodayAnomalyLogs(currentUsername)
	if err != nil {
		log.Printf("[Monitor] Error loading today's anomaly logs: %v", err)
		return
	}
	if len(todayAnomalies) == 0 {
		return
	}
	m.mu.Lock()
	for _, al := range todayAnomalies {
		if al.UniqueID != "" {
			m.pinnedUsers[normalizeID(al.UniqueID)] = true
		}
	}
	m.mu.Unlock()
	log.Printf("[Monitor] Restored %d pinned users from today's anomaly logs", len(todayAnomalies))
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

	if m.stdin != nil {
		m.sendBridge(map[string]interface{}{
			"action": "disconnect",
		})
	}
	m.emit(EventConnectionStatus, EventData{
		"success": false,
		"error":   "Desconectado pelo usuário",
	})
}

// Emit dispatches an event to all registered handlers. It is exported so the
// controller layer can surface derived events (e.g. reply suggestions).
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

func (m *Monitor) FetchAvailableGifts() ([]string, error) {
	if cached := m.CachedAvailableGifts(); len(cached) > 0 {
		return cached, nil
	}
	m.mu.Lock()
	stdin := m.stdin
	m.mu.Unlock()
	if stdin != nil {
		go m.requestAvailableGifts()
	}
	return []string{}, nil
}

func (m *Monitor) requestAvailableGifts() {
	if err := m.sendBridge(map[string]interface{}{
		"action": "fetch-gifts",
	}); err != nil {
		log.Printf("[Monitor] request available gifts: %v", err)
	}
}

// CachedAvailableGifts returns a copy of the last non-empty gift catalog.
func (m *Monitor) CachedAvailableGifts() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.availableGifts) == 0 {
		return nil
	}
	out := make([]string, len(m.availableGifts))
	copy(out, m.availableGifts)
	return out
}

func (m *Monitor) cacheAvailableGifts(names []string) {
	if len(names) == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.availableGifts = append([]string(nil), names...)
}

func parseGiftNames(data EventData) []string {
	raw, ok := data["gifts"]
	if !ok || raw == nil {
		return nil
	}
	switch names := raw.(type) {
	case []string:
		out := make([]string, 0, len(names))
		for _, s := range names {
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	case []interface{}:
		out := make([]string, 0, len(names))
		for _, n := range names {
			if s, ok := n.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func extractFromData(data EventData) UserInfo {
	info := UserInfo{
		UniqueID: asString(data["uniqueId"]),
		Nickname: asString(data["nickname"]),
	}
	if info.Nickname == "" {
		info.Nickname = info.UniqueID
	}
	if f, ok := parseFollowerFlag(data["isFollower"]); ok {
		info.IsFollower = f
	}
	return info
}

func asString(v interface{}) string {
	switch n := v.(type) {
	case string:
		return strings.TrimSpace(n)
	case float64:
		return strconv.FormatInt(int64(n), 10)
	case json.Number:
		return strings.TrimSpace(n.String())
	case int:
		return strconv.Itoa(n)
	case int64:
		return strconv.FormatInt(n, 10)
	default:
		return ""
	}
}

func parseFollowerFlag(v interface{}) (*bool, bool) {
	switch n := v.(type) {
	case bool:
		b := n
		return &b, true
	case float64:
		if n == 1 || n == 2 {
			b := true
			return &b, true
		}
		if n == 0 {
			b := false
			return &b, true
		}
	case string:
		switch strings.TrimSpace(strings.ToLower(n)) {
		case "true", "1", "2":
			b := true
			return &b, true
		case "false", "0":
			b := false
			return &b, true
		}
	}
	return nil, false
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

func (m *Monitor) isGiftCountingSettlement(data EventData) bool {
	giftType, _ := toInt(data["giftType"])
	if giftType != 1 {
		return true
	}
	if _, ok := data["repeatEnd"]; !ok {
		return true
	}
	return truthy(data["repeatEnd"])
}

func truthy(v interface{}) bool {
	switch n := v.(type) {
	case bool:
		return n
	case float64:
		return n != 0
	case int:
		return n != 0
	case int64:
		return n != 0
	case string:
		s := strings.TrimSpace(strings.ToLower(n))
		return s != "" && s != "false" && s != "0"
	default:
		return false
	}
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
