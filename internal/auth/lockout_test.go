package auth

import (
	"testing"
	"time"
)

func TestLoginLockoutBlocksAfterMaxAttempts(t *testing.T) {
	lockout := NewLoginLockout(LockoutConfig{MaxAttempts: 3, Lockout: time.Minute})

	for i := 0; i < 2; i++ {
		status := lockout.RecordFailure("user@example.com", "127.0.0.1")
		if status.Locked {
			t.Fatalf("attempt %d should not lock yet", i+1)
		}
		if status.Remaining != 3-(i+1) {
			t.Fatalf("attempt %d remaining = %d, want %d", i+1, status.Remaining, 3-(i+1))
		}
	}

	status := lockout.RecordFailure("user@example.com", "127.0.0.1")
	if !status.Locked {
		t.Fatal("expected lockout after max attempts")
	}
	if status.RetryAfterSec <= 0 {
		t.Fatalf("expected retryAfterSec > 0, got %d", status.RetryAfterSec)
	}

	locked := lockout.Status("user@example.com", "127.0.0.1")
	if !locked.Locked {
		t.Fatal("status should report locked")
	}
}

func TestLoginLockoutClearsOnSuccess(t *testing.T) {
	lockout := NewLoginLockout(LockoutConfig{MaxAttempts: 3, Lockout: time.Minute})
	lockout.RecordFailure("user@example.com", "127.0.0.1")
	lockout.RecordSuccess("user@example.com", "127.0.0.1")

	status := lockout.Status("user@example.com", "127.0.0.1")
	if status.Remaining != 3 {
		t.Fatalf("remaining = %d, want 3 after success", status.Remaining)
	}
}

func TestLoadThemeFromEnvDefaults(t *testing.T) {
	t.Setenv("AUTH_THEME_PINK", "")
	t.Setenv("AUTH_THEME_CYAN", "")
	t.Setenv("AUTH_THEME_BG", "")

	theme := LoadThemeFromEnv()
	if theme.Pink != "#fe2c55" || theme.Cyan != "#25f4ee" || theme.BG != "#0b0d12" {
		t.Fatalf("unexpected defaults: %+v", theme)
	}
}

func TestLoadThemeFromEnvCustom(t *testing.T) {
	t.Setenv("AUTH_THEME_PINK", "#aabbcc")
	t.Setenv("AUTH_THEME_CYAN", "#112233")
	t.Setenv("AUTH_THEME_BG", "#445566")

	theme := LoadThemeFromEnv()
	if theme.Pink != "#aabbcc" || theme.Cyan != "#112233" || theme.BG != "#445566" {
		t.Fatalf("unexpected theme: %+v", theme)
	}
}
