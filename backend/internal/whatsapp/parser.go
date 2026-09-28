package whatsapp

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

// InboundMessage is a normalized WAHA message delivered by the webhook.
type InboundMessage struct {
	MessageID     string
	Session       string
	MeJID         string
	JID           string // canonical destination JID (digits@c.us or local@lid)
	PhoneE164     string // digits only; empty for unresolved @lid
	PushName      string
	Timestamp     time.Time
	Type          string
	Body          string
	MediaURL      string
	MediaMime     string
	MediaFilename string
}

// messageEvents are the WAHA events carrying inbound messages.
var messageEvents = map[string]bool{"message": true, "message.any": true}

// ParseInbound normalizes a WAHA webhook envelope. ok=false means the event is
// not an actionable inbound message (ack, fromMe echo, group or unknown event).
func ParseInbound(raw []byte) (InboundMessage, bool, error) {
	var envelope map[string]any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&envelope); err != nil {
		return InboundMessage{}, false, fmt.Errorf("parse wa envelope: %w", err)
	}

	event := asString(envelope["event"])
	if !messageEvents[event] {
		return InboundMessage{}, false, nil
	}
	payload, _ := envelope["payload"].(map[string]any)
	if payload == nil {
		return InboundMessage{}, false, nil
	}
	if b, ok := payload["fromMe"].(bool); ok && b {
		return InboundMessage{}, false, nil
	}
	from := asString(payload["from"])
	jid, phone, ok := NormalizeJID(from)
	if !ok {
		return InboundMessage{}, false, nil
	}

	meJID := ""
	if me, ok := envelope["me"].(map[string]any); ok {
		meJID = asString(me["id"])
	}

	// Location messages carry coordinates; the body holds a huge base64
	// thumbnail that must never be persisted.
	body := asString(payload["body"])
	if location, ok := payload["location"].(map[string]any); ok {
		lat := asString(location["latitude"])
		lon := asString(location["longitude"])
		if lat != "" && lon != "" {
			body = "https://www.google.com/maps?q=" + lat + "," + lon
		}
	}

	mediaURL, mediaMime, mediaFilename := "", "", ""
	if media, ok := payload["media"].(map[string]any); ok {
		mediaURL = asString(media["url"])
		mediaMime = strings.ToLower(asString(media["mimetype"]))
		mediaFilename = asString(media["filename"])
		if mediaFilename == "" {
			mediaFilename = filenameFromURL(mediaURL)
		}
	}

	msgType := deriveType(payload, mediaMime, mediaURL, body)

	return InboundMessage{
		MessageID:     asString(payload["id"]),
		Session:       asString(envelope["session"]),
		MeJID:         meJID,
		JID:           jid,
		PhoneE164:     phone,
		PushName:      pushName(payload),
		Timestamp:     epochSeconds(payload["timestamp"]),
		Type:          msgType,
		Body:          body,
		MediaURL:      mediaURL,
		MediaMime:     mediaMime,
		MediaFilename: mediaFilename,
	}, true, nil
}

// NormalizeJID strips the WAHA JID domain into a canonical destination.
// Group JIDs are rejected; @lid keeps its domain because it carries no number.
func NormalizeJID(raw string) (jid, phone string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}
	at := strings.Index(raw, "@")
	if at < 0 {
		digits := digitsOnly(raw)
		if digits == "" {
			return "", "", false
		}
		return digits + "@c.us", digits, true
	}
	local := raw[:at]
	domain := strings.ToLower(raw[at+1:])
	switch domain {
	case "g.us":
		return "", "", false
	case "lid":
		if strings.TrimSpace(local) == "" {
			return "", "", false
		}
		return local + "@lid", "", true
	default:
		digits := digitsOnly(local)
		if digits == "" {
			return "", "", false
		}
		return digits + "@c.us", digits, true
	}
}

func deriveType(payload map[string]any, mime, mediaURL, body string) string {
	if _, isLocation := payload["location"].(map[string]any); isLocation {
		return model.PixTypeUnsupported
	}
	hasMedia, _ := payload["hasMedia"].(bool)
	if !hasMedia && mediaURL == "" {
		return model.PixTypeText
	}
	switch mime {
	case "image/jpeg", "image/jpg":
		return model.PixTypeImage
	case "application/pdf":
		return model.PixTypeDocument
	default:
		return model.PixTypeUnsupported
	}
}

func pushName(payload map[string]any) string {
	for _, key := range []string{"pushName", "notifyName", "senderName"} {
		if v := asString(payload[key]); v != "" {
			return v
		}
	}
	if data, ok := payload["_data"].(map[string]any); ok {
		if v := asString(data["notifyName"]); v != "" {
			return v
		}
	}
	return ""
}

func asString(v any) string {
	switch n := v.(type) {
	case string:
		return strings.TrimSpace(n)
	case json.Number:
		return n.String()
	case float64:
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.0f", n), "0"), ".")
	default:
		return ""
	}
}

func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func filenameFromURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	name := path.Base(u.Path)
	if name == "." || name == "/" {
		return ""
	}
	return name
}

// epochSeconds normalizes WAHA's mixed timestamps (s, ms or float) to time.Time.
func epochSeconds(v any) time.Time {
	var raw float64
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return time.Time{}
		}
		raw = f
	case float64:
		raw = n
	case string:
		if strings.TrimSpace(n) == "" {
			return time.Time{}
		}
		f, err := parseFloat(n)
		if err != nil {
			return time.Time{}
		}
		raw = f
	default:
		return time.Time{}
	}
	if raw <= 0 {
		return time.Time{}
	}
	if raw > 1e12 {
		return time.UnixMilli(int64(math.Floor(raw))).UTC()
	}
	return time.Unix(int64(math.Floor(raw)), 0).UTC()
}

func parseFloat(s string) (float64, error) {
	var f float64
	err := json.Unmarshal([]byte(s), &f)
	return f, err
}
