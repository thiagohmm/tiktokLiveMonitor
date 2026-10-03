package auth_test

import (
	"context"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/auth"
	"github.com/thiagohmm/tiktok-live-monitor/internal/database"
)

func localStore(t *testing.T) *auth.Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	testDSN, cleanup, err := database.CreateTestDatabase(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	db, err := database.OpenPostgres(testDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return auth.NewStore(db.SQLDB())
}
func TestPasswordHashPreservesWhitespace(t *testing.T) {
	password := "  correct horse battery  "
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, password string
		valid          bool
	}{{"exact", password, true}, {"trimmed", "correct horse battery", false}, {"wrong", "a different password", false}} {
		t.Run(tc.name, func(t *testing.T) {
			if auth.VerifyPassword(hash, tc.password) != tc.valid {
				t.Fatal("unexpected password verification")
			}
		})
	}
	if _, err := auth.HashPassword("short"); err == nil {
		t.Fatal("short password accepted")
	}
	if auth.VerifyPassword("$argon2id$v=19$m=999999999,t=3,p=4$invalid$invalid", password) {
		t.Fatal("unsupported hash accepted")
	}
}
func TestLocalSessionsAndOneUseRecovery(t *testing.T) {
	s := localStore(t)
	p, err := s.CreateSubscriber(auth.CreateSubscriberRequest{Email: " Person@example.com ", Password: "initial password 12"})
	if err != nil {
		t.Fatal(err)
	}
	active := true
	if _, err = s.UpdateSubscriber(auth.UpdateSubscriberRequest{ID: p.ID, Active: &active}); err != nil {
		t.Fatal(err)
	}
	login, err := s.SignInWithPassword(p.Email, "initial password 12")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ValidateToken(login.AccessToken); err != nil {
		t.Fatal(err)
	}
	if !s.CSRF(login.AccessToken, login.CSRFToken) || s.CSRF(login.AccessToken, "wrong") {
		t.Fatal("CSRF validation")
	}
	// Opening a second tab must not invalidate the first tab's CSRF token.
	csrf, err := s.RotateCSRF(login.AccessToken)
	if err != nil || csrf != login.CSRFToken {
		t.Fatalf("csrf tabs: %v", err)
	}
	link, err := s.GenerateRecoveryLink(p.Email, "https://example.com/reset-password.html")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(link)
	params, _ := url.ParseQuery(u.Fragment)
	token := params.Get("token")
	if err = s.UpdatePassword(token, "  replacement password  "); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdatePassword(token, "another password 12"); err == nil {
		t.Fatal("recovery token reused")
	}
	if _, err = s.ValidateToken(login.AccessToken); err == nil {
		t.Fatal("old session survived password reset")
	}
	login, err = s.SignInWithPassword(p.Email, "  replacement password  ")
	if err != nil {
		t.Fatal(err)
	}
	inactive := false
	if _, err = s.UpdateSubscriber(auth.UpdateSubscriberRequest{ID: p.ID, Active: &inactive}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ValidateToken(login.AccessToken); err == nil {
		t.Fatal("disabled identity retained session")
	}
}
func TestImportIsIdempotentAndPreservesIdentity(t *testing.T) {
	s := localStore(t)
	ctx := context.Background()
	accounts := []auth.ImportAccount{{SubscriberProfile: auth.SubscriberProfile{ID: "original-id", Email: "owner@example.com", Role: "admin", Active: true}}}
	r, err := s.ImportUsers(ctx, accounts, false)
	if err != nil || r.Imported != 1 {
		t.Fatalf("dry run: %+v %v", r, err)
	}
	if _, err = s.GetProfileByID("original-id"); err == nil {
		t.Fatal("dry run wrote user")
	}
	if _, err = s.ImportUsers(ctx, accounts, true); err != nil {
		t.Fatal(err)
	}
	r, err = s.ImportUsers(ctx, accounts, true)
	if err != nil || r.Existing != 1 {
		t.Fatalf("repeat: %+v %v", r, err)
	}
	accounts[0].Email = "different@example.com"
	if _, err = s.ImportUsers(ctx, accounts, true); err == nil {
		t.Fatal("identity conflict accepted")
	}
}
func TestAdminLoginIgnoresExpiredSubscription(t *testing.T) {
	s := localStore(t)
	p, err := s.CreateSubscriber(auth.CreateSubscriberRequest{Email: "admin@example.com", Password: "admin password 12", SubscriptionExpiresAt: time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`UPDATE users SET role='admin', active=true WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	login, err := s.SignInWithPassword("admin@example.com", "admin password 12")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ValidateToken(login.AccessToken); err != nil {
		t.Fatal(err)
	}
	sub, err := s.CreateSubscriber(auth.CreateSubscriberRequest{Email: "paid@example.com", Password: "subscriber password 12", SubscriptionExpiresAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	active := true
	if _, err = s.UpdateSubscriber(auth.UpdateSubscriberRequest{ID: sub.ID, Active: &active}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SignInWithPassword("paid@example.com", "subscriber password 12"); err == nil {
		t.Fatal("expired subscriber accepted")
	}
}

func TestNormalizeEmail(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		ok       bool
	}{
		{" Person@Example.com ", "person@example.com", true},
		{"a@", "", false},
		{"@b.com", "", false},
		{"Name <person@example.com>", "", false},
		{"not-an-email", "", false},
	} {
		got, err := auth.NormalizeEmail(tc.in)
		if tc.ok {
			if err != nil || got != tc.want {
				t.Fatalf("%q: got %q %v", tc.in, got, err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("%q accepted as %q", tc.in, got)
		}
	}
}

func TestRateLimitSurvivesRestart(t *testing.T) {
	s := localStore(t)
	cfg := auth.LockoutConfig{MaxAttempts: 2, Lockout: time.Minute}
	l := auth.NewLoginLockout(cfg)
	l.UseDatabase(s.DB)
	l.RecordFailure("person", "ip")
	if !l.RecordFailure("person", "ip").Locked {
		t.Fatal("not locked")
	}
	restarted := auth.NewLoginLockout(cfg)
	restarted.UseDatabase(s.DB)
	if !restarted.Status("person", "ip").Locked {
		t.Fatal("restart cleared lock")
	}
	restarted.RecordSuccess("person", "ip")
	if restarted.Status("person", "ip").Locked {
		t.Fatal("success did not clear lock")
	}
}
