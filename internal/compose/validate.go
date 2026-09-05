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
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrUnsafe is returned when a compose file uses a directive that would breach
// tenant isolation.
var ErrUnsafe = errors.New("unsafe compose directive")

// ErrInvalid is returned when a compose file cannot be understood at all.
var ErrInvalid = errors.New("invalid compose file")

// allowedSecurityOpt is the single security_opt Cargo itself sets. Anything
// else a user adds can only weaken the sandbox — `seccomp:unconfined` and
// `apparmor:unconfined` in particular hand back the syscall filtering that
// makes a shared host tenable — and compose *appends* the two lists on merge,
// so the overlay cannot override them.
const allowedSecurityOpt = "no-new-privileges:true"

// reservedLabelPrefix is the label namespace Traefik routes on. Cargo emits
// these itself in the overlay, one router per app bound to the domains the app
// actually owns. A tenant that sets them too is not configuring their own app:
// Traefik's Docker provider watches every container on cargo-proxy, so a
// router declared here claims a hostname instance-wide — including the
// platform's own — and an explicit `priority` wins the tie deterministically.
// Whoever holds the hostname receives the session cookies sent to it.
const reservedLabelPrefix = "traefik."

// ReservedLabel reports whether a label key belongs to the namespace Cargo
// controls. It is exported because compose files are not the only way a label
// reaches a container: Docker merges an image's own LABEL instructions into
// every container started from it, so the deploy pipeline applies the same
// rule to the images it is about to run.
func ReservedLabel(key string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(key)), reservedLabelPrefix)
}

type composeFile struct {
	Services map[string]composeService `yaml:"services"`
	Volumes  map[string]composeVolume  `yaml:"volumes"`
	Secrets  map[string]composeMount   `yaml:"secrets"`
	Configs  map[string]composeMount   `yaml:"configs"`
}

type composeService struct {
	Privileged        bool        `yaml:"privileged"`
	NetworkMode       string      `yaml:"network_mode"`
	Pid               string      `yaml:"pid"`
	Ipc               string      `yaml:"ipc"`
	UsernsMode        string      `yaml:"userns_mode"`
	CgroupParent      string      `yaml:"cgroup_parent"`
	CapAdd            []string    `yaml:"cap_add"`
	Devices           []yaml.Node `yaml:"devices"`
	DeviceCgroupRules []string    `yaml:"device_cgroup_rules"`
	VolumesFrom       []string    `yaml:"volumes_from"`
	SecurityOpt       []string    `yaml:"security_opt"`
	Volumes           []yaml.Node `yaml:"volumes"`
	Ports             []yaml.Node `yaml:"ports"`
	Build             yaml.Node   `yaml:"build"`
	EnvFile           yaml.Node   `yaml:"env_file"`
	LabelFile         yaml.Node   `yaml:"label_file"`
	Labels            yaml.Node   `yaml:"labels"`
}

// composeVolume is a top-level volume definition. A "named" volume with
// local-driver bind options is a bind mount wearing a disguise.
type composeVolume struct {
	Driver     string            `yaml:"driver"`
	DriverOpts map[string]string `yaml:"driver_opts"`
}

// composeMount is the shape of a top-level secret or config, which can read an
// arbitrary host file into the container.
type composeMount struct {
	File string `yaml:"file"`
}

// volumeSpec is the long-form volume syntax; the short form is a plain string.
type volumeSpec struct {
	Type   string `yaml:"type"`
	Source string `yaml:"source"`
}

