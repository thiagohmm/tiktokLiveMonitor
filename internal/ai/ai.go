// Package ai manages the local LLM worker (llama-server) and processes completion requests.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	basePort        = 8080
	healthMaxTries  = 90
	healthRetryMs   = 2000
	healthTimeout   = 2 * time.Second
	completionPath  = "/v1/chat/completions"
	completionTimeout = 120 * time.Second
	queueMax        = 50
	ctxSize         = 16384
)

// CompletionRequest represents a queued LLM completion task.
type CompletionRequest struct {
	SystemContent string
	UserContent   string
	MaxTokens     int
}

// Worker represents an LLM worker node (local or remote).
type Worker struct {
	Host    string
	Port    int
	IsLocal bool
	Process *exec.Cmd
	busy    bool
	ready   bool
	lastSeen time.Time
	mu      sync.Mutex
}

// Manager handles LLM worker lifecycle and request queueing.
type Manager struct {
	mu          sync.Mutex
	worker      *Worker
	queue       []queuedTask
	isRestarting bool
	restartTimer *time.Timer
	modelsDir   string
	binDir      string
	external    bool // LLM_ENDPOINT set: use a remote OpenAI-compatible server instead of spawning llama-server
	extHost     string
	extPort     int
}

type queuedTask struct {
	req     CompletionRequest
	ctx     context.Context
	resolve chan string
	errCh   chan error
}

// NewManager creates a new AI Manager.
func NewManager(modelsDir, binDir string) *Manager {
	m := &Manager{
		modelsDir: modelsDir,
		binDir:    binDir,
	}
	if host, port, ok := llmEndpointFromEnv(); ok {
		m.external = true
		m.extHost = host
		m.extPort = port
		log.Printf("[AI-Queue] LLM externo configurado via LLM_ENDPOINT: %s:%d", host, port)
	}
	return m
}

// llmEndpointFromEnv parses the LLM_ENDPOINT env var (e.g.
// "http://host:8080", "https://host" or "host:8080") into host/port.
func llmEndpointFromEnv() (host string, port int, ok bool) {
	raw := strings.TrimSpace(os.Getenv("LLM_ENDPOINT"))
	if raw == "" {
		return "", 0, false
	}
	secure := strings.HasPrefix(raw, "https://")
	v := strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
	v = strings.TrimSuffix(v, "/")
	if i := strings.Index(v, "/"); i >= 0 {
		v = v[:i]
	}
	if h, p, err := net.SplitHostPort(v); err == nil {
		if n, err2 := strconv.Atoi(p); err2 == nil {
			return h, n, true
		}
		if secure {
			return h, 443, true
		}
		return h, basePort, true
	}
	if secure {
		return v, 443, true
	}
	return v, basePort, true
}

// ProbeReady checks if the LLM worker is healthy.
func (m *Manager) ProbeReady(ctx context.Context) (bool, error) {
	m.mu.Lock()
	if m.worker == nil {
		m.mu.Unlock()
		if err := m.spawnLocal(ctx); err != nil {
			return false, err
		}
		m.mu.Lock()
	}
	w := m.worker
	m.mu.Unlock()

	if w == nil {
		return false, nil
	}

	return w.checkHealth(ctx), nil
}

// Complete queues a moderation request and returns the LLM response.
func (m *Manager) Complete(ctx context.Context, req CompletionRequest) (string, error) {
	m.mu.Lock()
	if len(m.queue) >= queueMax {
		m.mu.Unlock()
		log.Printf("[AI-Queue] Fila cheia (%d). Descartando mensagem.", queueMax)
		return "NAO", nil
	}

	task := queuedTask{
		req:     req,
		ctx:     ctx,
		resolve: make(chan string, 1),
		errCh:   make(chan error, 1),
	}
	m.queue = append(m.queue, task)
	m.mu.Unlock()

	go m.processQueue(ctx)

	select {
	case result := <-task.resolve:
		return result, nil
	case err := <-task.errCh:
		return "NAO", err
	case <-ctx.Done():
		return "NAO", ctx.Err()
	}
}

// RegisterWorker registers a remote worker node.
func (m *Manager) RegisterWorker(host string, port int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.worker != nil && m.worker.Host == host && m.worker.Port == port {
		m.worker.lastSeen = time.Now()
		m.worker.ready = true
		return
	}

	log.Printf("[AI-Queue] Novo worker registrado: %s:%d", host, port)
	if m.worker != nil && m.worker.IsLocal {
		m.worker.kill()
	}
	m.worker = &Worker{
		Host:    host,
		Port:    port,
		IsLocal: false,
		ready:   true,
		lastSeen: time.Now(),
	}
}

