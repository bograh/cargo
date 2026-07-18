package reconciler

import (
	"fmt"
	"sort"
	"strings"
)

// GenerateCompose renders the per-app compose file. Output is fully
// controlled (no user YAML), so plain string building is safe.
func GenerateCompose(spec Spec) string {
	var b strings.Builder
	router := "app-" + spec.Slug
	hosts := make([]string, 0, len(spec.Domains))
	for _, d := range spec.Domains {
		hosts = append(hosts, fmt.Sprintf("Host(`%s`)", d))
	}
	fmt.Fprintf(&b, "name: cargo-app-%s\n", spec.Slug)
	b.WriteString("services:\n")
	b.WriteString("  app:\n")
	fmt.Fprintf(&b, "    image: %s\n", spec.Image)
	b.WriteString("    env_file: .env\n")
	b.WriteString("    restart: unless-stopped\n")
	b.WriteString("    networks:\n      - cargo-proxy\n")
	b.WriteString("    labels:\n")
	b.WriteString("      - traefik.enable=true\n")
	fmt.Fprintf(&b, "      - traefik.http.routers.%s.rule=%s\n", router, strings.Join(hosts, " || "))
	fmt.Fprintf(&b, "      - traefik.http.routers.%s.entrypoints=websecure\n", router)
	fmt.Fprintf(&b, "      - traefik.http.routers.%s.tls=true\n", router)
	fmt.Fprintf(&b, "      - traefik.http.services.%s.loadbalancer.server.port=%d\n", router, spec.Port)
	b.WriteString("networks:\n  cargo-proxy:\n    external: true\n")
	return b.String()
}

// generateEnvFile renders KEY=VALUE lines, keys sorted. Values containing
// newlines are rejected — the env file format cannot represent them.
func generateEnvFile(env map[string]string) (string, error) {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		v := env[k]
		if strings.ContainsAny(v, "\n\r") {
			return "", fmt.Errorf("env var %s: value must not contain newlines", k)
		}
		fmt.Fprintf(&b, "%s=%s\n", k, v)
	}
	return b.String(), nil
}
