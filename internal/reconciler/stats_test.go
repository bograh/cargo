package reconciler

import "testing"

func TestParseSize(t *testing.T) {
	cases := map[string]int64{
		"0B":     0,
		"1B":     1,
		"1kB":    1000,
		"1.5kB":  1500,
		"12MiB":  12 * 1024 * 1024,
		"1GiB":   1024 * 1024 * 1024,
		"1.05GB": 1_050_000_000,
	}
	for in, want := range cases {
		got, err := parseSize(in)
		if err != nil {
			t.Fatalf("parseSize(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("parseSize(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestParseDockerStats(t *testing.T) {
	line := `{"BlockIO":"0B / 0B","CPUPerc":"12.50%","Container":"abc","ID":"abc","MemPerc":"1.23%","MemUsage":"64MiB / 512MiB","Name":"cargo-app-web-app-1","NetIO":"1.5kB / 2kB","PIDs":"7"}`
	s, err := parseDockerStats(line)
	if err != nil {
		t.Fatal(err)
	}
	if s.CPUPercent != 12.5 {
		t.Errorf("cpu = %v, want 12.5", s.CPUPercent)
	}
	if s.MemBytes != 64*1024*1024 || s.MemLimitBytes != 512*1024*1024 {
		t.Errorf("mem = %d/%d", s.MemBytes, s.MemLimitBytes)
	}
	if s.NetRxBytes != 1500 || s.NetTxBytes != 2000 {
		t.Errorf("net = %d/%d, want 1500/2000", s.NetRxBytes, s.NetTxBytes)
	}
}

// docker stats identifies a container by its short id; compose ps returns the
// full one. A batched sample is worthless if it cannot be matched back.
func TestParseStatsLineReturnsContainerID(t *testing.T) {
	line := `{"Container":"a1b2c3d4e5f6","CPUPerc":"1.50%","MemUsage":"12MiB / 512MiB","NetIO":"1kB / 2kB"}`
	s, id, err := parseStatsLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if id != "a1b2c3d4e5f6" {
		t.Fatalf("id = %q, want the short container id", id)
	}
	if s.CPUPercent != 1.5 {
		t.Fatalf("cpu = %v, want 1.5", s.CPUPercent)
	}
}

func TestAttributeStats(t *testing.T) {
	const (
		fullA = "a1b2c3d4e5f6aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		fullB = "b9b8b7b6b5b4bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	byApp := map[string]string{"app-a": fullA, "app-b": fullB, "app-gone": "cccccccccccc"}
	samples := map[string]ContainerStats{
		"a1b2c3d4e5f6": {CPUPercent: 1},
		"b9b8b7b6b5b4": {CPUPercent: 2},
	}
	got := attributeStats(byApp, samples)
	if len(got) != 2 {
		t.Fatalf("attributed %d apps, want 2", len(got))
	}
	if got["app-a"].CPUPercent != 1 || got["app-b"].CPUPercent != 2 {
		t.Fatalf("samples attributed to the wrong apps: %+v", got)
	}
	// A container that vanished between resolution and sampling is absent, not
	// attributed to somebody else.
	if _, ok := got["app-gone"]; ok {
		t.Fatal("an app with no sample was given one")
	}
}