// buildSpec covers both the short (`build: .`) and long build syntax.
type buildSpec struct {
	Context string `yaml:"context"`
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

// Validate rejects compose configurations that would break out of the sandbox
// the platform relies on. An overlay can add to a service but never take away,
// so anything dangerous has to be refused up front rather than corrected later.
//
// IMPORTANT: run this against *rendered* configuration — the output of
// `docker compose config` — not the file as written. Validating the source text
// is not sound, because compose resolves several things afterwards that each
// hide a directive from a textual reader:
//
//   - `${VAR}` interpolation. Cargo writes the app's own environment into the
//     project's .env, so a tenant could otherwise set a variable through the UI
//     and expand it into a bind mount of `/`.
//   - `extends: {file: …}`, which pulls directives out of a file that was never
//     inspected.
//   - YAML anchors and merge keys.
//
// root is the directory the app is allowed to reference — its checkout. Every
// host path in the configuration must resolve inside it.
//
// Errors name the service and the directive so the deploy log tells the user
// exactly what to remove.
func Validate(raw []byte, root string) error {
	var f composeFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if len(f.Services) == 0 {
		return fmt.Errorf("%w: no services defined", ErrInvalid)
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	absRoot, err = filepath.EvalSymlinks(absRoot)
	if err != nil {
		// The checkout should always exist by now; if it doesn't, fail closed.
		return fmt.Errorf("%w: cannot resolve project directory: %v", ErrInvalid, err)
	}

	if err := validateServices(f, absRoot); err != nil {
		return err
	}
	return validateTopLevel(f, absRoot)
}

func validateServices(f composeFile, absRoot string) error {
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
		if strings.TrimSpace(svc.CgroupParent) != "" {
			return bad("cgroup_parent", "escapes the cgroup the platform's resource limits are applied to")
		}
		if len(svc.CapAdd) > 0 {
			return bad("cap_add", "grants Linux capabilities the platform withholds by default")
		}
		if len(svc.Devices) > 0 {
			return bad("devices", "maps host devices into the container")
		}
		if len(svc.DeviceCgroupRules) > 0 {
			return bad("device_cgroup_rules", "grants access to host devices")
		}
		if len(svc.VolumesFrom) > 0 {
			return bad("volumes_from", "inherits mounts from another container")
		}
		for _, opt := range svc.SecurityOpt {
			if strings.TrimSpace(opt) != allowedSecurityOpt {
				return fmt.Errorf("%w: service %q sets security_opt %q, which weakens the container sandbox "+
					"(compose appends to Cargo's own hardening rather than replacing it)",
					ErrUnsafe, name, opt)
			}
		}
		// Cargo apps are reached through Traefik and never publish host ports;
		// a published port would also collide across apps on a single host.
		if len(svc.Ports) > 0 {
			return fmt.Errorf(
				"%w: service %q publishes ports; Cargo routes traffic through Traefik instead — "+
					"remove the `ports:` block and set the app's exposed port", ErrUnsafe, name)
		}
		keys, err := labelKeys(svc.Labels)
		if err != nil {
			return fmt.Errorf("%w: service %q: could not read labels: %v", ErrInvalid, name, err)
		}
		for _, key := range keys {
			if ReservedLabel(key) {
				return fmt.Errorf(
					"%w: service %q sets the label %q; Cargo generates its own Traefik routing and a "+
						"second router would claim a hostname instance-wide — remove the `traefik.*` labels "+
						"and add the domain to the app instead", ErrUnsafe, name, key)
			}
		}
		for _, node := range svc.Volumes {
			source, err := volumeSource(node)
			if err != nil {
				return fmt.Errorf("%w: service %q: %v", ErrInvalid, name, err)
			}
			if err := checkPath(name, "bind-mounts", source, absRoot); err != nil {
				return err
			}
		}
		if ctx := buildContext(svc.Build); ctx != "" {
			if err := checkPath(name, "builds from", ctx, absRoot); err != nil {
				return err
			}
		}
		// env_file and label_file read a file's contents into the container's
		// environment or labels, so a host path here is an exfiltration route
		// just as much as a bind mount is.
		for _, group := range []struct {
			what string
			node yaml.Node
		}{{"env_file", svc.EnvFile}, {"label_file", svc.LabelFile}} {
			for _, path := range filePaths(group.node) {
				if err := checkPath(name, "reads "+group.what+" from", path, absRoot); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// validateTopLevel checks the definitions services refer to by name. A volume
// or secret is where a host path can hide behind an innocuous-looking
// reference in the service itself.
func validateTopLevel(f composeFile, absRoot string) error {
	names := make([]string, 0, len(f.Volumes))
	for name := range f.Volumes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		v := f.Volumes[name]
		// `driver_opts: {type: none, device: /etc, o: bind}` is the documented
		// way to define a bind mount as a named volume. A service referencing
		// it looks completely ordinary.
		if dev := strings.TrimSpace(v.DriverOpts["device"]); dev != "" {
			opts := v.DriverOpts["o"] + "," + v.DriverOpts["type"]
			if strings.Contains(opts, "bind") || strings.HasPrefix(dev, "/") {
				return fmt.Errorf("%w: volume %q bind-mounts the host path %q via driver_opts; "+
					"use an ordinary named volume instead", ErrUnsafe, name, dev)
			}
		}
	}

	checkMounts := func(kind string, mounts map[string]composeMount) error {
		keys := make([]string, 0, len(mounts))
		for k := range mounts {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			file := strings.TrimSpace(mounts[k].File)
			if file == "" {
				continue
			}
			if err := checkPath(kind+" "+k, "reads the host file", file, absRoot); err != nil {
				return err
			}
		}
		return nil
	}
	if err := checkMounts("secret", f.Secrets); err != nil {
		return err
	}
	return checkMounts("config", f.Configs)
}

// buildContext extracts the context path from either build syntax.
func buildContext(node yaml.Node) string {
	if node.Kind == 0 {
		return ""
	}
	if node.Kind == yaml.ScalarNode {
		var short string
		if err := node.Decode(&short); err != nil {
			return ""
		}
		return short
	}
	var long buildSpec
	if err := node.Decode(&long); err != nil {
		return ""
	}
	return long.Context
}

// filePaths reads an env_file / label_file field. Compose accepts a bare path,
// a list of paths, or — in rendered output — a list of {path, required} maps,
// and a field declared as only one of those shapes silently drops the others.
func filePaths(node yaml.Node) []string {
	switch node.Kind {
	case yaml.ScalarNode:
		var one string
		if err := node.Decode(&one); err != nil {
			return nil
		}
		return []string{one}
	case yaml.SequenceNode:
		var items []yaml.Node
		if err := node.Decode(&items); err != nil {
			return nil
		}
		out := make([]string, 0, len(items))
		for _, item := range items {
			if item.Kind == yaml.ScalarNode {
				var one string
				if err := item.Decode(&one); err == nil {
					out = append(out, one)
				}
				continue
			}
			var long struct {
				Path string `yaml:"path"`
			}
			if err := item.Decode(&long); err == nil && long.Path != "" {
				out = append(out, long.Path)
			}
		}
		return out
	default:
		return nil
	}
}

// labelKeys reads a service's label keys. Compose accepts both a mapping of
// key to value and a sequence of "key=value" strings; rendered configuration
// uses the mapping form, but Validate is reachable with either. An
// unrecognisable node is an error rather than an empty result — a label block
// this cannot read is one it cannot vouch for.
func labelKeys(node yaml.Node) ([]string, error) {
	switch node.Kind {
	case 0:
		return nil, nil
	case yaml.MappingNode:
		var m map[string]yaml.Node
		if err := node.Decode(&m); err != nil {
			return nil, err
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return keys, nil
	case yaml.SequenceNode:
		var items []yaml.Node
		if err := node.Decode(&items); err != nil {
			return nil, err
		}
		keys := make([]string, 0, len(items))
		for _, item := range items {
			var entry string
			if err := item.Decode(&entry); err != nil {
				return nil, err
			}
			key, _, _ := strings.Cut(entry, "=")
			keys = append(keys, strings.TrimSpace(key))
		}
		return keys, nil
	default:
		return nil, fmt.Errorf("labels must be a mapping or a list")
	}
}

// volumeSource extracts the host side of a volume entry in either the short
// ("src:dst:ro") or long (type/source/target) syntax. An empty result means an
// anonymous or named volume, which carries no host path of its own.
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
		src := parts[0]
		// A bare name (no separator, not relative) is a named volume; those are
		// checked via the top-level definitions instead.
		if !strings.ContainsAny(src, "/~") {
			return "", nil
		}
		return src, nil
	}
	var long volumeSpec
	if err := node.Decode(&long); err != nil {
		return "", err
	}
	if long.Type != "" && long.Type != "bind" {
		return "", nil // named volume, tmpfs, npipe — no host path involved
	}
	if long.Type == "" && !strings.ContainsAny(long.Source, "/~") {
		return "", nil // named volume in long form
	}
	return long.Source, nil
}

// checkPath rejects any host path that resolves outside the app's checkout.
// Rendered configuration reports bind sources and build contexts as absolute
// paths, so this is a containment check rather than a syntactic one — it holds
// regardless of how the path was spelled.
func checkPath(subject, verb, path, absRoot string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	if strings.HasPrefix(path, "~") {
		return fmt.Errorf("%w: %s %s %q, which is outside the repository",
			ErrUnsafe, subject, verb, path)
	}
	resolved := path
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(absRoot, resolved)
	}
	resolved = filepath.Clean(resolved)
	// Resolve symlinks where possible so a link inside the checkout cannot
	// point out of it. A path that does not exist yet is checked lexically.
	if real, err := filepath.EvalSymlinks(resolved); err == nil {
		resolved = real
	}
	if resolved != absRoot && !strings.HasPrefix(resolved, absRoot+string(filepath.Separator)) {
		return fmt.Errorf("%w: %s %s %q, which is outside the repository; "+
			"use a named volume or a path inside your repo instead",
			ErrUnsafe, subject, verb, path)
	}
	return nil
}
