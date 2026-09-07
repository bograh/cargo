// Package giturl validates the repository URLs tenants hand the control plane.
//
// A repo URL reaches `git clone` running inside the control plane, on the
// control plane's network. Two escalations that this normally implies do not
// land on a current git: `ext::` is refused outright (fatal: transport 'ext'
// not allowed), and argument injection fails because `git clone` consumes a
// leading-dash first positional as the repository rather than as a flag. What
// remains is a tenant-controlled outbound request to any address the control
// plane can reach — link-local metadata endpoints, internal admin panels —
// with the response surfaced in the deploy log. That is a blind SSRF, and it
// is what this package is for.
package giturl

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// ErrInvalid is returned for a URL whose form is not an accepted one.
var ErrInvalid = errors.New("repository URL is not allowed")

// ErrPrivateAddress is returned when a repository host resolves to an address
// that is not reachable from the public internet — loopback, link-local, or
// RFC1918 — which on this host means "somewhere inside the deployment".
var ErrPrivateAddress = errors.New("repository host resolves to a private or link-local address")

// scpLike matches git's alternative syntax, `user@host:path`, which is not a
// URL and so cannot be handed to url.Parse. Git treats a colon before the
// first slash as the separator.
var scpLike = regexp.MustCompile(`^[A-Za-z0-9._~%+-]+@([A-Za-z0-9.\-\[\]:]+):(?:[^/].*|/.*)$`)

// allowedSchemes are the transports a tenant may name. https and ssh cover
// every hosting service and self-hosted server; the rest are excluded on
// purpose. git:// and http:// are unauthenticated and cleartext, file:// reads
// the control plane's own disk, and ext::/transport helpers run a command.
var allowedSchemes = map[string]bool{"https": true, "ssh": true}

// Validate checks the form of a repository URL, and rejects one that names a
// private address as a literal. It performs no name resolution, so it is safe
// to run on the API path; the check that does resolve is CheckHost, run at
// clone time.
//
// allowPrivate is the operator's escape hatch (CARGO_ALLOW_PRIVATE_GIT_HOSTS):
// a self-hosted PaaS whose git server sits on the same private network as the
// control plane is an ordinary setup, not an attack.
func Validate(raw string, allowPrivate bool) error {
	host, err := hostOf(raw)
	if err != nil {
		return err
	}
	if allowPrivate {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && !publicIP(ip) {
		return fmt.Errorf("%w: %s", ErrPrivateAddress, host)
	}
	return nil
}

// hostOf returns the hostname a URL names, after checking that the URL is one
// of the accepted forms. It says nothing about where that host points.
func hostOf(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("%w: it is empty", ErrInvalid)
	}
	if strings.ContainsAny(s, " \t\n\r\x00") {
		return "", fmt.Errorf("%w: it contains whitespace or control characters", ErrInvalid)
	}
	// A leading dash is refused by git clone today, but only as a side effect
	// of how it assigns positionals. Do not depend on that.
	if strings.HasPrefix(s, "-") {
		return "", fmt.Errorf("%w: it must not start with %q", ErrInvalid, "-")
	}
	// "ext::sh -c ..." and friends. Modern git refuses the ext transport, but
	// the remote-helper syntax as a whole is not something to leave open.
	if strings.Contains(s, "::") && !strings.Contains(s, "://") {
		return "", fmt.Errorf("%w: git remote helpers are not accepted", ErrInvalid)
	}

	if m := scpLike.FindStringSubmatch(s); m != nil && !strings.Contains(s, "://") {
		return normalizeHost(m[1])
	}

	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if !allowedSchemes[strings.ToLower(u.Scheme)] {
		return "", fmt.Errorf("%w: use https:// or ssh:// (or git@host:path), not %q",
			ErrInvalid, u.Scheme)
	}
	if u.Hostname() == "" {
		return "", fmt.Errorf("%w: it names no host", ErrInvalid)
	}
	return normalizeHost(u.Hostname())
}

// normalizeHost strips the brackets an IPv6 literal carries in a URL.
func normalizeHost(h string) (string, error) {
	h = strings.TrimSuffix(strings.TrimPrefix(h, "["), "]")
	if h == "" {
		return "", fmt.Errorf("%w: it names no host", ErrInvalid)
	}
	return h, nil
}

// CheckHost resolves a repository URL's host and refuses one that lands inside
// the deployment. allowPrivate turns it off for an install whose git server is
// on the same private network as the control plane, which is an ordinary thing
// for a self-hosted PaaS (CARGO_ALLOW_PRIVATE_GIT_HOSTS).
//
// This runs at clone time rather than at write time because a name can be
// repointed after the app is created. It is still a check against a resolution
// git will make again a moment later, so it narrows the window rather than
// closing it — the guarantee is "not a straightforward request to
// 169.254.169.254", not "no request can ever reach a private address".
func CheckHost(raw string, allowPrivate bool) error {
	host, err := hostOf(raw)
	if err != nil {
		return err
	}
	if allowPrivate {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		if !publicIP(ip) {
			return fmt.Errorf("%w: %s", ErrPrivateAddress, host)
		}
		return nil
	}
	addrs, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", host, err)
	}
	for _, ip := range addrs {
		if !publicIP(ip) {
			return fmt.Errorf("%w: %s is %s", ErrPrivateAddress, host, ip)
		}
	}
	return nil
}

// publicIP reports whether an address is one the control plane could plausibly
// have been asked to reach on purpose.
func publicIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	// Carrier-grade NAT (100.64.0.0/10) and the "this network" block
	// (0.0.0.0/8) are neither loopback nor RFC1918, and both reach places a
	// tenant should not be able to aim the control plane at.
	if v4 := ip.To4(); v4 != nil {
		switch {
		case v4[0] == 0:
			return false
		case v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127:
			return false
		}
	}
	// IPv4-mapped and NAT64 forms of the above.
	if v4 := ip.To4(); v4 == nil && len(ip) == net.IPv6len {
		if ip[0] == 0xfc || ip[0] == 0xfd { // unique local, fc00::/7
			return false
		}
	}
	return true
}
