package monitor

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

// LiveState associates a monitored live with its current connection state.
type LiveState struct {
	State State  `json:"state"`
	Live  string `json:"live"`
}

// Manager owns independent Monitor instances. Each live gets its own bridge,
// buffers, reconnect supervisor and settings while sharing the repository.
type Manager struct {
	mu           sync.RWMutex
	operationMu  sync.Mutex
	monitors     map[string]*Monitor
	repo         model.Repository
	settings     Settings
	max          int
	eventHandler EventHandler
}

const defaultMaxMonitors = 10

// NewManager creates a manager that can monitor up to maxMonitors lives.
func NewManager(repo model.Repository, maxMonitors int) *Manager {
	if maxMonitors < 1 {
		maxMonitors = defaultMaxMonitors
	}
	return &Manager{monitors: make(map[string]*Monitor), repo: repo, max: maxMonitors, settings: Settings{ModerationEnabled: true, LogLevel: "info"}}
}

// StartMonitoring creates or reconnects the monitor identified by username.
func (m *Manager) StartMonitoring(ctx context.Context, username string) error {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	username = normalizeLiveName(username)
	if username == "" {
		return fmt.Errorf("username is required")
	}

	m.mu.Lock()
	if existing := m.monitors[username]; existing != nil {
		m.mu.Unlock()
		return existing.StartMonitoring(ctx, username)
	}
	if len(m.monitors) >= m.max {
		m.mu.Unlock()
		return fmt.Errorf("maximum of %d monitored lives reached", m.max)
	}
	monitor, err := New()
	if err != nil {
		m.mu.Unlock()
		return fmt.Errorf("create monitor for %s: %w", username, err)
	}
	monitor.SetRepo(m.repo)
	monitor.SetSettings(m.settings)
	monitor.OnEvent(func(eventType string, data EventData) {
		payload := cloneEventData(data)
		payload["liveName"] = username
		// O id da sessão viaja com o evento: é o que permite gravar cada linha
		// na live certa e depois apagar só aquela live.
		if id := monitor.CurrentLiveID(); id != "" {
			payload["liveId"] = id
		}
		m.emit(eventType, payload)
	})
	m.monitors[username] = monitor
	m.mu.Unlock()

	if err := monitor.StartMonitoring(ctx, username); err != nil {
		m.mu.Lock()
		delete(m.monitors, username)
		m.mu.Unlock()
		monitor.Close()
		return fmt.Errorf("start monitor for %s: %w", username, err)
	}
	return nil
}

// StopMonitoring stops one live, or all lives when username is empty.
func (m *Manager) StopMonitoring(username string) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	username = normalizeLiveName(username)
	m.mu.Lock()
	if username != "" {
		monitor := m.monitors[username]
		delete(m.monitors, username)
		m.mu.Unlock()
		if monitor != nil {
			monitor.Close()
		}
		return
	}
	monitors := make([]*Monitor, 0, len(m.monitors))
	for name, monitor := range m.monitors {
		delete(m.monitors, name)
		monitors = append(monitors, monitor)
	}
	m.mu.Unlock()
	for _, monitor := range monitors {
		monitor.Close()
	}
}

// States returns a stable snapshot of all configured live monitors.
func (m *Manager) States() []LiveState {
	m.mu.RLock()
	states := make([]LiveState, 0, len(m.monitors))
	for name, monitor := range m.monitors {
		states = append(states, LiveState{Live: name, State: monitor.GetState()})
	}
	m.mu.RUnlock()
	sort.Slice(states, func(i, j int) bool { return states[i].Live < states[j].Live })
	return states
}

// CurrentState returns the first monitored live for legacy single-live APIs.
func (m *Manager) CurrentState() State {
	states := m.States()
	if len(states) == 0 {
		return State{}
	}
	return states[0].State
}

// SetSettings updates the default and all active live monitors.
func (m *Manager) SetSettings(settings Settings) {
	m.mu.Lock()
	m.settings = settings
	monitors := make([]*Monitor, 0, len(m.monitors))
	for _, monitor := range m.monitors {
		monitors = append(monitors, monitor)
	}
	m.mu.Unlock()
	for _, monitor := range monitors {
		monitor.SetSettings(settings)
	}
}

// GetSettings returns the defaults applied to active and future monitors.
func (m *Manager) GetSettings() Settings {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.settings
}

// FetchAvailableGifts returns the gift catalog from one monitored live.
func (m *Manager) FetchAvailableGifts(username string) ([]string, error) {
	username = normalizeLiveName(username)
	m.mu.RLock()
	monitor := m.monitors[username]
	m.mu.RUnlock()
	if monitor == nil {
		return nil, fmt.Errorf("live %q is not monitored", username)
	}
	return monitor.FetchAvailableGifts()
}

// OnEvent registers a callback receiving events from every live.
func (m *Manager) OnEvent(handler EventHandler) {
	m.mu.Lock()
	// Store the callback through the manager's event fan-out slot.
	m.eventHandler = handler
	m.mu.Unlock()
}

// Close stops every live monitor and releases all bridge processes.
func (m *Manager) Close() { m.StopMonitoring("") }

func (m *Manager) emit(eventType string, data EventData) {
	m.mu.RLock()
	handler := m.eventHandler
	m.mu.RUnlock()
	if handler != nil {
		handler(eventType, data)
	}
}

func normalizeLiveName(username string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(username), "@"))
}

func cloneEventData(data EventData) EventData {
	clone := make(EventData, len(data)+1)
	for key, value := range data {
		clone[key] = value
	}
	return clone
}
