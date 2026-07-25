package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type stubNotify struct {
	url        string
	configured bool
}

func (s *stubNotify) SetWebhook(_ context.Context, url string) error {
	s.url = url
	s.configured = url != ""
	return nil
}
func (s *stubNotify) WebhookConfigured(context.Context) bool { return s.configured }

func TestNotifyWebhookSetAndGetHidesURL(t *testing.T) {
	n := &stubNotify{}
	s := &Server{notify: n}

	// PUT stores the URL.
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"url":"https://hooks.example.com/secret-path"}`)
	s.handlePutNotifyWebhook(rec, httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings/notify-webhook", body))
	if rec.Code != http.StatusOK || n.url != "https://hooks.example.com/secret-path" {
		t.Fatalf("put failed: code=%d url=%q", rec.Code, n.url)
	}

	// GET reports configured=true but must NEVER echo the URL.
	rec = httptest.NewRecorder()
	s.handleGetNotifyWebhook(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings/notify-webhook", nil))
	if strings.Contains(rec.Body.String(), "secret-path") {
		t.Fatalf("GET leaked the webhook URL: %s", rec.Body)
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["configured"] != true {
		t.Fatalf("expected configured=true, got %v", out)
	}
}

func TestNotifyWebhookRequiresURL(t *testing.T) {
	s := &Server{notify: &stubNotify{}}
	rec := httptest.NewRecorder()
	s.handlePutNotifyWebhook(rec, httptest.NewRequest(http.MethodPut, "/x", strings.NewReader(`{}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty url should be 400, got %d", rec.Code)
	}
}
