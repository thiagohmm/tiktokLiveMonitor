package auth

import (
	"database/sql"
	"time"
)

// UseDatabase persists rate limits across application restarts.
func (l *LoginLockout) UseDatabase(db *sql.DB) { l.db = db }

func (l *LoginLockout) persistentStatus(email, ip string, increment bool) LockoutStatus {
	ctx, cancel := dbContext()
	defer cancel()
	key := TokenHash(lockoutKey(email, ip))
	var failures int
	var until time.Time
	var err error
	if increment {
		err = l.db.QueryRowContext(ctx, `INSERT INTO auth_rate_limits(key,failures,locked_until) VALUES($1,1,CASE WHEN $2<=1 THEN now()+$3::interval ELSE now() END)
		 ON CONFLICT(key) DO UPDATE SET
		 failures=CASE WHEN auth_rate_limits.locked_until>now() THEN auth_rate_limits.failures WHEN auth_rate_limits.failures>=$2 OR auth_rate_limits.updated_at<now()-$3::interval THEN 1 ELSE auth_rate_limits.failures+1 END,
		 locked_until=CASE WHEN auth_rate_limits.locked_until>now() THEN auth_rate_limits.locked_until WHEN (CASE WHEN auth_rate_limits.failures>=$2 OR auth_rate_limits.updated_at<now()-$3::interval THEN 1 ELSE auth_rate_limits.failures+1 END)>=$2 THEN now()+$3::interval ELSE now() END,
		 updated_at=now() RETURNING failures,locked_until`, key, l.cfg.MaxAttempts, l.cfg.Lockout.String()).Scan(&failures, &until)
	} else {
		err = l.db.QueryRowContext(ctx, `SELECT failures,locked_until FROM auth_rate_limits WHERE key=$1 AND updated_at>now()-$2::interval`, key, l.cfg.Lockout.String()).Scan(&failures, &until)
	}
	if err != nil && err != sql.ErrNoRows {
		return LockoutStatus{Locked: true, Remaining: 0, RetryAfterSec: 60, MaxAttempts: l.cfg.MaxAttempts}
	}
	if until.After(time.Now()) {
		return l.lockedStatus(&lockoutEntry{lockedUntil: until})
	}
	if failures >= l.cfg.MaxAttempts {
		failures = 0
	}
	return LockoutStatus{Remaining: l.cfg.MaxAttempts - failures, MaxAttempts: l.cfg.MaxAttempts}
}
