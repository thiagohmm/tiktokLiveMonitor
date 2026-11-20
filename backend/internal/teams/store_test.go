package teams_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/thiagohmm/tiktok-live-monitor/internal/auth"
	"github.com/thiagohmm/tiktok-live-monitor/internal/database"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"github.com/thiagohmm/tiktok-live-monitor/internal/teams"
)

func fixture(t *testing.T) (*database.DB, *teams.Store, string) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	dsn, cleanup, err := database.CreateTestDatabase(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	db, err := database.OpenPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	org, err := db.CreateOrganization("Org A", 3)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	identity := auth.NewStore(db.SQLDB())
	if _, err = identity.ImportUsers(ctx, []auth.ImportAccount{{SubscriberProfile: auth.SubscriberProfile{ID: "owner", Email: "owner@example.com", Role: "subscriber", Active: true}}}, true); err != nil {
		t.Fatal(err)
	}
	if _, err = db.UpsertOrgMember(org.ID, "owner", "owner@example.com", model.OrgRoleOwner); err != nil {
		t.Fatal(err)
	}
	s := teams.New(db.SQLDB())
	if err = s.Allocate(ctx, org.ID, "admin", teams.Allocation{PrimaryOwner: "owner"}, true); err != nil {
		t.Fatal(err)
	}
	return db, s, org.ID
}
func accept(t *testing.T, s *teams.Store, org, email string) (teams.Invitation, string, string) {
	t.Helper()
	i, token, err := s.CreateInvitation(context.Background(), org, "owner", email)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.AcceptInvitation(context.Background(), token, "invited password 12", "Member", "")
	if err != nil {
		t.Fatal(err)
	}
	return i, token, id
}

func TestInvitationRejectsInvalidEmail(t *testing.T) {
	_, s, org := fixture(t)
	for _, email := range []string{"a@", "@b.com", "Name <x@example.com>", "not-an-email", ""} {
		if _, _, err := s.CreateInvitation(context.Background(), org, "owner", email); err == nil {
			t.Fatalf("accepted %q", email)
		}
	}
	if _, _, err := s.CreateInvitation(context.Background(), org, "owner", " Member@Example.com "); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentInvitationsReserveDistinctSeats(t *testing.T) {
	_, s, org := fixture(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	var mu sync.Mutex
	seats := map[int]bool{}
	for n := 0; n < 10; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			i, _, err := s.CreateInvitation(ctx, org, "owner", string(rune('a'+n))+"@example.com")
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				t.Errorf("unexpected: %v", err)
				return
			}
			if i.Seat < 1 || seats[i.Seat] {
				t.Errorf("seat %d reused", i.Seat)
			}
			seats[i.Seat] = true
		}(n)
	}
	wg.Wait()
	if len(seats) != 10 {
		t.Fatalf("reserved %d distinct seats, want 10", len(seats))
	}
	summary, err := s.Summary(ctx, org)
	if err != nil || summary.Pending != 10 || len(summary.Seats) != 10 {
		t.Fatalf("summary: %+v %v", summary, err)
	}
}
func TestInvitationResendCancellationAndOneUse(t *testing.T) {
	_, s, org := fixture(t)
	ctx := context.Background()
	i, old, err := s.CreateInvitation(ctx, org, "owner", "member@example.com")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := s.ChangeInvitation(ctx, org, "owner", i.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcceptInvitation(ctx, old, "new password 12", "", ""); err == nil {
		t.Fatal("previous link survived resend")
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := 0
	for n := 0; n < 2; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.AcceptInvitation(ctx, token, "new password 12", "", "")
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				accepted++
			}
		}()
	}
	wg.Wait()
	if accepted != 1 {
		t.Fatalf("accepted %d times", accepted)
	}
	i, token, err = s.CreateInvitation(ctx, org, "owner", "second@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.ChangeInvitation(ctx, org, "owner", i.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcceptInvitation(ctx, token, "new password 12", "", ""); err == nil {
		t.Fatal("cancelled invitation accepted")
	}
}
func TestSeatAllocationExpiryAndRevocation(t *testing.T) {
	db, s, org := fixture(t)
	ctx := context.Background()
	accept(t, s, org, "first@example.com")
	accept(t, s, org, "second@example.com")
	// Without any paid period, the third member takes the next available seat.
	_, token, extra := accept(t, s, org, "extra@example.com")
	if err := s.SetRole(ctx, org, "owner", extra, "owner"); err != nil {
		t.Fatal(err)
	}
	summary, err := s.Summary(ctx, org)
	if err != nil || len(summary.Seats) != 3 {
		t.Fatalf("summary: %+v %v", summary, err)
	}
	freed := -1
	for _, seat := range summary.Seats {
		if seat.UserID == extra {
			freed = seat.Number
		}
	}
	if freed != 3 {
		t.Fatalf("extra seat number = %d, want 3", freed)
	}
	if err := s.Revoke(ctx, org, "owner", extra); err != nil {
		t.Fatal(err)
	}
	// The freed seat is offered again to the next invitation.
	again, againToken, err := s.CreateInvitation(ctx, org, "owner", "again@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if again.Seat != freed {
		t.Fatalf("invitation reused seat %d, want freed seat %d", again.Seat, freed)
	}
	if _, err := s.AcceptInvitation(ctx, againToken, "new password 12", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcceptInvitation(ctx, token, "password again 12", "", ""); err == nil {
		t.Fatal("consumed token restored revoked member")
	}
	if _, err := auth.NewStore(db.SQLDB()).GetProfileByID(extra); err != nil {
		t.Fatal("identity deleted")
	}
}
func TestCrossOrganizationAndPrimaryProtection(t *testing.T) {
	db, s, org := fixture(t)
	ctx := context.Background()
	other, err := db.CreateOrganization("Org B", 3)
	if err != nil {
		t.Fatal(err)
	}
	i, token, member := accept(t, s, org, "member@example.com")
	if _, _, err = s.ChangeInvitation(ctx, other.ID, "other-owner", i.ID, false); !errors.Is(err, teams.ErrNotFound) {
		t.Fatalf("cross-org invitation: %v", err)
	}
	if err = s.Revoke(ctx, other.ID, "other-owner", member); !errors.Is(err, teams.ErrNotFound) {
		t.Fatalf("cross-org revoke: %v", err)
	}
	if err = s.Revoke(ctx, org, "admin", "owner"); err == nil {
		t.Fatal("primary owner removed")
	}
	if err = s.SetRole(ctx, org, "admin", "owner", "operator"); err == nil {
		t.Fatal("primary owner demoted")
	}
	if _, err = s.AcceptInvitation(ctx, token, "another password 12", "", ""); err == nil {
		t.Fatal("consumed invitation accepted")
	}
}

