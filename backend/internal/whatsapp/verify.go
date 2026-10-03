package whatsapp

import (
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"strings"
)

// VerifyWebhookSignature checks WAHA's X-Webhook-Hmac header: HMAC-SHA512 (hex)
// of the raw body. An optional "sha512=" prefix is tolerated. Fail-closed.
func VerifyWebhookSignature(secret string, rawBody []byte, header string) bool {
	secret = strings.TrimSpace(secret)
	if secret == "" || len(rawBody) == 0 {
		return false
	}
	provided := strings.TrimSpace(header)
	if provided == "" {
		return false
	}
	provided = strings.TrimPrefix(provided, "sha512=")
	provided = strings.ToLower(provided)

	mac := hmac.New(sha512.New, []byte(secret))
	_, _ = mac.Write(rawBody)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(provided), []byte(expected))
}
