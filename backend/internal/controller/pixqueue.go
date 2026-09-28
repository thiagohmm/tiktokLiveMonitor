package controller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/media"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"github.com/thiagohmm/tiktok-live-monitor/internal/receipt"
	"github.com/thiagohmm/tiktok-live-monitor/internal/whatsapp"
)

// ErrPixDisabled is returned when WAHA/MinIO are not configured.
var ErrPixDisabled = errors.New("fila pix indisponível (WAHA/MinIO não configurados)")

// ErrPixMediaGone mirrors model.ErrPixMediaGone for the view layer.
var ErrPixMediaGone = model.ErrPixMediaGone

// ErrPixAlreadyPaired means the number is already connected: the frontend must
// drop the QR instead of offering a stale one.
var ErrPixAlreadyPaired = errors.New("whatsapp already paired")

// ErrPixQRPending means WAHA accepted the start but has no QR yet: the frontend
// shows "generating" instead of an error.
var ErrPixQRPending = errors.New("whatsapp qr not ready yet")

// PixBroadcaster delivers a Fila PIX SSE event to one organization's clients.
type PixBroadcaster func(orgID, eventType string, data interface{})

// wahaClient is the subset of the WAHA client used by the service (fakeable).
type wahaClient interface {
	StartSession(ctx context.Context, session string) error
	QR(ctx context.Context, session string) ([]byte, error)
	Status(ctx context.Context, session string) (whatsapp.SessionStatus, error)
	Logout(ctx context.Context, session string) error
	SendText(ctx context.Context, session, chatID, text string) (string, error)
	DownloadMedia(ctx context.Context, rawURL string) ([]byte, error)
	Contact(ctx context.Context, session, chatID string) (whatsapp.Contact, error)
	Config() whatsapp.Config
}

// PixQueueService orchestrates the Fila PIX.
type PixQueueService struct {
	repo            model.Repository
	waha            wahaClient
	store           media.Store
	broadcast       PixBroadcaster
	pairingWait     time.Duration
	pairingInterval time.Duration
	purgeMu         sync.Mutex
	purgeLocks      map[string]*sync.Mutex
	// receiptMu guards receiptLocks: one mutex per ticket serializes the
	// value-gate read/accumulate/store so two receipts arriving together cannot
	// both see the same paid total.
	receiptMu    sync.Mutex
	receiptLocks map[int64]*sync.Mutex
	// nameLookupMu guards nameTried: JIDs whose display name was already
	// resolved through WAHA, so the list backfill does not hammer the gateway.
	nameLookupMu sync.Mutex
	nameTried    map[string]bool
	// extractText overrides receipt text extraction (tests inject fixed OCR
	// results; nil uses the real PDF/tesseract pipeline).
	extractText func(ctx context.Context, rcpt media.Receipt, data []byte) (string, error)
}

// NewPixQueueService creates the service.
func NewPixQueueService(repo model.Repository, waha *whatsapp.Client, store media.Store) *PixQueueService {
	return &PixQueueService{
		repo:            repo,
		waha:            waha,
		store:           store,
		pairingWait:     defaultPairingWait,
		pairingInterval: defaultPairingInterval,
		purgeLocks:      make(map[string]*sync.Mutex),
		receiptLocks:    make(map[int64]*sync.Mutex),
		nameTried:       make(map[string]bool),
	}
}

// SetBroadcaster wires the SSE fan-out.
func (s *PixQueueService) SetBroadcaster(b PixBroadcaster) {
	s.broadcast = b
}

// Enabled reports whether WAHA and MinIO are configured.
func (s *PixQueueService) Enabled() bool {
	if s == nil || s.waha == nil || s.store == nil {
		return false
	}
	return s.waha.Config().Configured()
}

// WebhookSecret exposes the HMAC secret for the public webhook handler.
func (s *PixQueueService) WebhookSecret() string {
	if s == nil || s.waha == nil {
		return ""
	}
	return s.waha.Config().WebhookSecret
}

// emit sends a Fila PIX event to the organization's clients only.
func (s *PixQueueService) emit(orgID, eventType string, data interface{}) {
	if s != nil && s.broadcast != nil && orgID != "" {
		s.broadcast(orgID, eventType, data)
	}
}

// --- Pairing ---

// Pairing waits are bounded: WAHA takes a couple of seconds to reach
// SCAN_QR_CODE, and a gateway that is down must not hang the browser.
const (
	defaultPairingWait     = 6 * time.Second
	defaultPairingInterval = 700 * time.Millisecond
)

// StatusView is the pairing state returned to the frontend.
type StatusView struct {
	SessionName string `json:"sessionName"`
	Status      string `json:"status"`
	Connected   bool   `json:"connected"`
	MePhone     string `json:"mePhone,omitempty"`
}

