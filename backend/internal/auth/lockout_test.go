package auth

import (
	"net/http"
	"net/http/httptest"
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

func TestClientIPIgnoresForwardedHeadersWithoutTrust(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/auth/login", nil)
	req.RemoteAddr = "203.0.113.5:4444"
	req.Header.Set("X-Forwarded-For", "1.2.3.4,5.6.7.8")
	req.Header.Set("X-Real-IP", "9.9.9.9")

	if got := ClientIP(req, ProxyTrust{}); got != "203.0.113.5" {
		t.Fatalf("ClientIP = %q, want RemoteAddr (headers não confiáveis)", got)
	}
}

func TestClientIPHonorsForwardedFromTrustedProxy(t *testing.T) {
	trust := ParseProxyTrust("10.0.0.0/8, 192.0.2.99")
	req := httptest.NewRequest(http.MethodGet, "/api/auth/login", nil)
	req.RemoteAddr = "10.0.0.2:5555"
	req.Header.Set("X-Forwarded-For", "198.51.100.7, 10.0.0.2")

	if got := ClientIP(req, trust); got != "198.51.100.7" {
		t.Fatalf("ClientIP = %q, want 198.51.100.7 (cliente real no XFF)", got)
	}
}

func TestClientIPFallsBackWhenAllHopsTrusted(t *testing.T) {
	trust := ParseProxyTrust("10.0.0.0/8")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.2:5555"
	req.Header.Set("X-Forwarded-For", "10.0.0.9, 10.0.0.2")

	if got := ClientIP(req, trust); got != "10.0.0.2" {
		t.Fatalf("ClientIP = %q, want fallback para RemoteAddr", got)
	}
}

func TestClientIPIPv6RemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "[::1]:8080"
	if got := ClientIP(req, ProxyTrust{}); got != "::1" {
		t.Fatalf("ClientIP = %q, want ::1 (IPv6 sem perder o host)", got)
	}
}

func TestLoginLockoutPrunesStaleEntries(t *testing.T) {
	l := NewLoginLockout(LockoutConfig{MaxAttempts: 5, Lockout: time.Minute})
	l.reapInterval = 0 // força limpeza em toda chamada
	l.keys["old@x|1.1.1.1"] = &lockoutEntry{lastSeen: time.Now().Add(-2 * time.Hour)}
	l.keys["fresh@x|2.2.2.2"] = &lockoutEntry{failures: 1, lastSeen: time.Now()}

	_ = l.Status("unrelated@x", "3.3.3.3") // dispara a limpeza
	if _, ok := l.keys["old@x|1.1.1.1"]; ok {
		t.Fatal("entrada antiga deveria ter sido removida")
	}
	if _, ok := l.keys["fresh@x|2.2.2.2"]; !ok {
		t.Fatal("entrada recente não deveria ter sido removida")
	}
}
