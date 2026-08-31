package auth

import (
	"net"
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
	lastSeen    time.Time
}

// LoginLockout tracks failed login attempts per email and client IP.
// Stale entries are pruned periodically so the key set cannot grow unbounded.
type LoginLockout struct {
	cfg  LockoutConfig
	mu   sync.Mutex
	keys map[string]*lockoutEntry

	// Pruning knobs (defaults are set by NewLoginLockout).
	reapInterval time.Duration
	entryTTL     time.Duration
	maxKeys      int
	lastReap     time.Time
}

func NewLoginLockout(cfg LockoutConfig) *LoginLockout {
	ttl := 2 * cfg.Lockout
	if ttl < time.Minute {
		ttl = time.Minute
	}
	return &LoginLockout{
		cfg:          cfg,
		keys:         make(map[string]*lockoutEntry),
		reapInterval: 30 * time.Second,
		entryTTL:     ttl,
		maxKeys:      100_000,
		lastReap:     time.Now(),
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

// ProxyTrust decides whether forwarded-for headers may be honored, based on
// the direct peer address of the request.
type ProxyTrust struct {
	parsed bool
	nets   []*net.IPNet
	ips    map[string]bool
}

// ParseProxyTrust builds a trust set from a comma-separated list of IPs and
// CIDR ranges ("10.0.0.2,172.17.0.0/16,::1").
func ParseProxyTrust(raw string) ProxyTrust {
	var t ProxyTrust
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		t.parsed = true
		if strings.Contains(item, "/") {
			if _, ipnet, err := net.ParseCIDR(item); err == nil {
				t.nets = append(t.nets, ipnet)
			}
			continue
		}
		if ip := net.ParseIP(item); ip != nil {
			if t.ips == nil {
				t.ips = make(map[string]bool)
			}
			t.ips[ip.String()] = true
		}
	}
	return t
}

// LoadProxyTrustFromEnv reads TRUSTED_PROXIES. Leave it empty to ignore
// X-Forwarded-For/X-Real-IP entirely (recommended unless the app runs
// behind a reverse proxy you control).
func LoadProxyTrustFromEnv() ProxyTrust {
	return ParseProxyTrust(os.Getenv("TRUSTED_PROXIES"))
}

// IsZero reports whether no trusted proxy is configured.
func (t ProxyTrust) IsZero() bool {
	return !t.parsed
}

// trustsIP reports whether ip belongs to the trusted set.
func (t ProxyTrust) trustsIP(ip net.IP) bool {
	if t.ips[ip.String()] {
		return true
	}
	for _, n := range t.nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// remoteHost extracts the host portion of r.RemoteAddr (IPv6-safe).
func remoteHost(remoteAddr string) string {
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return host
	}
	return strings.Trim(remoteAddr, "[]")
}

// ClientIP resolves the caller IP. X-Forwarded-For/X-Real-IP headers are
// honored only when the direct peer (RemoteAddr) is a trusted reverse proxy;
// otherwise the header values could be spoofed by any client.
func ClientIP(r *http.Request, trust ProxyTrust) string {
	remote := remoteHost(r.RemoteAddr)
	remoteIP := net.ParseIP(remote)
	if remoteIP == nil || trust.IsZero() || !trust.trustsIP(remoteIP) {
		return remote
	}

	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		// Walk from the right (closest to the trusted proxy) and return the
		// first address that is not itself a trusted proxy.
		for i := len(parts) - 1; i >= 0; i-- {
			candidate := strings.TrimSpace(parts[i])
			if candidate == "" {
				continue
			}
			ip := net.ParseIP(candidate)
			if ip == nil {
				continue
			}
			if trust.trustsIP(ip) {
				continue
			}
			return ip.String()
		}
		return remote
	}

	if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
		if ip := net.ParseIP(realIP); ip != nil && !trust.trustsIP(ip) {
			return ip.String()
		}
	}
	return remote
}

// pruneLocked removes entries whose lockout expired and which went stale
// before l.entryTTL, bounding memory usage. It must be called with l.mu held.
func (l *LoginLockout) pruneLocked(now time.Time) {
	for key, entry := range l.keys {
		if entry.lockedUntil.After(now) {
			continue
		}
		if now.Sub(entry.lastSeen) > l.entryTTL {
			delete(l.keys, key)
		}
	}
	l.lastReap = now
}

// reap prunes stale entries at most once per reapInterval.
func (l *LoginLockout) reap(now time.Time) {
	if now.Sub(l.lastReap) < l.reapInterval {
		return
	}
	l.pruneLocked(now)
}

func (l *LoginLockout) Status(email, ip string) LockoutStatus {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	l.reap(now)

	entry := l.keys[lockoutKey(email, ip)]
	status := LockoutStatus{
		MaxAttempts: l.cfg.MaxAttempts,
		Remaining:   l.cfg.MaxAttempts,
	}
	if entry == nil {
		return status
	}
	entry.lastSeen = now
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

	l.reap(now)

	entry := l.keys[key]
	if entry == nil {
		if len(l.keys) >= l.maxKeys {
			// Hard cap: drop the stalest entry to bound memory under abuse
			// of many distinct email/IP pairs.
			var oldestKey string
			var oldest time.Time
			first := true
			for k, e := range l.keys {
				if first || e.lastSeen.Before(oldest) {
					oldestKey, oldest, first = k, e.lastSeen, false
				}
			}
			delete(l.keys, oldestKey)
		}
		entry = &lockoutEntry{lastSeen: now}
		l.keys[key] = entry
	}
	entry.lastSeen = now
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
