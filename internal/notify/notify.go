// Package notify fans platform events out to an outbound webhook (Slack/Discord
// compatible) and, when SMTP is configured, email. It is best-effort: a failing
// sink is logged and never propagated to the caller, so a notification problem
// never fails a deploy, backup, or disk check.
package notify

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/bograh/cargo/internal/crypto"
	"github.com/bograh/cargo/internal/db/sqlc"
	"github.com/bograh/cargo/internal/mailer"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const webhookSettingKey = "notify_webhook"

// Event is a notification to deliver.
type Event struct {
	Kind    string      // deploy_failed | deploy_succeeded | disk_low | backup_failed
	Title   string      // short subject line
	Message string      // human-readable detail
	OrgID   pgtype.UUID // valid → email org owners/admins; invalid → instance admins
}

// SMTPProvider returns the current SMTP relay config, or nil when unconfigured.
type SMTPProvider func(ctx context.Context) *mailer.SMTP

type Service struct {
	q    *sqlc.Queries
	box  *crypto.Box
	smtp SMTPProvider
	http *http.Client

	// Test seams; nil → real implementations.
	sendMail       func(cfg mailer.SMTP, to []string, subject, html, text string) error
	postHook       func(ctx context.Context, url, payload string) error
	sendWebhookURL func(ctx context.Context) (string, error)
}

func NewService(pool *pgxpool.Pool, box *crypto.Box, smtp SMTPProvider) *Service {
	return &Service{
		q:    sqlc.New(pool),
		box:  box,
		smtp: smtp,
		http: &http.Client{Timeout: 10 * time.Second},
	}
}

// sealWebhook encodes the URL as the {"enc": base64} instance_settings shape.
//
// The base64 matters: sealed bytes are not valid UTF-8, and encoding/json
// replaces invalid bytes in a Go string with U+FFFD. Storing the raw ciphertext
// in a JSON string therefore corrupts it on the way in, and the value can never
// be decrypted again. sealWebhook/openWebhook are split out so that round-trip
// is unit-testable without a database — the bug above shipped precisely because
// the tests stubbed this path out.
func sealWebhook(box *crypto.Box, url string) ([]byte, error) {
	sealed, err := box.Seal([]byte(url))
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{"enc": base64.StdEncoding.EncodeToString(sealed)})
}

func openWebhook(box *crypto.Box, raw []byte) (string, error) {
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", err
	}
	sealed, err := base64.StdEncoding.DecodeString(m["enc"])
	if err != nil {
		return "", err
	}
	plain, err := box.Open(sealed)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// SetWebhook stores (or, with an empty url, clears) the outbound webhook URL,
// encrypted at rest.
func (s *Service) SetWebhook(ctx context.Context, url string) error {
	if url == "" {
		return s.q.DeleteInstanceSetting(ctx, webhookSettingKey)
	}
	val, err := sealWebhook(s.box, url)
	if err != nil {
		return err
	}
	_, err = s.q.UpsertInstanceSetting(ctx, sqlc.UpsertInstanceSettingParams{Key: webhookSettingKey, Value: val})
	return err
}

// WebhookConfigured reports whether an outbound webhook URL is set (without
// returning it — the URL is write-only).
func (s *Service) WebhookConfigured(ctx context.Context) bool {
	configured, _ := s.WebhookStatus(ctx)
	return configured
}

// WebhookStatus distinguishes "no webhook" from "a webhook is stored but
// cannot be read". A released version corrupted the URL on save (raw ciphertext
// inside a JSON string, which encoding/json mangles), and such a row can never
// be decrypted. Reporting that as simply "not configured" would leave an admin
// who did set one believing alerts were on their way, so the second return
// value marks a stored-but-unreadable value that has to be entered again.
func (s *Service) WebhookStatus(ctx context.Context) (configured, needsReentry bool) {
	row, err := s.q.GetInstanceSetting(ctx, webhookSettingKey)
	if err != nil {
		return false, false
	}
	// An explicitly cleared value is "not configured", not a corrupt one — it
	// would otherwise decrypt-fail and raise a false alarm.
	var m map[string]string
	if err := json.Unmarshal(row.Value, &m); err != nil {
		return false, true
	}
	if m["enc"] == "" {
		return false, false
	}
	if _, err := openWebhook(s.box, row.Value); err != nil {
		return false, true
	}
	return true, false
}

