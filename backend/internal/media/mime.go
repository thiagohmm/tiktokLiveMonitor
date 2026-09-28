package media

import (
	"bytes"
	"strings"

	"github.com/thiagohmm/tiktok-live-monitor/internal/model"
)

// Receipt is a validated inbound PIX receipt.
type Receipt struct {
	Type        string // model.PixTypeImage ou model.PixTypeDocument
	Extension   string // "jpg" ou "pdf"
	ContentType string // image/jpeg ou application/pdf
}

var (
	jpegMagic = []byte{0xFF, 0xD8, 0xFF}
	pdfMagic  = []byte("%PDF-")
)

// DetectReceipt validates a receipt by magic bytes. The declared mimetype is
// never trusted alone. Only JPG and PDF are accepted, mirroring the requirement.
func DetectReceipt(data []byte) (Receipt, bool) {
	switch {
	case bytes.HasPrefix(data, jpegMagic):
		return Receipt{Type: model.PixTypeImage, Extension: "jpg", ContentType: "image/jpeg"}, true
	case bytes.HasPrefix(data, pdfMagic):
		return Receipt{Type: model.PixTypeDocument, Extension: "pdf", ContentType: "application/pdf"}, true
	default:
		return Receipt{}, false
	}
}

// SanitizeFilename keeps only the base name and a safe subset of characters, so
// a client-provided name can never influence the stored object key.
func SanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '-', r == '_', r == ' ':
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	if len(out) > 120 {
		out = out[:120]
	}
	return out
}

// ObjectKey builds the storage key for one message media.
func ObjectKey(orgID, messageUUID, extension string) string {
	return "pix-media/" + strings.TrimSpace(orgID) + "/" + messageUUID + "." + extension
}
