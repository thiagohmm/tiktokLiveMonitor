package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ImportAccount is the password-free migration format. IDs must remain unchanged.
type ImportAccount struct{ SubscriberProfile }
type ImportReport struct {
	Imported int  `json:"imported"`
	Existing int  `json:"existing"`
	DryRun   bool `json:"dryRun"`
}

// ImportUsers validates every identity and dangling membership inside one transaction.
func (s *Store) ImportUsers(ctx context.Context, accounts []ImportAccount, apply bool) (ImportReport, error) {
	report := ImportReport{DryRun: !apply}
	if len(accounts) == 0 {
		return report, errors.New("exportação vazia")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return report, err
	}
	defer rollback(tx)
	ids, emails := map[string]bool{}, map[string]bool{}
	for _, account := range accounts {
		email, err := normalizedEmail(account.Email)
		if err != nil {
			return report, err
		}
		if account.ID == "" || ids[account.ID] || emails[email] {
			return report, errors.New("ID ou e-mail ausente/duplicado")
		}
		ids[account.ID] = true
		emails[email] = true
		if account.Role != "admin" && account.Role != "subscriber" {
			return report, fmt.Errorf("papel inválido para %s", account.ID)
		}
		var existingID, existingEmail string
		err = tx.QueryRowContext(ctx, `SELECT id,email FROM users WHERE id=$1 OR email=$2 FOR UPDATE`, account.ID, email).Scan(&existingID, &existingEmail)
		if err == nil {
			if existingID != account.ID || existingEmail != email {
				return report, fmt.Errorf("conflito de identidade para %s", account.ID)
			}
			report.Existing++
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return report, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO users(id,email,display_name,role,active,notes,subscription_expires_at,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,COALESCE($8,now()),COALESCE($9,now()))`, account.ID, email, account.DisplayName, account.Role, account.Active, account.Notes, account.SubscriptionExpiresAt, nullableTime(account.CreatedAt), nullableTime(account.UpdatedAt))
		if err != nil {
			return report, err
		}
		report.Imported++
	}
	var dangling int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM organization_members m LEFT JOIN users u ON u.id=m.user_id WHERE u.id IS NULL`).Scan(&dangling); err != nil {
		return report, err
	}
	if dangling > 0 {
		return report, fmt.Errorf("%d vínculos sem identidade: complete a exportação", dangling)
	}
	if apply {
		return report, tx.Commit()
	}
	return report, nil
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
