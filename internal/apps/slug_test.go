package apps

import (
	"strings"
	"testing"
)

// A slug must never end in a suffix the reconciler appends, or two apps can
// render the same compose project or Traefik router name.
func TestSlugifyAvoidsDerivedNameCollisions(t *testing.T) {
	for name, want := range map[string]string{
		"My API":      "my-api",
		"foo blue":    "foo-blue-app",
		"Foo-Green":   "foo-green-app",
		"shop custom": "shop-custom-app",
		"blue":        "blue",
		"bluegreen":   "bluegreen",
		"!!!":         "org",
	} {
		if got := slugify(name); got != want {
			t.Errorf("slugify(%q) = %q, want %q", name, got, want)
		}
	}
	// "foo" deployed blue/green and "foo blue" deployed with recreate used to
	// share the project cargo-app-foo-blue.
	colored := "cargo-app-" + slugify("foo") + "-blue"
	plain := "cargo-app-" + slugify("foo blue")
	if colored == plain {
		t.Fatalf("project names collide: %s", plain)
	}
}

func TestSlugifyNeverEndsInHyphen(t *testing.T) {
	// 39 letters then a separator: truncation at 40 lands on the hyphen.
	name := strings.Repeat("a", 39) + " tail"
	got := slugify(name)
	if strings.HasSuffix(got, "-") || len(got) > 40 {
		t.Fatalf("slugify = %q, want ≤40 chars and no trailing hyphen", got)
	}
}
