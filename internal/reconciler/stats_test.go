package reconciler

import "testing"

func TestParseSize(t *testing.T) {
	cases := map[string]int64{
		"0B":       0,
		"1B":       1,
		"1kB":      1000,
		"1.5kB":    1500,
		"12MiB":    12 * 1024 * 1024,
		"1GiB":     1024 * 1024 * 1024,
		"1.05GB":   1_050_000_000,
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
