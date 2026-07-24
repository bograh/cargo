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

// validateLimits checks the resource-cap fields. Empty string / zero means
// "unset" (fall back to the instance default) and is always valid.
func validateLimits(mem, cpu string, pids int32) error {
	if mem != "" && !memLimitRe.MatchString(mem) {
		return fmt.Errorf("%w: mem_limit must be a positive integer with an optional b/k/m/g suffix, e.g. 512m", ErrValidation)
	}
	if cpu != "" {
		f, err := strconv.ParseFloat(cpu, 64)
		if err != nil || f <= 0 {
			return fmt.Errorf("%w: cpu_limit must be a positive number, e.g. 1 or 1.5", ErrValidation)
		}
	}
	if pids < 0 {
		return fmt.Errorf("%w: pids_limit must be a positive integer", ErrValidation)
	}
	return nil
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
