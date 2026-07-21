package mailer

import (
	"bytes"
	"fmt"
	"html/template"
)

// InviteData is the render context for an organization invite email.
type InviteData struct {
	OrgName   string
	Role      string
	InvitedBy string // email of the admin who created the invite
	Link      string
}

// Freight palette (mirrors the web theme). Email clients require inline styles,
// so the template interpolates these directly.
const (
	colBg      = "#0b0c0e"
	colSurface = "#121316"
	colRaised  = "#17181c"
	colBorder  = "#23252b"
	colText    = "#e8e9eb"
	colMuted   = "#9aa0a8"
	colAmber   = "#f5a524"
	colOnAmber = "#14100a"
)

var inviteTmpl = template.Must(template.New("invite").Parse(`<!doctype html>
<html>
<body style="margin:0;padding:0;background:` + colBg + `;">
  <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:` + colBg + `;padding:32px 12px;">
    <tr>
      <td align="center">
        <table role="presentation" width="480" cellpadding="0" cellspacing="0" style="width:480px;max-width:100%;background:` + colSurface + `;border:1px solid ` + colBorder + `;border-radius:10px;overflow:hidden;font-family:-apple-system,'Segoe UI',system-ui,sans-serif;">
          <tr><td style="height:4px;background:` + colAmber + `;font-size:0;line-height:0;">&nbsp;</td></tr>
          <tr>
            <td style="padding:28px 32px 8px 32px;">
              <div style="font-size:20px;font-weight:700;letter-spacing:-0.01em;color:` + colText + `;">
                <span style="color:` + colAmber + `;">▤</span> Cargo
              </div>
            </td>
          </tr>
          <tr>
            <td style="padding:8px 32px 4px 32px;">
              <h1 style="margin:0;font-size:19px;font-weight:700;letter-spacing:-0.01em;color:` + colText + `;">You've been invited to {{.OrgName}}</h1>
            </td>
          </tr>
          <tr>
            <td style="padding:6px 32px 0 32px;font-size:15px;line-height:1.6;color:` + colMuted + `;">
              <p style="margin:0 0 6px 0;">
                <span style="color:` + colText + `;">{{.InvitedBy}}</span> invited you to join
                <span style="color:` + colText + `;">{{.OrgName}}</span> on Cargo as
                <span style="display:inline-block;padding:1px 8px;border-radius:9999px;background:rgba(245,165,36,0.12);border:1px solid rgba(245,165,36,0.35);color:` + colAmber + `;font-size:12px;font-weight:600;text-transform:uppercase;letter-spacing:0.08em;">{{.Role}}</span>.
              </p>
            </td>
          </tr>
          <tr>
            <td style="padding:22px 32px 8px 32px;">
              <a href="{{.Link}}" style="display:inline-block;background:` + colAmber + `;color:` + colOnAmber + `;font-size:15px;font-weight:600;text-decoration:none;padding:11px 22px;border-radius:8px;">Accept invitation</a>
            </td>
          </tr>
          <tr>
            <td style="padding:8px 32px 0 32px;font-size:13px;line-height:1.6;color:` + colMuted + `;">
              <p style="margin:0;">Or paste this link into your browser:</p>
              <p style="margin:4px 0 0 0;">
                <a href="{{.Link}}" style="color:` + colAmber + `;word-break:break-all;">{{.Link}}</a>
              </p>
            </td>
          </tr>
          <tr>
            <td style="padding:24px 32px 28px 32px;border-top:1px solid ` + colBorder + `;margin-top:20px;">
              <p style="margin:20px 0 0 0;font-size:12px;line-height:1.6;color:` + colMuted + `;">
                This invitation expires in 7 days. If you weren't expecting it, you can safely ignore this email.
              </p>
            </td>
          </tr>
        </table>
      </td>
    </tr>
  </table>
</body>
</html>`))

// RenderInvite returns the subject, HTML body, and plain-text body for an
// organization invite email.
func RenderInvite(d InviteData) (subject, htmlBody, textBody string) {
	subject = fmt.Sprintf("You've been invited to %s on Cargo", d.OrgName)
	var buf bytes.Buffer
	if err := inviteTmpl.Execute(&buf, d); err != nil {
		// A template execution error should never happen with a static
		// template; fall back to a minimal body rather than sending nothing.
		htmlBody = fmt.Sprintf(`<p>You've been invited to join %s on Cargo. <a href="%s">Accept invitation</a></p>`,
			template.HTMLEscapeString(d.OrgName), template.HTMLEscapeString(d.Link))
	} else {
		htmlBody = buf.String()
	}
	textBody = fmt.Sprintf(
		"You've been invited to %s on Cargo.\n\n%s invited you to join as %s.\n\nAccept the invitation:\n%s\n\nThis invitation expires in 7 days. If you weren't expecting it, you can ignore this email.\n",
		d.OrgName, d.InvitedBy, d.Role, d.Link)
	return subject, htmlBody, textBody
}
