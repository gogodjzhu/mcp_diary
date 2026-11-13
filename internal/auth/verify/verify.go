// Package verify issues and redeems email verification codes, enforcing the
// rate limits and the daily send quota around them.
package verify

import (
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/gogodjzhu/mcp-diary/internal/auth/mail"
	"github.com/gogodjzhu/mcp-diary/internal/auth/user"
)

// Quota thresholds for the Aliyun DirectMail free tier.
const (
	dailyWarnAt  = 180
	dailyLimitAt = 200

	emailCooldown   = 60 * time.Second
	emailHourlyMax  = 5
	ipHourlyMax     = 10
	globalPerMinute = 30
)

// Sentinel errors.
var (
	ErrRateLimited  = errors.New("too many requests, try again later")
	ErrQuotaReached = errors.New("daily email quota reached")
	ErrSendFailed   = errors.New("failed to send email")
)

// Service issues verification codes.
type Service struct {
	store  *user.Store
	mailer *mail.Sender
	logger *slog.Logger

	mu      sync.Mutex
	sentAt  map[string][]time.Time // key -> send times within the last hour
	lastAt  map[string]time.Time   // email -> last send, for the cooldown
	global  []time.Time
	nowFunc func() time.Time
}

// New builds the service. mailer may be nil only when no code will be issued.
func New(store *user.Store, mailer *mail.Sender, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		store:   store,
		mailer:  mailer,
		logger:  logger,
		sentAt:  map[string][]time.Time{},
		lastAt:  map[string]time.Time{},
		nowFunc: time.Now,
	}
}

// Issue sends a code for purpose to email and stores it. The code is only
// persisted after the message is accepted, so a user never holds a code for a
// message that was never sent.
func (s *Service) Issue(email, purpose, ip string) error {
	email = user.NormalizeEmail(email)
	if !user.ValidEmail(email) {
		return user.ErrInvalidEmail
	}
	now := s.nowFunc()

	if err := s.allow(email, ip, now); err != nil {
		return err
	}
	if s.store.DailySent(now) >= dailyLimitAt {
		return ErrQuotaReached
	}

	code, err := user.NewCode()
	if err != nil {
		return err
	}
	if s.mailer == nil {
		return ErrSendFailed
	}
	if err := s.mailer.Send(mail.Message{
		To:      email,
		Subject: subjectFor(purpose, code),
		Body:    bodyFor(purpose, code),
	}); err != nil {
		s.logger.Warn("verification email failed", "email", user.MaskEmail(email), "purpose", purpose, "error", err)
		return ErrSendFailed
	}

	if err := s.store.PutPendingCode(purpose, email, code, now); err != nil {
		return err
	}
	total, err := s.store.AddDailySent(now, 1)
	if err != nil {
		return err
	}
	s.record(email, ip, now)
	if total >= dailyWarnAt {
		s.logger.Warn("daily email quota nearing limit", "sent", total, "limit", dailyLimitAt)
	}
	s.logger.Info("verification email sent", "email", user.MaskEmail(email), "purpose", purpose)
	return nil
}

// Redeem checks a code and consumes it.
func (s *Service) Redeem(email, purpose, code string) error {
	return s.store.RedeemCode(purpose, email, code, s.nowFunc())
}

func (s *Service) allow(email, ip string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if last, ok := s.lastAt[email]; ok && now.Sub(last) < emailCooldown {
		return ErrRateLimited
	}
	if len(s.recent(s.sentAt["email:"+email], now)) >= emailHourlyMax {
		return ErrRateLimited
	}
	if ip != "" && len(s.recent(s.sentAt["ip:"+ip], now)) >= ipHourlyMax {
		return ErrRateLimited
	}
	if len(s.recent(s.global, now, time.Minute)) >= globalPerMinute {
		return ErrRateLimited
	}
	return nil
}

func (s *Service) record(email, ip string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastAt[email] = now
	s.sentAt["email:"+email] = append(s.recent(s.sentAt["email:"+email], now), now)
	if ip != "" {
		s.sentAt["ip:"+ip] = append(s.recent(s.sentAt["ip:"+ip], now), now)
	}
	s.global = append(s.recent(s.global, now, time.Minute), now)
}

// recent drops timestamps older than an hour.
func (s *Service) recent(times []time.Time, now time.Time, window ...time.Duration) []time.Time {
	w := time.Hour
	if len(window) > 0 {
		w = window[0]
	}
	cutoff := now.Add(-w)
	out := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			out = append(out, t)
		}
	}
	return out
}

func subjectFor(purpose, code string) string {
	switch purpose {
	case user.PurposeReset:
		return "【mcp-diary】重置密码验证码 " + code
	case user.PurposeBind, user.PurposeChange:
		return "【mcp-diary】邮箱验证码 " + code
	default:
		return "【mcp-diary】注册验证码 " + code
	}
}

func bodyFor(purpose, code string) string {
	action := "完成操作"
	switch purpose {
	case user.PurposeRegister:
		action = "完成注册"
	case user.PurposeReset:
		action = "重置密码"
	case user.PurposeBind, user.PurposeChange:
		action = "验证邮箱"
	}
	return "你的验证码是 " + code + "，10 分钟内有效，用于" + action + "。\n" +
		"如果不是你本人操作，请忽略这封邮件。\n"
}
