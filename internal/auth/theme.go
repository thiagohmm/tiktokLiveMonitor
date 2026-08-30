package auth

import (
	"os"
	"regexp"
	"strings"
)

var hexColorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// ThemeColors holds brand colors exposed to the login page and main UI.
type ThemeColors struct {
	Pink string `json:"pink"`
	Cyan string `json:"cyan"`
	BG   string `json:"bg"`
}

func normalizeHexColor(raw, fallback string) string {
	v := strings.TrimSpace(raw)
	if hexColorPattern.MatchString(v) {
		return strings.ToLower(v)
	}
	return fallback
}

// LoadThemeFromEnv reads AUTH_THEME_PINK, AUTH_THEME_CYAN and AUTH_THEME_BG.
func LoadThemeFromEnv() ThemeColors {
	return ThemeColors{
		Pink: normalizeHexColor(os.Getenv("AUTH_THEME_PINK"), "#fe2c55"),
		Cyan: normalizeHexColor(os.Getenv("AUTH_THEME_CYAN"), "#25f4ee"),
		BG:   normalizeHexColor(os.Getenv("AUTH_THEME_BG"), "#0b0d12"),
	}
}
