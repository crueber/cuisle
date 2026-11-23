package relsign_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"strings"
	"testing"

	"github.com/crueber/cuisle/internal/relsign"
)

// rfc8032Seed, rfc8032Pub and rfc8032Sig are RFC 8032 section 7.1, TEST 1 (empty message).
const (
	rfc8032Seed = "9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60"
	rfc8032Pub  = "d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a"
	rfc8032Sig  = "e5564300c360ac729086e2cc806e828a84877f1eb8e5d974d873e065224901555fb8821590a33bacc61e39701cf9b46bd25bf5f0595bbe24655141438e7a100b"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex fixture: %v", err)
	}
	return b
}

func TestSign_KnownAnswer(t *testing.T) {
	seed := base64.StdEncoding.EncodeToString(mustHex(t, rfc8032Seed))
	got, err := relsign.Sign(seed, nil)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if hex.EncodeToString(got) != rfc8032Sig {
		t.Errorf("signature = %x, want %s", got, rfc8032Sig)
	}
	pub := base64.StdEncoding.EncodeToString(mustHex(t, rfc8032Pub))
	if err := relsign.Verify(pub, nil, got); err != nil {
		t.Errorf("Verify of RFC vector: %v", err)
	}
}

func TestParsePrivateKey_Forms(t *testing.T) {
	seed := mustHex(t, rfc8032Seed)
	full := ed25519.NewKeyFromSeed(seed)

	pkcs8, err := x509.MarshalPKCS8PrivateKey(full)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	pkcs8PEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}))

	tests := []struct {
		name    string
		text    string
		wantErr bool
	}{
		{"base64 seed", base64.StdEncoding.EncodeToString(seed), false},
		{"base64 seed with trailing newline", base64.StdEncoding.EncodeToString(seed) + "\n", false},
		{"base64 64-byte key", base64.StdEncoding.EncodeToString(full), false},
		{"base64 64-byte key with wrong public half", base64.StdEncoding.EncodeToString(append(append([]byte{}, seed...), make([]byte, 32)...)), true},
		{"PKCS#8 PEM", pkcs8PEM, false},
		{"PEM of the wrong type", string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: pkcs8})), true},
		{"wrong length", base64.StdEncoding.EncodeToString(seed[:16]), true},
		{"not base64", "!!!not a key!!!", true},
		{"empty", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			priv, err := relsign.ParsePrivateKey(tt.text)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParsePrivateKey: want error, got key %d bytes", len(priv))
				}
				return
			}
			if err != nil {
				t.Fatalf("ParsePrivateKey: %v", err)
			}
			if !bytes.Equal(priv, full) {
				t.Errorf("parsed key differs from the seed-derived key")
			}
		})
	}
}

func TestParsePublicKey_Forms(t *testing.T) {
	pub := mustHex(t, rfc8032Pub)
	pkix, err := x509.MarshalPKIXPublicKey(ed25519.PublicKey(pub))
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey: %v", err)
	}
	pkixPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pkix}))

	tests := []struct {
		name    string
		text    string
		wantErr bool
	}{
		{"base64 32 bytes", base64.StdEncoding.EncodeToString(pub), false},
		{"PKIX PEM", pkixPEM, false},
		{"base64 wrong length", base64.StdEncoding.EncodeToString(pub[:31]), true},
		{"PEM of the wrong type", strings.Replace(pkixPEM, "PUBLIC KEY", "PRIVATE KEY", 2), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := relsign.ParsePublicKey(tt.text)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParsePublicKey: want error, got %x", []byte(got))
				}
				return
			}
			if err != nil {
				t.Fatalf("ParsePublicKey: %v", err)
			}
			if hex.EncodeToString(got) != rfc8032Pub {
				t.Errorf("public key = %x, want %s", []byte(got), rfc8032Pub)
			}
		})
	}
}

func TestVerify_Outcomes(t *testing.T) {
	seed := base64.StdEncoding.EncodeToString(mustHex(t, rfc8032Seed))
	pub := base64.StdEncoding.EncodeToString(mustHex(t, rfc8032Pub))

	data := []byte("e3b0c44298fc1c149afbf4c8996fb924  cuisle-0.0.1-linux.deb\n")
	sig, err := relsign.Sign(seed, data)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	otherPub := func() string {
		p, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("GenerateKey: %v", err)
		}
		return base64.StdEncoding.EncodeToString(p)
	}()

	tampered := append([]byte{}, data...)
	tampered[0] ^= 0x01
	badSig := append([]byte{}, sig...)
	badSig[len(badSig)-1] ^= 0x01

	tests := []struct {
		name string
		pub  string
		data []byte
		sig  []byte
		want error
	}{
		{"valid", pub, data, sig, nil},
		{"tampered data", pub, tampered, sig, relsign.ErrInvalidSignature},
		{"tampered signature", pub, data, badSig, relsign.ErrInvalidSignature},
		{"wrong public key", otherPub, data, sig, relsign.ErrInvalidSignature},
		{"truncated signature", pub, data, sig[:10], relsign.ErrInvalidSignature},
		{"empty signature", pub, data, nil, relsign.ErrInvalidSignature},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := relsign.Verify(tt.pub, tt.data, tt.sig)
			if !errors.Is(err, tt.want) && !(err == nil && tt.want == nil) {
				t.Errorf("Verify = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestVerify_BadPublicKeyIsNotInvalidSignature(t *testing.T) {
	err := relsign.Verify("not a key", []byte("x"), make([]byte, ed25519.SignatureSize))
	if err == nil || errors.Is(err, relsign.ErrInvalidSignature) {
		t.Errorf("Verify with a bad key = %v, want a key-format error", err)
	}
}

func TestSign_BadKeyErrors(t *testing.T) {
	if _, err := relsign.Sign("garbage", []byte("x")); err == nil {
		t.Error("Sign with a garbage key: want error")
	}
}
