package database

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

// pixTimestamp renders a timestamp in the same RFC3339Nano UTC form used by the
// other operational tables, so the frontend sees one format everywhere.
func pixTimestamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func pixNullableTimestamp(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

// --- Sessões WAHA ---

// UpsertPixSession creates or updates the owner's WAHA session row.
func (db *DB) UpsertPixSession(orgID, sessionName, status, mePhone, meJID string, connectedAt *time.Time) (model.PixWhatsAppSession, error) {
	orgID = strings.TrimSpace(orgID)
	sessionName = strings.TrimSpace(sessionName)
	if orgID == "" || sessionName == "" {
		return model.PixWhatsAppSession{}, fmt.Errorf("owner and session name are required")
	}
	status = normalizePixSessionStatus(status)

	var connected any
	if connectedAt != nil && !connectedAt.IsZero() {
		connected = connectedAt.UTC().Format(time.RFC3339Nano)
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	if _, err := db.exec(
		`INSERT INTO pix_whatsapp_sessions
			(org_id, session_name, status, me_phone, me_jid, connected_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT (org_id) DO UPDATE SET
			status = EXCLUDED.status,
			me_phone = EXCLUDED.me_phone,
			me_jid = EXCLUDED.me_jid,
			connected_at = EXCLUDED.connected_at,
			updated_at = CURRENT_TIMESTAMP`,
		orgID, sessionName, status, strings.TrimSpace(mePhone), strings.TrimSpace(meJID), connected,
	); err != nil {
		return model.PixWhatsAppSession{}, fmt.Errorf("upsert pix session: %w", err)
	}
	return db.getPixSessionByOrgLocked(orgID)
}

// GetPixSessionByOrg returns the owner's session or ErrPixNotFound.
func (db *DB) GetPixSessionByOrg(orgID string) (model.PixWhatsAppSession, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.getPixSessionByOrgLocked(strings.TrimSpace(orgID))
}

// GetPixSessionByName resolves the owner from a WAHA session name.
func (db *DB) GetPixSessionByName(sessionName string) (model.PixWhatsAppSession, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	row := db.queryRow(pixSessionSelect+` WHERE session_name = ?`, strings.TrimSpace(sessionName))
	return scanPixSession(row)
}

func (db *DB) getPixSessionByOrgLocked(orgID string) (model.PixWhatsAppSession, error) {
	row := db.queryRow(pixSessionSelect+` WHERE org_id = ?`, orgID)
	return scanPixSession(row)
}

const pixSessionSelect = `SELECT id, org_id, session_name, status, me_phone, me_jid, connected_at, created_at, updated_at
	FROM pix_whatsapp_sessions`

func scanPixSession(row *sql.Row) (model.PixWhatsAppSession, error) {
	var (
		s                      model.PixWhatsAppSession
		connectedAt, createdAt sql.NullString
		updatedAt              sql.NullString
	)
	if err := row.Scan(&s.ID, &s.OrgID, &s.SessionName, &s.Status, &s.MePhone, &s.MeJID, &connectedAt, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.PixWhatsAppSession{}, model.ErrPixNotFound
		}
		return model.PixWhatsAppSession{}, fmt.Errorf("scan pix session: %w", err)
	}
	s.ConnectedAt = pixNullableTimestamp(connectedAt)
	s.CreatedAt = pixNullableTimestamp(createdAt)
	s.UpdatedAt = pixNullableTimestamp(updatedAt)
	return s, nil
}

func normalizePixSessionStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case model.PixSessionConnected:
		return model.PixSessionConnected
	case model.PixSessionScanQR:
		return model.PixSessionScanQR
	case model.PixSessionStarting:
		return model.PixSessionStarting
	case model.PixSessionFailed:
		return model.PixSessionFailed
	case model.PixSessionStopped:
		return model.PixSessionStopped
	default:
		return model.PixSessionDisconnected
	}
}

// --- Contatos ---