func (s *Service) webhookURL(ctx context.Context) (string, error) {
	row, err := s.q.GetInstanceSetting(ctx, webhookSettingKey)
	if err != nil {
		return "", err
	}
	return openWebhook(s.box, row.Value)
}

// Notify delivers an event to every configured sink. Best-effort.
func (s *Service) Notify(ctx context.Context, ev Event) {
	s.sendWebhook(ctx, ev)
	s.sendEmail(ctx, ev)
}

// Alert implements the jobs.Alerter seam for platform-level conditions
// (backup_failed, disk_low) that carry no org.
func (s *Service) Alert(ctx context.Context, kind, detail string) {
	s.Notify(ctx, Event{Kind: kind, Title: "Cargo: " + kind, Message: detail})
}

// DeployFinished implements the jobs.Notifier seam. Failures always notify;
// successes only when the app opted in.
func (s *Service) DeployFinished(ctx context.Context, appID pgtype.UUID, result string) {
	app, err := s.q.GetApplication(ctx, appID)
	if err != nil {
		return
	}
	if result == "live" && !app.NotifyOnSuccess {
		return
	}
	kind := "deploy_failed"
	title := fmt.Sprintf("Cargo: deploy of %s failed", app.Name)
	if result == "live" {
		kind = "deploy_succeeded"
		title = fmt.Sprintf("Cargo: deploy of %s succeeded", app.Name)
	}
	s.Notify(ctx, Event{Kind: kind, Title: title, Message: title, OrgID: app.OrgID})
}

func (s *Service) sendWebhook(ctx context.Context, ev Event) {
	resolve := s.sendWebhookURL
	if resolve == nil {
		resolve = s.webhookURL
	}
	url, err := resolve(ctx)
	if err != nil || url == "" {
		return // not configured
	}
	text := ev.Title
	if ev.Message != "" && ev.Message != ev.Title {
		text = ev.Title + "\n" + ev.Message
	}
	// "text" (Slack) and "content" (Discord) — each ignores the other's field.
	payload, _ := json.Marshal(map[string]string{"text": text, "content": text})
	post := s.postHook
	if post == nil {
		post = s.httpPost
	}
	if err := post(ctx, url, string(payload)); err != nil {
		slog.Warn("notify webhook failed", "kind", ev.Kind, "err", err)
	}
}

func (s *Service) httpPost(ctx context.Context, url, payload string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader([]byte(payload)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %d", resp.StatusCode)
	}
	return nil
}

func (s *Service) sendEmail(ctx context.Context, ev Event) {
	if s.smtp == nil {
		return
	}
	cfg := s.smtp(ctx)
	if cfg == nil {
		return // SMTP not configured
	}
	to, err := s.recipients(ctx, ev.OrgID)
	if err != nil || len(to) == 0 {
		return
	}
	send := s.sendMail
	if send == nil {
		send = mailer.Send
	}
	html := "<p>" + ev.Message + "</p>"
	if err := send(*cfg, to, ev.Title, html, ev.Message+"\n"); err != nil {
		slog.Warn("notify email failed", "kind", ev.Kind, "err", err)
	}
}

// recipients returns email addresses: org owners/admins for an org event, else
// all instance admins for a platform event.
func (s *Service) recipients(ctx context.Context, orgID pgtype.UUID) ([]string, error) {
	if orgID.Valid {
		members, err := s.q.ListMembers(ctx, orgID)
		if err != nil {
			return nil, err
		}
		var to []string
		for _, m := range members {
			if m.Role == "owner" || m.Role == "admin" {
				to = append(to, m.Email)
			}
		}
		return to, nil
	}
	users, err := s.q.ListUsers(ctx)
	if err != nil && err != pgx.ErrNoRows {
		return nil, err
	}
	var to []string
	for _, u := range users {
		if u.IsInstanceAdmin {
			to = append(to, u.Email)
		}
	}
	return to, nil
}