// Stop shuts down the local LLM worker.
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.worker != nil && m.worker.IsLocal {
		m.worker.kill()
		m.worker = nil
	}
	if m.restartTimer != nil {
		m.restartTimer.Stop()
		m.restartTimer = nil
	}
}

func (m *Manager) spawnLocal(ctx context.Context) error {
	m.mu.Lock()
	if m.isRestarting {
		m.mu.Unlock()
		return nil
	}
	m.isRestarting = true
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		m.isRestarting = false
		m.mu.Unlock()
	}()

	// Remote LLM via LLM_ENDPOINT: no local process is spawned.
	if m.external {
		w := &Worker{
			Host:     m.extHost,
			Port:     m.extPort,
			IsLocal:  false,
			ready:    false,
			lastSeen: time.Now(),
		}
		for i := 0; i < healthMaxTries; i++ {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(healthRetryMs * time.Millisecond):
			}
			if w.checkHealth(ctx) {
				log.Printf("[AI-Queue] LLM externo pronto em %s:%d", w.Host, w.Port)
				m.mu.Lock()
				m.worker = w
				m.mu.Unlock()
				return nil
			}
		}
		m.mu.Lock()
		m.worker = w // Keep worker even if not fully ready.
		m.mu.Unlock()
		return nil
	}

	binPath, modelPath := m.resolvePaths()
	if _, err := os.Stat(binPath); err != nil {
		return fmt.Errorf("llama-server binary not found at %s: %w", binPath, err)
	}
	if _, err := os.Stat(modelPath); err != nil {
		return fmt.Errorf("model not found at %s: %w", modelPath, err)
	}

	bindHost := os.Getenv("LLAMA_SERVER_BIND")
	if bindHost == "" {
		if _, err := os.Stat("/.dockerenv"); err == nil {
			bindHost = "0.0.0.0"
		} else {
			bindHost = "127.0.0.1"
		}
	}

	// Use all CPU cores.
	threads := fmt.Sprintf("%d", max(1, numCPU()))

	args := []string{
		"-m", modelPath,
		"--host", bindHost,
		"--port", fmt.Sprintf("%d", basePort),
		"--n-gpu-layers", "0",
		"--threads", threads,
		"--ctx-size", fmt.Sprintf("%d", ctxSize),
	}
	if os.Getenv("LLAMA_USE_MMAP") != "1" {
		args = append(args, "--no-mmap")
	}

	log.Printf("[AI-Queue] Iniciando llama-server na porta %d...", basePort)
	cmd := exec.CommandContext(ctx, binPath, args...)
	cmd.Dir = filepath.Dir(binPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start llama-server: %w", err)
	}

	w := &Worker{
		Host:    bindHost,
		Port:    basePort,
		IsLocal: true,
		Process: cmd,
	}

	// Wait for health.
	for i := 0; i < healthMaxTries; i++ {
		select {
		case <-ctx.Done():
			w.kill()
			return ctx.Err()
		case <-time.After(healthRetryMs * time.Millisecond):
		}
		if w.checkHealth(ctx) {
			log.Printf("[AI-Queue] llama-server pronto na porta %d.", basePort)
			m.mu.Lock()
			m.worker = w
			m.mu.Unlock()
			return nil
		}
	}

	m.mu.Lock()
	m.worker = w // Keep worker even if not fully ready.
	m.mu.Unlock()
	return nil
}

