package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteJSON(t *testing.T) {
	tests := []struct {
		name     string
		data     interface{}
		wantKey  string
		wantType string
	}{
		{
			name:     "map with string",
			data:     map[string]string{"answer": "hello"},
			wantKey:  "answer",
			wantType: "string",
		},
		{
			name:     "map with bool",
			data:     map[string]bool{"success": true},
			wantKey:  "success",
			wantType: "bool",
		},
		{
			name:     "map with int",
			data:     map[string]interface{}{"deleted": int64(5)},
			wantKey:  "deleted",
			wantType: "number",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeJSON(rec, tt.data)

			if rec.Code != http.StatusOK {
				t.Errorf("expected status 200, got %d", rec.Code)
			}

			ct := rec.Header().Get("Content-Type")
			if ct != "application/json" {
				t.Errorf("expected Content-Type application/json, got %q", ct)
			}

			var result map[string]interface{}
			if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
				t.Fatalf("failed to decode JSON: %v", err)
			}
			if _, ok := result[tt.wantKey]; !ok {
				t.Errorf("expected key %q in response", tt.wantKey)
			}
		})
	}
}

func TestWriteError(t *testing.T) {
	tests := []struct {
		status int
		msg    string
	}{
		{http.StatusBadRequest, "bad request"},
		{http.StatusInternalServerError, "internal error"},
		{http.StatusMethodNotAllowed, "method not allowed"},
	}

	for _, tt := range tests {
		t.Run(tt.msg, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeError(rec, tt.status, tt.msg)

			if rec.Code != tt.status {
				t.Errorf("expected status %d, got %d", tt.status, rec.Code)
			}

			ct := rec.Header().Get("Content-Type")
			if ct != "application/json" {
				t.Errorf("expected Content-Type application/json, got %q", ct)
			}

			var result map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
				t.Fatalf("failed to decode JSON: %v", err)
			}
			if result["error"] != tt.msg {
				t.Errorf("expected error %q, got %q", tt.msg, result["error"])
			}
		})
	}
}

func TestWriteJSONNil(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, nil)
	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
}

func TestWriteJSONEmptyStruct(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, struct{}{})
	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty object, got %v", result)
	}
}

func TestWriteJSONSlice(t *testing.T) {
	rec := httptest.NewRecorder()
	data := []map[string]string{{"id": "1"}, {"id": "2"}}
	writeJSON(rec, data)

	var result []map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("failed to decode JSON: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 items, got %d", len(result))
	}
}

func TestWriteSSE(t *testing.T) {
	rec := httptest.NewRecorder()
	s := &Server{}
	s.writeSSE(rec, "test-event", map[string]string{"key": "value"})

	body := rec.Body.String()
	if !strings.Contains(body, "event: test-event") {
		t.Error("expected event header in SSE output")
	}
	if !strings.Contains(body, `"key":"value"`) {
		t.Error("expected JSON data in SSE output")
	}
	if !strings.HasSuffix(body, "\n\n") {
		t.Error("expected SSE output to end with double newline")
	}
}
