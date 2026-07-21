// Package mailer sends transactional email over SMTP using the instance's
// configured credentials. It is a leaf package: callers map their own config
// (e.g. settings.SMTPConfig) onto SMTP and render message bodies with the
// templates in this package.
package mailer

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
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

// Send delivers one message to the given recipients. When textBody is
// non-empty the message is multipart/alternative (text + HTML); otherwise it
// is HTML-only.
//
// Transport is chosen by port: 465 uses implicit TLS; every other port dials
// plaintext and upgrades via STARTTLS when the server advertises it (25/587).
// PLAIN auth is used when a username is set. Errors are wrapped per stage
// (connect / starttls / auth / from / rcpt / data) so failures are diagnosable.
func Send(cfg SMTP, to []string, subject, htmlBody, textBody string) error {
	if cfg.Host == "" || cfg.Port == 0 || cfg.From == "" {
		return ErrNotConfigured
	}
	if len(to) == 0 {
		return errors.New("mailer: no recipients")
	}
	msg, err := buildMessage(cfg.From, to, subject, htmlBody, textBody)
	if err != nil {
		return err
	}
	return deliver(cfg, to, msg)
}

func deliver(cfg SMTP, to []string, msg []byte) error {
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	tlsCfg := &tls.Config{ServerName: cfg.Host}

	var client *smtp.Client
	if cfg.Port == 465 {
		conn, err := tls.Dial("tcp", addr, tlsCfg)
		if err != nil {
			return fmt.Errorf("smtp: TLS connect to %s failed: %w", addr, err)
		}
		if client, err = smtp.NewClient(conn, cfg.Host); err != nil {
			_ = conn.Close()
			return fmt.Errorf("smtp: handshake failed: %w", err)
		}
	} else {
		var err error
		if client, err = smtp.Dial(addr); err != nil {
			return fmt.Errorf("smtp: connect to %s failed: %w", addr, err)
		}
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(tlsCfg); err != nil {
				_ = client.Close()
				return fmt.Errorf("smtp: STARTTLS failed: %w", err)
			}
		}
	}
	defer func() { _ = client.Close() }()

	if cfg.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)); err != nil {
			return fmt.Errorf("smtp: authentication failed: %w", err)
		}
	}
	if err := client.Mail(cfg.From); err != nil {
		return fmt.Errorf("smtp: MAIL FROM %q rejected: %w", cfg.From, err)
	}
	for _, rcpt := range to {
		if err := client.Rcpt(rcpt); err != nil {
			return fmt.Errorf("smtp: recipient %q rejected: %w", rcpt, err)
		}
	}
	wc, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp: DATA rejected: %w", err)
	}
	if _, err := wc.Write(msg); err != nil {
		return fmt.Errorf("smtp: writing message failed: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("smtp: server rejected message: %w", err)
	}
	return client.Quit()
}

// buildMessage assembles RFC 5322 headers and body. Header values are stripped
// of CR/LF to prevent header injection via a crafted subject or address.
func buildMessage(from string, to []string, subject, htmlBody, textBody string) ([]byte, error) {
	sanitized := make([]string, len(to))
	for i, t := range to {
		sanitized[i] = header(t)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", header(from))
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(sanitized, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", header(subject))
	b.WriteString("MIME-Version: 1.0\r\n")

	if textBody == "" {
		b.WriteString("Content-Type: text/html; charset=UTF-8\r\n\r\n")
		b.WriteString(htmlBody)
		return []byte(b.String()), nil
	}

	boundary, err := randomBoundary()
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", boundary)
	fmt.Fprintf(&b, "--%s\r\n", boundary)
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
	b.WriteString(textBody)
	b.WriteString("\r\n")
	fmt.Fprintf(&b, "--%s\r\n", boundary)
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n\r\n")
	b.WriteString(htmlBody)
	b.WriteString("\r\n")
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return []byte(b.String()), nil
}

func header(v string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(v)
}

func randomBoundary() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "cargo-" + hex.EncodeToString(buf), nil
}
