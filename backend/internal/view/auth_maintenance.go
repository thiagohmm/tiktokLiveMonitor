package view

import (
	"context"
	"log"
	"time"
)

// maintainIdentity cleans retired tokens; authorization never depends on this worker.
func (s *HTTPServer) maintainIdentity(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			operation, cancel := context.WithTimeout(ctx, 15*time.Second)
			for _, statement := range []string{
				`DELETE FROM auth_sessions WHERE expires_at<now()-interval '30 days' OR revoked_at<now()-interval '30 days'`,
				`DELETE FROM auth_action_tokens WHERE expires_at<now()-interval '30 days'`,
				`DELETE FROM auth_rate_limits WHERE updated_at<now()-interval '1 day'`,
				`UPDATE organization_invitations SET status='expired' WHERE status='pending' AND (expires_at<=now() OR (seat_number>2 AND NOT EXISTS(SELECT 1 FROM organization_seat_subscriptions p WHERE p.org_id=organization_invitations.org_id AND p.starts_at<=now() AND p.expires_at>now() AND p.quantity>=seat_number-2)))`,
			} {
				if _, err := s.auth.Store.DB.ExecContext(operation, statement); err != nil {
					log.Printf("[auth] token maintenance: %v", err)
				}
			}
			cancel()
		}
	}
}
