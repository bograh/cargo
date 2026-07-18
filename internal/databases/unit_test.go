package databases

import (
	"errors"
	"testing"
)

func TestValidateCreate(t *testing.T) {
	cases := []struct {
		name string
		in   CreateInput
		ok   bool
	}{
		{"good postgres", CreateInput{Name: "orders-db", Engine: "postgres", Version: "16"}, true},
		{"good redis acl", CreateInput{Name: "cache", Engine: "redis", Version: "7", RedisMode: "acl"}, true},
		{"good redis shared", CreateInput{Name: "cache", Engine: "redis", Version: "7", RedisMode: "shared"}, true},
		{"single char name", CreateInput{Name: "a", Engine: "postgres", Version: "16"}, true},
		{"leading digit name", CreateInput{Name: "3scale", Engine: "postgres", Version: "16"}, true},
		{"32-char name", CreateInput{Name: "a234567890123456789012345678901b", Engine: "postgres", Version: "16"}, true},
		{"33-char name", CreateInput{Name: "a2345678901234567890123456789012b", Engine: "postgres", Version: "16"}, false},
		{"upper name", CreateInput{Name: "OrdersDB", Engine: "postgres", Version: "16"}, false},
		{"leading dash", CreateInput{Name: "-x", Engine: "postgres", Version: "16"}, false},
		{"trailing dash", CreateInput{Name: "x-", Engine: "postgres", Version: "16"}, false},
		{"bad engine", CreateInput{Name: "orders", Engine: "mysql", Version: "16"}, false},
		{"no version", CreateInput{Name: "orders", Engine: "postgres"}, false},
		{"redis no mode", CreateInput{Name: "cache", Engine: "redis", Version: "7"}, false},
		{"redis bad mode", CreateInput{Name: "cache", Engine: "redis", Version: "7", RedisMode: "cluster"}, false},
		{"postgres with mode", CreateInput{Name: "orders", Engine: "postgres", Version: "16", RedisMode: "acl"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateCreate(c.in)
			if c.ok && err != nil {
				t.Fatalf("expected ok, got %v", err)
			}
			if !c.ok && !errors.Is(err, ErrValidation) {
				t.Fatalf("expected ErrValidation, got %v", err)
			}
		})
	}
}

func TestPickIndex(t *testing.T) {
	if got, _ := pickIndex(nil); got != 0 {
		t.Fatalf("empty -> %d, want 0", got)
	}
	if got, _ := pickIndex([]int32{0, 1, 2}); got != 3 {
		t.Fatalf("gapless -> %d, want 3", got)
	}
	if got, _ := pickIndex([]int32{0, 2, 3}); got != 1 {
		t.Fatalf("gap -> %d, want 1", got)
	}
	full := make([]int32, maxRedisDBs)
	for i := range full {
		full[i] = int32(i)
	}
	if _, err := pickIndex(full); !errors.Is(err, ErrConflict) {
		t.Fatalf("full -> %v, want ErrConflict", err)
	}
}

func TestQuoteLiteral(t *testing.T) {
	cases := map[string]string{
		"plain":     "plain",
		"o'brien":   "o''brien",
		"''":        "''''",
		"a'b'c":     "a''b''c",
		"no-quotes": "no-quotes",
	}
	for in, want := range cases {
		if got := quoteLiteral(in); got != want {
			t.Fatalf("quoteLiteral(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDBIdent(t *testing.T) {
	if got := dbIdent("my-api"); got != "app_my_api" {
		t.Fatalf("dbIdent = %q", got)
	}
	if got := dbIdent("web"); got != "app_web" {
		t.Fatalf("dbIdent = %q", got)
	}
}

func TestValidSnapshotName(t *testing.T) {
	snaps := []SnapshotInfo{{Name: "20260101-000000.sql"}}
	if !validSnapshotName("20260101-000000.sql", snaps) {
		t.Fatal("expected valid name accepted")
	}
	for _, bad := range []string{"../etc/passwd", "/etc/passwd", "nope.sql", "", "sub/x.sql"} {
		if validSnapshotName(bad, snaps) {
			t.Fatalf("expected %q rejected", bad)
		}
	}
}
