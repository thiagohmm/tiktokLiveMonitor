package auth

import "errors"

var (
	ErrInvalidCredentials = errors.New("email ou senha inválidos")
	ErrAuthUnavailable    = errors.New("serviço de autenticação indisponível, tente novamente")
	ErrInvalidResetLink   = errors.New("link inválido ou expirado")
)

// LoginSession stays server-side; only CSRFToken may be returned to the browser.
type LoginSession struct {
	AccessToken string
	CSRFToken   string
	ExpiresIn   int
}

func (c Config) ValidateToken(token string) (*User, error) {
	if !c.Enabled {
		return &User{Role: "admin", Active: true}, nil
	}
	if c.Store == nil {
		return nil, ErrAuthUnavailable
	}
	return c.Store.ValidateToken(token)
}
func (c Config) SignInWithPassword(email, password string) (*LoginSession, error) {
	if c.Store == nil {
		return nil, ErrAuthUnavailable
	}
	return c.Store.SignInWithPassword(email, password)
}
func (c Config) SignOutGlobal(token string) error {
	if c.Store == nil {
		return ErrAuthUnavailable
	}
	return c.Store.SignOutGlobal(token)
}
func (c Config) UpdatePassword(token, password string) error {
	if c.Store == nil {
		return ErrAuthUnavailable
	}
	return c.Store.UpdatePassword(token, password)
}
