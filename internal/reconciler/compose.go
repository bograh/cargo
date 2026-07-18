package reconciler

import (
	"fmt"
	"sort"
	"strings"
)

// GenerateCompose renders the per-app compose file. Output is fully
// controlled (no user YAML), so plain string building is safe.
//
// Domains[0] is the auto subdomain: its router uses the websecure
// entrypoint's default TLS configuration (per-domain HTTP-01 or the
// wildcard cert, chosen at install). Remaining domains are custom and get
// a second router pinned to the HTTP-01 resolver `le` (FR-5.3).
func GenerateCompose(spec Spec) string {
	var b strings.Builder
	router := "app-" + spec.Slug
	hostRule := func(domains []string) string {
		hosts := make([]string, 0, len(domains))
		for _, d := range domains {
			hosts = append(hosts, fmt.Sprintf("Host(`%s`)", d))
		}
		return strings.Join(hosts, " || ")
	}
	networks := spec.Networks
	if len(networks) == 0 {
		networks = []string{"cargo-proxy"}
	}
	fmt.Fprintf(&b, "name: cargo-app-%s\n", spec.Slug)
	b.WriteString("services:\n")
	b.WriteString("  app:\n")
	fmt.Fprintf(&b, "    image: %s\n", spec.Image)
	b.WriteString("    env_file: .env\n")
	b.WriteString("    restart: unless-stopped\n")
	b.WriteString("    networks:\n")
	for _, n := range networks {
		fmt.Fprintf(&b, "      - %s\n", n)
	}
	b.WriteString("    labels:\n")
	b.WriteString("      - traefik.enable=true\n")
	fmt.Fprintf(&b, "      - traefik.http.routers.%s.rule=%s\n", router, hostRule(spec.Domains[:1]))
	fmt.Fprintf(&b, "      - traefik.http.routers.%s.entrypoints=websecure\n", router)
	fmt.Fprintf(&b, "      - traefik.http.routers.%s.tls=true\n", router)
	fmt.Fprintf(&b, "      - traefik.http.routers.%s.service=%s\n", router, router)
	if len(spec.Domains) > 1 {
		custom := router + "-custom"
		fmt.Fprintf(&b, "      - traefik.http.routers.%s.rule=%s\n", custom, hostRule(spec.Domains[1:]))
		fmt.Fprintf(&b, "      - traefik.http.routers.%s.entrypoints=websecure\n", custom)
		fmt.Fprintf(&b, "      - traefik.http.routers.%s.tls.certresolver=le\n", custom)
		fmt.Fprintf(&b, "      - traefik.http.routers.%s.service=%s\n", custom, router)
	}
	fmt.Fprintf(&b, "      - traefik.http.services.%s.loadbalancer.server.port=%d\n", router, spec.Port)
	b.WriteString("networks:\n")
	for _, n := range networks {
		fmt.Fprintf(&b, "  %s:\n    external: true\n", n)
	}
	return b.String()
}

// GenerateDBCompose renders the compose file for a managed database
// instance. Secrets (POSTGRES_PASSWORD, redis requirepass) never appear in
// this output — they are supplied via env_file / a mounted config file
// written separately by ProvisionDB.
func GenerateDBCompose(spec DBSpec) string {
	var b strings.Builder
	name := "cargo-db-" + spec.InstanceID
	fmt.Fprintf(&b, "name: %s\n", name)
	b.WriteString("services:\n")
	b.WriteString("  db:\n")
	fmt.Fprintf(&b, "    image: %s:%s\n", spec.Engine, spec.Version)
	fmt.Fprintf(&b, "    container_name: %s\n", name)
	switch spec.Engine {
	case "redis":
		b.WriteString("    command: [\"redis-server\", \"/etc/cargo/redis.conf\"]\n")
	default:
		b.WriteString("    env_file: .env\n")
	}
	b.WriteString("    restart: unless-stopped\n")
	b.WriteString("    networks:\n      - cargo-data\n")
	b.WriteString("    volumes:\n")
	switch spec.Engine {
	case "redis":
		b.WriteString("      - data:/data\n")
		b.WriteString("      - ./redis.conf:/etc/cargo/redis.conf:ro\n")
	default:
		b.WriteString("      - data:/var/lib/postgresql/data\n")
	}
	if spec.HostPort > 0 {
		port := "5432"
		if spec.Engine == "redis" {
			port = "6379"
		}
		fmt.Fprintf(&b, "    ports:\n      - \"%d:%s\"\n", spec.HostPort, port)
	}
	b.WriteString("volumes:\n  data:\n")
	b.WriteString("networks:\n  cargo-data:\n    external: true\n")
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
