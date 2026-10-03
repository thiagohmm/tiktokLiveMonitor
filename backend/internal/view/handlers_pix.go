package view

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/thiagohmm/tiktok-live-monitor/internal/controller"
	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
	"github.com/thiagohmm/tiktok-live-monitor/internal/receipt"
	"github.com/thiagohmm/tiktok-live-monitor/internal/whatsapp"
)

// pixUser resolves the organization that owns the Fila PIX of the caller. The
// queue, the WhatsApp number and the receipts are shared by the organization's
// members and invisible to every other organization.
func (s *HTTPServer) pixUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	t, ok := requestTenant(w, r)
	if !ok {
		return "", false
	}
	return t.OrgID, true
}

// pixManager resolves the organization for the WhatsApp pairing endpoints
// (connect, QR, disconnect): registering or removing the organization's
// number is reserved to its owner; operators only work the queue.
func (s *HTTPServer) pixManager(w http.ResponseWriter, r *http.Request) (string, bool) {
	t, ok := requireOrgManager(w, r)
	if !ok {
		return "", false
	}
	return t.OrgID, true
}

func (s *HTTPServer) pixService(w http.ResponseWriter) (*controller.PixQueueService, bool) {
	svc := s.controller.GetPixQueueService()
	if svc == nil || !svc.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "fila pix indisponível")
		return nil, false
	}
	return svc, true
}

func (s *HTTPServer) handlePixWhatsAppConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	orgID, ok := s.pixManager(w, r)
	if !ok {
		return
	}
	svc, ok := s.pixService(w)
	if !ok {
		return
	}
	session, err := svc.Connect(r.Context(), orgID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "não foi possível iniciar a conexão com o WhatsApp")
		return
	}
	writeJSON(w, map[string]any{
		"sessionName": session.SessionName,
		"status":      session.Status,
	})
}

func (s *HTTPServer) handlePixWhatsAppQR(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	orgID, ok := s.pixManager(w, r)
	if !ok {
		return
	}
	svc, ok := s.pixService(w)
	if !ok {
		return
	}
	png, err := svc.QR(r.Context(), orgID)
	if err != nil {
		switch {
		case errors.Is(err, controller.ErrPixAlreadyPaired):
			writePixQRState(w, http.StatusConflict, "already_connected")
		case errors.Is(err, controller.ErrPixQRPending):
			// WAHA accepted the start but has no QR yet: a wait, not a failure.
			writePixQRState(w, http.StatusAccepted, "qr_pending")
		default:
			log.Printf("[View] pix qr (%s): %v", orgID, err)
			writePixQRState(w, http.StatusBadGateway, "qr_failed")
		}
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(png); err != nil {
		log.Printf("[View] pix qr write: %v", err)
	}
}

func (s *HTTPServer) handlePixWhatsAppStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	orgID, ok := s.pixUser(w, r)
	if !ok {
		return
	}
	svc, ok := s.pixService(w)
	if !ok {
		return
	}
	status, err := svc.Status(r.Context(), orgID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "não foi possível consultar o WhatsApp")
		return
	}
	writeJSON(w, status)
}

func (s *HTTPServer) handlePixWhatsAppDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	orgID, ok := s.pixManager(w, r)
	if !ok {
		return
	}
	svc, ok := s.pixService(w)
	if !ok {
		return
	}
	if err := svc.Disconnect(r.Context(), orgID); err != nil {
		writeInternalError(w, r, err)
		return
	}
	writeJSON(w, map[string]bool{"success": true})
}

func (s *HTTPServer) handlePixTickets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	orgID, ok := s.pixUser(w, r)
	if !ok {
		return
	}
	svc, ok := s.pixService(w)
	if !ok {
		return
	}
	status := r.URL.Query().Get("status")
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	items, err := svc.ListTickets(orgID, status, limit)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	if items == nil {
		items = []model.PixTicket{}
	}
	writeJSON(w, items)
}

