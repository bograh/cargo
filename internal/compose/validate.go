// Package compose supports the "user-supplied compose file" app source: Cargo
// runs a compose file from the user's repository and layers its own networking,
// Traefik labels, and resource limits over it via a generated overlay file.
//
// The overlay is a second `-f` argument rather than a rewrite of the user's
// YAML, so Cargo never has to round-trip a file it does not fully understand.
// What an overlay cannot do is *remove* something, which is why Validate
// rejects the directives that would let a tenant escape the isolation the rest
// of the platform depends on.
package compose

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrUnsafe is returned when a compose file uses a directive that would breach
// tenant isolation.
var ErrUnsafe = errors.New("unsafe compose directive")

// ErrInvalid is returned when a compose file cannot be understood at all.
var ErrInvalid = errors.New("invalid compose file")

type composeFile struct {
	Services map[string]composeService `yaml:"services"`
}

type composeService struct {
	Privileged  bool        `yaml:"privileged"`
	NetworkMode string      `yaml:"network_mode"`
	Pid         string      `yaml:"pid"`
	Ipc         string      `yaml:"ipc"`
	UsernsMode  string      `yaml:"userns_mode"`
	CapAdd      []string    `yaml:"cap_add"`
	Devices     []yaml.Node `yaml:"devices"`
	Volumes     []yaml.Node `yaml:"volumes"`
	Ports       []yaml.Node `yaml:"ports"`
}

// volumeSpec is the long-form volume syntax; the short form is a plain string.
type volumeSpec struct {
	Type   string `yaml:"type"`
	Source string `yaml:"source"`
}

// Parse reads a compose file and returns the names of its services, in a
// stable order. It does not validate safety — call Validate for that.
func Parse(raw []byte) ([]string, error) {
	var f composeFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if len(f.Services) == 0 {
		return nil, fmt.Errorf("%w: no services defined", ErrInvalid)
	}
	names := make([]string, 0, len(f.Services))
	for name := range f.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// Validate rejects compose files that would break out of the sandbox the
// platform relies on. An overlay can add to a service but never take away, so
// anything dangerous has to be refused up front rather than corrected later.
//
// Errors name the service and the directive so the deploy log tells the user
// exactly what to remove.
func Validate(raw []byte) error {
	var f composeFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if len(f.Services) == 0 {
		return fmt.Errorf("%w: no services defined", ErrInvalid)
	}

	names := make([]string, 0, len(f.Services))
	for name := range f.Services {
		names = append(names, name)
	}
	sort.Strings(names) // deterministic error for a file with several problems

	for _, name := range names {
		svc := f.Services[name]
		bad := func(directive, why string) error {
			return fmt.Errorf("%w: service %q uses %s, which %s", ErrUnsafe, name, directive, why)
		}

		if svc.Privileged {
			return bad("privileged: true", "grants the container full control of the host")
		}
		if mode := strings.TrimSpace(svc.NetworkMode); mode != "" {
			if mode == "host" {
				return bad("network_mode: host", "bypasses network isolation and exposes host ports directly")
			}
			if strings.HasPrefix(mode, "container:") {
				return bad("network_mode: container:...", "joins another container's network namespace")
			}
		}
		if strings.TrimSpace(svc.Pid) == "host" {
			return bad("pid: host", "exposes every process on the host")
		}
		if strings.TrimSpace(svc.Ipc) == "host" {
			return bad("ipc: host", "shares the host's IPC namespace")
		}
		if strings.TrimSpace(svc.UsernsMode) == "host" {
			return bad("userns_mode: host", "disables user-namespace remapping")
		}
		if len(svc.CapAdd) > 0 {
			return bad("cap_add", "grants Linux capabilities the platform withholds by default")
		}
		if len(svc.Devices) > 0 {
			return bad("devices", "maps host devices into the container")
		}
		// Cargo apps are reached through Traefik and never publish host ports;
		// a published port would also collide across apps on a single host.
		if len(svc.Ports) > 0 {
			return fmt.Errorf(
				"%w: service %q publishes ports; Cargo routes traffic through Traefik instead — "+
					"remove the `ports:` block and set the app's exposed port", ErrUnsafe, name)
		}
		for _, node := range svc.Volumes {
			source, err := volumeSource(node)
			if err != nil {
				return fmt.Errorf("%w: service %q: %v", ErrInvalid, name, err)
			}
			if err := checkVolumeSource(name, source); err != nil {
				return err
			}
		}
	}
	return nil
}

// volumeSource extracts the host side of a volume entry in either the short
// ("src:dst:ro") or long (type/source/target) syntax. An empty result means an
// anonymous volume, which is always safe.
func volumeSource(node yaml.Node) (string, error) {
	if node.Kind == yaml.ScalarNode {
		var short string
		if err := node.Decode(&short); err != nil {
			return "", err
		}
		parts := strings.Split(short, ":")
		if len(parts) < 2 {
			return "", nil // anonymous volume: just a container path
		}
		return parts[0], nil
	}
	var long volumeSpec
	if err := node.Decode(&long); err != nil {
		return "", err
	}
	if long.Type != "" && long.Type != "bind" {
		return "", nil // named volume, tmpfs, npipe — no host path involved
	}
	return long.Source, nil
}

// checkVolumeSource rejects bind mounts that reach outside the app's own
// checkout. A named volume (no path separator, not relative) is fine, and so is
// a relative path, which compose resolves inside the cloned repository.
func checkVolumeSource(service, source string) error {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil
	}
	isPath := strings.HasPrefix(source, "/") || strings.HasPrefix(source, "./") ||
		strings.HasPrefix(source, "../") || strings.HasPrefix(source, "~")
	if !isPath {
		return nil // a named volume
	}
	if strings.HasPrefix(source, "/") || strings.HasPrefix(source, "~") {
		return fmt.Errorf("%w: service %q bind-mounts the host path %q; "+
			"use a named volume or a path inside the repository instead",
			ErrUnsafe, service, source)
	}
	// Relative, but it may still climb out of the project directory.
	for _, seg := range strings.Split(source, "/") {
		if seg == ".." {
			return fmt.Errorf("%w: service %q bind-mounts %q, which escapes the repository",
				ErrUnsafe, service, source)
		}
	}
	return nil
}
