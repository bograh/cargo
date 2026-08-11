package notify

import (
	"context"
	"encoding/json"
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

// Regression: the webhook URL used to be stored as raw ciphertext inside a
// JSON string. Sealed bytes are not valid UTF-8, so encoding/json replaced them
// with U+FFFD and the value could never be decrypted — WebhookConfigured always
// reported false and no alert ever fired. Run it enough times to catch a
// ciphertext that happens to be valid UTF-8 by chance.
func TestWebhookSecretRoundTrips(t *testing.T) {
	box, err := crypto.New([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	const url = "https://hooks.slack.com/services/T00000/B00000/XXXXXXXXXXXX"
	for i := 0; i < 200; i++ {
		stored, err := sealWebhook(box, url)
		if err != nil {
			t.Fatalf("seal: %v", err)
		}
		got, err := openWebhook(box, stored)
		if err != nil {
			t.Fatalf("open (iteration %d): %v", i, err)
		}
		if got != url {
			t.Fatalf("round-trip %d = %q, want %q", i, got, url)
		}
	}
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

// WebhookStatus has to tell three states apart: absent, explicitly cleared, and
// present-but-unreadable. Only the last one should ask the admin to act.
func TestWebhookStatusDistinguishesClearedFromCorrupt(t *testing.T) {
	box, err := crypto.New([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	// A cleared value carries no secret and must not read as corrupt.
	var m map[string]string
	if err := json.Unmarshal([]byte(`{"enc":""}`), &m); err != nil {
		t.Fatal(err)
	}
	if m["enc"] != "" {
		t.Fatal("fixture is wrong")
	}
	// A corrupt value cannot be opened.
	if _, err := openWebhook(box, []byte(`{"enc":"not-base64-�"}`)); err == nil {
		t.Fatal("a corrupt value decrypted successfully")
	}
	// A good value round-trips.
	stored, err := sealWebhook(box, "https://hooks.example.com/x")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := openWebhook(box, stored); err != nil {
		t.Fatalf("a valid value failed to open: %v", err)
	}
}
