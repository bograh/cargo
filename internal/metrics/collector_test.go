package metrics

import (
	"math"
	"testing"
)

func TestSampleTrafficRates(t *testing.T) {
	c := &Collector{}
	prev := TrafficCounters{Requests: 100, Errors: 5, DurationSum: 10, DurationCount: 100}
	cur := TrafficCounters{
		Requests: 130, Errors: 11, DurationSum: 13, DurationCount: 130,
		Buckets: map[float64]float64{0.1: 60, 0.5: 125, math.Inf(1): 130},
	}
	reqRate, errRate, _, p95 := c.sampleTraffic(prev, cur, 15)
	if math.Abs(reqRate-2.0) > 1e-9 { // (130-100)/15
		t.Errorf("reqRate = %v, want 2", reqRate)
	}
	if math.Abs(errRate-(6.0/30.0)) > 1e-9 { // 6 new errors / 30 new reqs
		t.Errorf("errRate = %v, want 0.2", errRate)
	}
	if p95 <= 0 {
		t.Errorf("p95 = %v, want > 0", p95)
	}
}

func TestSampleTrafficNoTraffic(t *testing.T) {
	c := &Collector{}
	prev := TrafficCounters{Requests: 50}
	cur := TrafficCounters{Requests: 50}
	reqRate, errRate, p50, p95 := c.sampleTraffic(prev, cur, 15)
	if reqRate != 0 || errRate != 0 || p50 != 0 || p95 != 0 {
		t.Errorf("expected all zero, got %v %v %v %v", reqRate, errRate, p50, p95)
	}
}

// Percentiles describe the interval, not Traefik's whole uptime: a burst of
// slow requests must show up even after a long history of fast ones.
func TestSampleTrafficPercentilesArePerInterval(t *testing.T) {
	c := &Collector{}
	prev := TrafficCounters{
		Requests: 10000, DurationCount: 10000,
		Buckets: map[float64]float64{0.1: 10000, 1: 10000, math.Inf(1): 10000},
	}
	cur := TrafficCounters{
		Requests: 10100, DurationCount: 10100,
		Buckets: map[float64]float64{0.1: 10000, 1: 10100, math.Inf(1): 10100},
	}
	_, _, p50, _ := c.sampleTraffic(prev, cur, 15)
	if p50 <= 100 {
		t.Errorf("p50 = %vms, want > 100ms (every request this interval was slow)", p50)
	}
}