// UpsertPixContact creates or refreshes the contact identified by its JID.
func (db *DB) UpsertPixContact(orgID, phoneE164, jid, pushName string, at time.Time) (model.PixContact, error) {
	orgID = strings.TrimSpace(orgID)
	jid = strings.TrimSpace(jid)
	if orgID == "" || jid == "" {
		return model.PixContact{}, fmt.Errorf("owner and jid are required")
	}
	if at.IsZero() {
		at = time.Now()
	}
	ts := pixTimestamp(at)

	db.mu.Lock()
	defer db.mu.Unlock()

	if _, err := db.exec(
		`INSERT INTO pix_contacts
			(org_id, phone_e164, whatsapp_jid, push_name, first_contact_at, last_contact_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (org_id, whatsapp_jid) DO UPDATE SET
			phone_e164 = CASE WHEN EXCLUDED.phone_e164 != '' THEN EXCLUDED.phone_e164 ELSE pix_contacts.phone_e164 END,
			push_name = CASE WHEN EXCLUDED.push_name != '' THEN EXCLUDED.push_name ELSE pix_contacts.push_name END,
			last_contact_at = EXCLUDED.last_contact_at`,
		orgID, strings.TrimSpace(phoneE164), jid, strings.TrimSpace(pushName), ts, ts,
	); err != nil {
		return model.PixContact{}, fmt.Errorf("upsert pix contact: %w", err)
	}
	return db.getPixContactByJIDLocked(orgID, jid)
}

// GetPixContactByID returns one contact of the owner or ErrPixNotFound.
func (db *DB) GetPixContactByID(orgID string, id int64) (model.PixContact, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	row := db.queryRow(pixContactSelect+` WHERE org_id = ? AND id = ?`, strings.TrimSpace(orgID), id)
	return scanPixContact(row)
}

// SetPixContactName fills the display name of a contact that has none yet
// (used to backfill @lid senders resolved through WAHA). It never overwrites a
// name that is already known.
func (db *DB) SetPixContactName(orgID string, contactID int64, pushName string) error {
	pushName = strings.TrimSpace(pushName)
	if pushName == "" {
		return nil
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	_, err := db.exec(
		`UPDATE pix_contacts SET push_name = ?
		 WHERE org_id = ? AND id = ? AND push_name = ''`,
		pushName, strings.TrimSpace(orgID), contactID)
	if err != nil {
		return fmt.Errorf("set pix contact name: %w", err)
	}
	return nil
}

func (db *DB) getPixContactByJIDLocked(orgID, jid string) (model.PixContact, error) {
	row := db.queryRow(pixContactSelect+` WHERE org_id = ? AND whatsapp_jid = ?`, orgID, jid)
	return scanPixContact(row)
}

const pixContactSelect = `SELECT id, org_id, phone_e164, whatsapp_jid, push_name, first_contact_at, last_contact_at
	FROM pix_contacts`

func scanPixContact(row *sql.Row) (model.PixContact, error) {
	var (
		c               model.PixContact
		firstAt, lastAt sql.NullString
	)
	if err := row.Scan(&c.ID, &c.OrgID, &c.PhoneE164, &c.WhatsAppJID, &c.PushName, &firstAt, &lastAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.PixContact{}, model.ErrPixNotFound
		}
		return model.PixContact{}, fmt.Errorf("scan pix contact: %w", err)
	}
	c.FirstContactAt = pixNullableTimestamp(firstAt)
	c.LastContactAt = pixNullableTimestamp(lastAt)
	return c, nil
}

// --- Tickets ---

// GetPendingPixTicket returns the open ticket of a contact, or ErrPixNotFound.
func (db *DB) GetPendingPixTicket(orgID string, contactID int64) (model.PixTicket, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	row := db.queryRow(pixTicketSelect+`
		 AND t.contact_id = ? AND t.status = 'pending'`,
		strings.TrimSpace(orgID), contactID)
	return scanPixTicket(row)
}