// handlePixTicketSubtree dispatches /api/pix/tickets/{id}, /messages and /answer.
func (s *HTTPServer) handlePixTicketSubtree(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/pix/tickets/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.handlePixTicketGet(w, r, id)
		return
	}

	switch parts[1] {
	case "messages":
		s.handlePixTicketMessages(w, r, id)
	case "answer":
		s.handlePixTicketAnswer(w, r, id)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

func (s *HTTPServer) handlePixTicketGet(w http.ResponseWriter, r *http.Request, id int64) {
	orgID, ok := s.pixUser(w, r)
	if !ok {
		return
	}
	svc, ok := s.pixService(w)
	if !ok {
		return
	}
	ticket, err := svc.GetTicket(orgID, id)
	if err != nil {
		writePixError(w, r, err)
		return
	}
	writeJSON(w, ticket)
}

func (s *HTTPServer) handlePixTicketMessages(w http.ResponseWriter, r *http.Request, id int64) {
	orgID, ok := s.pixUser(w, r)
	if !ok {
		return
	}
	svc, ok := s.pixService(w)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		limit := 200
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 {
				limit = n
			}
		}
		items, err := svc.ListMessages(orgID, id, limit)
		if err != nil {
			writePixError(w, r, err)
			return
		}
		if items == nil {
			items = []model.PixMessage{}
		}
		writeJSON(w, items)
	case http.MethodPost:
		var body struct {
			Body string `json:"body"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body")
			return
		}
		msgID, err := svc.SendText(r.Context(), orgID, id, body.Body)
		if err != nil {
			writePixSendError(w, r, err)
			return
		}
		writeJSON(w, map[string]any{"success": true, "messageId": msgID})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *HTTPServer) handlePixTicketAnswer(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	t, ok := requestTenant(w, r)
	if !ok {
		return
	}
	svc, ok := s.pixService(w)
	if !ok {
		return
	}
	if err := svc.Answer(t.OrgID, t.UserID, id); err != nil {
		writePixError(w, r, err)
		return
	}
	writeJSON(w, map[string]bool{"success": true})
}

func (s *HTTPServer) handlePixContactHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/pix/contacts/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 || parts[1] != "history" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	contactID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || contactID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	orgID, ok := s.pixUser(w, r)
	if !ok {
		return
	}
	svc, ok := s.pixService(w)
	if !ok {
		return
	}
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	items, err := svc.ContactHistory(orgID, contactID, limit)
	if err != nil {
		writeInternalError(w, r, err)
		return
	}
	if items == nil {
		items = []model.PixTicket{}
	}
	writeJSON(w, items)
}

func (s *HTTPServer) handlePixMedia(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/pix/media/"), "/")
	id, err := strconv.ParseInt(rest, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	orgID, ok := s.pixUser(w, r)
	if !ok {
		return
	}
	svc, ok := s.pixService(w)
	if !ok {
		return
	}
	data, contentType, filename, err := svc.Media(r.Context(), orgID, id)
	if err != nil {
		writePixError(w, r, err)
		return
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; sandbox")
	w.Header().Set("Content-Disposition", "inline; filename=\""+safeFilename(filename)+"\"")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(data); err != nil {
		log.Printf("[View] pix media write: %v", err)
	}
}

// handlePixValues reads/replaces the PIX amounts the organization accepts. The
// frontend sends human strings ("15,00"); the API stores/serves cents.
func (s *HTTPServer) handlePixValues(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.pixUser(w, r)
	if !ok {
		return
	}
	svc, ok := s.pixService(w)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		values, err := svc.ListPixValues(orgID)
		if err != nil {
			writeInternalError(w, r, err)
			return
		}
		if values == nil {
			values = []int64{}
		}
		writeJSON(w, map[string]any{"values": values})
	case http.MethodPut:
		var body struct {
			Values []string `json:"values"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid body")
			return
		}
		cents := make([]int64, 0, len(body.Values))
		for _, raw := range body.Values {
			if strings.TrimSpace(raw) == "" {
				continue
			}
			v, ok := receipt.ParseBRLCents(raw)
			if !ok {
				writeError(w, http.StatusBadRequest, "valor inválido: use o formato 15,00")
				return
			}
			cents = append(cents, v)
		}
		if err := svc.ReplacePixValues(orgID, cents); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "máximo") ||
				strings.Contains(strings.ToLower(err.Error()), "limite") {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeInternalError(w, r, err)
			return
		}
		writeJSON(w, map[string]bool{"success": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleWhatsAppWebhook receives WAHA events. It is public (authenticated by webhook signature)
// but fail-closed: without a valid HMAC-SHA512 signature it returns 401.
func (s *HTTPServer) handleWhatsAppWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	svc := s.controller.GetPixQueueService()
	if svc == nil || !svc.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "whatsapp webhook indisponível")
		return
	}
	secret := svc.WebhookSecret()
	if secret == "" {
		writeError(w, http.StatusServiceUnavailable, "webhook sem segredo configurado")
		return
	}

	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if !whatsapp.VerifyWebhookSignature(secret, raw, r.Header.Get("X-Webhook-Hmac")) {
		writeError(w, http.StatusUnauthorized, "assinatura inválida")
		return
	}

	msg, ok, err := whatsapp.ParseInbound(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "payload inválido")
		return
	}
	if !ok {
		writeJSON(w, map[string]any{"received": true, "ignored": true})
		return
	}
	if err := svc.HandleInbound(r.Context(), msg); err != nil {
		if errors.Is(err, controller.ErrPixDisabled) {
			writeError(w, http.StatusServiceUnavailable, "fila pix indisponível")
			return
		}
		log.Printf("[View] whatsapp webhook: %v", err)
		writeError(w, http.StatusInternalServerError, "erro ao processar mensagem")
		return
	}
	writeJSON(w, map[string]any{"received": true})
}

