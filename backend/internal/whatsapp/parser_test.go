package whatsapp

import (
	"testing"
	"time"
)

func TestParseInboundText(t *testing.T) {
	raw := []byte(`{
		"event": "message",
		"session": "pix_abc",
		"me": {"id": "5511999990000@c.us"},
		"payload": {
			"id": "false_5511888887777@c.us_ABCD",
			"from": "5511888887777@c.us",
			"fromMe": false,
			"body": "segue o comprovante",
			"hasMedia": false,
			"timestamp": 1667561485,
			"pushName": "Fulano"
		}
	}`)

	msg, ok, err := ParseInbound(raw)
	if err != nil || !ok {
		t.Fatalf("expected inbound message, ok=%v err=%v", ok, err)
	}
	if msg.Type != "text" || msg.Body != "segue o comprovante" {
		t.Fatalf("unexpected message: %+v", msg)
	}
	if msg.PhoneE164 != "5511888887777" || msg.JID != "5511888887777@c.us" {
		t.Fatalf("unexpected destination: %+v", msg)
	}
	if !msg.Timestamp.Equal(time.Unix(1667561485, 0).UTC()) {
		t.Fatalf("unexpected timestamp: %v", msg.Timestamp)
	}
	if msg.Session != "pix_abc" || msg.MeJID != "5511999990000@c.us" {
		t.Fatalf("unexpected session data: %+v", msg)
	}
}

func TestParseInboundIgnoresFromMeGroupAndAck(t *testing.T) {
	cases := map[string]string{
		"fromMe": `{"event":"message","payload":{"fromMe":true,"from":"5511@c.us","body":"x"}}`,
		"group":  `{"event":"message","payload":{"from":"12345@g.us","body":"x"}}`,
		"ack":    `{"event":"message.ack","payload":{"id":"x","ackName":"READ"}}`,
	}
	for name, raw := range cases {
		if _, ok, err := ParseInbound([]byte(raw)); err != nil || ok {
			t.Fatalf("%s: expected ignore, ok=%v err=%v", name, ok, err)
		}
	}
}

func TestParseInboundMediaTypes(t *testing.T) {
	jpeg := `{"event":"message","payload":{"id":"1","from":"5511@c.us","hasMedia":true,
		"media":{"url":"http://waha:3000/api/files/abc.jpeg","mimetype":"image/jpeg","filename":"comp.jpg"}}}`
	msg, ok, err := ParseInbound([]byte(jpeg))
	if err != nil || !ok || msg.Type != "image" || msg.MediaFilename != "comp.jpg" {
		t.Fatalf("jpeg: %+v ok=%v err=%v", msg, ok, err)
	}

	pdf := `{"event":"message","payload":{"id":"2","from":"5511@c.us","hasMedia":true,
		"media":{"url":"http://waha:3000/api/files/a.pdf","mimetype":"application/pdf"}}}`
	msg, ok, err = ParseInbound([]byte(pdf))
	if err != nil || !ok || msg.Type != "document" || msg.MediaFilename != "a.pdf" {
		t.Fatalf("pdf: %+v ok=%v err=%v", msg, ok, err)
	}

	audio := `{"event":"message","payload":{"id":"3","from":"5511@c.us","hasMedia":true,
		"media":{"url":"http://waha:3000/api/files/a.ogg","mimetype":"audio/ogg"}}}`
	msg, ok, err = ParseInbound([]byte(audio))
	if err != nil || !ok || msg.Type != "unsupported" {
		t.Fatalf("audio: %+v ok=%v err=%v", msg, ok, err)
	}
}

func TestParseInboundLidAndTimestamps(t *testing.T) {
	raw := `{"event":"message","payload":{"id":"1","from":"998877@lid","body":"oi",
		"timestamp":1667561485853}}`
	msg, ok, err := ParseInbound([]byte(raw))
	if err != nil || !ok {
		t.Fatalf("lid: ok=%v err=%v", ok, err)
	}
	if msg.JID != "998877@lid" || msg.PhoneE164 != "" {
		t.Fatalf("unexpected lid: %+v", msg)
	}
	if !msg.Timestamp.Equal(time.UnixMilli(1667561485853).UTC()) {
		t.Fatalf("unexpected ms timestamp: %v", msg.Timestamp)
	}
}

func TestParseInboundLocationBody(t *testing.T) {
	raw := `{"event":"message","payload":{"id":"1","from":"5511@c.us",
		"location":{"latitude":-23.5,"longitude":-46.6},"body":"<base64 gigante>"}}`
	msg, ok, err := ParseInbound([]byte(raw))
	if err != nil || !ok {
		t.Fatalf("location: ok=%v err=%v", ok, err)
	}
	if msg.Type != "unsupported" || msg.Body != "https://www.google.com/maps?q=-23.5,-46.6" {
		t.Fatalf("unexpected location: %+v", msg)
	}
}

func TestSessionNameForOrg(t *testing.T) {
	if got := SessionNameForOrg("A1B2-C3D4"); got != "pix_a1b2c3d4" {
		t.Fatalf("session name: %q", got)
	}
	if got := SessionNameForOrg("  "); got != "" {
		t.Fatalf("empty owner should yield empty session, got %q", got)
	}
}

func TestChatID(t *testing.T) {
	got, err := ChatID("+55 (11) 98888-7777")
	if err != nil || got != "5511988887777@c.us" {
		t.Fatalf("chatID: %q err=%v", got, err)
	}
	got, err = ChatID("998877@lid")
	if err != nil || got != "998877@lid" {
		t.Fatalf("chatID lid: %q err=%v", got, err)
	}
	if _, err := ChatID(""); err == nil {
		t.Fatal("empty destination should fail")
	}
}

func TestExtractMessageID(t *testing.T) {
	cases := []struct {
		payload any
		want    string
	}{
		{map[string]any{"id": "a"}, "a"},
		{map[string]any{"_data": map[string]any{"id": "b"}}, "b"},
		{map[string]any{"key": map[string]any{"id": "c"}}, "c"},
		{map[string]any{"data": map[string]any{"id": "d"}}, "d"},
		{map[string]any{"nope": true}, ""},
	}
	for _, c := range cases {
		if got := extractMessageID(c.payload); got != c.want {
			t.Fatalf("extractMessageID(%v) = %q, want %q", c.payload, got, c.want)
		}
	}
}
