package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type mockSupabase struct {
	t            *testing.T
	users        []map[string]any
	profiles     []map[string]any
	lastUserBody map[string]any
}

func (m *mockSupabase) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/auth/v1/admin/users":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			email, _ := body["email"].(string)
			for _, u := range m.users {
				if u["email"] == email {
					w.WriteHeader(http.StatusUnprocessableEntity)
					_ = json.NewEncoder(w).Encode(map[string]string{
						"msg": "A user with this email address has already been registered",
					})
					return
				}
			}
			m.lastUserBody = body
			id := "11111111-1111-1111-1111-111111111111"
			now := time.Now().UTC()
			meta, _ := body["app_metadata"].(map[string]any)
			active, _ := meta["active"].(bool)
			m.users = append(m.users, map[string]any{"id": id, "email": email})
			m.profiles = append(m.profiles, map[string]any{
				"id":                      id,
				"email":                   email,
				"display_name":            "",
				"role":                    "subscriber",
				"active":                  active,
				"notes":                   "",
				"subscription_expires_at": nil,
				"created_at":              now,
				"updated_at":              now,
			})
			_ = json.NewEncoder(w).Encode(map[string]any{"id": id})
		case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/rest/v1/profiles"):
			var patch map[string]any
			_ = json.NewDecoder(r.Body).Decode(&patch)
			if len(m.profiles) > 0 {
				for k, v := range patch {
					m.profiles[len(m.profiles)-1][k] = v
				}
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/rest/v1/profiles"):
			_ = json.NewEncoder(w).Encode(m.profiles)
		default:
			m.t.Logf("unexpected %s %s", r.Method, r.URL.RequestURI())
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func newTestAdmin(t *testing.T, profiles ...map[string]any) (*AdminClient, *mockSupabase) {
	t.Helper()
	mock := &mockSupabase{t: t, profiles: profiles}
	server := httptest.NewServer(mock.handler())
	t.Cleanup(server.Close)
	client := NewAdminClient(Config{
		Enabled:        true,
		SupabaseURL:    server.URL,
		ServiceRoleKey: "service-role",
	})
	return client, mock
}

func TestSignUpPendingCreatesInactiveSubscriber(t *testing.T) {
	client, mock := newTestAdmin(t)

	profile, err := client.SignUpPending(SignUpRequest{
		Email:       "cliente@example.com",
		Password:    "secret123",
		DisplayName: "Ana",
		Notes:       "PIX em nome de Ana",
	})
	if err != nil {
		t.Fatalf("SignUpPending: %v", err)
	}
	if profile.Active {
		t.Fatal("cadastro público não pode nascer aprovado")
	}
	if profile.Role != "subscriber" {
		t.Fatalf("role=%q, want subscriber", profile.Role)
	}

	meta, _ := mock.lastUserBody["app_metadata"].(map[string]any)
	if meta["role"] != "subscriber" {
		t.Fatalf("app_metadata.role=%v, want subscriber", meta["role"])
	}
	if meta["active"] != false {
		t.Fatalf("app_metadata.active=%v, want false", meta["active"])
	}
}

func TestSignUpPendingRejectsDuplicateEmail(t *testing.T) {
	client, _ := newTestAdmin(t)
	req := SignUpRequest{Email: "cliente@example.com", Password: "secret123"}
	if _, err := client.SignUpPending(req); err != nil {
		t.Fatalf("primeiro cadastro: %v", err)
	}
	_, err := client.SignUpPending(req)
	if err != ErrDuplicateSignup {
		t.Fatalf("erro=%v, want ErrDuplicateSignup", err)
	}
}

func TestSignUpPendingIgnoresAdminFieldsInJSON(t *testing.T) {
	// Extra fields are dropped by the typed SignUpRequest; this test documents
	// that CreateSubscriber always forces subscriber + pending.
	client, mock := newTestAdmin(t)
	if _, err := client.SignUpPending(SignUpRequest{
		Email:    "novo@example.com",
		Password: "secret123",
	}); err != nil {
		t.Fatalf("SignUpPending: %v", err)
	}
	meta, _ := mock.lastUserBody["app_metadata"].(map[string]any)
	if meta["role"] != "subscriber" || meta["active"] != false {
		t.Fatalf("metadata inesperada: %+v", meta)
	}
}

func TestListSubscribersPutsPendingFirst(t *testing.T) {
	now := time.Now().UTC()
	earlier := now.Add(-time.Hour)
	client, _ := newTestAdmin(t,
		map[string]any{
			"id": "a", "email": "ativo@example.com", "display_name": "",
			"role": "subscriber", "active": true, "notes": "",
			"created_at": now, "updated_at": now,
		},
		map[string]any{
			"id": "b", "email": "pendente@example.com", "display_name": "",
			"role": "subscriber", "active": false, "notes": "",
			"created_at": earlier, "updated_at": earlier,
		},
	)

	users, err := client.ListSubscribers()
	if err != nil {
		t.Fatalf("ListSubscribers: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("len=%d, want 2", len(users))
	}
	if users[0].Email != "pendente@example.com" {
		t.Fatalf("primeiro=%q, want pendente (fila de pagamento)", users[0].Email)
	}
}
