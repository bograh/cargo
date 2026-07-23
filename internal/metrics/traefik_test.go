package metrics

import (
	"math"
	"testing"
)

const sampleMetrics = `
# HELP traefik_service_requests_total How many HTTP requests processed on a service.
# TYPE traefik_service_requests_total counter
traefik_service_requests_total{code="200",method="GET",protocol="http",service="app-web@docker"} 90
traefik_service_requests_total{code="500",method="GET",protocol="http",service="app-web@docker"} 10
traefik_service_requests_total{code="200",method="GET",protocol="http",service="app-api@docker"} 5
# HELP traefik_service_request_duration_seconds request duration
# TYPE traefik_service_request_duration_seconds histogram
traefik_service_request_duration_seconds_bucket{service="app-web@docker",le="0.1"} 50
traefik_service_request_duration_seconds_bucket{service="app-web@docker",le="0.5"} 95
traefik_service_request_duration_seconds_bucket{service="app-web@docker",le="1"} 100
traefik_service_request_duration_seconds_bucket{service="app-web@docker",le="+Inf"} 100
traefik_service_request_duration_seconds_sum{service="app-web@docker"} 20
traefik_service_request_duration_seconds_count{service="app-web@docker"} 100
`

func TestParseTraefikMetrics(t *testing.T) {
	m := parseTraefikMetrics(sampleMetrics)
	web, ok := m["app-web@docker"]
	if !ok {
		t.Fatal("app-web@docker missing")
	}
	if web.Requests != 100 {
		t.Errorf("requests = %v, want 100", web.Requests)
	}
	if web.Errors != 10 {
		t.Errorf("errors = %v, want 10 (5xx)", web.Errors)
	}
	if web.DurationCount != 100 || web.DurationSum != 20 {
		t.Errorf("duration sum/count = %v/%v", web.DurationSum, web.DurationCount)
	}
	if m["app-api@docker"].Requests != 5 {
		t.Errorf("api requests = %v, want 5", m["app-api@docker"].Requests)
	}
}

func TestHistogramQuantile(t *testing.T) {
	web := parseTraefikMetrics(sampleMetrics)["app-web@docker"]
	// p50 falls in the (0.1,0.5] bucket: 50 already ≤0.1, need 50th of 100,
	// bucket 0.5 holds up to 95 → interpolate within [0.1,0.5].
	p50 := histogramQuantile(0.5, web.Buckets, web.DurationCount)
	if p50 < 0.1 || p50 > 0.5 {
		t.Errorf("p50 = %v, want within (0.1,0.5]", p50)
	}
	p95 := histogramQuantile(0.95, web.Buckets, web.DurationCount)
	if math.Abs(p95-0.5) > 1e-9 {
		t.Errorf("p95 = %v, want 0.5 (cumulative reaches 95 exactly at le=0.5)", p95)
	}
}

// TestHistogramQuantileInfClamp ensures an inconsistent histogram (where the
// cumulative count never reaches `total` even at +Inf) never causes
// histogramQuantile to return +Inf; it must clamp to the highest finite le.
func TestHistogramQuantileInfClamp(t *testing.T) {
	buckets := map[float64]float64{
		0.1:         5,
		math.Inf(1): 5,
	}
	got := histogramQuantile(0.95, buckets, 10)
	if math.IsInf(got, 0) {
		t.Fatalf("histogramQuantile returned non-finite value: %v", got)
	}
	if got != 0.1 {
		t.Errorf("histogramQuantile = %v, want 0.1 (highest finite le)", got)
	}
}
