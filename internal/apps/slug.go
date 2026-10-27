package apps

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"strings"
)

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// reservedSlugSuffixes are the suffixes the reconciler appends to a slug when
// it names things: compose projects (`cargo-app-<slug>-blue`/`-green`) and
// Traefik routers (`app-<slug>-custom`). Slugs are globally unique, but
// without this rule "foo blue" (slug foo-blue, recreate) and "foo" (blue/green,
// color blue) both rendered the project cargo-app-foo-blue — so one tenant's
// `up --remove-orphans` and teardown acted on another tenant's container.
// Keeping slugs off these suffixes makes every derived name unambiguous
// without renaming any project that is already running.
var reservedSlugSuffixes = []string{"-blue", "-green", "-custom"}

// slugify converts a display name into a DNS-safe slug.
func slugify(name string) string {
	s := nonSlug.ReplaceAllString(strings.ToLower(name), "-")
	s = strings.Trim(s, "-")
	if len(s) > 40 {
		// Trim again after truncating: a cut can land just after a hyphen,
		// and a DNS label must not end in one.
		s = strings.TrimRight(s[:40], "-")
	}
	if s == "" {
		s = "org"
	}
	for _, suf := range reservedSlugSuffixes {
		if strings.HasSuffix(s, suf) {
			s += "-app"
			break
		}
	}
	return s
}

func randomSuffix() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
