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

// ErrOrgLiveLimit is returned when an organization reached its live quota.
var ErrOrgLiveLimit = fmt.Errorf("limite de lives simultâneas da organização atingido")

// Manager owns independent Monitor instances, one per (organization, live).
// Each gets its own bridge, buffers, reconnect supervisor, session and the
// settings of its organization: two organizations watching the same streamer
// never share rows, settings or events.
type Manager struct {
	mu           sync.RWMutex
	operationMu  sync.Mutex
	monitors     map[liveKey]*Monitor
	repo         model.Repository
	max          int
	eventHandler EventHandler
}

type liveKey struct {
	org  string
	live string
}

const defaultMaxMonitors = 10

// NewManager creates a manager that can monitor up to maxMonitors lives in
// total, across every organization.
func NewManager(repo model.Repository, maxMonitors int) *Manager {
	if maxMonitors < 1 {
		maxMonitors = defaultMaxMonitors
	}
	return &Manager{monitors: make(map[liveKey]*Monitor), repo: repo, max: maxMonitors}
}

// StartMonitoring creates or reconnects the monitor of orgID for username.
// settings are the organization's settings; orgMax bounds how many lives the
// organization may monitor at once (0 = no organization bound).
func (m *Manager) StartMonitoring(ctx context.Context, orgID, username string, settings Settings, orgMax int) error {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	orgID = strings.TrimSpace(orgID)
	username = normalizeLiveName(username)
	if orgID == "" {
		return model.ErrOrgRequired
	}
	if username == "" {
		return fmt.Errorf("username is required")
	}
	key := liveKey{org: orgID, live: username}

	m.mu.Lock()
	if existing := m.monitors[key]; existing != nil {
		m.mu.Unlock()
		return existing.StartMonitoring(ctx, username)
	}
	if len(m.monitors) >= m.max {
		m.mu.Unlock()
		return fmt.Errorf("maximum of %d monitored lives reached", m.max)
	}
	if orgMax > 0 && m.countOrgLocked(orgID) >= orgMax {
		m.mu.Unlock()
		return ErrOrgLiveLimit
	}
	monitor, err := New()
	if err != nil {
		m.mu.Unlock()
		return fmt.Errorf("create monitor for %s: %w", username, err)
	}
	monitor.SetRepo(m.repo)
	monitor.SetOrgID(orgID)
	monitor.SetSettings(settings)
	monitor.OnEvent(func(eventType string, data EventData) {
		payload := cloneEventData(data)
		payload["liveName"] = username
		payload["orgId"] = orgID
		// O id da sessão viaja com o evento: é o que permite gravar cada linha
		// na live certa e depois apagar só aquela live.
		if id := monitor.CurrentLiveID(); id != "" {
			payload["liveId"] = id
		}
		m.emit(eventType, payload)
	})
	m.monitors[key] = monitor
	m.mu.Unlock()

	if err := monitor.StartMonitoring(ctx, username); err != nil {
		m.mu.Lock()
		delete(m.monitors, key)
		m.mu.Unlock()
		monitor.Close()
		return fmt.Errorf("start monitor for %s: %w", username, err)
	}
	return nil
}

func (m *Manager) countOrgLocked(orgID string) int {
	n := 0
	for key := range m.monitors {
		if key.org == orgID {
			n++
		}
	}
	return n
}

// StopMonitoring stops one live of the organization, or every live of the
// organization when username is empty.
func (m *Manager) StopMonitoring(orgID, username string) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	orgID = strings.TrimSpace(orgID)
	username = normalizeLiveName(username)
	if orgID == "" {
		return
	}
	m.mu.Lock()
	stopped := make([]*Monitor, 0, 1)
	for key, monitor := range m.monitors {
		if key.org != orgID || (username != "" && key.live != username) {
			continue
		}
		delete(m.monitors, key)
		stopped = append(stopped, monitor)
	}
	m.mu.Unlock()
	for _, monitor := range stopped {
		monitor.Close()
	}
}

// StopAll stops every live of every organization.
func (m *Manager) StopAll() {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	m.mu.Lock()
	monitors := make([]*Monitor, 0, len(m.monitors))
	for key, monitor := range m.monitors {
		delete(m.monitors, key)
		monitors = append(monitors, monitor)
	}
	m.mu.Unlock()
	for _, monitor := range monitors {
		monitor.Close()
	}
}

// States returns a stable snapshot of the organization's live monitors.
func (m *Manager) States(orgID string) []LiveState {
	m.mu.RLock()
	states := make([]LiveState, 0)
	for key, monitor := range m.monitors {
		if key.org != orgID {
			continue
		}
		states = append(states, LiveState{Live: key.live, State: monitor.GetState()})
	}
	m.mu.RUnlock()
	sort.Slice(states, func(i, j int) bool { return states[i].Live < states[j].Live })
	return states
}

// CurrentState returns the first monitored live of the organization, for the
// single-live APIs.
func (m *Manager) CurrentState(orgID string) State {
	states := m.States(orgID)
	if len(states) == 0 {
		return State{}
	}
	return states[0].State
}

// StateFor returns the state of one live of the organization.
func (m *Manager) StateFor(orgID, username string) (State, bool) {
	m.mu.RLock()
	monitor := m.monitors[liveKey{org: strings.TrimSpace(orgID), live: normalizeLiveName(username)}]
	m.mu.RUnlock()
	if monitor == nil {
		return State{}, false
	}
	return monitor.GetState(), true
}

// SetSettings applies the organization's settings to its active monitors.
func (m *Manager) SetSettings(orgID string, settings Settings) {
	m.mu.RLock()
	monitors := make([]*Monitor, 0)
	for key, monitor := range m.monitors {
		if key.org == orgID {
			monitors = append(monitors, monitor)
		}
	}
	m.mu.RUnlock()
	for _, monitor := range monitors {
		monitor.SetSettings(settings)
	}
}

// FetchAvailableGifts returns the gift catalog from one live of the organization.
func (m *Manager) FetchAvailableGifts(orgID, username string) ([]string, error) {
	username = normalizeLiveName(username)
	m.mu.RLock()
	monitor := m.monitors[liveKey{org: strings.TrimSpace(orgID), live: username}]
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
func (m *Manager) Close() { m.StopAll() }

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
	clone := make(EventData, len(data)+2)
	for key, value := range data {
		clone[key] = value
	}
	return clone
}
