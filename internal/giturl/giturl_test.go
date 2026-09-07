package giturl

import (
	"errors"
	"testing"
)

func TestValidateAcceptsRealRepoURLs(t *testing.T) {
	ok := []string{
		"https://github.com/bograh/cargo.git",
		"https://gitlab.example.com:8443/team/app",
		"ssh://git@github.com/bograh/cargo.git",
		"git@github.com:bograh/cargo.git",
		"git@gitlab.example.com:team/sub/app.git",
	}
	for _, u := range ok {
		if err := Validate(u, false); err != nil {
			t.Errorf("%q: want accepted, got %v", u, err)
		}
	}
}

func TestValidateRejectsTransportsAndLiterals(t *testing.T) {
	bad := []struct {
		url  string
		want error
	}{
		{"", ErrInvalid},
		{"ext::sh -c 'touch /pwned'", ErrInvalid},
		{"file:///var/lib/cargo", ErrInvalid},
		{"git://github.com/bograh/cargo.git", ErrInvalid},
		{"http://github.com/bograh/cargo.git", ErrInvalid},
		{"--upload-pack=touch /pwned", ErrInvalid},
		{"https://github.com/a b", ErrInvalid},
		{"https://", ErrInvalid},
		{"http://169.254.169.254/latest/meta-data/", ErrInvalid},
		{"https://169.254.169.254/latest/meta-data/", ErrPrivateAddress},
		{"https://127.0.0.1:8080/x", ErrPrivateAddress},
		{"https://10.0.0.5/internal.git", ErrPrivateAddress},
		{"https://[::1]/x", ErrPrivateAddress},
		{"git@192.168.1.10:internal.git", ErrPrivateAddress},
		{"https://100.100.100.200/x", ErrPrivateAddress},
	}
	for _, c := range bad {
		err := Validate(c.url, false)
		if err == nil {
			t.Errorf("%q: want rejected, got nil", c.url)
			continue
		}
		if !errors.Is(err, c.want) {
			t.Errorf("%q: want %v, got %v", c.url, c.want, err)
		}
	}
}

// An install whose git server is on the same private network as the control
// plane is an ordinary self-hosted setup. The hatch has to reach every layer,
// or the app cannot even be created.
func TestPrivateHostsAllowedWhenOperatorOptsIn(t *testing.T) {
	for _, u := range []string{
		"https://10.0.0.5/internal.git",
		"git@192.168.1.10:internal.git",
		"https://gitlab.internal/team/app.git",
	} {
		if err := Validate(u, true); err != nil {
			t.Errorf("Validate(%q, true) = %v, want nil", u, err)
		}
		if err := CheckHost(u, true); err != nil {
			t.Errorf("CheckHost(%q, true) = %v, want nil", u, err)
		}
	}
}

// The hatch is about *where* a host points, never about which transports are
// accepted: ext:: stays refused however the instance is configured.
func TestHatchDoesNotWidenTransports(t *testing.T) {
	for _, u := range []string{"ext::sh -c id", "file:///etc", "git://h/r.git"} {
		if err := Validate(u, true); err == nil {
			t.Errorf("Validate(%q, true) = nil, want rejected", u)
		}
		if err := CheckHost(u, true); err == nil {
			t.Errorf("CheckHost(%q, true) = nil, want rejected", u)
		}
	}
}

func TestCheckHostRejectsLiteralPrivateAddress(t *testing.T) {
	if err := CheckHost("https://169.254.169.254/latest/meta-data/", false); !errors.Is(err, ErrPrivateAddress) {
		t.Fatalf("got %v, want ErrPrivateAddress", err)
	}
}
