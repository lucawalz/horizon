package wireguard

import (
	"encoding/base64"
	"testing"
)

func TestGenerateKeypair(t *testing.T) {
	kp, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}
	for name, key := range map[string]string{"private": kp.PrivateKey, "public": kp.PublicKey} {
		raw, err := base64.StdEncoding.DecodeString(key)
		if err != nil {
			t.Errorf("%s key not valid base64: %v", name, err)
		}
		if len(raw) != 32 {
			t.Errorf("%s key decodes to %d bytes, want 32", name, len(raw))
		}
	}
}

func TestPublicFromPrivateRoundTrip(t *testing.T) {
	kp, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}
	pub, err := PublicFromPrivate(kp.PrivateKey)
	if err != nil {
		t.Fatalf("PublicFromPrivate: %v", err)
	}
	if pub != kp.PublicKey {
		t.Errorf("PublicFromPrivate = %q, want %q", pub, kp.PublicKey)
	}
}

func TestPublicFromPrivateRejectsGarbage(t *testing.T) {
	if _, err := PublicFromPrivate("not-base64!!!"); err == nil {
		t.Error("expected error for invalid base64")
	}
	if _, err := PublicFromPrivate(base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Error("expected error for wrong-length key")
	}
}

func TestGenerateKeypairUnique(t *testing.T) {
	a, _ := GenerateKeypair()
	b, _ := GenerateKeypair()
	if a.PrivateKey == b.PrivateKey {
		t.Error("two generated keypairs share a private key")
	}
}