// CreatePixTicket opens a pending ticket. If another pending ticket already
// exists for the contact (partial unique index), its id is returned instead.
func (db *DB) CreatePixTicket(orgID string, contactID int64, at time.Time) (int64, error) {
	if at.IsZero() {
		at = time.Now()
	}
	ts := pixTimestamp(at)

	db.mu.Lock()
	defer db.mu.Unlock()

	var id int64
	err := db.queryRow(
		`INSERT INTO pix_tickets (org_id, contact_id, status, received_at, last_message_at)
		 VALUES (?, ?, 'pending', ?, ?)
		 ON CONFLICT DO NOTHING
		 RETURNING id`,
		strings.TrimSpace(orgID), contactID, ts, ts,
	).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("create pix ticket: %w", err)
	}
	// Conflict: another pending ticket already covers this contact.
	err = db.queryRow(
		`SELECT id FROM pix_tickets WHERE org_id = ? AND contact_id = ? AND status = 'pending'`,
		strings.TrimSpace(orgID), contactID).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, model.ErrPixNotFound
		}
		return 0, fmt.Errorf("resolve pending pix ticket: %w", err)
	}
	return id, nil
}

// ListPixTickets lists pending (FIFO) or answered (most recent first) tickets.
func (db *DB) ListPixTickets(orgID, status string, limit int) ([]model.PixTicket, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	status = strings.ToLower(strings.TrimSpace(status))
	if status != model.PixTicketPending && status != model.PixTicketAnswered {
		status = model.PixTicketPending
	}
	order := `t.received_at ASC, t.id ASC`
	if status == model.PixTicketAnswered {
		order = `t.answered_at DESC NULLS LAST, t.id DESC`
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.query(pixTicketSelect+` AND t.status = ? ORDER BY `+order+` LIMIT ?`,
		strings.TrimSpace(orgID), status, limit)
	if err != nil {
		return nil, fmt.Errorf("list pix tickets: %w", err)
	}
	defer closeRows(rows)

	out := make([]model.PixTicket, 0)
	for rows.Next() {
		t, err := scanPixTicketRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetPixTicket returns one ticket of the owner or ErrPixNotFound.
func (db *DB) GetPixTicket(orgID string, id int64) (model.PixTicket, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	row := db.queryRow(pixTicketSelect+` AND t.id = ?`, strings.TrimSpace(orgID), id)
	return scanPixTicket(row)
}

// MarkPixTicketAnswered closes a pending ticket. Idempotent.
func (db *DB) MarkPixTicketAnswered(orgID string, id int64, answeredBy string, at time.Time) error {
	if at.IsZero() {
		at = time.Now()
	}
	orgID = strings.TrimSpace(orgID)

	db.mu.Lock()
	defer db.mu.Unlock()

	res, err := db.exec(
		`UPDATE pix_tickets SET status = 'answered', answered_at = ?, answered_by = ?
		 WHERE org_id = ? AND id = ? AND status = 'pending'`,
		pixTimestamp(at), strings.TrimSpace(answeredBy), orgID, id)
	if err != nil {
		return fmt.Errorf("mark pix ticket answered: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("mark pix ticket answered rows: %w", err)
	}
	if affected > 0 {
		return nil
	}

	var status string
	if err := db.queryRow(`SELECT status FROM pix_tickets WHERE org_id = ? AND id = ?`, orgID, id).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.ErrPixNotFound
		}
		return fmt.Errorf("resolve pix ticket status: %w", err)
	}
	if status == model.PixTicketAnswered {
		return nil
	}
	return model.ErrPixInvalidTransition
}

// MarkPixTicketReceipt flags the ticket as having at least one stored receipt.
func (db *DB) MarkPixTicketReceipt(orgID string, id int64, at time.Time) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	_, err := db.exec(
		`UPDATE pix_tickets SET has_receipt = TRUE, last_message_at = GREATEST(last_message_at, ?)
		 WHERE org_id = ? AND id = ?`,
		pixTimestamp(at), strings.TrimSpace(orgID), id)
	if err != nil {
		return fmt.Errorf("mark pix ticket receipt: %w", err)
	}
	return nil
}

// ListPixContactHistory lists answered tickets of one contact.
func (db *DB) ListPixContactHistory(orgID string, contactID int64, limit int) ([]model.PixTicket, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.query(pixTicketSelect+`
		 AND t.contact_id = ? AND t.status = 'answered'
		 ORDER BY t.answered_at DESC NULLS LAST, t.id DESC LIMIT ?`,
		strings.TrimSpace(orgID), contactID, limit)
	if err != nil {
		return nil, fmt.Errorf("list pix contact history: %w", err)
	}
	defer closeRows(rows)

	out := make([]model.PixTicket, 0)
	for rows.Next() {
		t, err := scanPixTicketRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// MarkPixAutoReplySent records that the unsupported-type auto reply was sent.
func (db *DB) MarkPixAutoReplySent(orgID string, ticketID int64, at time.Time) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	_, err := db.exec(
		`UPDATE pix_tickets SET auto_reply_sent_at = ?
		 WHERE org_id = ? AND id = ? AND auto_reply_sent_at IS NULL`,
		pixTimestamp(at), strings.TrimSpace(orgID), ticketID)
	if err != nil {
		return fmt.Errorf("mark pix auto reply sent: %w", err)
	}
	return nil
}

// GetPixTicketPaidTotal sums the receipt values extracted so far for the
// ticket, in cents. Messages without an extracted value (-1/0) never count.
func (db *DB) GetPixTicketPaidTotal(orgID string, ticketID int64) (int64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	var total int64
	err := db.queryRow(
		`SELECT COALESCE(SUM(media_value_cents), 0) FROM pix_messages
		 WHERE org_id = ? AND ticket_id = ? AND direction = 'inbound'
		   AND type IN ('image','document') AND media_value_cents > 0`,
		strings.TrimSpace(orgID), ticketID).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("pix ticket paid total: %w", err)
	}
	return total, nil
}

// DeleteEmptyPixTicket removes a pending ticket without any message. Returns
// true when a row went away.
func (db *DB) DeleteEmptyPixTicket(orgID string, ticketID int64) (bool, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	res, err := db.exec(
		`DELETE FROM pix_tickets
		 WHERE org_id = ? AND id = ? AND status = 'pending'
		   AND NOT EXISTS (SELECT 1 FROM pix_messages m WHERE m.ticket_id = pix_tickets.id)`,
		strings.TrimSpace(orgID), ticketID)
	if err != nil {
		return false, fmt.Errorf("delete empty pix ticket: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("delete empty pix ticket rows: %w", err)
	}
	return affected > 0, nil
}

// --- Valores PIX aceitos ---

// ListPixValueRules returns the owner's accepted PIX amounts in cents, sorted.
func (db *DB) ListPixValueRules(orgID string) ([]int64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.query(
		`SELECT value_cents FROM pix_value_rules WHERE org_id = ? ORDER BY value_cents ASC`,
		strings.TrimSpace(orgID))
	if err != nil {
		return nil, fmt.Errorf("list pix value rules: %w", err)
	}
	defer closeRows(rows)

	out := make([]int64, 0)
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("scan pix value rule: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ReplacePixValueRules swaps the owner's accepted amounts atomically.
func (db *DB) ReplacePixValueRules(orgID string, cents []int64) error {
	orgID = strings.TrimSpace(orgID)
	if orgID == "" {
		return fmt.Errorf("owner is required")
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin replace pix value rules: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(db.bind(`DELETE FROM pix_value_rules WHERE org_id = ?`), orgID); err != nil {
		return fmt.Errorf("delete pix value rules: %w", err)
	}
	for _, v := range cents {
		if _, err := tx.Exec(db.bind(
			`INSERT INTO pix_value_rules (org_id, value_cents) VALUES (?, ?) ON CONFLICT DO NOTHING`),
			orgID, v); err != nil {
			return fmt.Errorf("insert pix value rule: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit replace pix value rules: %w", err)
	}
	return nil
}

const pixTicketSelect = `SELECT t.id, t.org_id, t.contact_id, t.status, t.has_receipt,
		t.auto_reply_sent_at IS NOT NULL AS auto_reply_sent,
		t.received_at, t.last_message_at, t.answered_at, t.answered_by,
		c.id, c.org_id, c.phone_e164, c.whatsapp_jid, c.push_name, c.first_contact_at, c.last_contact_at,
		COALESCE((SELECT m.body FROM pix_messages m WHERE m.ticket_id = t.id ORDER BY m.created_at DESC, m.id DESC LIMIT 1), '') AS preview,
		COALESCE((SELECT m.type FROM pix_messages m WHERE m.ticket_id = t.id ORDER BY m.created_at DESC, m.id DESC LIMIT 1), '') AS last_type,
		(SELECT COUNT(*) FROM pix_messages m WHERE m.ticket_id = t.id) AS message_count,
		(SELECT COUNT(*) FROM pix_messages m WHERE m.ticket_id = t.id AND m.type IN ('image','document') AND m.media_path != '' AND m.media_deleted_at IS NULL) AS active_media_count,
		COALESCE((SELECT SUM(m.media_value_cents) FROM pix_messages m WHERE m.ticket_id = t.id AND m.direction = 'inbound' AND m.type IN ('image','document') AND m.media_value_cents > 0), 0) AS paid_total_cents,
		EXISTS(SELECT 1 FROM pix_messages m WHERE m.ticket_id = t.id AND m.direction = 'inbound' AND m.type = 'text') AS has_inbound_text,
		EXISTS(SELECT 1 FROM pix_messages m WHERE m.ticket_id = t.id AND m.direction = 'inbound' AND m.type IN ('image','document') AND m.media_value_cents <= 0) AS has_unextracted_receipt
	FROM pix_tickets t
	JOIN pix_contacts c ON c.id = t.contact_id
	WHERE t.org_id = ?`

type pixTicketScanner interface {
	Scan(dest ...any) error
}

func scanPixTicket(row *sql.Row) (model.PixTicket, error) {
	t, err := scanPixTicketRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.PixTicket{}, model.ErrPixNotFound
		}
		return model.PixTicket{}, err
	}
	return t, nil
}

func scanPixTicketRows(rows *sql.Rows) (model.PixTicket, error) {
	return scanPixTicketRow(rows)
}

func scanPixTicketRow(row pixTicketScanner) (model.PixTicket, error) {
	var (
		t                         model.PixTicket
		receivedAt, lastMessageAt sql.NullString
		answeredAt                sql.NullString
		firstAt, lastAt           sql.NullString
		preview, lastType         sql.NullString
	)
	if err := row.Scan(
		&t.ID, &t.OrgID, &t.ContactID, &t.Status, &t.HasReceipt, &t.AutoReplySent,
		&receivedAt, &lastMessageAt, &answeredAt, &t.AnsweredBy,
		&t.Contact.ID, &t.Contact.OrgID, &t.Contact.PhoneE164, &t.Contact.WhatsAppJID,
		&t.Contact.PushName, &firstAt, &lastAt,
		&preview, &lastType, &t.MessageCount, &t.ActiveMediaCount,
		&t.PaidTotalCents, &t.HasInboundText, &t.HasUnextractedReceipt,
	); err != nil {
		return model.PixTicket{}, err
	}
	t.ReceivedAt = pixNullableTimestamp(receivedAt)
	t.LastMessageAt = pixNullableTimestamp(lastMessageAt)
	t.AnsweredAt = pixNullableTimestamp(answeredAt)
	t.Contact.FirstContactAt = pixNullableTimestamp(firstAt)
	t.Contact.LastContactAt = pixNullableTimestamp(lastAt)
	t.LastMessagePreview = pixNullableTimestamp(preview)
	t.LastMessageType = pixNullableTimestamp(lastType)
	return t, nil
}

// --- Mensagens e mídia ---

// AddPixMessage inserts a message. Duplicate WhatsApp ids are ignored (false).
func (db *DB) AddPixMessage(m model.PixMessage) (int64, bool, error) {
	m.OrgID = strings.TrimSpace(m.OrgID)
	if m.OrgID == "" || strings.TrimSpace(m.WhatsAppMessageID) == "" {
		return 0, false, fmt.Errorf("owner and whatsapp message id are required")
	}
	if m.CreatedAt == "" {
		m.CreatedAt = pixTimestamp(time.Now())
	}

	db.mu.Lock()
	defer db.mu.Unlock()

	var id int64
	value := m.MediaValueCents
	if value <= 0 {
		value = -1
	}
	err := db.queryRow(
		`INSERT INTO pix_messages
			(org_id, ticket_id, contact_id, whatsapp_message_id, direction, type, body,
			 media_path, media_mime, media_filename, media_value_cents, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (org_id, whatsapp_message_id) DO NOTHING
		 RETURNING id`,
		m.OrgID, m.TicketID, m.ContactID, m.WhatsAppMessageID, m.Direction, m.Type, m.Body,
		m.MediaPath, m.MediaMime, m.MediaFilename, value, m.CreatedAt,
	).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("add pix message: %w", err)
	}
	return id, true, nil
}

// ListPixMessages lists a ticket's messages in chronological order.
func (db *DB) ListPixMessages(orgID string, ticketID int64, limit int) ([]model.PixMessage, error) {
	if limit < 1 || limit > 500 {
		limit = 200
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.query(pixMessageSelect+`
		 WHERE org_id = ? AND ticket_id = ?
		 ORDER BY created_at ASC, id ASC LIMIT ?`,
		strings.TrimSpace(orgID), ticketID, limit)
	if err != nil {
		return nil, fmt.Errorf("list pix messages: %w", err)
	}
	defer closeRows(rows)

	out := make([]model.PixMessage, 0)
	for rows.Next() {
		m, err := scanPixMessageRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetPixMessageForMedia returns one message for the media endpoint.
func (db *DB) GetPixMessageForMedia(orgID string, id int64) (model.PixMessage, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	row := db.queryRow(pixMessageSelect+` WHERE org_id = ? AND id = ?`,
		strings.TrimSpace(orgID), id)
	return scanPixMessage(row)
}

// ListActivePixMedia lists messages whose file can still be deleted.
func (db *DB) ListActivePixMedia(orgID string, limit int) ([]model.PixMessage, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	rows, err := db.query(pixMessageSelect+`
		 WHERE org_id = ? AND media_path != '' AND media_deleted_at IS NULL
		 ORDER BY id ASC LIMIT ?`,
		strings.TrimSpace(orgID), limit)
	if err != nil {
		return nil, fmt.Errorf("list active pix media: %w", err)
	}
	defer closeRows(rows)

	out := make([]model.PixMessage, 0)
	for rows.Next() {
		m, err := scanPixMessageRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MarkPixMediaDeleted flags the media row as removed from storage. Idempotent.
func (db *DB) MarkPixMediaDeleted(orgID string, id int64, at time.Time, reason string) error {
	if at.IsZero() {
		at = time.Now()
	}
	db.mu.Lock()
	defer db.mu.Unlock()

	_, err := db.exec(
		`UPDATE pix_messages SET media_deleted_at = ?, media_delete_reason = ?
		 WHERE org_id = ? AND id = ? AND media_deleted_at IS NULL`,
		pixTimestamp(at), strings.TrimSpace(reason), strings.TrimSpace(orgID), id)
	if err != nil {
		return fmt.Errorf("mark pix media deleted: %w", err)
	}
	return nil
}

const pixMessageSelect = `SELECT id, org_id, ticket_id, contact_id, whatsapp_message_id,
		direction, type, body, media_path, media_mime, media_filename,
		media_deleted_at, media_delete_reason, media_value_cents, created_at
	FROM pix_messages`

func scanPixMessage(row *sql.Row) (model.PixMessage, error) {
	m, err := scanPixMessageRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.PixMessage{}, model.ErrPixNotFound
		}
		return model.PixMessage{}, err
	}
	return m, nil
}

func scanPixMessageRows(rows *sql.Rows) (model.PixMessage, error) {
	return scanPixMessageRow(rows)
}

func scanPixMessageRow(row pixTicketScanner) (model.PixMessage, error) {
	var (
		m          model.PixMessage
		deletedAt  sql.NullString
		createdAt  sql.NullString
		valueCents int64
	)
	if err := row.Scan(
		&m.ID, &m.OrgID, &m.TicketID, &m.ContactID, &m.WhatsAppMessageID,
		&m.Direction, &m.Type, &m.Body, &m.MediaPath, &m.MediaMime, &m.MediaFilename,
		&deletedAt, &m.MediaDeleteReason, &valueCents, &createdAt,
	); err != nil {
		return model.PixMessage{}, err
	}
	m.MediaDeletedAt = pixNullableTimestamp(deletedAt)
	m.CreatedAt = pixNullableTimestamp(createdAt)
	if valueCents > 0 {
		m.MediaValueCents = valueCents
	}
	return m, nil
}