// Connect starts the owner's WAHA session and persists the state the frontend
// shows next to the QR. A session WAHA left stopped or failed is restarted here,
// otherwise the click would only report "stopped" and no QR would ever appear.
func (s *PixQueueService) Connect(ctx context.Context, orgID string) (model.PixWhatsAppSession, error) {
	orgID, session, err := s.pairingTarget(orgID)
	if err != nil {
		return model.PixWhatsAppSession{}, err
	}
	if err := s.waha.StartSession(ctx, session); err != nil {
		return model.PixWhatsAppSession{}, err
	}
	status, err := s.waitUntilQRReady(ctx, session)
	if err != nil {
		// The session is live anyway: report "starting" instead of failing the click.
		log.Printf("[Pix] status after connect: %v", err)
		return s.repo.UpsertPixSession(orgID, session, model.PixSessionStarting, "", "", nil)
	}
	return s.persistStatus(orgID, session, status)
}

// QR returns the pairing QR PNG. WAHA only answers once the session reached
// SCAN_QR_CODE, so the session is started first and the QR is fetched after.
func (s *PixQueueService) QR(ctx context.Context, orgID string) ([]byte, error) {
	orgID, session, err := s.pairingTarget(orgID)
	if err != nil {
		return nil, err
	}
	status, err := s.startPairing(ctx, orgID, session)
	if err != nil {
		return nil, err
	}
	if status.Connected {
		return nil, ErrPixAlreadyPaired
	}
	if !status.CanServeQR() {
		return nil, ErrPixQRPending
	}
	return s.waha.QR(ctx, session)
}

// startPairing moves the session to a live state and waits for the QR.
func (s *PixQueueService) startPairing(ctx context.Context, orgID, session string) (whatsapp.SessionStatus, error) {
	if err := s.waha.StartSession(ctx, session); err != nil {
		return whatsapp.SessionStatus{}, err
	}
	return s.waitUntilQRReady(ctx, session)
}

// waitUntilQRReady polls WAHA until the session is paired or has a QR ready.
// The deadline keeps the browser on a short leash when WAHA misbehaves.
func (s *PixQueueService) waitUntilQRReady(ctx context.Context, session string) (whatsapp.SessionStatus, error) {
	deadline := time.Now().Add(s.pairingWait)
	for {
		status, err := s.waha.Status(ctx, session)
		if err != nil {
			return whatsapp.SessionStatus{}, err
		}
		if status.Connected || status.CanServeQR() {
			return status, nil
		}
		if time.Now().Add(s.pairingInterval).After(deadline) {
			return status, nil
		}
		if err := sleepWithContext(ctx, s.pairingInterval); err != nil {
			return status, err
		}
	}
}

// pairingTarget validates the organization and resolves its WAHA session name,
// so every pairing entry point rejects an anonymous caller in one place. A
// session already stored for the organization keeps its name (sessions paired
// before multi-tenancy were named after the user).
func (s *PixQueueService) pairingTarget(orgID string) (string, string, error) {
	if !s.Enabled() {
		return "", "", ErrPixDisabled
	}
	orgID = strings.TrimSpace(orgID)
	session := whatsapp.SessionNameForOrg(orgID)
	if session == "" {
		return "", "", model.ErrPixForbidden
	}
	if stored, err := s.repo.GetPixSessionByOrg(orgID); err == nil && stored.SessionName != "" {
		session = stored.SessionName
	}
	return orgID, session, nil
}

// Status syncs and returns the pairing state.
func (s *PixQueueService) Status(ctx context.Context, orgID string) (StatusView, error) {
	orgID, session, err := s.pairingTarget(orgID)
	if err != nil {
		return StatusView{}, err
	}
	status, err := s.waha.Status(ctx, session)
	if err != nil {
		return StatusView{}, err
	}
	if !status.Found {
		// A brand-new user has no session yet: create it so the QR can show up.
		if err := s.waha.StartSession(ctx, session); err != nil {
			return StatusView{}, err
		}
		status, err = s.waha.Status(ctx, session)
		if err != nil {
			return StatusView{}, err
		}
	}
	row, err := s.persistStatus(orgID, session, status)
	if err != nil {
		return StatusView{}, err
	}
	return StatusView{SessionName: session, Status: row.Status, Connected: status.Connected, MePhone: row.MePhone}, nil
}

// Disconnect logs the session out and updates local state.
func (s *PixQueueService) Disconnect(ctx context.Context, orgID string) error {
	_, session, err := s.pairingTarget(orgID)
	if err != nil {
		return err
	}
	if err := s.waha.Logout(ctx, session); err != nil {
		log.Printf("[Pix] logout: %v", err)
	}
	_, err = s.repo.UpsertPixSession(orgID, session, model.PixSessionDisconnected, "", "", nil)
	return err
}

