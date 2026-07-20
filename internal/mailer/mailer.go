// Package mailer sends transactional email over SMTP using the instance's
// configured credentials. It is a leaf package: callers map their own config
// (e.g. settings.SMTPConfig) onto SMTP and build the message body.
package mailer

import (
	"errors"
	"fmt"
	"net/smtp"
	"strings"
)

// SMTP holds the connection parameters for an SMTP relay.
type SMTP struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
}

// ErrNotConfigured is returned by callers when no SMTP relay is set up.
var ErrNotConfigured = errors.New("smtp is not configured")

// Send delivers one HTML message to the given recipients. It uses PLAIN auth
// when a username is set (net/smtp upgrades to STARTTLS when the server
// advertises it, e.g. on port 587). Implicit-TLS ports (465) are not supported.
func Send(cfg SMTP, to []string, subject, htmlBody string) error {
	if cfg.Host == "" || cfg.Port == 0 || cfg.From == "" {
		return ErrNotConfigured
	}
	if len(to) == 0 {
		return errors.New("mailer: no recipients")
	}
	var auth smtp.Auth
	if cfg.Username != "" {
		auth = smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", cfg.From)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(to, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(htmlBody)
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	return smtp.SendMail(addr, auth, cfg.From, to, []byte(b.String()))
}
