package mailer

import (
	"os"
	"strings"
	"testing"
)

func TestRenderInvite(t *testing.T) {
	d := InviteData{
		OrgName:   "Acme Freight",
		Role:      "member",
		InvitedBy: "admin@acme.co",
		Link:      "https://cargo.example.com/invite/abc123",
	}
	subject, html, text := RenderInvite(d)

	if !strings.Contains(subject, "Acme Freight") {
		t.Errorf("subject missing org name: %q", subject)
	}
	for _, want := range []string{"Acme Freight", "admin@acme.co", "member", d.Link, "#f5a524", "Accept invitation"} {
		if !strings.Contains(html, want) {
			t.Errorf("html missing %q", want)
		}
	}
	for _, want := range []string{"Acme Freight", "admin@acme.co", "member", d.Link} {
		if !strings.Contains(text, want) {
			t.Errorf("text missing %q", want)
		}
	}

	// Optionally dump a preview for manual inspection.
	if p := os.Getenv("INVITE_PREVIEW_OUT"); p != "" {
		if err := os.WriteFile(p, []byte(html), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRenderInviteEscapesUserInput(t *testing.T) {
	_, html, _ := RenderInvite(InviteData{
		OrgName:   `<script>alert(1)</script>`,
		Role:      "member",
		InvitedBy: "a@b.co",
		Link:      "https://x/invite/t",
	})
	if strings.Contains(html, "<script>alert(1)</script>") {
		t.Error("org name was not HTML-escaped")
	}
}

func TestBuildMessageMultipart(t *testing.T) {
	msg, err := buildMessage("from@x.co", []string{"to@y.co"}, "Subj", "<p>hi</p>", "hi")
	if err != nil {
		t.Fatal(err)
	}
	s := string(msg)
	for _, want := range []string{"multipart/alternative", "text/plain", "text/html", "Subject: Subj"} {
		if !strings.Contains(s, want) {
			t.Errorf("message missing %q", want)
		}
	}
}

func TestBuildMessageStripsHeaderInjection(t *testing.T) {
	msg, err := buildMessage("from@x.co", []string{"to@y.co"}, "Subj\r\nBcc: evil@x.co", "<p>hi</p>", "")
	if err != nil {
		t.Fatal(err)
	}
	// The CRLF must be stripped so no new header line is created; the text may
	// still appear inline on the (single) Subject line harmlessly.
	if strings.Contains(string(msg), "\nBcc:") {
		t.Error("header injection via subject created a new header line")
	}
}
