package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SubscriberProfile is the admin-facing view of a paying customer account.
type SubscriberProfile struct {
	ID                    string     `json:"id"`
	Email                 string     `json:"email"`
	DisplayName           string     `json:"displayName"`
	Role                  string     `json:"role"`
	Active                bool       `json:"active"`
	Notes                 string     `json:"notes"`
	SubscriptionExpiresAt *time.Time `json:"subscriptionExpiresAt,omitempty"`
	CreatedAt             time.Time  `json:"createdAt"`
	UpdatedAt             time.Time  `json:"updatedAt"`
}

func subscriberAppMetadata(active bool, expiresAt *time.Time) map[string]any {
	meta := map[string]any{
		"role":   "subscriber",
		"active": active,
	}
	if expiresAt != nil {
		meta["subscription_expires_at"] = expiresAt.UTC().Format(time.RFC3339)
	}
	return meta
}

type CreateSubscriberRequest struct {
	Email                 string `json:"email"`
	Password              string `json:"password"`
	DisplayName           string `json:"displayName"`
	Notes                 string `json:"notes"`
	SubscriptionExpiresAt string `json:"subscriptionExpiresAt"`
}

type UpdateSubscriberRequest struct {
	ID                    string  `json:"id"`
	Password              *string `json:"password,omitempty"`
	DisplayName           *string `json:"displayName,omitempty"`
	Active                *bool   `json:"active,omitempty"`
	Notes                 *string `json:"notes,omitempty"`
	SubscriptionExpiresAt *string `json:"subscriptionExpiresAt,omitempty"`
}

// AdminClient talks to Supabase Auth Admin and the profiles table.
type AdminClient struct {
	cfg    Config
	client *http.Client
}

func NewAdminClient(cfg Config) *AdminClient {
	return &AdminClient{
		cfg:    cfg,
		client: &http.Client{Timeout: 20 * time.Second},
	}
}

func (a *AdminClient) enabled() bool {
	return a.cfg.Enabled && a.cfg.SupabaseURL != "" && a.cfg.ServiceRoleKey != ""
}

