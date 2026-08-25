// Package agent manages the lifecycle of the Python AI agent subprocess and
// exposes a reverse proxy to its HTTP API.
package agent

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultHost = "127.0.0.1"
	defaultPort = 9001
)

// Manager spawns and supervises the Python agent subprocess.
type Manager struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	host    string
	port    int
	baseDir string
	started bool
}

// NewManager creates an agent Manager rooted at baseDir.
func NewManager(baseDir string) *Manager {
	port := defaultPort
	if p := os.Getenv("AGENT_PORT"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			port = n
		}
	}
	return &Manager{
		host:    defaultHost,
		port:    port,
		baseDir: baseDir,
	}
}

// Start spawns the Python agent subprocess. It is a no-op when Python is not
// available, so the application keeps working without the agent.
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started {
		return nil
	}

	// Reuse an agent that is already listening on the configured port (e.g. a
	// previous instance still alive) instead of spawning a duplicate that would
	// fail with "address already in use".
	if m.agentHealthy() {
		log.Printf("[Agent] Reutilizando agente Python já em execução na porta %d", m.port)
		m.started = true
		return nil
	}

	python, err := findPython()
	if err != nil {
		log.Printf("[Agent] Python não encontrado; agente desabilitado (%v)", err)
		return nil
	}

	monitorPort := os.Getenv("PORT")
	if monitorPort == "" {
		monitorPort = "3001"
	}
	llmURL := os.Getenv("LLM_URL")
	if llmURL == "" {
		llmURL = "http://127.0.0.1:8080/v1"
	}

	cmd := exec.Command(python, "-m", "agent")
	cmd.Dir = m.baseDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = withEnv(map[string]string{
		"AGENT_HOST":  m.host,
		"AGENT_PORT":  strconv.Itoa(m.port),
		"MONITOR_URL": "http://127.0.0.1:" + monitorPort,
		"LLM_URL":     llmURL,
	})

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start agent: %w", err)
	}
	m.cmd = cmd
	m.started = true
	go cmd.Wait() // reap the child process
	log.Printf("[Agent] Subprocesso Python iniciado (pid %d) na porta %d", cmd.Process.Pid, m.port)
	return nil
}

// Stop terminates the agent subprocess.
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd != nil && m.cmd.Process != nil {
		m.cmd.Process.Kill()
		m.cmd = nil
	}
	m.started = false
}

// BaseURL returns the base URL of the agent HTTP API.
func (m *Manager) BaseURL() string {
	return fmt.Sprintf("http://%s:%d", m.host, m.port)
}

// ProxyHandler returns an http.Handler that reverse-proxies /agent/* to the
// agent HTTP API.
func (m *Manager) ProxyHandler() http.Handler {
	target, _ := url.Parse(fmt.Sprintf("http://%s:%d", m.host, m.port))
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("[Agent] proxy error: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprintf(w, `{"error":"agent unavailable"}`)
	}
	return http.StripPrefix("/agent", proxy)
}

func findPython() (string, error) {
	for _, name := range []string{"python3", "python"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("python3/python não localizado no PATH")
}

// agentHealthy reports whether an agent is already serving /health on the
// configured host/port.
func (m *Manager) agentHealthy() bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://%s:%d/health", m.host, m.port))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// withEnv returns os.Environ() with the given keys overridden.
func withEnv(extra map[string]string) []string {
	keys := make(map[string]bool, len(extra))
	for k := range extra {
		keys[k] = true
	}
	env := os.Environ()
	out := make([]string, 0, len(env)+len(extra))
	for _, kv := range env {
		k := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			k = kv[:i]
		}
		if keys[k] {
			continue
		}
		out = append(out, kv)
	}
	for k, v := range extra {
		out = append(out, k+"="+v)
	}
	return out
}
