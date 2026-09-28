package whatsapp

import (
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"testing"
)

func sign(secret, body string) string {
	mac := hmac.New(sha512.New, []byte(secret))
	_, _ = mac.Write([]byte(body))
	return hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyWebhookSignature(t *testing.T) {
	secret := "topsecret"
	body := []byte(`{"event":"message"}`)
	valid := sign(secret, string(body))

	if !VerifyWebhookSignature(secret, body, valid) {
		t.Fatal("valid signature rejected")
	}
	if !VerifyWebhookSignature(secret, body, "sha512="+valid) {
		t.Fatal("prefixed signature rejected")
	}
	if VerifyWebhookSignature(secret, body, "deadbeef") {
		t.Fatal("invalid signature accepted")
	}
	if VerifyWebhookSignature(secret, body, "") {
		t.Fatal("missing signature accepted")
	}
	if VerifyWebhookSignature("", body, valid) {
		t.Fatal("empty secret accepted")
	}
	if VerifyWebhookSignature(secret, []byte(`{"event":"other"}`), valid) {
		t.Fatal("tampered body accepted")
	}
}
