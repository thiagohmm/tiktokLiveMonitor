package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Store owns local identities and persistent, revocable sessions.
type Store struct{ DB *sql.DB }

// SessionLifetime is the absolute lifetime of a local session.
const SessionLifetime = 7 * 24 * time.Hour

var hashSlots = make(chan struct{}, 2)

// NewStore uses the application's PostgreSQL connection pool.
func NewStore(db *sql.DB) *Store { return &Store{DB: db} }

// RandomToken creates an unpredictable URL-safe token.
func RandomToken() string { return base64.RawURLEncoding.EncodeToString(randomBytes(32)) }
func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("operating system randomness unavailable")
	}
	return b
}

// TokenHash is the only representation of bearer secrets kept in the database.
func TokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// ValidatePassword preserves the exact password, including whitespace.
func ValidatePassword(password string) error {
	n := utf8.RuneCountInString(password)
	if !utf8.ValidString(password) || n < 12 || n > 128 {
		return errors.New("senha deve ter entre 12 e 128 caracteres")
	}
	return nil
}

// HashPassword encodes parameters and salt alongside an Argon2id hash.
func HashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	hashSlots <- struct{}{}
	defer func() { <-hashSlots }()
	salt := randomBytes(16)
	h := argon2.IDKey([]byte(password), salt, 3, 64*1024, 4, 32)
	return fmt.Sprintf("$argon2id$v=19$m=65536,t=3,p=4$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(h)), nil
}

// VerifyPassword rejects unsupported or malformed hashes without allocating arbitrary memory.
func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" || parts[3] != "m=65536,t=3,p=4" || len(password) > 512 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) != 16 {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) != 32 {
		return false
	}
	hashSlots <- struct{}{}
	defer func() { <-hashSlots }()
	got := argon2.IDKey([]byte(password), salt, 3, 64*1024, 4, 32)
	return subtle.ConstantTimeCompare(got, want) == 1
}

// NormalizeEmail lowercases, trims and rejects display-name or malformed addresses.
func NormalizeEmail(email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	a, err := mail.ParseAddress(email)
	if err != nil || a.Address != email || len(email) > 254 {
		return "", errors.New("e-mail inválido")
	}
	return email, nil
}

func normalizedEmail(email string) (string, error) { return NormalizeEmail(email) }

// Context bounds database work initiated by the authentication interfaces.
func dbContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 15*time.Second)
}

// ValidateToken resolves a live session and the current account state.
func (s *Store) ValidateToken(token string) (*User, error) {
	if token == "" || s == nil {
		return nil, ErrInvalidCredentials
	}
	ctx, cancel := dbContext()
	defer cancel()
	var u User
	err := s.DB.QueryRowContext(ctx, `SELECT u.id,u.email,u.role,u.active FROM users u JOIN auth_sessions a ON a.user_id=u.id WHERE a.token_hash=$1 AND a.revoked_at IS NULL AND a.expires_at>now() AND (u.role='admin' OR u.subscription_expires_at IS NULL OR u.subscription_expires_at>now())`, TokenHash(token)).Scan(&u.ID, &u.Email, &u.Role, &u.Active)
	if err != nil {
		return nil, ErrInvalidCredentials
	}
	return &u, nil
}

// SignInWithPassword creates a seven-day server-side session.
func (s *Store) SignInWithPassword(email, password string) (*LoginSession, error) {
	ctx, cancel := dbContext()
	defer cancel()
	var id, hash, role string
	var active bool
	var expires sql.NullTime
	err := s.DB.QueryRowContext(ctx, `SELECT id,COALESCE(password_hash,''),active,role,subscription_expires_at FROM users WHERE email=lower(trim($1))`, email).Scan(&id, &hash, &active, &role, &expires)
	if err != nil {
		VerifyPassword("$argon2id$v=19$m=65536,t=3,p=4$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", password)
		return nil, ErrInvalidCredentials
	}
	if hash == "" {
		hash = "$argon2id$v=19$m=65536,t=3,p=4$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	}
	if !VerifyPassword(hash, password) {
		return nil, ErrInvalidCredentials
	}
	// Platform admins are not gated by customer subscription dates (ValidateToken already exempts them).
	expired := expires.Valid && time.Now().After(expires.Time) && role != "admin"
	if !active || expired {
		return nil, ErrInvalidCredentials
	}
	token := RandomToken()
	csrf := TokenHash("csrf:" + token)
	_, err = s.DB.ExecContext(ctx, `INSERT INTO auth_sessions(token_hash,user_id,csrf_hash,expires_at) VALUES($1,$2,$3,$4)`, TokenHash(token), id, TokenHash(csrf), time.Now().Add(SessionLifetime))
	if err != nil {
		return nil, ErrAuthUnavailable
	}
	return &LoginSession{AccessToken: token, CSRFToken: csrf, ExpiresIn: int(SessionLifetime.Seconds())}, nil
}

// CSRF checks a secret tied to the live session.
func (s *Store) CSRF(token, csrf string) bool {
	if csrf == "" || s == nil {
		return false
	}
	ctx, cancel := dbContext()
	defer cancel()
	var ok bool
	err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM auth_sessions WHERE token_hash=$1 AND csrf_hash=$2 AND revoked_at IS NULL AND expires_at>now())`, TokenHash(token), TokenHash(csrf)).Scan(&ok)
	return err == nil && ok
}

// RotateCSRF exposes a new CSRF secret without exposing the session cookie.
func (s *Store) RotateCSRF(token string) (string, error) {
	secret := TokenHash("csrf:" + token)
	if !s.CSRF(token, secret) {
		return "", ErrInvalidCredentials
	}
	return secret, nil
}

// ErrSessionNotFound indicates the presented token does not match any active
// (non-revoked, non-expired) session, so nothing was revoked.
var ErrSessionNotFound = errors.New("sessão não encontrada ou já revogada")

// SignOutGlobal revokes all sessions belonging to the authenticated identity.
// It reports ErrSessionNotFound when the token matches no active session
// instead of silently claiming success.
func (s *Store) SignOutGlobal(token string) error {
	ctx, cancel := dbContext()
	defer cancel()
	res, err := s.DB.ExecContext(ctx, `UPDATE auth_sessions SET revoked_at=now() WHERE revoked_at IS NULL AND expires_at>now() AND user_id=(SELECT user_id FROM auth_sessions WHERE token_hash=$1)`, TokenHash(token))
	if err != nil {
		return err
	}
	if n, rowsErr := res.RowsAffected(); rowsErr == nil && n == 0 {
		return ErrSessionNotFound
	}
	return nil
}

// UpdatePassword consumes one recovery/activation token and revokes old sessions atomically.
func (s *Store) UpdatePassword(token, password string) error {
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	ctx, cancel := dbContext()
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	var userID string
	err = tx.QueryRowContext(ctx, `UPDATE auth_action_tokens SET consumed_at=now() WHERE token_hash=$1 AND purpose IN ('recovery','activation') AND consumed_at IS NULL AND expires_at>now() RETURNING user_id`, TokenHash(token)).Scan(&userID)
	if err != nil {
		return ErrInvalidResetLink
	}
	if _, err = tx.ExecContext(ctx, `UPDATE users SET password_hash=$2,updated_at=now() WHERE id=$1`, userID, hash); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE auth_sessions SET revoked_at=now() WHERE user_id=$1`, userID); err != nil {
		return err
	}
	return tx.Commit()
}
