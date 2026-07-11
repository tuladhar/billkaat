// Package license verifies one-time license keys completely offline.
//
// A license key is two base64url segments joined by a dot:
//
//	base64url(payload JSON) + "." + base64url(ed25519 signature of payload)
//
// You generate a key pair once with `go run ./cmd/licensegen keygen`, keep
// the private key secret (it is the whole business), and compile the public
// key into your release binaries via ldflags. Buyers can be issued keys with
// `licensegen sign`. No server, no phone-home, works on air-gapped machines.
package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// PublicKeyHex is injected at build time:
//
//	go build -ldflags "-X github.com/billkaat/billkaat/internal/license.PublicKeyHex=<hex>"
//
// When empty (a plain dev build), every license is rejected with a clear
// message rather than silently failing.
var PublicKeyHex = ""

// Payload is the signed content of a license.
type Payload struct {
	V      int    `json:"v"`
	ID     string `json:"id"`
	Email  string `json:"email"`
	Name   string `json:"name"`
	Plan   string `json:"plan"`
	Issued string `json:"issued"`
}

var (
	ErrNoPublicKey = errors.New("this build was compiled without a license public key")
	ErrMalformed   = errors.New("license key is malformed")
	ErrBadSig      = errors.New("license signature is invalid")
)

// Verify checks a key against the compiled-in public key.
func Verify(key string) (*Payload, error) {
	return VerifyWithKey(PublicKeyHex, key)
}

// VerifyWithKey checks a key against an explicit hex-encoded public key.
func VerifyWithKey(pubHex, key string) (*Payload, error) {
	pubHex = strings.TrimSpace(pubHex)
	if pubHex == "" {
		return nil, ErrNoPublicKey
	}
	pub, err := hex.DecodeString(pubHex)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return nil, ErrNoPublicKey
	}

	key = strings.TrimSpace(key)
	parts := strings.Split(key, ".")
	if len(parts) != 2 {
		return nil, ErrMalformed
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, ErrMalformed
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrMalformed
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), payloadBytes, sig) {
		return nil, ErrBadSig
	}

	var p Payload
	if err := json.Unmarshal(payloadBytes, &p); err != nil {
		return nil, ErrMalformed
	}
	if p.Plan != "pro" {
		return nil, fmt.Errorf("unrecognized license plan %q", p.Plan)
	}
	return &p, nil
}

// Sign creates a license key from a payload and a hex-encoded Ed25519
// private key. Used by cmd/licensegen and by tests.
func Sign(privHex string, p Payload) (string, error) {
	priv, err := hex.DecodeString(strings.TrimSpace(privHex))
	if err != nil || len(priv) != ed25519.PrivateKeySize {
		return "", errors.New("private key must be a hex-encoded 64-byte Ed25519 key")
	}
	payloadBytes, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	sig := ed25519.Sign(ed25519.PrivateKey(priv), payloadBytes)
	return base64.RawURLEncoding.EncodeToString(payloadBytes) + "." +
		base64.RawURLEncoding.EncodeToString(sig), nil
}
