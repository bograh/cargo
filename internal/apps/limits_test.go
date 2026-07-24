package apps

import (
	"errors"
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
			err := validateLimits(c.mem, c.cpu, c.pids)
			if c.wantErr && err == nil {
				t.Fatalf("validateLimits(%q,%q,%d) = nil, want error", c.mem, c.cpu, c.pids)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("validateLimits(%q,%q,%d) = %v, want nil", c.mem, c.cpu, c.pids, err)
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
