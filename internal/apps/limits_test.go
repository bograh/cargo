package apps

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateLimits(t *testing.T) {
	cases := []struct {
		name    string
		mem     string
		cpu     string
		pids    int32
		wantErr bool
	}{
		{"all empty (use defaults)", "", "", 0, false},
		{"valid mem bytes", "268435456", "", 0, false},
		{"valid mem suffix", "512m", "", 0, false},
		{"valid mem g", "2g", "", 0, false},
		{"valid mem uppercase", "512M", "", 0, false},
		{"bad mem suffix", "512x", "", 0, true},
		{"bad mem non-numeric", "abc", "", 0, true},
		{"mem zero", "0m", "", 0, true},
		{"valid cpu int", "", "2", 0, false},
		{"valid cpu decimal", "", "1.5", 0, false},
		{"cpu zero", "", "0", 0, true},
		{"cpu negative", "", "-1", 0, true},
		{"cpu non-numeric", "", "one", 0, true},
		{"valid pids", "", "", 256, false},
		{"pids negative", "", "", -5, true},
		{"all valid together", "1g", "2.5", 1024, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateLimits(c.mem, c.cpu, c.pids, MaxLimits{})
			if c.wantErr && err == nil {
				t.Fatalf("validateLimits(%q,%q,%d, MaxLimits{}) = nil, want error", c.mem, c.cpu, c.pids)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("validateLimits(%q,%q,%d, MaxLimits{}) = %v, want nil", c.mem, c.cpu, c.pids, err)
			}
			if c.wantErr && err != nil && !errors.Is(err, ErrValidation) {
				t.Fatalf("error %v is not ErrValidation", err)
			}
		})
	}
}

func TestLimitNullHelpers(t *testing.T) {
	if textOrNull("").Valid {
		t.Fatal("empty string should map to NULL")
	}
	if v := textOrNull("512m"); !v.Valid || v.String != "512m" {
		t.Fatalf("textOrNull(512m) = %+v", v)
	}
	if int4OrNull(0).Valid {
		t.Fatal("zero should map to NULL")
	}
	if v := int4OrNull(256); !v.Valid || v.Int32 != 256 {
		t.Fatalf("int4OrNull(256) = %+v", v)
	}
}

func TestParseMemLimit(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int64
	}{
		{"", 0},
		{"512m", 512 << 20},
		{"1g", 1 << 30},
		{"2G", 2 << 30},
		{"1024k", 1 << 20},
		{"268435456", 268435456},
		{"4096b", 4096},
	} {
		got, err := parseMemLimit(tc.in)
		if err != nil {
			t.Fatalf("parseMemLimit(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("parseMemLimit(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// Without a ceiling the per-app caps are advisory: an org admin can raise
// their own app's memory to whatever they like through the ordinary settings
// form, so the instance defaults bound only the tenants who leave them alone.
func TestValidateLimitsAgainstInstanceCeiling(t *testing.T) {
	max := MaxLimits{MemBytes: 2 << 30, CPU: 2, Pids: 1024}
	for _, tc := range []struct {
		name             string
		mem, cpu         string
		pids             int32
		wantErrSubstring string
	}{
		{"within the ceiling", "1g", "1.5", 512, ""},
		{"exactly at the ceiling", "2g", "2", 1024, ""},
		{"unset is always fine", "", "", 0, ""},
		{"memory above", "4g", "1", 0, "mem_limit 4g exceeds"},
		{"memory above in bytes", "3221225472", "1", 0, "exceeds"},
		{"cpu above", "1g", "8", 0, "cpu_limit 8 exceeds"},
		{"pids above", "1g", "1", 4096, "pids_limit 4096 exceeds"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateLimits(tc.mem, tc.cpu, tc.pids, max)
			if tc.wantErrSubstring == "" {
				if err != nil {
					t.Fatalf("rejected a value inside the ceiling: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("value above the ceiling accepted")
			}
			if !strings.Contains(err.Error(), tc.wantErrSubstring) {
				t.Fatalf("error %q should say what exceeded and by how much", err)
			}
		})
	}
}

// An instance that sets no ceiling keeps the previous behaviour exactly.
func TestValidateLimitsWithoutCeiling(t *testing.T) {
	if err := validateLimits("512g", "64", 1<<20, MaxLimits{}); err != nil {
		t.Fatalf("unbounded instance rejected a large cap: %v", err)
	}
}

func TestValidatePortRange(t *testing.T) {
	for _, p := range []int32{1, 80, 8080, 65535} {
		if err := validatePort(p); err != nil {
			t.Errorf("port %d: want valid, got %v", p, err)
		}
	}
	for _, p := range []int32{0, -1, 65536, 70000} {
		if err := validatePort(p); err == nil {
			t.Errorf("port %d: want rejected, got nil", p)
		}
	}
}
