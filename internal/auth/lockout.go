package auth

import (
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// LockoutConfig controls brute-force protection on the login endpoint.
type LockoutConfig struct {
	MaxAttempts int
	Lockout     time.Duration
}

// LoadLockoutConfigFromEnv reads AUTH_MAX_LOGIN_ATTEMPTS and AUTH_LOCKOUT_MINUTES.
func LoadLockoutConfigFromEnv() LockoutConfig {
	maxAttempts := 5
	if v := strings.TrimSpace(os.Getenv("AUTH_MAX_LOGIN_ATTEMPTS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxAttempts = n
		}
	}
	lockoutMinutes := 15
	if v := strings.TrimSpace(os.Getenv("AUTH_LOCKOUT_MINUTES")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			lockoutMinutes = n
		}
	}
	return LockoutConfig{
		MaxAttempts: maxAttempts,
		Lockout:     time.Duration(lockoutMinutes) * time.Minute,
	}
}

type lockoutEntry struct {
	failures   int
	lockedUntil time.Time
}

// LoginLockout tracks failed login attempts per email and client IP.
type LoginLockout struct {
	cfg  LockoutConfig
	mu   sync.Mutex
	keys map[string]*lockoutEntry
}

func NewLoginLockout(cfg LockoutConfig) *LoginLockout {
	return &LoginLockout{
		cfg:  cfg,
		keys: make(map[string]*lockoutEntry),
	}
}

// Config exposes the lockout settings for the public auth config endpoint.
func (l *LoginLockout) Config() LockoutConfig {
	return l.cfg
}

type LockoutStatus struct {
	Locked          bool      `json:"locked"`
	Remaining       int       `json:"remainingAttempts"`
	RetryAfterSec   int       `json:"retryAfterSec,omitempty"`
	LockedUntil     time.Time `json:"lockedUntil,omitempty"`
	MaxAttempts     int       `json:"maxAttempts"`
}

func lockoutKey(email, ip string) string {
	return strings.ToLower(strings.TrimSpace(email)) + "|" + strings.TrimSpace(ip)
}

// ClientIP resolves the caller IP, honoring reverse-proxy headers when present.
func ClientIP(r *http.Request) string {
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		if len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
	}
	if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
		return realIP
	}
	host := r.RemoteAddr
	if idx := strings.LastIndex(host, ":"); idx > 0 {
		return host[:idx]
	}
	return host
}

func (l *LoginLockout) Status(email, ip string) LockoutStatus {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	entry := l.keys[lockoutKey(email, ip)]
	status := LockoutStatus{
		MaxAttempts: l.cfg.MaxAttempts,
		Remaining:   l.cfg.MaxAttempts,
	}
	if entry == nil {
		return status
	}
	if entry.lockedUntil.After(now) {
		status.Locked = true
		status.Remaining = 0
		status.LockedUntil = entry.lockedUntil
		status.RetryAfterSec = int(entry.lockedUntil.Sub(now).Seconds()) + 1
		return status
	}
	if entry.failures >= l.cfg.MaxAttempts {
		entry.failures = 0
		entry.lockedUntil = time.Time{}
	}
	remaining := l.cfg.MaxAttempts - entry.failures
	if remaining < 0 {
		remaining = 0
	}
	status.Remaining = remaining
	return status
}

func (l *LoginLockout) RecordFailure(email, ip string) LockoutStatus {
	now := time.Now()
	key := lockoutKey(email, ip)

	l.mu.Lock()
	defer l.mu.Unlock()

	entry := l.keys[key]
	if entry == nil {
		entry = &lockoutEntry{}
		l.keys[key] = entry
	}
	if entry.lockedUntil.After(now) {
		return l.lockedStatus(entry)
	}
	if entry.lockedUntil.Before(now) && !entry.lockedUntil.IsZero() {
		entry.failures = 0
		entry.lockedUntil = time.Time{}
	}

	entry.failures++
	if entry.failures >= l.cfg.MaxAttempts {
		entry.lockedUntil = now.Add(l.cfg.Lockout)
		return l.lockedStatus(entry)
	}

	remaining := l.cfg.MaxAttempts - entry.failures
	if remaining < 0 {
		remaining = 0
	}
	return LockoutStatus{
		MaxAttempts: l.cfg.MaxAttempts,
		Remaining:   remaining,
	}
}

func (l *LoginLockout) RecordSuccess(email, ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.keys, lockoutKey(email, ip))
}

func (l *LoginLockout) lockedStatus(entry *lockoutEntry) LockoutStatus {
	now := time.Now()
	retryAfter := 0
	if entry.lockedUntil.After(now) {
		retryAfter = int(entry.lockedUntil.Sub(now).Seconds()) + 1
	}
	return LockoutStatus{
		Locked:        true,
		Remaining:     0,
		RetryAfterSec: retryAfter,
		LockedUntil:   entry.lockedUntil,
		MaxAttempts:   l.cfg.MaxAttempts,
	}
}