func (s *PixQueueService) persistStatus(orgID, session string, status whatsapp.SessionStatus) (model.PixWhatsAppSession, error) {
	state := pairingStatus(status)
	var connectedAt *time.Time
	if status.Connected {
		now := time.Now()
		connectedAt = &now
	}
	return s.repo.UpsertPixSession(orgID, session, state, status.MePhone, status.MeJID, connectedAt)
}

// pairingStatus maps the WAHA state onto the state stored for the user.
func pairingStatus(status whatsapp.SessionStatus) string {
	switch {
	case status.Connected:
		return model.PixSessionConnected
	case status.CanServeQR():
		return model.PixSessionScanQR
	case status.Failed():
		return model.PixSessionFailed
	case status.NeedsRestart():
		return model.PixSessionStopped
	case !status.Found:
		return model.PixSessionDisconnected
	default:
		return model.PixSessionStarting
	}
}

// sleepWithContext waits for d or until the request is cancelled.
func sleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// --- Webhook / inbound ---

// AutoReplyUnsupported is sent once per ticket when the client sends media that
// is neither JPEG nor PDF (or an unavailable receipt).
const AutoReplyUnsupported = "Este atendimento aceita apenas mensagens de texto ou comprovante PIX em JPG/PDF."

// AutoReplyUnreadable is sent when the value gate is on but no monetary value
// could be extracted from the receipt (bad photo, scanned PDF...).
const AutoReplyUnreadable = "Não foi possível identificar o valor neste comprovante. Envie o PDF do comprovante do banco ou uma foto nítida."

// AutoReplyPartial asks the client for the missing amount: received, missing,
// target (all formatted as "R$ 15,00").
const AutoReplyPartial = "Recebemos um comprovante de %s. Falta %s para completar %s. Envie um comprovante com esse valor para continuar o atendimento."

// maxPixValuesPerOrg bounds the value configuration.
const maxPixValuesPerOrg = 20

// HandleInbound processes one normalized inbound message.
func (s *PixQueueService) HandleInbound(ctx context.Context, msg whatsapp.InboundMessage) error {
	if !s.Enabled() {
		return ErrPixDisabled
	}
	if strings.TrimSpace(msg.Session) == "" || strings.TrimSpace(msg.MessageID) == "" {
		return nil
	}
	session, err := s.repo.GetPixSessionByName(msg.Session)
	if err != nil {
		if errors.Is(err, model.ErrPixNotFound) {
			return nil // webhook from an unknown/removed session
		}
		return err
	}
	orgID := session.OrgID

	// WhatsApp delivers privacy-restricted senders as an @lid with no phone and
	// (often) no pushName in the webhook. Resolve the profile name through WAHA so
	// the queue shows a person, not an opaque id.
	pushName := msg.PushName
	if pushName == "" && s.waha != nil {
		if c, err := s.waha.Contact(ctx, session.SessionName, msg.JID); err != nil {
			log.Printf("[Pix] contact lookup (%s): %v", msg.JID, err)
		} else {
			pushName = c.DisplayName()
		}
	}

	contact, err := s.repo.UpsertPixContact(orgID, msg.PhoneE164, msg.JID, pushName, msg.Timestamp)
	if err != nil {
		return err
	}
	ticket, err := s.ensurePendingTicket(orgID, contact.ID, msg.Timestamp)
	if err != nil {
		return err
	}

	switch msg.Type {
	case model.PixTypeText:
		if strings.TrimSpace(msg.Body) == "" {
			return nil
		}
		_, inserted, err := s.repo.AddPixMessage(model.PixMessage{
			OrgID:             orgID,
			TicketID:          ticket.ID,
			ContactID:         contact.ID,
			WhatsAppMessageID: msg.MessageID,
			Direction:         model.PixDirectionInbound,
			Type:              model.PixTypeText,
			Body:              msg.Body,
			CreatedAt:         pixNow(msg.Timestamp),
		})
		if err != nil {
			return err
		}
		if inserted {
			s.emitTicket(orgID, ticket.ID, contact.ID)
		}
		return nil
	case model.PixTypeImage, model.PixTypeDocument:
		return s.handleReceipt(ctx, orgID, ticket, contact, msg)
	default:
		return s.handleUnsupported(ctx, orgID, ticket, contact, session.SessionName, msg)
	}
}