func (a *AdminClient) request(method, path string, body any, out any) error {
	if !a.enabled() {
		return fmt.Errorf("supabase admin não configurado")
	}

	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(raw)
	}

	req, err := http.NewRequest(method, a.cfg.SupabaseURL+path, payload)
	if err != nil {
		return err
	}
	req.Header.Set("apikey", a.cfg.ServiceRoleKey)
	req.Header.Set("Authorization", "Bearer "+a.cfg.ServiceRoleKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		return fmt.Errorf("supabase %s %s: %s", method, path, strings.TrimSpace(string(raw)))
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// ListSubscribers returns all app profiles ordered by creation date.
func (a *AdminClient) ListSubscribers() ([]SubscriberProfile, error) {
	var rows []struct {
		ID                    string     `json:"id"`
		Email                 string     `json:"email"`
		DisplayName           string     `json:"display_name"`
		Role                  string     `json:"role"`
		Active                bool       `json:"active"`
		Notes                 string     `json:"notes"`
		SubscriptionExpiresAt *time.Time `json:"subscription_expires_at"`
		CreatedAt             time.Time  `json:"created_at"`
		UpdatedAt             time.Time  `json:"updated_at"`
	}
	q := url.Values{}
	q.Set("select", "id,email,display_name,role,active,notes,subscription_expires_at,created_at,updated_at")
	q.Set("order", "created_at.desc")
	if err := a.request(http.MethodGet, "/rest/v1/profiles?"+q.Encode(), nil, &rows); err != nil {
		return nil, err
	}

	out := make([]SubscriberProfile, 0, len(rows))
	for _, row := range rows {
		out = append(out, SubscriberProfile{
			ID:                    row.ID,
			Email:                 row.Email,
			DisplayName:           row.DisplayName,
			Role:                  row.Role,
			Active:                row.Active,
			Notes:                 row.Notes,
			SubscriptionExpiresAt: row.SubscriptionExpiresAt,
			CreatedAt:             row.CreatedAt,
			UpdatedAt:             row.UpdatedAt,
		})
	}
	return out, nil
}

// CreateSubscriber creates a Supabase Auth user and matching profile row.
func (a *AdminClient) CreateSubscriber(req CreateSubscriberRequest) (*SubscriberProfile, error) {
	email := strings.TrimSpace(strings.ToLower(req.Email))
	password := strings.TrimSpace(req.Password)
	if email == "" || password == "" {
		return nil, fmt.Errorf("email e senha são obrigatórios")
	}
	if len(password) < 8 {
		return nil, fmt.Errorf("senha deve ter pelo menos 8 caracteres")
	}

	var expiresAt *time.Time
	if strings.TrimSpace(req.SubscriptionExpiresAt) != "" {
		parsed, err := time.Parse(time.RFC3339, req.SubscriptionExpiresAt)
		if err != nil {
			return nil, fmt.Errorf("subscriptionExpiresAt inválido (use RFC3339)")
		}
		expiresAt = &parsed
	}

	var authResp struct {
		ID string `json:"id"`
	}
	if err := a.request(http.MethodPost, "/auth/v1/admin/users", map[string]any{
		"email":         email,
		"password":      password,
		"email_confirm": true,
		// O cliente nasce pendente. A liberação é uma ação separada do admin,
		// feita depois da confirmação do pagamento.
		"app_metadata": subscriberAppMetadata(false, expiresAt),
	}, &authResp); err != nil {
		return nil, err
	}

	profile := map[string]any{
		"email":        email,
		"display_name": strings.TrimSpace(req.DisplayName),
		"role":         "subscriber",
		"active":       false,
		"notes":        strings.TrimSpace(req.Notes),
		"updated_at":   time.Now().UTC(),
	}
	if expiresAt != nil {
		profile["subscription_expires_at"] = expiresAt.UTC().Format(time.RFC3339Nano)
	}

	q := url.Values{}
	q.Set("id", "eq."+authResp.ID)
	if err := a.request(http.MethodPatch, "/rest/v1/profiles?"+q.Encode(), profile, nil); err != nil {
		_ = a.request(http.MethodDelete, "/auth/v1/admin/users/"+authResp.ID, nil, nil)
		return nil, err
	}

	profiles, err := a.ListSubscribers()
	if err != nil {
		return nil, err
	}
	for _, p := range profiles {
		if p.ID == authResp.ID {
			return &p, nil
		}
	}
	return nil, fmt.Errorf("perfil criado, mas não encontrado")
}

// UpdateSubscriber updates profile data and optional password.
func (a *AdminClient) UpdateSubscriber(req UpdateSubscriberRequest) (*SubscriberProfile, error) {
	id := strings.TrimSpace(req.ID)
	if id == "" {
		return nil, fmt.Errorf("id é obrigatório")
	}

	profiles, err := a.ListSubscribers()
	if err != nil {
		return nil, err
	}
	var current *SubscriberProfile
	for i := range profiles {
		if profiles[i].ID == id {
			current = &profiles[i]
			break
		}
	}
	if current == nil {
		return nil, fmt.Errorf("usuário não encontrado")
	}
	if current.Role == "admin" {
		return nil, fmt.Errorf("não é possível editar o administrador por aqui")
	}

	active := current.Active
	if req.Active != nil {
		active = *req.Active
	}
	displayName := current.DisplayName
	if req.DisplayName != nil {
		displayName = strings.TrimSpace(*req.DisplayName)
	}
	notes := current.Notes
	if req.Notes != nil {
		notes = strings.TrimSpace(*req.Notes)
	}

	var expiresAt *time.Time
	if req.SubscriptionExpiresAt != nil {
		raw := strings.TrimSpace(*req.SubscriptionExpiresAt)
		if raw == "" {
			expiresAt = nil
		} else {
			parsed, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return nil, fmt.Errorf("subscriptionExpiresAt inválido (use RFC3339)")
			}
			expiresAt = &parsed
		}
	} else {
		expiresAt = current.SubscriptionExpiresAt
	}

	if req.Password != nil && strings.TrimSpace(*req.Password) != "" {
		password := strings.TrimSpace(*req.Password)
		if len(password) < 8 {
			return nil, fmt.Errorf("senha deve ter pelo menos 8 caracteres")
		}
		if err := a.request(http.MethodPut, "/auth/v1/admin/users/"+id, map[string]any{
			"password":     password,
			"app_metadata": subscriberAppMetadata(active, expiresAt),
		}, nil); err != nil {
			return nil, err
		}
	} else {
		if err := a.request(http.MethodPut, "/auth/v1/admin/users/"+id, map[string]any{
			"app_metadata": subscriberAppMetadata(active, expiresAt),
		}, nil); err != nil {
			return nil, err
		}
	}

	patch := map[string]any{
		"display_name": displayName,
		"active":       active,
		"notes":        notes,
		"updated_at":   time.Now().UTC(),
	}
	if req.SubscriptionExpiresAt != nil {
		if expiresAt == nil {
			patch["subscription_expires_at"] = nil
		} else {
			patch["subscription_expires_at"] = expiresAt.UTC().Format(time.RFC3339Nano)
		}
	}

	q := url.Values{}
	q.Set("id", "eq."+id)
	if err := a.request(http.MethodPatch, "/rest/v1/profiles?"+q.Encode(), patch, nil); err != nil {
		return nil, err
	}

	updated, err := a.ListSubscribers()
	if err != nil {
		return nil, err
	}
	for _, p := range updated {
		if p.ID == id {
			return &p, nil
		}
	}
	return nil, fmt.Errorf("usuário atualizado, mas não encontrado")
}

// DeleteSubscriber removes a subscriber account.
func (a *AdminClient) DeleteSubscriber(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("id é obrigatório")
	}

	profiles, err := a.ListSubscribers()
	if err != nil {
		return err
	}
	for _, p := range profiles {
		if p.ID == id {
			if p.Role == "admin" {
				return fmt.Errorf("não é possível remover o administrador")
			}
			return a.request(http.MethodDelete, "/auth/v1/admin/users/"+id, nil, nil)
		}
	}
	return fmt.Errorf("usuário não encontrado")
}

// GetProfileByID returns a profile row for the authenticated user.
func (a *AdminClient) GetProfileByID(id string) (*SubscriberProfile, error) {
	profiles, err := a.ListSubscribers()
	if err != nil {
		return nil, err
	}
	for _, p := range profiles {
		if p.ID == id {
			return &p, nil
		}
	}
	return nil, fmt.Errorf("perfil não encontrado")
}
