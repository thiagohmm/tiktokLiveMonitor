// Package receipt extracts monetary values from PIX receipts (JPG via
// tesseract OCR, PDF via the embedded text layer) and decides whether the
// accumulated total of a ticket matches one of the values the owner accepts.
package receipt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/ledongthuc/pdf"
)

// ErrEngineUnavailable reports that the OCR engine (tesseract binary) is not
// installed. Callers treat it as an infrastructure failure (fail-open), not as
// an unreadable receipt.
var ErrEngineUnavailable = errors.New("ocr engine unavailable")

// Decision statuses returned by Decide.
const (
	StatusComplete = "complete" // sum equals one accepted value
	StatusPartial  = "partial"  // sum is below the next accepted value
	StatusOver     = "over"     // sum is above every accepted value
)

// Decision is the outcome of comparing a ticket's new total against the
// accepted values.
type Decision struct {
	Status  string
	Missing int64 // cents still missing (partial only)
	Target  int64 // accepted value the client is short of (partial only)
}

const maxCents int64 = 999_999_999 // R$ 9.999.999,99

// OCRBinary returns the tesseract executable, overridable via TESSERACT_BIN.
func OCRBinary() string {
	if bin := strings.TrimSpace(os.Getenv("TESSERACT_BIN")); bin != "" {
		return bin
	}
	return "tesseract"
}

// ExtractTextPDF returns the text embedded in a PDF receipt. A PDF without a
// text layer (a scan) yields an empty string and no error. The ledongthuc/pdf
// reader panics on malformed files, so the call is guarded.
func ExtractTextPDF(data []byte) (text string, err error) {
	defer func() {
		if r := recover(); r != nil {
			text = ""
			err = fmt.Errorf("parse pdf: %v", r)
		}
	}()
	reader := bytes.NewReader(data)
	r, err := pdf.NewReader(reader, int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("open pdf: %w", err)
	}
	plain, err := r.GetPlainText()
	if err != nil {
		return "", fmt.Errorf("pdf text: %w", err)
	}
	out, err := io.ReadAll(io.LimitReader(plain, 1<<20))
	if err != nil {
		return "", fmt.Errorf("pdf text read: %w", err)
	}
	return string(out), nil
}

