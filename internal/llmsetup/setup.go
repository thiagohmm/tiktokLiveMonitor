// Package llmsetup checks local GGUF models and downloads missing ones with progress.
package llmsetup

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/config"
)

// ModelStatus describes one registered LLM on disk.
type ModelStatus struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	Filename  string `json:"filename"`
	Exists    bool   `json:"exists"`
	SizeBytes int64  `json:"sizeBytes"`
	Selected  bool   `json:"selected"`
}

// Progress is the transient state of an active (or last) download.
type Progress struct {
	Active    bool   `json:"active"`
	Key       string `json:"key,omitempty"`
	Filename  string `json:"filename,omitempty"`
	Percent   int    `json:"percent"`
	BytesDone int64  `json:"bytesDone"`
	BytesTotal int64 `json:"bytesTotal"`
	Status    string `json:"status"` // idle | downloading | done | error | exists
	Error     string `json:"error,omitempty"`
}

// Snapshot is returned by GET /api/models.
type Snapshot struct {
	Selected   string        `json:"selected"`
	Models     []ModelStatus `json:"models"`
	AllPresent bool          `json:"allPresent"`
	Missing    int           `json:"missing"`
	Progress   Progress      `json:"progress"`
}

// Manager tracks model files under modelsDir and runs at most one download.
type Manager struct {
	modelsDir string
	client    *http.Client

	mu       sync.Mutex
	progress Progress
	cancel   context.CancelFunc
}

// New creates a Manager rooted at modelsDir.
func New(modelsDir string) *Manager {
	return &Manager{
		modelsDir: modelsDir,
		client:    &http.Client{Timeout: 0}, // long downloads; per-request ctx cancels
		progress:  Progress{Status: "idle"},
	}
}

// Snapshot returns registry status plus current download progress.
func (m *Manager) Snapshot() Snapshot {
	selected := config.GetSelectedModel()
	keys := make([]string, 0, len(config.Models))
	for k := range config.Models {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	models := make([]ModelStatus, 0, len(keys))
	missing := 0
	for _, key := range keys {
		info := config.Models[key]
		st := ModelStatus{
			Key:      key,
			Name:     info.Name,
			Filename: info.Filename,
			Selected: key == selected,
		}
		path := filepath.Join(m.modelsDir, info.Filename)
		if ok, size := fileOK(path); ok {
			st.Exists = true
			st.SizeBytes = size
		} else {
			missing++
		}
		models = append(models, st)
	}

	m.mu.Lock()
	prog := m.progress
	m.mu.Unlock()

	return Snapshot{
		Selected:   selected,
		Models:     models,
		AllPresent: missing == 0,
		Missing:    missing,
		Progress:   prog,
	}
}

// StartDownload downloads missing models (or the given keys). Returns false if already running.
func (m *Manager) StartDownload(keys []string) (started bool, err error) {
	m.mu.Lock()
	if m.progress.Active {
		m.mu.Unlock()
		return false, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.progress = Progress{Active: true, Status: "downloading", Percent: 0}
	m.mu.Unlock()

	if len(keys) == 0 {
		selected := config.GetSelectedModel()
		snap := m.Snapshot()
		selectedMissing := false
		for _, mod := range snap.Models {
			if mod.Key == selected && !mod.Exists {
				keys = append(keys, mod.Key)
				selectedMissing = true
				break
			}
		}
		if !selectedMissing {
			for _, mod := range snap.Models {
				if !mod.Exists {
					keys = append(keys, mod.Key)
				}
			}
		}
	}

	go func() {
		defer func() {
			m.mu.Lock()
			m.progress.Active = false
			if m.progress.Status == "downloading" {
				m.progress.Status = "done"
				m.progress.Percent = 100
			}
			m.cancel = nil
			m.mu.Unlock()
			cancel()
		}()

		if len(keys) == 0 {
			m.setProgress(Progress{Status: "exists", Percent: 100})
			return
		}

		for _, key := range keys {
			info, ok := config.Models[key]
			if !ok {
				m.setProgress(Progress{Status: "error", Error: fmt.Sprintf("modelo desconhecido: %s", key)})
				return
			}
			dest := filepath.Join(m.modelsDir, info.Filename)
			if ok, _ := fileOK(dest); ok {
				m.setProgress(Progress{
					Status:   "exists",
					Key:      key,
					Filename: info.Filename,
					Percent:  100,
				})
				continue
			}
			if err := m.downloadOne(ctx, key, info, dest); err != nil {
				if ctx.Err() != nil {
					m.setProgress(Progress{Status: "error", Key: key, Filename: info.Filename, Error: "download cancelado"})
					return
				}
				m.setProgress(Progress{Status: "error", Key: key, Filename: info.Filename, Error: err.Error()})
				return
			}
		}
		m.setProgress(Progress{Status: "done", Percent: 100})
	}()

	return true, nil
}

func (m *Manager) downloadOne(ctx context.Context, key string, info config.ModelInfo, dest string) error {
	if err := os.MkdirAll(m.modelsDir, 0o755); err != nil {
		return err
	}
	tmp := dest + ".partial"
	_ = os.Remove(tmp)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, info.URL, nil)
	if err != nil {
		return err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Follow redirects manually is automatic for GET via client; check final status.
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download HTTP %d", resp.StatusCode)
	}

	total := resp.ContentLength
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer out.Close()

	var done int64
	buf := make([]byte, 256*1024)
	lastPct := -1
	lastEmit := time.Time{}
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				_ = os.Remove(tmp)
				return werr
			}
			done += int64(n)
			pct := 0
			if total > 0 {
				pct = int(done * 100 / total)
				if pct > 100 {
					pct = 100
				}
			}
			now := time.Now()
			if pct != lastPct && (lastPct < 0 || pct == 100 || now.Sub(lastEmit) >= 200*time.Millisecond) {
				lastPct = pct
				lastEmit = now
				m.setProgress(Progress{
					Active:     true,
					Key:        key,
					Filename:   info.Filename,
					Percent:    pct,
					BytesDone:  done,
					BytesTotal: total,
					Status:     "downloading",
				})
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = os.Remove(tmp)
			return readErr
		}
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if total > 0 && done != total {
		_ = os.Remove(tmp)
		return fmt.Errorf("download incompleto: %d/%d bytes", done, total)
	}
	if ok, _ := fileOK(tmp); !ok {
		_ = os.Remove(tmp)
		return fmt.Errorf("arquivo GGUF inválido após download")
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (m *Manager) setProgress(p Progress) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil && p.Status != "error" && p.Status != "done" {
		p.Active = true
	}
	m.progress = p
}

func fileOK(path string) (bool, int64) {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() || st.Size() == 0 {
		return false, 0
	}
	f, err := os.Open(path)
	if err != nil {
		return false, 0
	}
	defer f.Close()
	var magic [4]byte
	n, err := f.Read(magic[:])
	if err != nil || n < 4 || string(magic[:]) != "GGUF" {
		return false, 0
	}
	return true, st.Size()
}
