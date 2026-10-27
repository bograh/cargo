package apps

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bograh/cargo/internal/giturl"
)

// maxPort is the highest TCP port an app can serve on. The check used to be
// `< 1` only, while the error message already promised a range — so a value
// like 70000 was accepted and rendered straight into a Traefik service label.
const maxPort = 65535

// validatePort checks the port an app serves on against the range its own
// error message advertises.
func validatePort(p int32) error {
	if p < 1 || p > maxPort {
		return fmt.Errorf("%w: exposed_port must be 1-%d", ErrValidation, maxPort)
	}
	return nil
}

// validateRepoPath checks a path an app points at inside its own checkout —
// dockerfile_path, build_context, compose_path. All three were stored
// unvalidated and joined onto the checkout directory, so "../../.." aimed
// `docker build -f` or `docker compose -f` at control-plane files.
//
// This is the lexical half, run at write time so the user is told at the point
// they typed it. The half that resolves symlinks against the real tree runs at
// build time (builder.Within) — a path can only be checked properly once the
// checkout exists.
func validateRepoPath(field, p string) error {
	p = strings.TrimSpace(p)
	if p == "" {
		return nil
	}
	if strings.ContainsAny(p, "\x00\n\r") {
		return fmt.Errorf("%w: %s must not contain control characters", ErrValidation, field)
	}
	if filepath.IsAbs(p) || strings.HasPrefix(p, "~") {
		return fmt.Errorf("%w: %s must be relative to the repository root, got %q",
			ErrValidation, field, p)
	}
	// Clean collapses "a/../.." to ".."; anything that still starts there
	// leaves the checkout.
	clean := filepath.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%w: %s %q points outside the repository", ErrValidation, field, p)
	}
	return nil
}

// envKeyRe is the POSIX shell-variable name form: a letter or underscore,
// then letters, digits and underscores.
var envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// validateEnvKey checks an environment-variable name. Only emptiness was
// rejected before, and generateEnvFile screens newlines in *values* but not in
// keys — so a key containing "\n" wrote extra lines into the app's .env. The
// blast radius is the tenant's own container, which they already control, so
// this was a correctness gap rather than a breach; it was also one character
// away from mattering.
func validateEnvKey(k string) error {
	if k == "" {
		return fmt.Errorf("%w: env var key must not be empty", ErrValidation)
	}
	if !envKeyRe.MatchString(k) {
		return fmt.Errorf("%w: env var key %q must start with a letter or underscore "+
			"and contain only letters, digits and underscores", ErrValidation, k)
	}
	return nil
}

// validateGitURL screens the repository URL a tenant hands the control plane.
// The URL reaches `git clone` running on the control plane's network, so an
// unrestricted one is a blind SSRF; see internal/giturl.
func validateGitURL(raw string, allowPrivate bool) error {
	if err := giturl.Validate(raw, allowPrivate); err != nil {
		return fmt.Errorf("%w: git_repo_url %v", ErrValidation, err)
	}
	return nil
}

// imageRefRe is the docker reference grammar (distribution/reference):
// an optional registry host[:port]/, slash-separated lowercase path
// components, an optional :tag and an optional @digest. image_ref is rendered
// into the generated compose file, so anything outside this grammar — a
// newline above all — would let a tenant write their own compose keys
// (privileged, host mounts) into a file the control plane applies.
var imageRefRe = regexp.MustCompile(
	`^(?:(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?)(?:\.(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?))*(?::[0-9]+)?/)?` +
		`[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*(?:/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*)*` +
		`(?::[A-Za-z0-9_][A-Za-z0-9_.-]{0,127})?` +
		`(?:@[A-Za-z][A-Za-z0-9]*(?:[-_+.][A-Za-z][A-Za-z0-9]*)*:[0-9a-fA-F]{32,})?$`)

// ValidateImageRef checks an image reference against the docker grammar.
func ValidateImageRef(ref string) error {
	if len(ref) > 255 || !imageRefRe.MatchString(ref) {
		return fmt.Errorf("%w: image_ref %q is not a valid image reference "+
			"(e.g. nginx:1.27, ghcr.io/org/app:tag)", ErrValidation, ref)
	}
	return nil
}

// healthPathRe is an absolute URL path (with optional query) built from the
// RFC 3986 unreserved/sub-delim characters. healthcheck_path becomes a
// Traefik label in the generated compose file, so it must not carry
// whitespace, quotes, '#', or anything YAML would read as structure.
var healthPathRe = regexp.MustCompile(`^/[A-Za-z0-9._~/%!$&()*+,;=:@?-]*$`)

func validateHealthcheckPath(p string) error {
	if p == "" {
		return nil
	}
	if len(p) > 512 || !healthPathRe.MatchString(p) {
		return fmt.Errorf("%w: healthcheck_path must be an absolute URL path such as /healthz", ErrValidation)
	}
	return nil
}

// composeServiceRe is the compose service-name form.
var composeServiceRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

func validateComposeService(s string) error {
	if s == "" {
		return nil
	}
	if len(s) > 128 || !composeServiceRe.MatchString(s) {
		return fmt.Errorf("%w: compose_service %q is not a valid service name", ErrValidation, s)
	}
	return nil
}

// validateBuilder checks the builder selector on both create and update.
func validateBuilder(b string) error {
	switch b {
	case "", "auto", "dockerfile", "nixpacks":
		return nil
	}
	return fmt.Errorf("%w: builder must be auto, dockerfile, or nixpacks", ErrValidation)
}
