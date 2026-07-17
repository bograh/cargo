package crypto

import (
	"bytes"
	"testing"
)

func TestSealOpenRoundTrip(t *testing.T) {
	box, err := New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ct, err := box.Seal([]byte("secret-value"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Contains(ct, []byte("secret-value")) {
		t.Fatal("ciphertext contains plaintext")
	}
	pt, err := box.Open(ct)
	if err != nil || string(pt) != "secret-value" {
		t.Fatalf("Open = %q, %v", pt, err)
	}
	ct[len(ct)-1] ^= 0xFF
	if _, err := box.Open(ct); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
}

func TestNewRejectsBadKey(t *testing.T) {
	if _, err := New([]byte("short")); err == nil {
		t.Fatal("expected error for short key")
	}
}