func (s *PixQueueService) handleReceipt(ctx context.Context, orgID string, ticket model.PixTicket, contact model.PixContact, msg whatsapp.InboundMessage) error {
	rcpt, data, ok := s.downloadReceipt(ctx, msg)
	if !ok {
		// The receipt could not be validated/stored: keep the conversation
		// visible and orient the client to resend instead of retrying forever.
		return s.handleUnsupported(ctx, orgID, ticket, contact, whatsapp.SessionNameForOrg(orgID), msg)
	}

	accepted, err := s.repo.ListPixValueRules(orgID)
	if err != nil {
		// Cannot read the rules: behave as if the gate were off (fail-open).
		log.Printf("[Pix] value rules (%s): %v", orgID, err)
		accepted = nil
	}
	if len(accepted) == 0 {
		_, err := s.storeReceipt(ctx, orgID, ticket, contact, msg, rcpt, data, 0, true)
		return err
	}
	return s.handleGatedReceipt(ctx, orgID, ticket, contact, msg, rcpt, data, accepted)
}

// handleGatedReceipt applies the value filter: the ticket only surfaces when
// the sum of the extracted receipt values equals one of the accepted amounts
// (or exceeds every one of them, leaving the decision to the operator).
func (s *PixQueueService) handleGatedReceipt(ctx context.Context, orgID string, ticket model.PixTicket, contact model.PixContact, msg whatsapp.InboundMessage, rcpt media.Receipt, data []byte, accepted []int64) error {
	lock := s.ticketLock(ticket.ID)
	lock.Lock()
	defer lock.Unlock()

	text, err := s.extractReceiptText(ctx, rcpt, data)
	if errors.Is(err, receipt.ErrEngineUnavailable) {
		// OCR engine missing is an infrastructure failure, not the client's
		// fault: accept the receipt like before the gate existed.
		log.Printf("[Pix] %v — comprovante aceito sem filtro de valor", err)
		_, err := s.storeReceipt(ctx, orgID, ticket, contact, msg, rcpt, data, 0, true)
		return err
	}
	if err != nil {
		log.Printf("[Pix] extract receipt text (%s): %v", msg.MessageID, err)
	}
	values := receipt.ExtractValuesCents(text)
	if len(values) == 0 {
		return s.rejectUnreadableReceipt(ctx, orgID, ticket, contact, msg)
	}
	// The largest extracted amount is the receipt's principal value; smaller
	// ones are fees/IOF lines on the same comprovante.
	paid := values[len(values)-1]

	current, err := s.repo.GetPixTicketPaidTotal(orgID, ticket.ID)
	if err != nil {
		// Without the accumulator the gate cannot decide; fail-open keeps the
		// money flowing and the operator reviews the ticket.
		log.Printf("[Pix] paid total (ticket %d): %v", ticket.ID, err)
		_, storeErr := s.storeReceipt(ctx, orgID, ticket, contact, msg, rcpt, data, paid, true)
		return storeErr
	}
	decision := receipt.Decide(current+paid, accepted)
	if decision.Status == receipt.StatusPartial {
		// Store the partial payment (it counts towards the total) but keep the
		// ticket out of the queue and ask the client for the missing amount.
		inserted, err := s.storeReceipt(ctx, orgID, ticket, contact, msg, rcpt, data, paid, false)
		if err != nil || !inserted {
			return err
		}
		s.sendValueGapReply(ctx, orgID, ticket, contact, paid, decision)
		return nil
	}
	_, err = s.storeReceipt(ctx, orgID, ticket, contact, msg, rcpt, data, paid, true)
	return err
}

// extractReceiptText pulls the text out of the receipt file (PDF text layer or
// JPEG OCR).
func (s *PixQueueService) extractReceiptText(ctx context.Context, rcpt media.Receipt, data []byte) (string, error) {
	if s.extractText != nil {
		return s.extractText(ctx, rcpt, data)
	}
	if rcpt.Extension == "pdf" {
		return receipt.ExtractTextPDF(data)
	}
	return receipt.ExtractTextJPEG(ctx, data)
}

// rejectUnreadableReceipt discards a receipt whose value could not be read:
// nothing is stored, the client is asked to resend, and the empty ticket that
// this webhook created disappears again.
func (s *PixQueueService) rejectUnreadableReceipt(ctx context.Context, orgID string, ticket model.PixTicket, contact model.PixContact, msg whatsapp.InboundMessage) error {
	log.Printf("[Pix] receipt %s: valor ilegível, descartado (ticket %d)", msg.MessageID, ticket.ID)
	chatID, err := whatsapp.ChatID(contact.WhatsAppJID)
	if err != nil {
		return err
	}
	if _, err := s.waha.SendText(ctx, whatsapp.SessionNameForOrg(orgID), chatID, AutoReplyUnreadable); err != nil {
		log.Printf("[Pix] unreadable reply (ticket %d): %v", ticket.ID, err)
	}
	deleted, err := s.repo.DeleteEmptyPixTicket(orgID, ticket.ID)
	if err != nil {
		log.Printf("[Pix] delete empty ticket %d: %v", ticket.ID, err)
	} else if deleted {
		log.Printf("[Pix] ticket %d vazio removido após comprovante ilegível", ticket.ID)
	}
	return nil
}

