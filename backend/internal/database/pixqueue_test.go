package database

import (
	"errors"
	"testing"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

func TestPixSessionUpsertAndLookup(t *testing.T) {
	db := openTestDB(t)

	row, err := db.UpsertPixSession("owner-a", "pix_ownera", model.PixSessionScanQR, "", "", nil)
	if err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if row.SessionName != "pix_ownera" || row.Status != model.PixSessionScanQR {
		t.Fatalf("unexpected session: %+v", row)
	}

	connected := time.Now()
	row, err = db.UpsertPixSession("owner-a", "pix_ownera", model.PixSessionConnected, "5511999990000", "5511999990000@c.us", &connected)
	if err != nil {
		t.Fatalf("update session: %v", err)
	}
	if row.Status != model.PixSessionConnected || row.MePhone != "5511999990000" || row.ConnectedAt == "" {
		t.Fatalf("session not updated: %+v", row)
	}
	if row.ID == 0 {
		t.Fatal("expected session id")
	}

	byName, err := db.GetPixSessionByName("pix_ownera")
	if err != nil || byName.OrgID != "owner-a" {
		t.Fatalf("lookup by name: %+v err=%v", byName, err)
	}
	if _, err := db.GetPixSessionByOrg("nobody"); !errors.Is(err, model.ErrPixNotFound) {
		t.Fatalf("expected ErrPixNotFound, got %v", err)
	}
}

func TestPixTicketLifecycleAndOwnership(t *testing.T) {
	db := openTestDB(t)
	now := time.Now()

	contact, err := db.UpsertPixContact("owner-a", "5511888887777", "5511888887777@c.us", "Fulano", now)
	if err != nil {
		t.Fatalf("upsert contact: %v", err)
	}
	if contact.ID == 0 {
		t.Fatal("expected contact id")
	}
	// Upsert same JID updates name and keeps one row.
	contact2, err := db.UpsertPixContact("owner-a", "5511888887777", "5511888887777@c.us", "Fulano 2", now.Add(time.Minute))
	if err != nil || contact2.ID != contact.ID || contact2.PushName != "Fulano 2" {
		t.Fatalf("contact upsert: %+v err=%v", contact2, err)
	}

	ticketID, err := db.CreatePixTicket("owner-a", contact.ID, now)
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	// Partial unique index keeps a single pending ticket per contact.
	again, err := db.CreatePixTicket("owner-a", contact.ID, now.Add(time.Second))
	if err != nil || again != ticketID {
		t.Fatalf("expected same pending ticket, got %d err=%v", again, err)
	}
	pending, err := db.GetPendingPixTicket("owner-a", contact.ID)
	if err != nil || pending.ID != ticketID || pending.Status != model.PixTicketPending {
		t.Fatalf("pending ticket: %+v err=%v", pending, err)
	}

	// Ownership: another owner cannot read the ticket.
	if _, err := db.GetPixTicket("owner-b", ticketID); !errors.Is(err, model.ErrPixNotFound) {
		t.Fatalf("expected ownership isolation, got %v", err)
	}

	if _, inserted, err := db.AddPixMessage(model.PixMessage{
		OrgID:             "owner-a",
		TicketID:          ticketID,
		ContactID:         contact.ID,
		WhatsAppMessageID: "msg-1",
		Direction:         model.PixDirectionInbound,
		Type:              model.PixTypeText,
		Body:              "oi",
	}); err != nil || !inserted {
		t.Fatalf("add message: inserted=%v err=%v", inserted, err)
	}
	// Dedup by (owner, whatsapp_message_id).
	if _, inserted, err := db.AddPixMessage(model.PixMessage{
		OrgID:             "owner-a",
		TicketID:          ticketID,
		ContactID:         contact.ID,
		WhatsAppMessageID: "msg-1",
		Direction:         model.PixDirectionInbound,
		Type:              model.PixTypeText,
		Body:              "duplicada",
	}); err != nil || inserted {
		t.Fatalf("expected dedup, inserted=%v err=%v", inserted, err)
	}

	msgs, err := db.ListPixMessages("owner-a", ticketID, 10)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("list messages: %v err=%v", len(msgs), err)
	}

	// Receipt + purge listing.
	if _, inserted, err := db.AddPixMessage(model.PixMessage{
		OrgID:             "owner-a",
		TicketID:          ticketID,
		ContactID:         contact.ID,
		WhatsAppMessageID: "msg-2",
		Direction:         model.PixDirectionInbound,
		Type:              model.PixTypeDocument,
		MediaPath:         "pix-media/owner-a/abc.pdf",
		MediaMime:         "application/pdf",
	}); err != nil || !inserted {
		t.Fatalf("add receipt: inserted=%v err=%v", inserted, err)
	}
	if err := db.MarkPixTicketReceipt("owner-a", ticketID, now); err != nil {
		t.Fatalf("mark receipt: %v", err)
	}
	active, err := db.ListActivePixMedia("owner-a", 10)
	if err != nil || len(active) != 1 {
		t.Fatalf("active media: %v err=%v", len(active), err)
	}
	if err := db.MarkPixMediaDeleted("owner-a", active[0].ID, now, "live disconnected"); err != nil {
		t.Fatalf("mark deleted: %v", err)
	}
	// Idempotent: second mark must not fail.
	if err := db.MarkPixMediaDeleted("owner-a", active[0].ID, now, "again"); err != nil {
		t.Fatalf("mark deleted twice: %v", err)
	}
	active, _ = db.ListActivePixMedia("owner-a", 10)
	if len(active) != 0 {
		t.Fatalf("expected no active media, got %d", len(active))
	}

	// Answer is idempotent and moves the ticket to history.
	if err := db.MarkPixTicketAnswered("owner-a", ticketID, "owner-a", now); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if err := db.MarkPixTicketAnswered("owner-a", ticketID, "owner-a", now); err != nil {
		t.Fatalf("answer twice: %v", err)
	}
	pendingList, err := db.ListPixTickets("owner-a", model.PixTicketPending, 10)
	if err != nil || len(pendingList) != 0 {
		t.Fatalf("pending after answer: %v err=%v", len(pendingList), err)
	}
	answered, err := db.ListPixContactHistory("owner-a", contact.ID, 10)
	if err != nil || len(answered) != 1 || answered[0].Contact.PhoneE164 != "5511888887777" {
		t.Fatalf("history: %+v err=%v", answered, err)
	}
	if !answered[0].HasReceipt {
		t.Fatal("expected has_receipt flag")
	}

	// A new message reopens a pending ticket.
	newTicket, err := db.CreatePixTicket("owner-a", contact.ID, now.Add(time.Hour))
	if err != nil || newTicket == ticketID {
		t.Fatalf("reopen ticket: id=%d err=%v", newTicket, err)
	}
}

