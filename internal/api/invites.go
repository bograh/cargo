package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/bograh/cargo/internal/mailer"
	"github.com/bograh/cargo/internal/orgs"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const defaultInviteTTL = 7 * 24 * time.Hour

// baseURL reconstructs the public origin from the request, honoring the
// reverse proxy's X-Forwarded-Proto (Cargo runs behind Traefik).
func baseURL(r *http.Request) string {
	scheme := "http"
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = p
	} else if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// inviteResult reports the outcome for one email invite. When the email could
// not be sent (SMTP unset or a send error), link is populated so the admin can
// share it manually.
type inviteResult struct {
	Email string `json:"email"`
	Sent  bool   `json:"sent"`
	Link  string `json:"link,omitempty"`
	Error string `json:"error,omitempty"`
}

func (s *Server) handleCreateInvite(w http.ResponseWriter, r *http.Request) {
	id, ok := orgIDParam(w, r)
	if !ok {
		return
	}
	var body struct {
		Role   string   `json:"role"`
		Emails []string `json:"emails"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		Error(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	actor := userFrom(r.Context()).ID

	emails := normalizeEmails(body.Emails)

	// No emails → a single shareable link invite (unchanged behavior).
	if len(emails) == 0 {
		token, inv, err := s.orgs.CreateInvite(r.Context(), id, actor, body.Role, "", defaultInviteTTL)
		if err != nil {
			orgError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"invite": inv, "token": token})
		return
	}

	smtp := s.smtpConfig(r.Context())
	// Org name and inviter for the email body. Best-effort: the invite still
	// issues if these can't be resolved.
	orgName := "your organization"
	if org, _, err := s.orgs.Get(r.Context(), id, actor); err == nil {
		orgName = org.Name
	}
	invitedBy := userFrom(r.Context()).Email

	results := make([]inviteResult, 0, len(emails))
	for _, email := range emails {
		token, _, err := s.orgs.CreateInvite(r.Context(), id, actor, body.Role, email, defaultInviteTTL)
		if err != nil {
			// Role/permission errors apply to every email — fail the whole call.
			if errors.Is(err, orgs.ErrForbidden) || errors.Is(err, orgs.ErrBadRole) || errors.Is(err, orgs.ErrNotFound) {
				orgError(w, err)
				return
			}
			results = append(results, inviteResult{Email: email, Sent: false, Error: err.Error()})
			continue
		}
		link := baseURL(r) + "/invite/" + token
		res := inviteResult{Email: email}
		if smtp == nil {
			res.Link, res.Error = link, "email not sent (SMTP not configured)"
		} else if err := sendInviteEmail(*smtp, email, mailer.InviteData{
			OrgName: orgName, Role: body.Role, InvitedBy: invitedBy, Link: link,
		}); err != nil {
			slog.Error("invite email delivery failed", "to", email, "err", err)
			res.Link, res.Error = link, "email delivery failed — see server logs or use the link above"
		} else {
			res.Sent = true
		}
		results = append(results, res)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"results": results})
}

// smtpConfig loads the instance SMTP relay config, or nil if unavailable.
func (s *Server) smtpConfig(ctx context.Context) *mailer.SMTP {
	if s.instanceSettings == nil {
		return nil
	}
	cfg, err := s.instanceSettings.SMTP(ctx)
	if err != nil || cfg == nil {
		return nil
	}
	return &mailer.SMTP{Host: cfg.Host, Port: cfg.Port, Username: cfg.Username, Password: cfg.Password, From: cfg.From}
}

func sendInviteEmail(cfg mailer.SMTP, to string, data mailer.InviteData) error {
	subject, htmlBody, textBody := mailer.RenderInvite(data)
	return mailer.Send(cfg, []string{to}, subject, htmlBody, textBody)
}

// normalizeEmails trims, lowercases, drops blanks/duplicates, and keeps only
// entries that look like an email address.
func normalizeEmails(in []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(in))
	for _, raw := range in {
		e := strings.ToLower(strings.TrimSpace(raw))
		if e == "" || !strings.Contains(e, "@") || seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	return out
}

func (s *Server) handleListInvites(w http.ResponseWriter, r *http.Request) {
	id, ok := orgIDParam(w, r)
	if !ok {
		return
	}
	list, err := s.orgs.ListInvites(r.Context(), id, userFrom(r.Context()).ID)
	if err != nil {
		orgError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleRevokeInvite(w http.ResponseWriter, r *http.Request) {
	id, ok := orgIDParam(w, r)
	if !ok {
		return
	}
	var inviteID pgtype.UUID
	if err := inviteID.Scan(chi.URLParam(r, "inviteID")); err != nil {
		Error(w, http.StatusNotFound, "not_found", "invite not found")
		return
	}
	if err := s.orgs.RevokeInvite(r.Context(), id, userFrom(r.Context()).ID, inviteID); err != nil {
		orgError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

// handlePreviewInvite is public (no auth): it lets an invited user see the org
// and role before signing in.
func (s *Server) handlePreviewInvite(w http.ResponseWriter, r *http.Request) {
	p, err := s.orgs.PreviewInvite(r.Context(), chi.URLParam(r, "token"))
	if errors.Is(err, orgs.ErrInviteInvalid) {
		Error(w, http.StatusNotFound, "invite_invalid", "invite is invalid, revoked, or expired")
		return
	}
	if err != nil {
		orgError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"org_name": p.OrgName, "role": p.Role, "email": p.Email})
}

func (s *Server) handleAcceptInvite(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Token == "" {
		Error(w, http.StatusBadRequest, "validation_failed", "token is required")
		return
	}
	org, err := s.orgs.AcceptInvite(r.Context(), body.Token, userFrom(r.Context()).ID)
	if errors.Is(err, orgs.ErrInviteInvalid) {
		Error(w, http.StatusBadRequest, "invite_invalid", "invite is invalid, revoked, or expired")
		return
	}
	if err != nil {
		orgError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, orgJSON(org))
}
