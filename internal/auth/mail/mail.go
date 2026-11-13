// Package mail sends transactional email over SMTP. It targets Aliyun DirectMail
// but speaks plain SMTP, so any standards-compliant server works.
package mail

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"os"
	"strings"
	"time"
)

// TLS modes.
const (
	TLSModeSSL      = "ssl"
	TLSModeStartTLS = "starttls"
	// TLSModePlain sends without encryption. It exists for tests against a
	// local sink; a production config must not use it.
	TLSModePlain = "plain"
)

// Config describes an SMTP submission endpoint.
type Config struct {
	Host     string
	Port     int
	TLSMode  string
	Username string
	Password string
	From     string
	FromName string
	Timeout  time.Duration
}

// Message is one outgoing email.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Sender delivers messages.
type Sender struct {
	cfg Config
	now func() time.Time
}

// New validates the configuration and returns a sender.
func New(cfg Config) (*Sender, error) {
	if cfg.Host == "" {
		return nil, errors.New("smtp host is required")
	}
	if cfg.Port <= 0 {
		return nil, errors.New("smtp port must be positive")
	}
	switch cfg.TLSMode {
	case TLSModeSSL, TLSModeStartTLS, TLSModePlain:
	default:
		return nil, fmt.Errorf("smtp tls mode %q must be %q, %q or %q", cfg.TLSMode, TLSModeSSL, TLSModeStartTLS, TLSModePlain)
	}
	if cfg.Username == "" || cfg.Password == "" {
		return nil, errors.New("smtp username and password are required")
	}
	if cfg.From == "" {
		cfg.From = cfg.Username
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	return &Sender{cfg: cfg, now: time.Now}, nil
}

// Config returns the resolved configuration.
func (s *Sender) Config() Config { return s.cfg }

// Send delivers msg. The envelope sender is always the authenticated address:
// Aliyun DirectMail rejects a MAIL FROM that differs from it.
func (s *Sender) Send(msg Message) error {
	// Test-only sink: write the message to a file instead of dialing SMTP, so an
	// end-to-end driver can read the verification code. Never set in production.
	if sink := os.Getenv("MCP_DIARY_MAIL_SINK"); sink != "" {
		return os.WriteFile(sink, []byte(s.render(msg)), 0o600)
	}
	if strings.TrimSpace(msg.To) == "" {
		return errors.New("recipient is required")
	}

	addr := net.JoinHostPort(s.cfg.Host, fmt.Sprintf("%d", s.cfg.Port))
	deadline := time.Now().Add(s.cfg.Timeout)

	var (
		conn net.Conn
		err  error
	)
	dialer := &net.Dialer{Timeout: s.cfg.Timeout}
	conn, err = dialer.Dial("tcp", addr)
	if err == nil && s.cfg.TLSMode == TLSModeSSL {
		conn = tls.Client(conn, s.tlsConfig())
	}
	if err != nil {
		return fmt.Errorf("dial smtp: %w", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(deadline)

	client, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		return fmt.Errorf("smtp handshake: %w", err)
	}
	defer func() { _ = client.Close() }()

	if s.cfg.TLSMode == TLSModeStartTLS {
		if err := client.StartTLS(s.tlsConfig()); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}
	// Plain auth requires a TLS connection. The plain mode is only used against
	// a local test sink, which accepts mail without authentication.
	if s.cfg.TLSMode != TLSModePlain {
		auth := smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := client.Mail(s.cfg.From); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	if err := client.Rcpt(msg.To); err != nil {
		return fmt.Errorf("smtp rcpt: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write([]byte(s.render(msg))); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp submit: %w", err)
	}
	return client.Quit()
}

func (s *Sender) tlsConfig() *tls.Config {
	return &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}
}

func (s *Sender) render(msg Message) string {
	from := s.cfg.From
	if s.cfg.FromName != "" {
		from = fmt.Sprintf("%s <%s>", encodeDisplayName(s.cfg.FromName), s.cfg.From)
	}
	body := strings.ReplaceAll(msg.Body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\n", "\r\n")

	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", msg.To)
	fmt.Fprintf(&b, "Subject: %s\r\n", encodeSubject(msg.Subject))
	// Date and Message-ID are mandatory for a syntactically valid message.
	// Receivers reject submissions without them: Gmail, for instance, answers
	// "550 5.7.1 Messages missing a valid Message-ID header are not accepted".
	fmt.Fprintf(&b, "Date: %s\r\n", s.clock().Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: %s\r\n", s.messageID())
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	return b.String()
}

func (s *Sender) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// messageID builds a globally unique id scoped to the sender's domain, as RFC
// 5322 section 3.6.4 requires.
func (s *Sender) messageID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("<%d@%s>", time.Now().UnixNano(), headerDomain(s.cfg.From))
	}
	return fmt.Sprintf("<%s@%s>", hex.EncodeToString(raw[:]), headerDomain(s.cfg.From))
}

// headerDomain is the domain part of an address, used to scope Message-IDs.
func headerDomain(address string) string {
	if at := strings.LastIndex(address, "@"); at >= 0 && at+1 < len(address) {
		return address[at+1:]
	}
	return "mcp-diary.local"
}

// encodeSubject renders a header value that is safe on the wire. Non-ASCII text
// must use RFC 2047 encoded-words; raw UTF-8 in a header is invalid and makes
// receivers treat the message as spam or reject it outright.
func encodeSubject(subject string) string {
	subject = sanitizeHeader(subject)
	if isASCII(subject) {
		return subject
	}
	return mime.BEncoding.Encode("UTF-8", subject)
}

// encodeDisplayName quotes an ASCII display name and encoded-words a non-ASCII
// one, so "mcp-diary" and Chinese names are both legal.
func encodeDisplayName(name string) string {
	name = sanitizeHeader(name)
	if name == "" {
		return ""
	}
	if !isASCII(name) {
		return mime.BEncoding.Encode("UTF-8", name)
	}
	return `"` + strings.ReplaceAll(name, `"`, "'") + `"`
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

func sanitizeHeader(value string) string {
	value = strings.ReplaceAll(value, "\r", "")
	value = strings.ReplaceAll(value, "\n", "")
	return strings.TrimSpace(value)
}
