package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type LoginSession struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

// SignInWithPassword authenticates against Supabase Auth using the anon key.
func (c Config) SignInWithPassword(email, password string) (*LoginSession, error) {
	if !c.Enabled || c.SupabaseURL == "" || c.SupabaseAnon == "" {
		return nil, fmt.Errorf("autenticação indisponível")
	}

	body, err := json.Marshal(map[string]string{
		"email":    strings.TrimSpace(strings.ToLower(email)),
		"password": password,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(
		http.MethodPost,
		c.SupabaseURL+"/auth/v1/token?grant_type=password",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, err
	}
	req.Header.Set("apikey", c.SupabaseAnon)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 20 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		var errBody struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
			Msg              string `json:"msg"`
			Message          string `json:"message"`
		}
		_ = json.Unmarshal(raw, &errBody)
		switch {
		case errBody.ErrorDescription != "":
			msg = errBody.ErrorDescription
		case errBody.Message != "":
			msg = errBody.Message
		case errBody.Msg != "":
			msg = errBody.Msg
		case errBody.Error != "":
			msg = errBody.Error
		}
		if msg == "" {
			msg = "credenciais inválidas"
		}
		return nil, fmt.Errorf("%s", msg)
	}

	var session LoginSession
	if err := json.Unmarshal(raw, &session); err != nil {
		return nil, err
	}
	if session.AccessToken == "" {
		return nil, fmt.Errorf("sessão inválida")
	}
	return &session, nil
}

// SignOutGlobal revokes all refresh tokens for the session owner.
func (c Config) SignOutGlobal(accessToken string) error {
	accessToken = strings.TrimSpace(accessToken)
	if accessToken == "" {
		return fmt.Errorf("token ausente")
	}
	if !c.Enabled || c.SupabaseURL == "" || c.SupabaseAnon == "" {
		return fmt.Errorf("autenticação indisponível")
	}

	req, err := http.NewRequest(http.MethodPost, c.SupabaseURL+"/auth/v1/logout?scope=global", nil)
	if err != nil {
		return err
	}
	req.Header.Set("apikey", c.SupabaseAnon)
	req.Header.Set("Authorization", "Bearer "+accessToken)

	client := &http.Client{Timeout: 20 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = "falha ao encerrar sessão"
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}
