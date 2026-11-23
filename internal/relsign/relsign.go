// Package relsign signs and verifies release files with ed25519 (ADR 0013; port of Weave's release signing).
//
// Keys are accepted in two forms, both for private and public keys:
//   - base64 (standard alphabet, whitespace ignored) of the raw key: a 32-byte seed or a 64-byte private key
//     for private keys, a 32-byte public key for public keys;
//   - PEM: "PRIVATE KEY" (PKCS#8) or "PUBLIC KEY" (PKIX).
//
// Signatures are the raw 64-byte ed25519 signature. The CLI in cmd/relsign writes them base64-encoded.
//
// This package uses the standard library only and imports nothing else from this module.
package relsign

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidSignature is returned by Verify when the signature does not match the data and public key.
var ErrInvalidSignature = errors.New("relsign: signature does not match")

// ParsePrivateKey reads an ed25519 private key from base64 (32-byte seed or 64-byte key) or PKCS#8 PEM.
func ParsePrivateKey(text string) (ed25519.PrivateKey, error) {
	t := strings.TrimSpace(text)
	if strings.HasPrefix(t, "-----BEGIN") {
		block, _ := pem.Decode([]byte(t))
		if block == nil || block.Type != "PRIVATE KEY" {
			return nil, errors.New("relsign: PEM block is not a PRIVATE KEY")
		}
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("relsign: parse PKCS#8 key: %w", err)
		}
		priv, ok := k.(ed25519.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("relsign: key is %T, not ed25519", k)
		}
		return priv, nil
	}

	raw, err := decodeBase64(t)
	if err != nil {
		return nil, err
	}
	switch len(raw) {
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(raw), nil
	case ed25519.PrivateKeySize:
		// the second half is the public key; it must match the seed or the key is corrupt
		priv := ed25519.NewKeyFromSeed(raw[:ed25519.SeedSize])
		if !bytes.Equal(priv, raw) {
			return nil, errors.New("relsign: 64-byte private key does not match its seed")
		}
		return priv, nil
	default:
		return nil, fmt.Errorf("relsign: private key must be a 32-byte seed or 64-byte key, got %d bytes", len(raw))
	}
}

// ParsePublicKey reads an ed25519 public key from base64 (32 bytes) or PKIX PEM.
func ParsePublicKey(text string) (ed25519.PublicKey, error) {
	t := strings.TrimSpace(text)
	if strings.HasPrefix(t, "-----BEGIN") {
		block, _ := pem.Decode([]byte(t))
		if block == nil || block.Type != "PUBLIC KEY" {
			return nil, errors.New("relsign: PEM block is not a PUBLIC KEY")
		}
		k, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("relsign: parse PKIX key: %w", err)
		}
		pub, ok := k.(ed25519.PublicKey)
		if !ok {
			return nil, fmt.Errorf("relsign: key is %T, not ed25519", k)
		}
		return pub, nil
	}

	raw, err := decodeBase64(t)
	if err != nil {
		return nil, err
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("relsign: public key must be 32 bytes, got %d", len(raw))
	}
	return ed25519.PublicKey(raw), nil
}

// Sign returns the ed25519 signature of data under the private key given as text (see ParsePrivateKey).
func Sign(privateKey string, data []byte) ([]byte, error) {
	priv, err := ParsePrivateKey(privateKey)
	if err != nil {
		return nil, err
	}
	return ed25519.Sign(priv, data), nil
}

// Verify checks sig over data against the public key given as text (see ParsePublicKey).
// It returns nil when the signature is valid and ErrInvalidSignature when it is not.
func Verify(publicKey string, data, sig []byte) error {
	pub, err := ParsePublicKey(publicKey)
	if err != nil {
		return err
	}
	if len(sig) != ed25519.SignatureSize || !ed25519.Verify(pub, data, sig) {
		return ErrInvalidSignature
	}
	return nil
}

// decodeBase64 accepts standard base64 with any whitespace (line wrapping, trailing newline).
func decodeBase64(s string) ([]byte, error) {
	clean := strings.Join(strings.Fields(s), "")
	raw, err := base64.StdEncoding.DecodeString(clean)
	if err != nil {
		return nil, fmt.Errorf("relsign: key is neither PEM nor base64: %w", err)
	}
	return raw, nil
}
