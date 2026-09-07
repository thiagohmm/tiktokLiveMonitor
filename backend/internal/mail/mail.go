// Package mail implementa o envio transacional de e-mail (SMTP via stdlib)
// usado no e-mail de boas-vindas do cadastro público.
//
// O mailer é best-effort: sem SMTP_HOST/MAIL_FROM ele fica desabilitado e
// nenhum e-mail é enviado. Falhas de envio são logadas pelo chamador e
// nunca quebram a requisição de cadastro.
package mail

import (
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"log"
	"net"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds SMTP and welcome-email settings.
type Config struct {
	Host               string
	Port               int
	Username           string
	Password           string
	From               string
	Subject            string
	PixKey             string
	PaymentLink        string
	Price              string
	TLSMode            string // "starttls" | "implicit" | "none"
	InsecureSkipVerify bool
}

// Defaults aplicados quando as envs estão ausentes.
const (
	defaultPort     = 587
	defaultTLSMode  = "starttls"
	defaultPrice    = "20,00"
	defaultSubject  = "Liberação do acesso ao TikTok Live Monitor"
	defaultFrom     = "Equipe TikTok Live Monitor <no-reply@tiktoklivemonitor.com>"
	placeholderLine = "Pagamento: [inserir chave Pix ou link de pagamento]"
)

// LoadConfigFromEnv reads the SMTP_* and PAYMENT_* env vars.
func LoadConfigFromEnv() Config {
	port := defaultPort
	if v := strings.TrimSpace(os.Getenv("SMTP_PORT")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			port = n
		}
	}
	tlsMode := strings.ToLower(strings.TrimSpace(os.Getenv("SMTP_TLS_MODE")))
	if tlsMode == "" {
		tlsMode = defaultTLSMode
	}
	price := strings.TrimSpace(os.Getenv("PAYMENT_PRICE"))
	if price == "" {
		price = defaultPrice
	}
	subject := strings.TrimSpace(os.Getenv("MAIL_SUBJECT"))
	if subject == "" {
		subject = defaultSubject
	}
	from := strings.TrimSpace(os.Getenv("MAIL_FROM"))
	if from == "" {
		from = defaultFrom
	}
	return Config{
		Host:               strings.TrimSpace(os.Getenv("SMTP_HOST")),
		Port:               port,
		Username:           os.Getenv("SMTP_USERNAME"),
		Password:           os.Getenv("SMTP_PASSWORD"),
		From:               from,
		Subject:            subject,
		PixKey:             strings.TrimSpace(os.Getenv("PAYMENT_PIX_KEY")),
		PaymentLink:        strings.TrimSpace(os.Getenv("PAYMENT_LINK")),
		Price:              price,
		TLSMode:            tlsMode,
		InsecureSkipVerify: os.Getenv("SMTP_INSECURE_SKIP_VERIFY") == "1",
	}
}

// Mailer sends transactional emails over SMTP.
type Mailer struct {
	cfg Config
}

// NewMailer builds a Mailer from cfg.
func NewMailer(cfg Config) *Mailer {
	return &Mailer{cfg: cfg}
}

// Enabled reports whether the mailer is configured (host e from definidos).
func (m *Mailer) Enabled() bool {
	return m.cfg.Host != "" && m.cfg.From != ""
}

// SendWelcome envia o e-mail de boas-vindas do cadastro para `to`.
// Retorna erro se o mailer estiver desabilitado ou se o envio SMTP falhar.
func (m *Mailer) SendWelcome(to, displayName string) error {
	if !m.Enabled() {
		return fmt.Errorf("mailer desabilitado (defina SMTP_HOST e MAIL_FROM)")
	}
	if strings.TrimSpace(m.cfg.PaymentLink) == "" && strings.TrimSpace(m.cfg.PixKey) == "" {
		log.Printf("[Mail] PAYMENT_LINK e PAYMENT_PIX_KEY vazios; e-mail de boas-vindas usará placeholder de pagamento")
	}
	body := buildWelcomeBody(m.cfg, displayName)
	msg := buildMessage(m.cfg.From, to, m.cfg.Subject, body)
	return m.send(to, msg)
}

