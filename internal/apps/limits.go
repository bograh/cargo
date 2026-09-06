package apps

import (
	"fmt"
	"regexp"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"
)

// memLimitRe matches a docker memory value: a positive integer with an optional
// unit suffix (b, k, m, g — case-insensitive), e.g. "512m", "1g", "268435456".
var memLimitRe = regexp.MustCompile(`^[1-9][0-9]*[bkmgBKMG]?$`)

// MaxLimits is the ceiling an app may not raise its own caps above. A zero
// field means no ceiling for that resource.
//
// Without one, the per-app caps are advisory: an org admin can set mem_limit
// to 512g through the ordinary Settings form, so the isolation the instance
// defaults describe is something a tenant opts into. The ceiling is what makes
// them a bound. It is off by default — a single-operator install has nobody to
// bound — and set per instance through CARGO_MAX_* .
type MaxLimits struct {
	MemBytes int64
	CPU      float64
	Pids     int32
}

// memUnits maps docker's memory suffixes to their multiplier.
var memUnits = map[byte]int64{
	'b': 1,
	'k': 1 << 10,
	'm': 1 << 20,
	'g': 1 << 30,
}

// ParseMemLimit converts a docker memory value ("512m", "1g", "268435456") to
// bytes, so a configured ceiling can be compared against what an app asks for.
func ParseMemLimit(s string) (int64, error) { return parseMemLimit(s) }

// parseMemLimit converts a docker memory value ("512m", "1g", "268435456") to
// bytes. The input must already have matched memLimitRe.
func parseMemLimit(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	digits, mult := s, int64(1)
	last := s[len(s)-1]
	if m, ok := memUnits[last|0x20]; ok { // |0x20 lowercases an ASCII letter
		digits, mult = s[:len(s)-1], m
	}
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: mem_limit %q is not a number", ErrValidation, s)
	}
	return n * mult, nil
}

// formatMemLimit renders bytes back in the units a user would have typed, for
// error messages.
func formatMemLimit(b int64) string {
	switch {
	case b%(1<<30) == 0:
		return strconv.FormatInt(b/(1<<30), 10) + "g"
	case b%(1<<20) == 0:
		return strconv.FormatInt(b/(1<<20), 10) + "m"
	case b%(1<<10) == 0:
		return strconv.FormatInt(b/(1<<10), 10) + "k"
	default:
		return strconv.FormatInt(b, 10) + "b"
	}
}

// validateLimits checks the resource-cap fields against the format and, where
// the instance sets one, against the ceiling. Empty string / zero means "unset"
// (fall back to the instance default) and is always valid.
func validateLimits(mem, cpu string, pids int32, max MaxLimits) error {
	if mem != "" {
		if !memLimitRe.MatchString(mem) {
			return fmt.Errorf("%w: mem_limit must be a positive integer with an optional b/k/m/g suffix, e.g. 512m", ErrValidation)
		}
		bytes, err := parseMemLimit(mem)
		if err != nil {
			return err
		}
		if max.MemBytes > 0 && bytes > max.MemBytes {
			return fmt.Errorf("%w: mem_limit %s exceeds this instance's maximum of %s",
				ErrValidation, mem, formatMemLimit(max.MemBytes))
		}
	}
	if cpu != "" {
		f, err := strconv.ParseFloat(cpu, 64)
		if err != nil || f <= 0 {
			return fmt.Errorf("%w: cpu_limit must be a positive number, e.g. 1 or 1.5", ErrValidation)
		}
		if max.CPU > 0 && f > max.CPU {
			return fmt.Errorf("%w: cpu_limit %s exceeds this instance's maximum of %g", ErrValidation, cpu, max.CPU)
		}
	}
	if pids < 0 {
		return fmt.Errorf("%w: pids_limit must be a positive integer", ErrValidation)
	}
	if max.Pids > 0 && pids > max.Pids {
		return fmt.Errorf("%w: pids_limit %d exceeds this instance's maximum of %d", ErrValidation, pids, max.Pids)
	}
	return nil
}

// validateDeployStrategy checks the per-app strategy override. Empty means
// "unset" (fall back to the instance default) and is always valid.
func validateDeployStrategy(s string) error {
	switch s {
	case "", "bluegreen", "recreate":
		return nil
	}
	return fmt.Errorf("%w: deploy_strategy must be bluegreen or recreate", ErrValidation)
}

// textOrNull maps "" to a NULL pgtype.Text and any other value to a valid one.
func textOrNull(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

// int4OrNull maps 0 to NULL and any positive value to a valid pgtype.Int4.
func int4OrNull(n int32) pgtype.Int4 {
	if n <= 0 {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: n, Valid: true}
}