// sendValueGapReply tells the client how much is still missing and records the
// reply in the conversation so the operator sees the exchange once the ticket
// opens.
func (s *PixQueueService) sendValueGapReply(ctx context.Context, orgID string, ticket model.PixTicket, contact model.PixContact, paidCents int64, decision receipt.Decision) {
	chatID, err := whatsapp.ChatID(contact.WhatsAppJID)
	if err != nil {
		log.Printf("[Pix] gap reply chat id (ticket %d): %v", ticket.ID, err)
		return
	}
	body := fmt.Sprintf(AutoReplyPartial,
		receipt.FormatBRL(paidCents), receipt.FormatBRL(decision.Missing), receipt.FormatBRL(decision.Target))
	waID, err := s.waha.SendText(ctx, whatsapp.SessionNameForOrg(orgID), chatID, body)
	if err != nil {
		log.Printf("[Pix] gap reply (ticket %d): %v", ticket.ID, err)
		return
	}
	if waID == "" {
		waID = "waha-local-" + randomHex(8)
	}
	if _, _, err := s.repo.AddPixMessage(model.PixMessage{
		OrgID:             orgID,
		TicketID:          ticket.ID,
		ContactID:         contact.ID,
		WhatsAppMessageID: waID,
		Direction:         model.PixDirectionOutbound,
		Type:              model.PixTypeText,
		Body:              body,
		CreatedAt:         pixNow(time.Time{}),
	}); err != nil {
		log.Printf("[Pix] store gap reply (ticket %d): %v", ticket.ID, err)
	}
}

// storeReceipt persists the file and the message, returning whether a new row
// was inserted (false on webhook duplicates). valueCents <= 0 stores "no
// extracted value"; emit false keeps the ticket out of the SSE queue (partial
// payment under the value gate).
func (s *PixQueueService) storeReceipt(ctx context.Context, orgID string, ticket model.PixTicket, contact model.PixContact, msg whatsapp.InboundMessage, rcpt media.Receipt, data []byte, valueCents int64, emit bool) (bool, error) {
	objectKey := media.ObjectKey(orgID, randomHex(16), rcpt.Extension)
	if err := s.storePut(ctx, objectKey, data, rcpt.ContentType); err != nil {
		log.Printf("[Pix] store receipt: %v", err)
		return true, s.handleUnsupported(ctx, orgID, ticket, contact, whatsapp.SessionNameForOrg(orgID), msg)
	}

	id, inserted, err := s.repo.AddPixMessage(model.PixMessage{
		OrgID:             orgID,
		TicketID:          ticket.ID,
		ContactID:         contact.ID,
		WhatsAppMessageID: msg.MessageID,
		Direction:         model.PixDirectionInbound,
		Type:              rcpt.Type,
		Body:              msg.Body,
		MediaPath:         objectKey,
		MediaMime:         rcpt.ContentType,
		MediaFilename:     media.SanitizeFilename(msg.MediaFilename),
		MediaValueCents:   valueCents,
		CreatedAt:         pixNow(msg.Timestamp),
	})
	if err != nil {
		if delErr := s.storeDelete(ctx, objectKey); delErr != nil {
			log.Printf("[Pix] cleanup orphan media: %v", delErr)
		}
		return false, err
	}
	if !inserted {
		// Duplicate webhook: the already-stored object is redundant.
		if delErr := s.storeDelete(ctx, objectKey); delErr != nil {
			log.Printf("[Pix] cleanup duplicate media: %v", delErr)
		}
		return false, nil
	}
	if err := s.repo.MarkPixTicketReceipt(orgID, ticket.ID, time.Now()); err != nil {
		log.Printf("[Pix] mark receipt: %v", err)
	}
	if emit {
		s.emitTicket(orgID, ticket.ID, contact.ID)
		s.emitMessage(orgID, ticket.ID, id)
	}
	return true, nil
}

func (s *PixQueueService) ticketLock(ticketID int64) *sync.Mutex {
	s.receiptMu.Lock()
	defer s.receiptMu.Unlock()
	if s.receiptLocks == nil {
		s.receiptLocks = make(map[int64]*sync.Mutex)
	}
	l, ok := s.receiptLocks[ticketID]
	if !ok {
		l = &sync.Mutex{}
		s.receiptLocks[ticketID] = l
	}
	return l
}

