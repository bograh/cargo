package notify

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bograh/cargo/internal/crypto"
	"github.com/bograh/cargo/internal/mailer"
)

var errNoWebhook = errors.New("no webhook")

func newTestService(t *testing.T) *Service {
	t.Helper()
	box, err := crypto.New([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	// No pool: DB-touching paths (recipients, webhookURL) are exercised via
	// the seams below, not real queries.
	return &Service{box: box, smtp: func(context.Context) *mailer.SMTP { return nil }}
}

func TestNotifyFiresWebhook(t *testing.T) {
	s := newTestService(t)
	var posted []string
	s.postHook = func(_ context.Context, url, payload string) error {
		posted = append(posted, payload)
		return nil
	}
	// Stub webhookURL by storing a value through the seam: emulate configured
	// webhook by overriding webhookURL via a wrapper is not possible, so drive
	// through sendWebhook with a fake resolver.
	s.sendWebhookURL = func(context.Context) (string, error) { return "http://hook.example", nil }

	s.Notify(context.Background(), Event{Kind: "backup_failed", Title: "boom", Message: "disk gone"})
	if len(posted) != 1 {
		t.Fatalf("expected 1 webhook post, got %d", len(posted))
	}
	if !strings.Contains(posted[0], "boom") || !strings.Contains(posted[0], "\"content\"") {
		t.Fatalf("payload missing text/content: %s", posted[0])
	}
}

func TestNotifyWebhookErrorDoesNotPanic(t *testing.T) {
	s := newTestService(t)
	s.sendWebhookURL = func(context.Context) (string, error) { return "http://hook.example", nil }
	s.postHook = func(context.Context, string, string) error { return context.DeadlineExceeded }
	// Must not panic or propagate; best-effort.
	s.Notify(context.Background(), Event{Kind: "disk_low", Title: "low"})
}

func TestNotifyNoWebhookConfigured(t *testing.T) {
	s := newTestService(t)
	called := false
	s.postHook = func(context.Context, string, string) error { called = true; return nil }
	s.sendWebhookURL = func(context.Context) (string, error) { return "", errNoWebhook }
	s.Notify(context.Background(), Event{Kind: "disk_low", Title: "low"})
	if called {
		t.Fatal("webhook should not be posted when unconfigured")
	}
}