func (m *Manager) processQueue(ctx context.Context) {
	m.mu.Lock()
	if len(m.queue) == 0 {
		m.mu.Unlock()
		return
	}

	if m.worker == nil || !m.worker.ready || m.worker.busy {
		if !m.isRestarting && (m.worker == nil || !m.worker.ready) {
			m.mu.Unlock()
			go func() {
				if err := m.spawnLocal(ctx); err != nil {
					log.Printf("[AI-Queue] Falha ao iniciar worker: %v", err)
					m.scheduleRetry()
					return
				}
				m.processQueue(ctx)
			}()
			return
		}
		m.mu.Unlock()
		return
	}

	if m.restartTimer != nil {
		m.restartTimer.Stop()
		m.restartTimer = nil
	}

	// Skip cancelled tasks at dequeue.
	var task *queuedTask
	for len(m.queue) > 0 {
		candidate := &m.queue[0]
		m.queue = m.queue[1:]
		if candidate.ctx != nil && candidate.ctx.Err() != nil {
			log.Printf("[AI-Queue] Descartando tarefa cancelada (restam %d)", len(m.queue))
			select {
			case candidate.errCh <- candidate.ctx.Err():
			default:
			}
			continue
		}
		task = candidate
		break
	}

	if task == nil {
		m.mu.Unlock()
		return
	}

	m.worker.busy = true
	w := m.worker
	m.mu.Unlock()

	taskCtx := task.ctx
	if taskCtx == nil {
		taskCtx = ctx
	}

	log.Printf("[AI] Processando tarefa... (Restantes na fila: %d)", len(m.queue))
	go func() {
		defer func() {
			m.mu.Lock()
			if m.worker == w {
				m.worker.busy = false
			}
			m.mu.Unlock()
			m.processQueue(ctx)
		}()

		result, err := w.complete(taskCtx, task.req)
		if err != nil {
			if errors.Is(err, context.Canceled) || (task.ctx != nil && errors.Is(task.ctx.Err(), context.Canceled)) {
				log.Printf("[AI-Queue] Tarefa cancelada pelo usuário")
				select {
				case task.errCh <- err:
				default:
				}
				return
			}
			log.Printf("[AI] Erro ao processar tarefa: %v", err)
			m.mu.Lock()
			if m.worker == w {
				m.worker.ready = false
			}
			m.queue = append([]queuedTask{*task}, m.queue...)
			m.mu.Unlock()
			return
		}

		log.Printf("[AI] Resposta recebida: %s", result)
		task.resolve <- result
	}()
}

func (m *Manager) scheduleRetry() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.restartTimer != nil {
		return
	}
	m.restartTimer = time.AfterFunc(15*time.Second, func() {
		m.mu.Lock()
		m.restartTimer = nil
		m.mu.Unlock()
		m.processQueue(context.Background())
	})
}

func (m *Manager) resolvePaths() (binPath, modelPath string) {
	platform := runtime.GOOS
	arch := runtime.GOARCH

	binName := "llama-server"
	if platform == "windows" {
		binName = "llama-server.exe"
	}

	archDir := filepath.Join(m.binDir, platform, arch)
	binPath = m.resolveLlamaPath(archDir, binName)

	// Auto-detect .gguf model files in the models directory.
	entries, err := os.ReadDir(m.modelsDir)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".gguf") {
				modelPath = filepath.Join(m.modelsDir, e.Name())
				return binPath, modelPath
			}
		}
	}
	modelPath = filepath.Join(m.modelsDir, "ggml-model.gguf") // Fallback.

	return binPath, modelPath
}

func (m *Manager) resolveLlamaPath(archDir, binName string) string {
	candidates := []string{
		filepath.Join(archDir, binName),
		filepath.Join(archDir, "build", "bin", binName),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	// Try nested directories.
	entries, err := os.ReadDir(archDir)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				nested := filepath.Join(archDir, e.Name(), binName)
				if _, err := os.Stat(nested); err == nil {
					return nested
				}
			}
		}
	}
	return candidates[0]
}

func (w *Worker) checkHealth(ctx context.Context) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET",
		fmt.Sprintf("http://%s:%d/health", w.Host, w.Port), nil)
	if err != nil {
		w.ready = false
		return false
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		w.ready = false
		return false
	}
	resp.Body.Close()

	// 404 means the server is up but the /health route is missing
	// (non-llama-server OpenAI-compatible endpoints).
	w.ready = resp.StatusCode == 200 || resp.StatusCode == 404
	return w.ready
}

func (w *Worker) complete(ctx context.Context, req CompletionRequest) (string, error) {
	body := map[string]interface{}{
		"messages": []map[string]string{
			{"role": "system", "content": req.SystemContent},
			{"role": "user", "content": req.UserContent},
		},
		"temperature": 0.1,
		"max_tokens":  req.MaxTokens,
		"stream":      false,
		"chat_template_kwargs": map[string]bool{
			"enable_thinking": false,
		},
	}

	data, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	// Check if parent context is already cancelled before making HTTP request.
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	// Use parent context deadline if set, otherwise apply a reasonable timeout.
	httpCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		httpCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}

	httpReq, err := http.NewRequestWithContext(httpCtx, "POST",
		fmt.Sprintf("http://%s:%d%s", w.Host, w.Port, completionPath),
		bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respData, &chatResp); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("empty response")
	}

	text := strings.TrimSpace(chatResp.Choices[0].Message.Content)
	if text == "" {
		return "", fmt.Errorf("empty content in response")
	}
	return text, nil
}

func (w *Worker) kill() {
	if w.Process != nil {
		w.Process.Process.Kill()
		w.Process = nil
	}
}

func numCPU() int {
	return max(1, runtime.NumCPU())
}