// ExtractTextJPEG runs tesseract over a JPEG receipt and returns its text.
func ExtractTextJPEG(ctx context.Context, data []byte) (string, error) {
	bin := OCRBinary()
	if _, err := exec.LookPath(bin); err != nil {
		return "", fmt.Errorf("%w: %s", ErrEngineUnavailable, bin)
	}
	tmp, err := os.CreateTemp("", "pix-receipt-*.jpg")
	if err != nil {
		return "", fmt.Errorf("ocr temp file: %w", err)
	}
	defer func() {
		if err := os.Remove(tmp.Name()); err != nil {
			log.Printf("[receipt] temporary file cleanup: %v", err)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("ocr temp write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("ocr temp close: %w", err)
	}

	cmd := exec.CommandContext(ctx, bin, tmp.Name(), "stdout", "-l", "por")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("tesseract: %w: %s", err, firstLine(stderr.String()))
	}
	return stdout.String(), nil
}

var (
	// "R$ 15,00", "R$15.00", "R$ 1.234,56", "R$ 15"
	rePrefixed = regexp.MustCompile(`(?i)r\$\s*([0-9][0-9.,]*)`)
	// "15,00", "1.234,56" — only comma-decimals count without the R$ prefix,
	// so dates, phones and bare ids never masquerade as money. RE2 has no
	// lookahead, so the trailing boundary is consumed instead.
	reDecimal = regexp.MustCompile(`(^|[^\d.,])([0-9][0-9.,]*,\d{2})([^\d]|$)`)
)

// ExtractValuesCents returns the distinct monetary values found in text,
// sorted ascending, in cents.
func ExtractValuesCents(text string) []int64 {
	seen := map[int64]bool{}
	out := []int64{}
	collect := func(re *regexp.Regexp, groups []int) {
		for _, match := range re.FindAllStringSubmatch(text, -1) {
			for _, g := range groups {
				if g >= len(match) {
					continue
				}
				if cents, ok := parseCents(match[g]); ok && !seen[cents] {
					seen[cents] = true
					out = append(out, cents)
				}
			}
		}
	}
	collect(rePrefixed, []int{1})
	collect(reDecimal, []int{2})
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// parseCents converts one pt-BR money token to cents. Thousand dots are
// dropped, the comma (or a trailing dot-decimal, common in OCR output)
// separates the cents.
func parseCents(token string) (int64, bool) {
	s := strings.TrimSpace(token)
	s = strings.TrimRight(s, ".,")
	if s == "" {
		return 0, false
	}
	intPart, decPart := s, ""
	if i := strings.LastIndexAny(s, ",."); i >= 0 {
		intPart, decPart = s[:i], s[i+1:]
		// "1.200" without a comma is thousands, not a decimal.
		if len(decPart) == 3 && s[i] == '.' && !strings.Contains(intPart, ",") {
			units, err := strconv.ParseInt(strings.ReplaceAll(s, ".", ""), 10, 64)
			if err != nil || units <= 0 || units > maxCents/100 {
				return 0, false
			}
			return units * 100, true
		}
	}
	intPart = strings.ReplaceAll(intPart, ".", "")
	intPart = strings.ReplaceAll(intPart, ",", "")
	if intPart == "" {
		intPart = "0"
	}
	if len(intPart) > 9 || len(decPart) > 2 {
		return 0, false
	}
	for _, c := range intPart + decPart {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	units, err := strconv.ParseInt(intPart, 10, 64)
	if err != nil {
		return 0, false
	}
	cents := int64(0)
	switch len(decPart) {
	case 0:
		cents = 0
	case 1:
		d, err := strconv.ParseInt(decPart, 10, 64)
		if err != nil {
			return 0, false
		}
		cents = d * 10
	default:
		cents, err = strconv.ParseInt(decPart, 10, 64)
		if err != nil {
			return 0, false
		}
	}
	total := units*100 + cents
	if total <= 0 || total > maxCents {
		return 0, false
	}
	return total, true
}

// ParseBRLCents parses a user-entered amount ("15", "15,00", "R$ 1.234,56")
// into cents. It backs the values configuration endpoint.
func ParseBRLCents(input string) (int64, bool) {
	s := strings.ToLower(strings.TrimSpace(input))
	s = strings.ReplaceAll(s, "r$", "")
	s = strings.TrimSpace(s)
	if s == "" || !regexp.MustCompile(`^[0-9]{1,3}(\.[0-9]{3})*([.,][0-9]{1,2})?$`).MatchString(s) {
		return 0, false
	}
	return parseCents(s)
}

// Decide compares a ticket's new total against the accepted values (which must
// be sorted ascending). Exact match completes; anything above the largest
// value is over; otherwise the client is short of the next accepted value.
func Decide(newSumCents int64, accepted []int64) Decision {
	if len(accepted) == 0 {
		return Decision{Status: StatusComplete}
	}
	for _, a := range accepted {
		if a == newSumCents {
			return Decision{Status: StatusComplete}
		}
	}
	for _, a := range accepted {
		if a > newSumCents {
			return Decision{Status: StatusPartial, Missing: a - newSumCents, Target: a}
		}
	}
	return Decision{Status: StatusOver}
}

// FormatBRL renders cents as "R$ 15,00" for the auto-reply messages.
func FormatBRL(cents int64) string {
	units := cents / 100
	frac := cents % 100
	grouped := groupThousands(units)
	return fmt.Sprintf("R$ %s,%02d", grouped, frac)
}

func groupThousands(units int64) string {
	digits := strconv.FormatInt(units, 10)
	var b []byte
	for i, c := range []byte(digits) {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b = append(b, '.')
		}
		b = append(b, c)
	}
	return string(b)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
