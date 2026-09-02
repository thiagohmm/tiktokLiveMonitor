package monitor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

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
	// Best-effort: after Kill, Wait only reaps the process; its error is
	// not actionable here.
	if err := cmd.Process.Kill(); err == nil {
		_ = cmd.Wait()
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
	d := time.Duration(1<<uint(shift)) * time.Duration(reconnectBaseDelay.Load())
	if d > time.Duration(reconnectMaxDelay.Load()) {
		d = time.Duration(reconnectMaxDelay.Load())
	}
	jitter := time.Duration(rand.Float64() * math.Float64frombits(reconnectJitterPct.Load()) * float64(d))
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
		username := m.currentUsername
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
