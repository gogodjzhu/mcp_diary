package mail

import (
	"mime"
	netmail "net/mail"
	"regexp"
	"strings"
	"testing"
	"time"
)

func newTestSender(t *testing.T) *Sender {
	t.Helper()
	s, err := New(Config{
		Host:     "smtp.example.com",
		Port:     465,
		TLSMode:  TLSModeSSL,
		Username: "noreply@example.com",
		Password: "secret",
		FromName: "mcp-diary",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.now = func() time.Time { return time.Date(2026, 11, 13, 21, 34, 42, 0, time.FixedZone("CST", 8*3600)) }
	return s
}

// parse renders msg and parses the result the way a receiving MTA would. Raw
// UTF-8 in a header is illegal, so this also proves the message is RFC 5322
// compliant enough for stdlib parsing.
func parse(t *testing.T, s *Sender, msg Message) *netmail.Message {
	t.Helper()
	rendered := s.render(msg)
	parsed, err := netmail.ReadMessage(strings.NewReader(rendered))
	if err != nil {
		t.Fatalf("rendered message is not parsable: %v\n%s", err, rendered)
	}
	return parsed
}

func TestRenderIncludesDateAndMessageID(t *testing.T) {
	s := newTestSender(t)
	parsed := parse(t, s, Message{To: "user@example.org", Subject: "hello", Body: "body"})

	date := parsed.Header.Get("Date")
	if date == "" {
		t.Fatal("Date header is missing")
	}
	got, err := netmail.ParseDate(date)
	if err != nil {
		t.Fatalf("Date header %q does not parse: %v", date, err)
	}
	if !got.Equal(s.clock()) {
		t.Fatalf("Date = %v, want %v", got, s.clock())
	}

	id := parsed.Header.Get("Message-ID")
	ok, err := regexp.MatchString(`^<[0-9a-f]{32}@example\.com>$`, id)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("Message-ID %q is not a valid unique id on the sender domain", id)
	}
}

func TestRenderMessageIDIsUniquePerMessage(t *testing.T) {
	s := newTestSender(t)
	msg := Message{To: "user@example.org", Subject: "hello", Body: "body"}
	first := parse(t, s, msg).Header.Get("Message-ID")
	second := parse(t, s, msg).Header.Get("Message-ID")
	if first == second {
		t.Fatalf("Message-ID reused across messages: %s", first)
	}
}

func TestRenderEncodesNonASCIISubject(t *testing.T) {
	s := newTestSender(t)
	const subject = "【mcp-diary】注册验证码 427099"
	parsed := parse(t, s, Message{To: "user@example.org", Subject: subject, Body: "body"})

	raw := parsed.Header.Get("Subject")
	if strings.Contains(raw, "注册") || strings.Contains(raw, "【") {
		t.Fatalf("subject leaked raw UTF-8 into the header: %q", raw)
	}
	decoded, err := new(mime.WordDecoder).DecodeHeader(raw)
	if err != nil {
		t.Fatalf("decode subject %q: %v", raw, err)
	}
	if decoded != subject {
		t.Fatalf("subject = %q, want %q", decoded, subject)
	}
}

func TestRenderKeepsASCIISubjectPlain(t *testing.T) {
	s := newTestSender(t)
	parsed := parse(t, s, Message{To: "user@example.org", Subject: "mcp-diary verification code", Body: "body"})
	if got := parsed.Header.Get("Subject"); got != "mcp-diary verification code" {
		t.Fatalf("subject = %q, want it unchanged", got)
	}
}

func TestRenderEncodesNonASCIIDisplayName(t *testing.T) {
	s := newTestSender(t)
	s.cfg.FromName = "日记助手"
	parsed := parse(t, s, Message{To: "user@example.org", Subject: "code", Body: "body"})

	from := parsed.Header.Get("From")
	if strings.Contains(from, "日记助手") {
		t.Fatalf("display name leaked raw UTF-8: %q", from)
	}
	addrs, err := netmail.ParseAddressList(from)
	if err != nil {
		t.Fatalf("From %q does not parse: %v", from, err)
	}
	if len(addrs) != 1 {
		t.Fatalf("From has %d addresses, want 1", len(addrs))
	}
	if addrs[0].Name != "日记助手" {
		t.Fatalf("display name = %q, want %q", addrs[0].Name, "日记助手")
	}
	if addrs[0].Address != "noreply@example.com" {
		t.Fatalf("address = %q", addrs[0].Address)
	}
}

func TestRenderBodyUsesCRLF(t *testing.T) {
	s := newTestSender(t)
	rendered := s.render(Message{To: "user@example.org", Subject: "code", Body: "line1\nline2\n"})
	if !strings.Contains(rendered, "line1\r\nline2\r\n") {
		t.Fatalf("body is not CRLF-terminated:\n%q", rendered)
	}
	if strings.Contains(rendered, "line1\nline2") {
		t.Fatalf("body kept bare LF:\n%q", rendered)
	}
}