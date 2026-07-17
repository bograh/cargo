package auth

import (
	"strings"
	"testing"
)

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("s3cret-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("hash format = %q", hash)
	}
	ok, err := VerifyPassword(hash, "s3cret-password")
	if err != nil || !ok {
		t.Fatalf("verify correct = %v, %v", ok, err)
	}
	ok, err = VerifyPassword(hash, "wrong")
	if err != nil || ok {
		t.Fatalf("verify wrong = %v, %v", ok, err)
	}
}

func TestVerifyPasswordRejectsGarbage(t *testing.T) {
	if _, err := VerifyPassword("not-a-hash", "x"); err == nil {
		t.Fatal("expected error for malformed hash")
	}
}