func (s *PixQueueService) downloadReceipt(ctx context.Context, msg whatsapp.InboundMessage) (media.Receipt, []byte, bool) {
	if strings.TrimSpace(msg.MediaURL) == "" {
		return media.Receipt{}, nil, false
	}
	data, err := s.waha.DownloadMedia(ctx, msg.MediaURL)
	if err != nil {
		log.Printf("[Pix] download media: %v", err)
		return media.Receipt{}, nil, false
	}
	rcpt, ok := media.DetectReceipt(data)
	if !ok {
		return media.Receipt{}, nil, false
	}
	return rcpt, data, true
}

func (s *PixQueueService) handleUnsupported(ctx context.Context, orgID string, ticket model.PixTicket, contact model.PixContact, sessionName string, msg whatsapp.InboundMessage) error {
	_, inserted, err := s.repo.AddPixMessage(model.PixMessage{
		OrgID:             orgID,
		TicketID:          ticket.ID,
		ContactID:         contact.ID,
		WhatsAppMessageID: msg.MessageID,
		Direction:         model.PixDirectionInbound,
		Type:              model.PixTypeUnsupported,
		Body:              msg.Body,
		CreatedAt:         pixNow(msg.Timestamp),
	})
	if err != nil {
		return err
	}
	if !inserted {
		return nil
	}
	if !ticket.AutoReplySent {
		if err := s.sendAutoReply(ctx, orgID, ticket, contact, sessionName); err != nil {
			log.Printf("[Pix] auto reply: %v", err)
		} else if err := s.repo.MarkPixAutoReplySent(orgID, ticket.ID, time.Now()); err != nil {
			log.Printf("[Pix] mark auto reply: %v", err)
		}
	}
	s.emitTicket(orgID, ticket.ID, contact.ID)
	return nil
}

func (s *PixQueueService) sendAutoReply(ctx context.Context, orgID string, ticket model.PixTicket, contact model.PixContact, sessionName string) error {
	chatID, err := whatsapp.ChatID(contact.WhatsAppJID)
	if err != nil {
		return err
	}
	waID, err := s.waha.SendText(ctx, sessionName, chatID, AutoReplyUnsupported)
	if err != nil {
		return err
	}
	if waID == "" {
		waID = "waha-local-" + randomHex(8)
	}
	_, _, err = s.repo.AddPixMessage(model.PixMessage{
		OrgID:             orgID,
		TicketID:          ticket.ID,
		ContactID:         contact.ID,
		WhatsAppMessageID: waID,
		Direction:         model.PixDirectionOutbound,
		Type:              model.PixTypeText,
		Body:              AutoReplyUnsupported,
		CreatedAt:         pixNow(time.Time{}),
	})
	return err
}

func (s *PixQueueService) ensurePendingTicket(orgID string, contactID int64, at time.Time) (model.PixTicket, error) {
	ticket, err := s.repo.GetPendingPixTicket(orgID, contactID)
	if err == nil {
		return ticket, nil
	}
	if !errors.Is(err, model.ErrPixNotFound) {
		return model.PixTicket{}, err
	}
	id, err := s.repo.CreatePixTicket(orgID, contactID, at)
	if err != nil {
		return model.PixTicket{}, err
	}
	ticket, err = s.repo.GetPixTicket(orgID, id)
	if err != nil {
		return model.PixTicket{}, err
	}
	return ticket, nil
}

// --- Fila / conversa ---

// ListTickets lists pending or answered tickets. With the value gate on,
// pending tickets that only hold partial payments stay hidden until the sum
// matches an accepted value.
func (s *PixQueueService) ListTickets(orgID, status string, limit int) ([]model.PixTicket, error) {
	items, err := s.repo.ListPixTickets(orgID, status, limit)
	if err != nil {
		return nil, err
	}
	s.backfillContactNames(orgID, items)
	if status != model.PixTicketPending || len(items) == 0 {
		return items, nil
	}
	accepted, err := s.repo.ListPixValueRules(orgID)
	if err != nil {
		log.Printf("[Pix] value rules (%s): %v", orgID, err)
		return items, nil // fail-open: never hide the queue over a config read
	}
	if len(accepted) == 0 {
		return items, nil
	}
	filtered := make([]model.PixTicket, 0, len(items))
	for _, t := range items {
		if pixTicketVisible(t, accepted) {
			filtered = append(filtered, t)
		}
	}
	return filtered, nil
}

