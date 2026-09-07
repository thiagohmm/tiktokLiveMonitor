package mail

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

func TestBuildWelcomeBody(t *testing.T) {
	tests := []struct {
		name            string
		cfg             Config
		displayName     string
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:            "link tem prioridade sobre pix",
			cfg:             Config{PaymentLink: "https://pay.exemplo/abc", PixKey: "chave@pix.com", Price: "20,00"},
			wantContains:    []string{"Pagamento: https://pay.exemplo/abc"},
			wantNotContains: []string{"Pix chave@pix.com", placeholderLine},
		},
		{
			name:         "pix quando não há link",
			cfg:          Config{PixKey: "chave@pix.com", Price: "20,00"},
			wantContains: []string{"Pagamento: Pix chave@pix.com"},
		},
		{
			name:         "placeholder quando não há link nem pix",
			cfg:          Config{Price: "20,00"},
			wantContains: []string{placeholderLine},
		},
		{
			name:         "preço interpolado",
			cfg:          Config{PaymentLink: "https://pay.exemplo/abc", Price: "25,00"},
			wantContains: []string{"pagamento de R$ 25,00"},
		},
		{
			name:         "displayName na saudação",
			cfg:          Config{PaymentLink: "https://pay.exemplo/abc", Price: "20,00"},
			displayName:  "  João  ",
			wantContains: []string{"Olá, João! Tudo bem?"},
		},
		{
			name:            "sem displayName usa saudação genérica",
			cfg:             Config{PaymentLink: "https://pay.exemplo/abc", Price: "20,00"},
			wantContains:    []string{"Olá! Tudo bem?"},
			wantNotContains: []string{"Olá, "},
		},
		{
			name:         "assinatura fixa",
			cfg:          Config{PaymentLink: "https://pay.exemplo/abc", Price: "20,00"},
			wantContains: []string{"Atenciosamente,\nEquipe TikTok Live Monitor"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildWelcomeBody(tt.cfg, tt.displayName)
			for _, want := range tt.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("corpo não contém %q\n--- corpo ---\n%s", want, got)
				}
			}
			for _, not := range tt.wantNotContains {
				if strings.Contains(got, not) {
					t.Errorf("corpo contém %q indevido\n--- corpo ---\n%s", not, got)
				}
			}
		})
	}
}

func TestBuildMessage(t *testing.T) {
	cfg := Config{
		PaymentLink: "https://pay.exemplo/abc",
		Price:       "20,00",
	}
	body := buildWelcomeBody(cfg, "João")
	msg := string(buildMessage("Equipe <no-reply@tlm.com>", "joao@exemplo.com", "Liberação do acesso ao TikTok Live Monitor", body))

	for _, want := range []string{
		"From: Equipe <no-reply@tlm.com>",
		"To: joao@exemplo.com",
		"Date: ",
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: quoted-printable",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("mensagem não contém %q\n--- msg ---\n%s", want, msg)
		}
	}

	// Assunto não-ASCII deve vir codificado (MIME B-encoding) e decodificar
	// de volta para o original.
	subjLine := headerValue(t, msg, "Subject")
	if !strings.HasPrefix(subjLine, "=?UTF-8?B?") {
		t.Fatalf("assunto não-ASCII deve ser codificado MIME, got %q", subjLine)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(subjLine, "=?UTF-8?B?"), "?="))
	if err != nil {
		t.Fatalf("assunto codificado é base64 inválido: %v", err)
	}
	if string(decoded) != "Liberação do acesso ao TikTok Live Monitor" {
		t.Errorf("assunto decodificado = %q", decoded)
	}

	// Corpo: decodificar quoted-printable deve devolver o original (acentos e R$).
	_, bodyPart, ok := strings.Cut(msg, "\r\n\r\n")
	if !ok {
		t.Fatal("mensagem sem separação header/corpo (\\r\\n\\r\\n)")
	}
	decodedBody := decodeQP(t, bodyPart)
	if decodedBody != body {
		t.Errorf("corpo decodificado difere do original:\n--- decodificado ---\n%s\n--- original ---\n%s", decodedBody, body)
	}
	for _, want := range []string{"Olá, João! Tudo bem?", "R$ 20,00"} {
		if !strings.Contains(decodedBody, want) {
			t.Errorf("corpo decodificado não contém %q", want)
		}
	}

	// Finalização com CRLF e sem LF "solto".
	if !strings.HasSuffix(msg, "\r\n") {
		t.Error("mensagem deve terminar com \\r\\n")
	}
	for i := 1; i < len(msg); i++ {
		if msg[i] == '\n' && msg[i-1] != '\r' {
			t.Error("mensagem contém LF sem CRLF")
			break
		}
	}
}

func TestBuildMessageSubjectASCII(t *testing.T) {
	msg := string(buildMessage("a@b.com", "c@d.com", "Simple subject", "ola"))
	if got := headerValue(t, msg, "Subject"); got != "Simple subject" {
		t.Errorf("assunto ASCII não deve ser codificado, got %q", got)
	}
}

func TestQPEncodeLine(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"abc", "abc"},
		{"Olá", "Ol=C3=A1"},
		{"a = b", "a =3D b"},
		{"R$ 20,00", "R$ 20,00"},
	}
	for _, tt := range tests {
		if got := qpEncodeLine(tt.in); got != tt.want {
			t.Errorf("qpEncodeLine(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestPaymentLinePriority(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{"link", Config{PaymentLink: "https://x", PixKey: "k"}, "Pagamento: https://x"},
		{"pix", Config{PixKey: "k"}, "Pagamento: Pix k"},
		{"placeholder", Config{}, placeholderLine},
		{"whitespace ignora", Config{PaymentLink: "  ", PixKey: "k"}, "Pagamento: Pix k"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := paymentLine(tt.cfg); got != tt.want {
				t.Errorf("paymentLine = %q, want %q", got, tt.want)
			}
		})
	}
}

// headerValue extrai o valor do header `name` da mensagem.
func headerValue(t *testing.T, msg, name string) string {
	t.Helper()
	for _, line := range strings.Split(msg, "\r\n") {
		if strings.HasPrefix(line, name+": ") {
			return strings.TrimPrefix(line, name+": ")
		}
	}
	t.Fatalf("header %q não encontrado", name)
	return ""
}

// decodeQP decodifica um corpo quoted-printable (linhas separadas por CRLF).
func decodeQP(t *testing.T, s string) string {
	t.Helper()
	s = strings.TrimSuffix(s, "\r\n")
	lines := strings.Split(s, "\r\n")
	out := make([]string, len(lines))
	for i, line := range lines {
		var b strings.Builder
		for j := 0; j < len(line); j++ {
			if line[j] == '=' && j+2 < len(line) {
				if v, err := hex.DecodeString(line[j+1 : j+3]); err == nil {
					b.Write(v)
					j += 2
					continue
				}
			}
			b.WriteByte(line[j])
		}
		out[i] = b.String()
	}
	return strings.Join(out, "\n")
}
