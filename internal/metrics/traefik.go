// Package metrics collects per-app resource and traffic metrics.
package metrics

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// TrafficCounters holds cumulative Traefik counters for one service.
type TrafficCounters struct {
	Requests      float64             // total requests (all codes)
	Errors        float64             // 5xx requests
	DurationSum   float64             // seconds
	DurationCount float64             // observations
	Buckets       map[float64]float64 // le -> cumulative count (+Inf as math.Inf)
}

// parseLabels extracts the label map from a `{a="1",b="2"}` fragment.
func parseLabels(s string) map[string]string {
	out := map[string]string{}
	s = strings.TrimSuffix(strings.TrimPrefix(s, "{"), "}")
	for _, part := range splitTopLevel(s) {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		out[strings.TrimSpace(kv[0])] = strings.Trim(strings.TrimSpace(kv[1]), `"`)
	}
	return out
}

// splitTopLevel splits on commas that are not inside quotes.
func splitTopLevel(s string) []string {
	var parts []string
	var b strings.Builder
	inQuote := false
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
			b.WriteRune(r)
		case r == ',' && !inQuote:
			parts = append(parts, b.String())
			b.Reset()
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		parts = append(parts, b.String())
	}
	return parts
}

// parseTraefikMetrics parses the Prometheus exposition text into per-service
// counters. Non-traefik-service lines are ignored.
func parseTraefikMetrics(body string) map[string]TrafficCounters {
	out := map[string]TrafficCounters{}
	get := func(svc string) TrafficCounters {
		c := out[svc]
		if c.Buckets == nil {
			c.Buckets = map[float64]float64{}
		}
		return c
	}
	sc := bufio.NewScanner(strings.NewReader(body))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		brace := strings.IndexByte(line, '{')
		if brace < 0 {
			continue
		}
		name := line[:brace]
		close := strings.LastIndexByte(line, '}')
		if close < 0 {
			continue
		}
		labels := parseLabels(line[brace : close+1])
		svc := labels["service"]
		if svc == "" {
			continue
		}
		val, err := strconv.ParseFloat(strings.TrimSpace(line[close+1:]), 64)
		if err != nil {
			continue
		}
		switch name {
		case "traefik_service_requests_total":
			c := get(svc)
			c.Requests += val
			if code := labels["code"]; strings.HasPrefix(code, "5") {
				c.Errors += val
			}
			out[svc] = c
		case "traefik_service_request_duration_seconds_sum":
			c := get(svc)
			c.DurationSum = val
			out[svc] = c
		case "traefik_service_request_duration_seconds_count":
			c := get(svc)
			c.DurationCount = val
			out[svc] = c
		case "traefik_service_request_duration_seconds_bucket":
			c := get(svc)
			le, err := strconv.ParseFloat(labels["le"], 64) // "+Inf" -> +Inf
			if err == nil {
				c.Buckets[le] = val
			}
			out[svc] = c
		}
	}
	return out
}

// histogramQuantile estimates the q-quantile (0..1) from cumulative buckets,
// returning seconds. Linear interpolation within the target bucket. Returns 0
// when there is no data.
func histogramQuantile(q float64, buckets map[float64]float64, total float64) float64 {
	if total <= 0 || len(buckets) == 0 {
		return 0
	}
	les := make([]float64, 0, len(buckets))
	for le := range buckets {
		les = append(les, le)
	}
	sort.Float64s(les)
	rank := q * total
	prevLe, prevCount := 0.0, 0.0
	for _, le := range les {
		count := buckets[le]
		if count >= rank {
			if le == prevLe || count == prevCount {
				return le
			}
			// interpolate within (prevLe, le]
			frac := (rank - prevCount) / (count - prevCount)
			if le > 1e300 { // +Inf bucket: no upper bound, return prevLe
				return prevLe
			}
			return prevLe + frac*(le-prevLe)
		}
		prevLe, prevCount = le, count
	}
	return les[len(les)-1]
}

// ScrapeTraefik fetches and parses Traefik's Prometheus endpoint.
func ScrapeTraefik(ctx context.Context, client *http.Client, url string) (map[string]TrafficCounters, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("traefik metrics: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	return parseTraefikMetrics(string(body)), nil
}
