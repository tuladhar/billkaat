package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"testing"
)

func keyPair(t *testing.T) (pubHex, privHex string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(pub), hex.EncodeToString(priv)
}

func TestSignAndVerify(t *testing.T) {
	pubHex, privHex := keyPair(t)
	key, err := Sign(privHex, Payload{
		V: 1, ID: "abc123", Email: "buyer@example.com",
		Name: "Buyer", Plan: "pro", Issued: "2026-07-11",
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	p, err := VerifyWithKey(pubHex, key)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if p.Email != "buyer@example.com" || p.Plan != "pro" {
		t.Fatalf("unexpected payload: %+v", p)
	}
}

func TestTamperedKeyFails(t *testing.T) {
	pubHex, privHex := keyPair(t)
	key, _ := Sign(privHex, Payload{V: 1, ID: "x", Email: "a@b.c", Plan: "pro"})
	// Flip a character in the payload segment.
	i := strings.Index(key, ".")
	tampered := "A" + key[1:i] + key[i:]
	if key[0] == 'A' {
		tampered = "B" + key[1:i] + key[i:]
	}
	if _, err := VerifyWithKey(pubHex, tampered); err == nil {
		t.Fatal("tampered key verified")
	}
}

func TestWrongKeyFails(t *testing.T) {
	_, privHex := keyPair(t)
	otherPub, _ := keyPair(t)
	key, _ := Sign(privHex, Payload{V: 1, ID: "x", Email: "a@b.c", Plan: "pro"})
	if _, err := VerifyWithKey(otherPub, key); err == nil {
		t.Fatal("key signed with a different private key verified")
	}
}

func TestEmptyPublicKey(t *testing.T) {
	if _, err := VerifyWithKey("", "anything.anything"); err != ErrNoPublicKey {
		t.Fatalf("expected ErrNoPublicKey, got %v", err)
	}
}
