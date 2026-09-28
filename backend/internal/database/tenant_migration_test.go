package database

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

// legacyTenantDB returns a migrated database turned back into the
// pre-multi-tenant shape: pix_* keyed by owner_user_id, no organizations
// besides the legacy one and no members. Reopening it runs the upgrade.
func legacyTenantDB(t *testing.T) (string, *DB) {
	t.Helper()
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if baseDSN == "" {
		t.Skip("TEST_DATABASE_URL não configurado: testes exigem PostgreSQL descartável")
	}
	dsn, cleanup, err := CreateTestDatabase(baseDSN)
	if err != nil {
		t.Fatalf("create test database: %v", err)
	}
	t.Cleanup(cleanup)
	db, err := OpenPostgres(dsn)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	for _, table := range legacyPixTables {
		if _, err := db.conn.Exec(`ALTER TABLE ` + table + ` RENAME COLUMN org_id TO owner_user_id`); err != nil {
			t.Fatalf("legacy shape %s: %v", table, err)
		}
	}
	if _, err := db.conn.Exec(`DELETE FROM organizations WHERE id <> $1`, model.DefaultOrgID); err != nil {
		t.Fatalf("reset organizations: %v", err)
	}
	return dsn, db
}

func TestLegacyPixOwnersGetSeparateOrganizations(t *testing.T) {
	dsn, legacy := legacyTenantDB(t)
	const userA, userB = "11111111-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "22222222-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	now := time.Now().UTC()
	for i, user := range []string{userA, userB} {
		jid := "5511900000000@c.us"
		var contactID, ticketID int64
		if err := legacy.conn.QueryRow(`INSERT INTO pix_contacts (owner_user_id, phone_e164, whatsapp_jid, first_contact_at, last_contact_at)
			VALUES ($1, '+5511900000000', $2, $3, $3) RETURNING id`, user, jid, now).Scan(&contactID); err != nil {
			t.Fatalf("seed contact: %v", err)
		}
		if err := legacy.conn.QueryRow(`INSERT INTO pix_tickets (owner_user_id, contact_id, received_at, last_message_at)
			VALUES ($1, $2, $3, $3) RETURNING id`, user, contactID, now).Scan(&ticketID); err != nil {
			t.Fatalf("seed ticket: %v", err)
		}
		if _, err := legacy.conn.Exec(`INSERT INTO pix_messages (owner_user_id, ticket_id, contact_id, whatsapp_message_id, direction, type, body)
			VALUES ($1, $2, $3, 'msg-1', 'inbound', 'text', $4)`, user, ticketID, contactID, "segredo de "+user); err != nil {
			t.Fatalf("seed message: %v", err)
		}
		if _, err := legacy.conn.Exec(`INSERT INTO pix_whatsapp_sessions (owner_user_id, session_name) VALUES ($1, $2)`,
			user, "pix_"+user); err != nil {
			t.Fatalf("seed session: %v", err)
		}
		if _, err := legacy.conn.Exec(`INSERT INTO pix_value_rules (owner_user_id, value_cents) VALUES ($1, $2)`,
			user, 1000*(i+1)); err != nil {
			t.Fatalf("seed value rule: %v", err)
		}
	}
	// Session and event rows without an identifiable owner.
	if _, err := legacy.conn.Exec(`INSERT INTO live_sessions (id, live_name, day, started_at, last_seen_at, org_id)
		VALUES ('legacy-session', 'streamer', CURRENT_DATE, $1, $1, '')`, now); err != nil {
		t.Fatalf("seed legacy live session: %v", err)
	}
	legacy.Close()

	// Two boots: the upgrade must be idempotent.
	for boot := 0; boot < 2; boot++ {
		db, err := OpenPostgres(dsn)
		if err != nil {
			t.Fatalf("boot %d: %v", boot, err)
		}
		db.Close()
	}
	db, err := OpenPostgres(dsn)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	memberA, err := db.GetMembership(userA)
	if err != nil {
		t.Fatalf("membership A: %v", err)
	}
	memberB, err := db.GetMembership(userB)
	if err != nil {
		t.Fatalf("membership B: %v", err)
	}
	if memberA.OrgID == memberB.OrgID {
		t.Fatalf("users share organization %s", memberA.OrgID)
	}
	for _, m := range []model.OrgMember{memberA, memberB} {
		if m.OrgID == model.DefaultOrgID {
			t.Fatalf("user %s landed in the legacy organization", m.UserID)
		}
		if m.Role != model.OrgRoleOwner {
			t.Fatalf("user %s role = %q, want owner", m.UserID, m.Role)
		}
	}
	orgs, err := db.ListOrganizations()
	if err != nil {
		t.Fatalf("list organizations: %v", err)
	}
	if len(orgs) != 3 {
		t.Fatalf("organizations = %d, want legacy + 2 (idempotent)", len(orgs))
	}

	for _, tc := range []struct {
		member model.OrgMember
		other  string
		cents  int64
	}{{memberA, userB, 1000}, {memberB, userA, 2000}} {
		tickets, err := db.ListPixTickets(tc.member.OrgID, "", 50)
		if err != nil {
			t.Fatalf("list tickets: %v", err)
		}
		if len(tickets) != 1 {
			t.Fatalf("org of %s sees %d tickets, want 1", tc.member.UserID, len(tickets))
		}
		messages, err := db.ListPixMessages(tc.member.OrgID, tickets[0].ID, 50)
		if err != nil {
			t.Fatalf("list messages: %v", err)
		}
		if len(messages) != 1 || strings.Contains(messages[0].Body, tc.other) {
			t.Fatalf("org of %s sees messages %+v", tc.member.UserID, messages)
		}
		values, err := db.ListPixValueRules(tc.member.OrgID)
		if err != nil {
			t.Fatalf("list values: %v", err)
		}
		if len(values) != 1 || values[0] != tc.cents {
			t.Fatalf("org of %s values = %v, want [%d]", tc.member.UserID, values, tc.cents)
		}
		session, err := db.GetPixSessionByOrg(tc.member.OrgID)
		if err != nil || session.SessionName != "pix_"+tc.member.UserID {
			t.Fatalf("org of %s session = %+v, %v", tc.member.UserID, session, err)
		}
	}

	// Ownerless data stays in the legacy organization, which has no members.
	legacyLives, err := db.ListLives(model.DefaultOrgID, 10)
	if err != nil || len(legacyLives) != 1 {
		t.Fatalf("legacy lives = %+v, %v", legacyLives, err)
	}
	for _, m := range []model.OrgMember{memberA, memberB} {
		lives, err := db.ListLives(m.OrgID, 10)
		if err != nil || len(lives) != 0 {
			t.Fatalf("org of %s sees legacy lives %+v, %v", m.UserID, lives, err)
		}
	}
	members, err := db.ListOrgMembers(model.DefaultOrgID)
	if err != nil || len(members) != 0 {
		t.Fatalf("legacy organization members = %+v, %v", members, err)
	}
	if _, err := db.UpsertOrgMember(model.DefaultOrgID, userA, "", model.OrgRoleOperator); err == nil {
		t.Fatal("legacy organization accepted a member")
	}
}
