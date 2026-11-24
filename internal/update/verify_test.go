package update

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"testing"
)

func sumOf(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func TestParseSums(t *testing.T) {
	a, b := sumOf([]byte("a")), sumOf([]byte("b"))
	got, err := ParseSums([]byte(a + "  Cuisle-v1.dmg\n" + b + " *cuisle.AppImage\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got["Cuisle-v1.dmg"] != a || got["cuisle.AppImage"] != b {
		t.Fatalf("%v", got)
	}
	for _, bad := range []string{"", "nothex  x\n", "zz" + a[2:] + "  x\n", a + "\n"} {
		if _, err := ParseSums([]byte(bad)); !errors.Is(err, ErrVerify) {
			t.Errorf("%q: err %v", bad, err)
		}
	}
}

func TestCheckSum(t *testing.T) {
	table := map[string]string{"x.dmg": sumOf([]byte("good"))}
	if err := CheckSum(table, "x.dmg", sumOf([]byte("good"))); err != nil {
		t.Fatal(err)
	}
	if err := CheckSum(table, "x.dmg", sumOf([]byte("evil"))); !errors.Is(err, ErrVerify) {
		t.Fatalf("bad checksum accepted: %v", err)
	}
	if err := CheckSum(table, "y.dmg", sumOf([]byte("good"))); !errors.Is(err, ErrVerify) {
		t.Fatalf("unlisted file accepted: %v", err)
	}
}

func TestVerifySums(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	pubText := base64.StdEncoding.EncodeToString(pub)
	sums := []byte(sumOf([]byte("x")) + "  x.dmg\n")
	sig := ed25519.Sign(priv, sums)
	if err := VerifySums(pubText, sums, []byte(base64.StdEncoding.EncodeToString(sig)+"\n")); err != nil {
		t.Fatalf("base64 signature: %v", err)
	}
	if err := VerifySums(pubText, sums, sig); err != nil {
		t.Fatalf("raw signature: %v", err)
	}
	if err := VerifySums(pubText, append([]byte("tampered"), sums...), sig); !errors.Is(err, ErrVerify) {
		t.Fatalf("tampered sums accepted: %v", err)
	}
	if err := VerifySums(pubText, sums, []byte("not a signature")); !errors.Is(err, ErrVerify) {
		t.Fatalf("junk accepted: %v", err)
	}
}
