package media

import "testing"

func TestDetectReceipt(t *testing.T) {
	jpg := append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, []byte("junk")...)
	r, ok := DetectReceipt(jpg)
	if !ok || r.Extension != "jpg" || r.Type != "image" {
		t.Fatalf("jpeg not detected: %+v ok=%v", r, ok)
	}

	pdf := []byte("%PDF-1.7\n...")
	r, ok = DetectReceipt(pdf)
	if !ok || r.Extension != "pdf" || r.Type != "document" {
		t.Fatalf("pdf not detected: %+v ok=%v", r, ok)
	}

	if _, ok := DetectReceipt([]byte("<html>")); ok {
		t.Fatal("html must not be accepted as receipt")
	}
	if _, ok := DetectReceipt(nil); ok {
		t.Fatal("empty payload must not be accepted")
	}
}

func TestSanitizeFilename(t *testing.T) {
	if got := SanitizeFilename("../../etc/passwd"); got != "passwd" {
		t.Fatalf("traversal not sanitized: %q", got)
	}
	if got := SanitizeFilename(`C:\tmp\comp rovante.jpg`); got != "comp rovante.jpg" {
		t.Fatalf("windows path not sanitized: %q", got)
	}
	if got := SanitizeFilename("comp*?<>.jpg"); got != "comp.jpg" {
		t.Fatalf("special chars not removed: %q", got)
	}
}

func TestObjectKey(t *testing.T) {
	got := ObjectKey("owner-1", "uuid-2", "pdf")
	if got != "pix-media/owner-1/uuid-2.pdf" {
		t.Fatalf("object key: %q", got)
	}
}