// pixTicketVisible reports whether a pending ticket may show up while the value
// gate is on: a client that wrote text, a fail-open receipt with no extracted
// value, or a total that equals/exceeds an accepted amount.
func pixTicketVisible(t model.PixTicket, accepted []int64) bool {
	if t.HasInboundText || t.HasUnextractedReceipt {
		return true
	}
	if t.PaidTotalCents <= 0 {
		return false
	}
	for _, a := range accepted {
		if a == t.PaidTotalCents {
			return true
		}
	}
	return t.PaidTotalCents > accepted[len(accepted)-1]
}

// backfillContactNames resolves display names for contacts stored without one
// (WhatsApp delivers @lid senders with no pushName in the webhook). Each JID is
// attempted at most once per process so listing the queue never hammers WAHA;
// WAHA gives back the saved contact name when the number is in the operator's
// address book and otherwise the WhatsApp profile name. A @lid phone number is
// never available — that is WhatsApp's privacy rule, not our omission.
func (s *PixQueueService) backfillContactNames(orgID string, items []model.PixTicket) {
	if s.waha == nil {
		return
	}
	session := whatsapp.SessionNameForOrg(orgID)
	for i := range items {
		c := &items[i].Contact
		if c.PushName != "" || c.WhatsAppJID == "" {
			continue
		}
		key := orgID + "|" + c.WhatsAppJID
		s.nameLookupMu.Lock()
		tried := s.nameTried[key]
		if !tried {
			if s.nameTried == nil {
				s.nameTried = make(map[string]bool)
			}
			s.nameTried[key] = true
		}
		s.nameLookupMu.Unlock()
		if tried {
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		found, err := s.waha.Contact(ctx, session, c.WhatsAppJID)
		cancel()
		if err != nil {
			log.Printf("[Pix] contact backfill (%s): %v", c.WhatsAppJID, err)
			continue
		}
		name := found.DisplayName()
		if name == "" {
			continue
		}
		if err := s.repo.SetPixContactName(orgID, c.ID, name); err != nil {
			log.Printf("[Pix] save contact name (%s): %v", c.WhatsAppJID, err)
			continue
		}
		c.PushName = name
	}
}

// ListPixValues returns the owner's accepted PIX amounts in cents.
func (s *PixQueueService) ListPixValues(orgID string) ([]int64, error) {
	return s.repo.ListPixValueRules(orgID)
}

// ReplacePixValues validates and replaces the owner's accepted amounts. The
// values are plain configuration: they can be edited even while WAHA is down.
func (s *PixQueueService) ReplacePixValues(orgID string, cents []int64) error {
	seen := make(map[int64]bool, len(cents))
	unique := make([]int64, 0, len(cents))
	for _, c := range cents {
		if c < 1 || c > 999_999_999 {
			return fmt.Errorf("valor fora do limite (R$ 0,01 a R$ 9.999.999,99)")
		}
		if !seen[c] {
			seen[c] = true
			unique = append(unique, c)
		}
	}
	if len(unique) > maxPixValuesPerOrg {
		return fmt.Errorf("máximo de %d valores", maxPixValuesPerOrg)
	}
	sort.Slice(unique, func(i, j int) bool { return unique[i] < unique[j] })
	if err := s.repo.ReplacePixValueRules(strings.TrimSpace(orgID), unique); err != nil {
		return err
	}
	s.emit(orgID, "pix-ticket-update", map[string]any{"orgId": orgID})
	return nil
}

// GetTicket returns one ticket (ownership enforced).
func (s *PixQueueService) GetTicket(orgID string, id int64) (model.PixTicket, error) {
	return s.repo.GetPixTicket(orgID, id)
}

// ListMessages returns a ticket's messages.
func (s *PixQueueService) ListMessages(orgID string, ticketID int64, limit int) ([]model.PixMessage, error) {
	return s.repo.ListPixMessages(orgID, ticketID, limit)
}

// ContactHistory lists the answered tickets of one contact.
func (s *PixQueueService) ContactHistory(orgID string, contactID int64, limit int) ([]model.PixTicket, error) {
	return s.repo.ListPixContactHistory(orgID, contactID, limit)
}

// SendText sends an operator text reply. Media is not allowed by design.
func (s *PixQueueService) SendText(ctx context.Context, orgID string, ticketID int64, body string) (int64, error) {
	if !s.Enabled() {
		return 0, ErrPixDisabled
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return 0, fmt.Errorf("mensagem vazia")
	}
	if len([]rune(body)) > 4000 {
		return 0, fmt.Errorf("mensagem excede 4000 caracteres")
	}

	ticket, err := s.repo.GetPixTicket(orgID, ticketID)
	if err != nil {
		return 0, err
	}
	if ticket.Status != model.PixTicketPending {
		return 0, model.ErrPixInvalidTransition
	}
	chatID, err := whatsapp.ChatID(ticket.Contact.WhatsAppJID)
	if err != nil {
		return 0, err
	}
	session := whatsapp.SessionNameForOrg(orgID)
	waID, err := s.waha.SendText(ctx, session, chatID, body)
	if err != nil {
		return 0, err
	}
	if waID == "" {
		waID = "waha-local-" + randomHex(8)
	}
	id, _, err := s.repo.AddPixMessage(model.PixMessage{
		OrgID:             orgID,
		TicketID:          ticket.ID,
		ContactID:         ticket.ContactID,
		WhatsAppMessageID: waID,
		Direction:         model.PixDirectionOutbound,
		Type:              model.PixTypeText,
		Body:              body,
		CreatedAt:         pixNow(time.Time{}),
	})
	if err != nil {
		return 0, err
	}
	s.emitMessage(orgID, ticket.ID, id)
	return id, nil
}

// Answer closes a ticket of the organization and moves the number to the
// history, recording the member (answeredBy) who closed it.
func (s *PixQueueService) Answer(orgID, answeredBy string, ticketID int64) error {
	if err := s.repo.MarkPixTicketAnswered(orgID, ticketID, answeredBy, time.Now()); err != nil {
		return err
	}
	s.emitTicket(orgID, ticketID, 0)
	return nil
}

// --- Mídia ---

// Media returns the receipt bytes and its content type.
func (s *PixQueueService) Media(ctx context.Context, orgID string, messageID int64) ([]byte, string, string, error) {
	msg, err := s.repo.GetPixMessageForMedia(orgID, messageID)
	if err != nil {
		return nil, "", "", err
	}
	if msg.MediaPath == "" {
		return nil, "", "", model.ErrPixNotFound
	}
	if msg.MediaDeletedAt != "" {
		return nil, "", "", model.ErrPixMediaGone
	}
	data, err := s.store.Get(ctx, msg.MediaPath)
	if err != nil {
		if errors.Is(err, media.ErrObjectNotFound) {
			_ = s.repo.MarkPixMediaDeleted(orgID, msg.ID, time.Now(), "missing")
			return nil, "", "", model.ErrPixMediaGone
		}
		return nil, "", "", err
	}
	return data, msg.MediaMime, msg.MediaFilename, nil
}

// --- Expurgo ---

// PurgeOrgMedia deletes all active receipts of one owner from the bucket.
func (s *PixQueueService) PurgeOrgMedia(ctx context.Context, orgID, reason string) (int, error) {
	if s == nil || orgID == "" {
		return 0, nil
	}
	lock := s.ownerLock(orgID)
	lock.Lock()
	defer lock.Unlock()

	removed := 0
	for {
		batch, err := s.repo.ListActivePixMedia(orgID, 100)
		if err != nil {
			return removed, err
		}
		if len(batch) == 0 {
			return removed, nil
		}
		for _, m := range batch {
			if err := s.storeDelete(ctx, m.MediaPath); err != nil {
				log.Printf("[Pix] purge media %d: %v", m.ID, err)
				continue
			}
			if err := s.repo.MarkPixMediaDeleted(orgID, m.ID, time.Now(), reason); err != nil {
				log.Printf("[Pix] mark purged media %d: %v", m.ID, err)
				continue
			}
			removed++
		}
		if len(batch) < 100 {
			return removed, nil
		}
	}
}

func (s *PixQueueService) ownerLock(owner string) *sync.Mutex {
	s.purgeMu.Lock()
	defer s.purgeMu.Unlock()
	l, ok := s.purgeLocks[owner]
	if !ok {
		l = &sync.Mutex{}
		s.purgeLocks[owner] = l
	}
	return l
}

func (s *PixQueueService) storePut(ctx context.Context, key string, data []byte, contentType string) error {
	if s.store == nil {
		return ErrPixDisabled
	}
	return s.store.Put(ctx, key, data, contentType)
}

func (s *PixQueueService) storeDelete(ctx context.Context, key string) error {
	if s.store == nil {
		return errors.New("storage unavailable")
	}
	return s.store.Delete(ctx, key)
}

func (s *PixQueueService) emitTicket(orgID string, ticketID, contactID int64) {
	s.emit(orgID, "pix-ticket-update", map[string]any{
		"orgId":     orgID,
		"ticketId":  ticketID,
		"contactId": contactID,
	})
}

func (s *PixQueueService) emitMessage(orgID string, ticketID, messageID int64) {
	s.emit(orgID, "pix-new-message", map[string]any{
		"orgId":     orgID,
		"ticketId":  ticketID,
		"messageId": messageID,
	})
}

func pixNow(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func logPix(format string, args ...any) {
	log.Printf("[Pix] "+format, args...)
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
