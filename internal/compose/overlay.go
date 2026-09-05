package compose

import (
	"fmt"
	"strings"
)

// OverlaySpec describes what Cargo layers over a user's compose file.
type OverlaySpec struct {
	Slug    string
	Service string   // the service that receives traffic
	Port    int32    // the port that service listens on
	Domains []string // [0] is the auto subdomain; the rest are custom
	// Networks are the Cargo-managed networks the traffic-serving service
	// joins, alongside the project's own default. Empty means cargo-proxy
	// alone; an app with a managed database attachment also needs cargo-data.
	Networks []string
	// Resource caps applied to the web service. The user's other services keep
	// whatever they declared — capping a database the user configured would be
	// a surprising thing for a deploy to do.
	MemoryLimit string
	CPULimit    string
	PidsLimit   int
}

// GenerateOverlay renders the compose file Cargo merges over the user's own.
//
// It is applied as a second `-f`, so compose's own merge rules combine it with
// the user's file: mappings merge key-by-key and sequences append. That is why
// the overlay can add the cargo-proxy network without displacing the networks
// the user's services already use.
//
// `default` is listed explicitly alongside cargo-proxy because naming any
// network on a service removes compose's implicit default — without it, a web
// service would lose the ability to reach the database in its own compose file.
func GenerateOverlay(spec OverlaySpec) string {
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
	fmt.Fprintf(&b, "  %s:\n", spec.Service)
	// Compose appends env_file entries when merging, so the app's Cargo-managed
	// variables land alongside any the user already declared. The path resolves
	// against the project directory, which is the user's checkout.
	b.WriteString("    env_file: .env\n")
	b.WriteString("    restart: unless-stopped\n")
	if spec.MemoryLimit != "" {
		fmt.Fprintf(&b, "    mem_limit: %s\n", spec.MemoryLimit)
	}
	if spec.CPULimit != "" {
		fmt.Fprintf(&b, "    cpus: %s\n", spec.CPULimit)
	}
	if spec.PidsLimit > 0 {
		fmt.Fprintf(&b, "    pids_limit: %d\n", spec.PidsLimit)
	}
	b.WriteString("    security_opt:\n      - no-new-privileges:true\n")
	b.WriteString("    logging:\n      driver: json-file\n      options:\n")
	b.WriteString("        max-size: \"10m\"\n        max-file: \"3\"\n")
	b.WriteString("    networks:\n      - default\n")
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