// send roda a conversa SMTP: dial → (TLS) → auth → MAIL/RCPT/DATA.
func (m *Mailer) send(to string, msg []byte) error {
	addr := net.JoinHostPort(m.cfg.Host, strconv.Itoa(m.cfg.Port))

	var (
		conn net.Conn
		err  error
	)
	switch m.cfg.TLSMode {
	case "implicit":
		conn, err = tls.Dial("tcp", addr, &tls.Config{
			ServerName:         m.cfg.Host,
			InsecureSkipVerify: m.cfg.InsecureSkipVerify,
		})
	case "none":
		conn, err = net.Dial("tcp", addr)
	default: // "starttls"
		conn, err = net.Dial("tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("conectar ao SMTP %s: %w", addr, err)
	}
	defer conn.Close()

	c, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		return fmt.Errorf("smtp.NewClient: %w", err)
	}
	defer c.Close()

	if m.cfg.TLSMode == "starttls" {
		if err := c.StartTLS(&tls.Config{
			ServerName:         m.cfg.Host,
			InsecureSkipVerify: m.cfg.InsecureSkipVerify,
		}); err != nil {
			return fmt.Errorf("STARTTLS: %w", err)
		}
	}

	if m.cfg.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)); err != nil {
			return fmt.Errorf("autenticação SMTP: %w", err)
		}
	}

	if err := c.Mail(m.cfg.From); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		w.Close()
		return fmt.Errorf("escrever corpo: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("finalizar corpo: %w", err)
	}
	if err := c.Quit(); err != nil {
		return fmt.Errorf("QUIT: %w", err)
	}
	return nil
}

// paymentLine monta a linha "Pagamento:". Prioridade: PAYMENT_LINK >
// PAYMENT_PIX_KEY > placeholder.
func paymentLine(cfg Config) string {
	if link := strings.TrimSpace(cfg.PaymentLink); link != "" {
		return "Pagamento: " + link
	}
	if key := strings.TrimSpace(cfg.PixKey); key != "" {
		return "Pagamento: Pix " + key
	}
	return placeholderLine
}

// buildWelcomeBody renderiza o corpo fixo do e-mail de boas-vindas (texto puro).
func buildWelcomeBody(cfg Config, displayName string) string {
	greeting := "Olá!"
	if name := strings.TrimSpace(displayName); name != "" {
		greeting = "Olá, " + name + "!"
	}
	return fmt.Sprintf(
		"%s Tudo bem?\n"+
			"\n"+
			"Identificamos que o seu cadastro no TikTok Live Monitor foi concluído com sucesso.\n"+
			"\n"+
			"Para liberar o acesso à plataforma, é necessário realizar o pagamento de R$ %s.\n"+
			"\n"+
			"%s\n"+
			"\n"+
			"Após o pagamento, envie o comprovante respondendo a este e-mail. Assim que confirmarmos, seu acesso será liberado.\n"+
			"\n"+
			"Caso tenha alguma dúvida, estou à disposição.\n"+
			"\n"+
			"Atenciosamente,\n"+
			"Equipe TikTok Live Monitor",
		greeting,
		cfg.Price,
		paymentLine(cfg),
	)
}

// encodeSubjectHeader codifica o header Subject em UTF-8 (MIME B-encoding)
// quando contém caracteres não-ASCII.
func encodeSubjectHeader(subject string) string {
	if isASCII(subject) {
		return subject
	}
	return "=?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(subject)) + "?="
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 0x7e {
			return false
		}
	}
	return true
}

// qpEncodeLine codifica uma linha em quoted-printable: bytes > 0x7e, < 0x20
// e '=' viram =XX (bytes de sequências UTF-8 são codificados individualmente).
func qpEncodeLine(line string) string {
	var b strings.Builder
	b.Grow(len(line))
	for i := 0; i < len(line); i++ {
		c := line[i]
		if c == '=' || c < 0x20 || c > 0x7e {
			b.WriteString(fmt.Sprintf("=%02X", c))
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// buildMessage monta a mensagem RFC 822 completa (quebras CRLF).
func buildMessage(from, to, subject, body string) []byte {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + encodeSubjectHeader(subject) + "\r\n")
	b.WriteString("Date: " + time.Now().UTC().Format(time.RFC1123) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	b.WriteString("\r\n")
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if i > 0 {
			b.WriteString("\r\n")
		}
		b.WriteString(qpEncodeLine(line))
	}
	b.WriteString("\r\n")
	return []byte(b.String())
}
