package suggestions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSuggestSkipsNonQuestions(t *testing.T) {
	e := New(nil)
	_, ok := e.Suggest(context.Background(), "live", "u1", "nick", "ola pessoal")
	if ok {
		t.Fatal("expected non-question to be skipped")
	}
}

func TestSuggestRequiresAgentURL(t *testing.T) {
	e := New(nil)
	_, ok := e.Suggest(context.Background(), "live", "u1", "nick", "quanto custa o produto?")
	if ok {
		t.Fatal("expected false when agent URL is unset")
	}
}

func TestSuggestCallsAgent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/suggest" || r.Method != http.MethodPost {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body["question"] != "quanto custa o produto?" {
			t.Fatalf("question = %q", body["question"])
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"suggested": "O valor está na bio!",
			"reason":    "pergunta identificada como relevante",
		})
	}))
	defer srv.Close()

	e := New(nil)
	e.SetAgentBaseURL(srv.URL)
	cand, ok := e.Suggest(context.Background(), "live", "u1", "Ana", "quanto custa o produto?")
	if !ok {
		t.Fatal("expected suggestion")
	}
	if cand.Suggested != "O valor está na bio!" {
		t.Fatalf("Suggested = %q", cand.Suggested)
	}
	if cand.Reason == "" {
		t.Fatal("expected reason")
	}
}

func TestSuggestEmptyAgentReply(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"suggested": "", "reason": ""})
	}))
	defer srv.Close()

	e := New(nil)
	e.SetAgentBaseURL(srv.URL)
	_, ok := e.Suggest(context.Background(), "live", "u1", "Ana", "quanto custa?")
	if ok {
		t.Fatal("expected empty suggestion to be ignored")
	}
}

func TestLooksLikeQuestion(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"quanto custa?", true},
		{"como faço isso", true},
		{"bom dia galera", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := looksLikeQuestion(tc.msg); got != tc.want {
			t.Fatalf("looksLikeQuestion(%q)=%v want %v", tc.msg, got, tc.want)
		}
	}
}
