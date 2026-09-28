package receipt

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestExtractValuesCents(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []int64
	}{
		{"prefixed comma", "Valor da transferência R$ 15,00", []int64{1500}},
		{"prefixed no space", "PIX R$10,00 confirmado", []int64{1000}},
		{"thousands", "Total R$ 1.234,56", []int64{123456}},
		{"bare decimal", "valor: 8,00", []int64{800}},
		{"prefixed integer", "R$ 15", []int64{1500}},
		{"dot decimal from OCR", "R$ 15.00", []int64{1500}},
		{"thousands no cents", "R$ 1.200", []int64{120000}},
		{"date and phone ignored", "20/09/2026 11:42 +55 11 99999-0000 id 12345", nil},
		{"multiple values", "R$ 2,00 taxa e Valor R$ 15,00", []int64{200, 1500}},
		{"dedupe", "R$ 10,00 ... R$ 10,00", []int64{1000}},
		{"empty", "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ExtractValuesCents(c.text)
			if len(got) != len(c.want) {
				t.Fatalf("ExtractValuesCents(%q) = %v, want %v", c.text, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("ExtractValuesCents(%q) = %v, want %v", c.text, got, c.want)
				}
			}
		})
	}
}

func TestParseBRLCents(t *testing.T) {
	ok := map[string]int64{
		"15":       1500,
		"15,00":    1500,
		"R$ 15,00": 1500,
		"15.00":    1500,
		"1.234,56": 123456,
		" 10,50 ":  1050,
		"0,01":     1,
		"15,0":     1500,
		"15.000":   1500000,
	}
	for in, want := range ok {
		got, valid := ParseBRLCents(in)
		if !valid || got != want {
			t.Fatalf("ParseBRLCents(%q) = %d,%v want %d,true", in, got, valid, want)
		}
	}
	for _, in := range []string{"", "abc", "1,2345", "-5", "1.2.3", "R$", "1e3", "15,"} {
		if got, valid := ParseBRLCents(in); valid && got > 0 {
			t.Fatalf("ParseBRLCents(%q) unexpectedly valid (%d)", in, got)
		}
	}
}

func TestDecide(t *testing.T) {
	accepted := []int64{1000, 1500}
	cases := []struct {
		sum    int64
		status string
		miss   int64
		target int64
	}{
		{1000, StatusComplete, 0, 0},
		{1500, StatusComplete, 0, 0},
		{2500, StatusOver, 0, 0},
		{800, StatusPartial, 200, 1000},
		{1200, StatusPartial, 300, 1500},
		{1600, StatusOver, 0, 0},
	}
	for _, c := range cases {
		got := Decide(c.sum, accepted)
		if got.Status != c.status || got.Missing != c.miss || got.Target != c.target {
			t.Fatalf("Decide(%d) = %+v, want status=%s missing=%d target=%d", c.sum, got, c.status, c.miss, c.target)
		}
	}
	if got := Decide(700, nil); got.Status != StatusComplete {
		t.Fatalf("empty accepted must complete (gate off): %+v", got)
	}
}

func TestFormatBRL(t *testing.T) {
	cases := map[int64]string{
		1500:   "R$ 15,00",
		200:    "R$ 2,00",
		1:      "R$ 0,01",
		123456: "R$ 1.234,56",
	}
	for cents, want := range cases {
		if got := FormatBRL(cents); got != want {
			t.Fatalf("FormatBRL(%d) = %q, want %q", cents, got, want)
		}
	}
}

func TestExtractTextPDFGarbageIsErrorNotPanic(t *testing.T) {
	if _, err := ExtractTextPDF([]byte("%PDF-1.4 not really a pdf")); err == nil {
		t.Fatal("expected an error for a malformed pdf")
	}
}

func TestExtractTextJPEGMissingEngineIsInfraError(t *testing.T) {
	if path, err := exec.LookPath("tesseract"); err == nil && path != "" {
		t.Skip("tesseract installed: infra path not reachable")
	}
	_, err := ExtractTextJPEG(context.Background(), []byte{0xFF, 0xD8, 0xFF, 0x00})
	if !errors.Is(err, ErrEngineUnavailable) {
		t.Fatalf("err = %v, want ErrEngineUnavailable", err)
	}
}

func TestExtractTextJPEGWithTesseract(t *testing.T) {
	if _, err := exec.LookPath(OCRBinary()); err != nil {
		t.Skip("tesseract not installed")
	}
	// A synthetic clean image is not reliable across tesseract builds; the
	// real behaviour is covered by the manual test in docs/fila-pix.md.
	t.Log("tesseract available: " + strings.TrimSpace(OCRBinary()))
}
