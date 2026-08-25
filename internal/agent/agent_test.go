package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindPythonPrefersBundledRuntime(t *testing.T) {
	base := t.TempDir()
	binDir := filepath.Join(base, "runtime", "python", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bundled := filepath.Join(binDir, "python3")
	if err := os.WriteFile(bundled, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := findPython(base)
	if err != nil {
		t.Fatalf("findPython: %v", err)
	}
	if got != bundled {
		t.Fatalf("got %q want bundled %q", got, bundled)
	}
}

func TestFindPythonMissingReturnsError(t *testing.T) {
	// Empty dir + clear PATH so LookPath fails.
	base := t.TempDir()
	t.Setenv("PATH", base)
	_, err := findPython(base)
	if err == nil {
		t.Fatal("expected error when no python is available")
	}
}
