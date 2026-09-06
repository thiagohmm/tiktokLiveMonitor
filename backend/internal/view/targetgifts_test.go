package view

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRejectInvalidTargetGiftQuantity(t *testing.T) {
	for _, quantity := range []string{"0", "-1", "1.5", `"2"`} {
		t.Run(quantity, func(t *testing.T) {
			srv := &HTTPServer{}
			req := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(`{"targetGifts":["Rosa"],"targetGiftQuantities":{"Rosa":`+quantity+`}}`))
			rec := httptest.NewRecorder()
			srv.handleSettings(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("got %d, want 400", rec.Code)
			}
		})
	}
}
