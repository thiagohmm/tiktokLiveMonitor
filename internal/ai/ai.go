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
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/config"
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
}

type queuedTask struct {
	req     CompletionRequest
	ctx     context.Context
	resolve chan string
	errCh   chan error
}

// NewManager creates a new AI Manager.
func NewManager(modelsDir, binDir string) *Manager {
	return &Manager{
		modelsDir: modelsDir,
		binDir:    binDir,
	}
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

	// Terminate any previous local server so it frees port 8080 before we
	// spawn a replacement (avoids "port is not free" + orphan processes).
	m.mu.Lock()
	if m.worker != nil && m.worker.IsLocal {
		m.worker.kill()
		m.worker = nil
	}
	m.mu.Unlock()

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
	// IMPORTANTE: o processo NÃO deve ser atrelado ao contexto do chamador
	// (ex.: r.Context() de uma requisição HTTP). O contexto da requisição é
	// cancelado assim que o handler responde, e `exec.CommandContext` mataria o
	// llama-server no fim de cada chamada a /api/probe-llm ou /api/readiness.
	// O processo só é encerrado por Manager.Stop() ou por um novo spawn.
	cmd := exec.Command(binPath, args...)
	cmd.Dir = filepath.Dir(binPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start llama-server: %w", err)
	}
	go cmd.Wait() // Reap the child when it exits (prevents zombie processes).

	w := &Worker{
		Host:    bindHost,
		Port:    basePort,
		IsLocal: true,
		Process: cmd,
	}

	// Wait for health. Se o contexto do chamador terminar no meio do carregamento,
	// abandonamos a espera mas NÃO matamos o processo: ele continua carregando em
	// segundo plano e fica pronto para as próximas sondagens.
	for i := 0; i < healthMaxTries; i++ {
		select {
		case <-ctx.Done():
			m.mu.Lock()
			m.worker = w // Keep worker; it may finish loading in the background.
			m.mu.Unlock()
			return nil
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
				if w.IsLocal && !w.processDead() {
					// Worker is still running: transient error (e.g. a slow
					// generation that timed out). Retry WITHOUT restarting the
					// server, otherwise every ask triggers a full model reload.
					log.Printf("[AI-Queue] Worker ainda ativo; erro transitório, mantendo sem restart.")
				} else {
					log.Printf("[AI-Queue] Worker morto; marcado para restart.")
					w.ready = false
				}
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

	// Prefer the model selected in model-config.json (config.GGUFPath resolves
	// the correct filename for the current platform). Only fall back to
	// auto-detect when the selected model file is missing.
	if selected := config.GGUFPath(m.modelsDir); selected != "" {
		if _, err := os.Stat(selected); err == nil {
			return binPath, selected
		}
	}

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
	modelPath = filepath.Join(m.modelsDir, "gemma-4-E4B-it-Q4_K_M.gguf") // Fallback.

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

	w.ready = resp.StatusCode == 200
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

	// Use parent context deadline if set, otherwise apply the completion
	// timeout (120s): local CPU generation can legitimately take that long.
	httpCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		httpCtx, cancel = context.WithTimeout(ctx, completionTimeout)
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

// processDead reports whether the local worker process has exited.
func (w *Worker) processDead() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.Process == nil || w.Process.Process == nil {
		return true
	}
	return w.Process.Process.Signal(syscall.Signal(0)) != nil
}

func numCPU() int {
	return max(1, runtime.NumCPU())
}