func TestPixTicketFIFOOrder(t *testing.T) {
	db := openTestDB(t)
	base := time.Now()

	ids := make([]int64, 0, 3)
	for i, jid := range []string{"5511000000001@c.us", "5511000000002@c.us", "5511000000003@c.us"} {
		contact, err := db.UpsertPixContact("owner-a", "", jid, "c", base)
		if err != nil {
			t.Fatalf("contact: %v", err)
		}
		id, err := db.CreatePixTicket("owner-a", contact.ID, base.Add(time.Duration(i)*time.Second))
		if err != nil {
			t.Fatalf("ticket: %v", err)
		}
		ids = append(ids, id)
	}
	list, err := db.ListPixTickets("owner-a", model.PixTicketPending, 10)
	if err != nil || len(list) != 3 {
		t.Fatalf("list: %v err=%v", len(list), err)
	}
	for i, want := range ids {
		if list[i].ID != want {
			t.Fatalf("FIFO order: index %d = %d, want %d", i, list[i].ID, want)
		}
	}
}

func TestPixValueRulesRoundTrip(t *testing.T) {
	db := openTestDB(t)

	values, err := db.ListPixValueRules("owner-a")
	if err != nil || len(values) != 0 {
		t.Fatalf("fresh owner must have no rules: %v err=%v", values, err)
	}
	if err := db.ReplacePixValueRules("owner-a", []int64{1500, 1000, 1000}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	values, err = db.ListPixValueRules("owner-a")
	if err != nil || len(values) != 2 || values[0] != 1000 || values[1] != 1500 {
		t.Fatalf("round trip: %v err=%v", values, err)
	}
	// Replace is atomic: the second call wipes the first.
	if err := db.ReplacePixValueRules("owner-a", []int64{2000}); err != nil {
		t.Fatalf("replace again: %v", err)
	}
	values, _ = db.ListPixValueRules("owner-a")
	if len(values) != 1 || values[0] != 2000 {
		t.Fatalf("replace must wipe old rules: %v", values)
	}
	// Owners are isolated.
	other, err := db.ListPixValueRules("owner-b")
	if err != nil || len(other) != 0 {
		t.Fatalf("owner isolation broken: %v err=%v", other, err)
	}
	// Empty list clears everything.
	if err := db.ReplacePixValueRules("owner-a", nil); err != nil {
		t.Fatalf("clear: %v", err)
	}
	values, _ = db.ListPixValueRules("owner-a")
	if len(values) != 0 {
		t.Fatalf("expected empty, got %v", values)
	}
}

func TestPixReceiptValuesAndEmptyTicketDelete(t *testing.T) {
	db := openTestDB(t)
	now := time.Now()

	contact, err := db.UpsertPixContact("owner-a", "5511", "5511000009999@c.us", "F", now)
	if err != nil {
		t.Fatalf("contact: %v", err)
	}
	ticketID, err := db.CreatePixTicket("owner-a", contact.ID, now)
	if err != nil {
		t.Fatalf("ticket: %v", err)
	}

	// No receipts yet: total is zero, and the empty ticket can be deleted.
	if total, err := db.GetPixTicketPaidTotal("owner-a", ticketID); err != nil || total != 0 {
		t.Fatalf("initial total = %d err=%v", total, err)
	}

	if _, inserted, err := db.AddPixMessage(model.PixMessage{
		OrgID:             "owner-a",
		TicketID:          ticketID,
		ContactID:         contact.ID,
		WhatsAppMessageID: "val-1",
		Direction:         model.PixDirectionInbound,
		Type:              model.PixTypeImage,
		MediaPath:         "pix-media/owner-a/x.jpg",
		MediaMime:         "image/jpeg",
		MediaValueCents:   800,
	}); err != nil || !inserted {
		t.Fatalf("add receipt: inserted=%v err=%v", inserted, err)
	}
	// A receipt without an extracted value (fail-open) must not count.
	if _, _, err := db.AddPixMessage(model.PixMessage{
		OrgID:             "owner-a",
		TicketID:          ticketID,
		ContactID:         contact.ID,
		WhatsAppMessageID: "val-2",
		Direction:         model.PixDirectionInbound,
		Type:              model.PixTypeImage,
		MediaPath:         "pix-media/owner-a/y.jpg",
		MediaMime:         "image/jpeg",
	}); err != nil {
		t.Fatalf("add fail-open receipt: %v", err)
	}
	if total, err := db.GetPixTicketPaidTotal("owner-a", ticketID); err != nil || total != 800 {
		t.Fatalf("paid total = %d err=%v, want 800", total, err)
	}
	list, err := db.ListPixTickets("owner-a", model.PixTicketPending, 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v err=%v", len(list), err)
	}
	if list[0].PaidTotalCents != 800 {
		t.Fatalf("list paid total = %d, want 800", list[0].PaidTotalCents)
	}
	if !list[0].HasUnextractedReceipt {
		t.Fatal("fail-open receipt must mark the ticket as unextracted")
	}
	if list[0].HasInboundText {
		t.Fatal("no text was sent")
	}
	// The stored value must round-trip through the message listing.
	msgs, err := db.ListPixMessages("owner-a", ticketID, 10)
	if err != nil || len(msgs) != 2 {
		t.Fatalf("messages: %v err=%v", len(msgs), err)
	}
	var found bool
	for _, m := range msgs {
		if m.WhatsAppMessageID == "val-1" && m.MediaValueCents != 800 {
			t.Fatalf("message value lost: %+v", m)
		}
		if m.WhatsAppMessageID == "val-2" && m.MediaValueCents != 0 {
			t.Fatalf("missing value must scan as 0: %+v", m)
		}
		found = found || m.WhatsAppMessageID == "val-1"
	}
	if !found {
		t.Fatal("val-1 not listed")
	}

	// A ticket with messages cannot be deleted as "empty".
	if deleted, err := db.DeleteEmptyPixTicket("owner-a", ticketID); err != nil || deleted {
		t.Fatalf("ticket with messages must survive delete-empty: %v err=%v", deleted, err)
	}

	// A fresh pending ticket with no messages disappears.
	contact2, _ := db.UpsertPixContact("owner-a", "5522", "5522000009999@c.us", "G", now)
	ticket2, err := db.CreatePixTicket("owner-a", contact2.ID, now)
	if err != nil {
		t.Fatalf("ticket2: %v", err)
	}
	if deleted, err := db.DeleteEmptyPixTicket("owner-a", ticket2); err != nil || !deleted {
		t.Fatalf("empty ticket must be deleted: %v err=%v", deleted, err)
	}
	if _, err := db.GetPixTicket("owner-a", ticket2); !errors.Is(err, model.ErrPixNotFound) {
		t.Fatalf("expected gone, got %v", err)
	}
	// Answered tickets are never touched by the empty delete.
	if err := db.MarkPixTicketAnswered("owner-a", ticketID, "owner-a", now); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if deleted, err := db.DeleteEmptyPixTicket("owner-a", ticketID); err != nil || deleted {
		t.Fatalf("answered ticket must not be deleted: %v err=%v", deleted, err)
	}
}
