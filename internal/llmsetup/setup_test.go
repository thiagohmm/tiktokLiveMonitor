package llmsetup

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/config"
)

func TestSnapshotReportsExistingGGUF(t *testing.T) {
	dir := t.TempDir()
	if err := config.InitConfig(dir); err != nil {
		t.Fatalf("InitConfig: %v", err)
	}
	info := config.Models["gemma-4b"]
	path := filepath.Join(dir, info.Filename)
	if err := os.WriteFile(path, []byte("GGUFfake-payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := New(dir)
	snap := m.Snapshot()
	if snap.Missing != 1 {
		t.Fatalf("missing=%d want 1 (only gemma present)", snap.Missing)
	}
	var found bool
	for _, mod := range snap.Models {
		if mod.Key == "gemma-4b" {
			found = true
			if !mod.Exists {
				t.Fatal("gemma-4b should exist")
			}
			if !mod.Selected {
				t.Fatal("gemma-4b should be selected by default")
			}
		}
	}
	if !found {
		t.Fatal("gemma-4b missing from snapshot")
	}
}

func TestFileOKRejectsNonGGUF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.bin")
	if err := os.WriteFile(path, []byte("not-a-gguf"), 0o644); err != nil {
		t.Fatal(err)
	}
	ok, _ := fileOK(path)
	if ok {
		t.Fatal("expected non-GGUF to be rejected")
	}
}

func TestStartDownloadNoopWhenPresent(t *testing.T) {
	dir := t.TempDir()
	if err := config.InitConfig(dir); err != nil {
		t.Fatalf("InitConfig: %v", err)
	}
	for key, info := range config.Models {
		path := filepath.Join(dir, info.Filename)
		if err := os.WriteFile(path, append([]byte("GGUF"), []byte(key)...), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := New(dir)
	started, err := m.StartDownload(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !started {
		t.Fatal("expected download goroutine to start")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !m.Snapshot().Progress.Active {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	snap := m.Snapshot()
	if snap.Missing != 0 {
		t.Fatalf("missing=%d", snap.Missing)
	}
	if snap.Progress.Active {
		t.Fatal("progress still active")
	}
	if snap.Progress.Status != "exists" && snap.Progress.Status != "done" {
		t.Fatalf("unexpected status %q", snap.Progress.Status)
	}
}
