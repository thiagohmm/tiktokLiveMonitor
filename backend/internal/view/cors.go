package view

import (
	"net/http"
	"os"
	"strings"
)

// LoadCORSOriginsFromEnv lê CORS_ALLOWED_ORIGINS (origens separadas por
// vírgula, ex.: http://localhost:3000,https://frontend.example.com).
// Vazio = sem CORS (frontend e backend na mesma origem, ex.: nginx/Vercel
// fazendo proxy).
func LoadCORSOriginsFromEnv() []string {
	raw := strings.TrimSpace(os.Getenv("CORS_ALLOWED_ORIGINS"))
	if raw == "" {
		return nil
	}
	var origins []string
	for _, part := range strings.Split(raw, ",") {
		if o := strings.TrimSpace(part); o != "" {
			origins = append(origins, strings.TrimRight(o, "/"))
		}
	}
	return origins
}

// cors adiciona os headers CORS para as origens permitidas e responde o
// preflight OPTIONS. Necessário apenas quando o frontend roda em outra
// origem (dev local ou deploy separado sem proxy).
func (s *HTTPServer) cors(next http.Handler) http.Handler {
	allowed := make(map[string]bool, len(s.corsOrigins))
	for _, o := range s.corsOrigins {
		allowed[o] = true
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimRight(r.Header.Get("Origin"), "/")
		if origin != "" && allowed[origin] {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			h.Set("Access-Control-Max-Age", "86400")
			h.Set("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