// handleMonitoringAttach registers the user as a watcher of a live.
func (s *HTTPServer) handleMonitoringAttach(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	t, ok := requestTenant(w, r)
	if !ok {
		return
	}
	if err := s.controller.AttachMonitoring(r.Context(), t.OrgID, t.UserID, body.Username); err != nil {
		writeMonitorError(w, r, err)
		return
	}
	writeJSON(w, map[string]bool{"success": true})
}

// handleBeaconDisconnect is the pagehide beacon: detach the authenticated user
// from their lives (the organization's receipts are purged only when none of
// its members watches a live anymore).
func (s *HTTPServer) handleBeaconDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	t, ok := requestTenant(w, r)
	if !ok {
		return
	}
	var body struct {
		Username string `json:"username"`
	}
	// The beacon body is best-effort: an empty/invalid payload still detaches all.
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body)
	s.controller.DetachMonitoring(t.OrgID, t.UserID, body.Username)
	w.WriteHeader(http.StatusNoContent)
}

// safeFilename keeps a receipt filename printable in a header: only ASCII
// letters, digits, dot, dash and underscore survive (at most 100 characters).
func safeFilename(name string) string {
	var b strings.Builder
	for _, r := range name {
		if b.Len() >= 100 {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if strings.Trim(b.String(), "._") == "" {
		return "comprovante"
	}
	return b.String()
}

// writePixQRState reports why there is no QR yet. The frontend owns the wording:
// a pending session must not look like a broken one.
func writePixQRState(w http.ResponseWriter, status int, state string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"state": state})
}

func writePixError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, model.ErrPixNotFound):
		writeError(w, http.StatusNotFound, "não encontrado")
	case errors.Is(err, model.ErrPixForbidden):
		writeError(w, http.StatusForbidden, "acesso negado")
	case errors.Is(err, model.ErrPixMediaGone):
		writeError(w, http.StatusGone, "comprovante removido")
	default:
		writeInternalError(w, r, err)
	}
}

func writePixSendError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, model.ErrPixInvalidTransition):
		writeError(w, http.StatusConflict, "atendimento já respondido")
	case errors.Is(err, controller.ErrPixDisabled):
		writeError(w, http.StatusServiceUnavailable, "fila pix indisponível")
	default:
		// Validation errors (empty body/too long) are client errors.
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "vazia") || strings.Contains(msg, "caracteres") || strings.Contains(msg, "destination") {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeInternalError(w, r, err)
	}
}
