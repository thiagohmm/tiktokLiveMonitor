package auth

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"
)

// ActivationRedirectIfLegacyPassword verifies the pre-migration password at the
// legacy identity provider and, when valid, returns a one-time activation URL.
// Used only while password_hash is still empty after the local-auth cutover.
func (s *Store) ActivationRedirectIfLegacyPassword(email, password, siteURL string) (string, bool) {
	email = strings.ToLower(strings.TrimSpace(email))
	siteURL = strings.TrimRight(strings.TrimSpace(siteURL), "/")
	if s == nil || email == "" || password == "" || siteURL == "" {
		return "", false
	}
	ctx, cancel := dbContext()
	defer cancel()
	var id string
	var hash sql.NullString
	var active bool
	err := s.DB.QueryRowContext(ctx, `SELECT id, password_hash, active FROM users WHERE email=$1`, email).Scan(&id, &hash, &active)
	if err != nil || !active || (hash.Valid && hash.String != "") {
		return "", false
	}
	if !verifyLegacyPassword(ctx, email, password) {
		return "", false
	}
	link, err := s.GenerateActionLink(id, "activation", siteURL+"/reset-password.html", 7*24*time.Hour)
	if err != nil {
		return "", false
	}
	return link, true
}

func verifyLegacyPassword(ctx context.Context, email, password string) bool {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("SUPABASE_URL")), "/")
	key := strings.TrimSpace(os.Getenv("SUPABASE_ANON_KEY"))
	if !strings.HasPrefix(base, "https://") || key == "" {
		return false
	}
	body, err := json.Marshal(map[string]string{"email": email, "password": password})
	if err != nil {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/auth/v1/token?grant_type=password", bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", key)
	req.Header.Set("Authorization", "Bearer "+key)
	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
