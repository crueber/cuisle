package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestRun_SignVerifyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	t.Setenv("TEST_RELSIGN_KEY", base64.StdEncoding.EncodeToString(priv.Seed()))

	sums := filepath.Join(dir, "SHA256SUMS.txt")
	sigPath := filepath.Join(dir, "SHA256SUMS.txt.sig")
	pubPath := filepath.Join(dir, "release.pub")
	if err := os.WriteFile(sums, []byte("abc  cuisle.deb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pubPath, []byte(base64.StdEncoding.EncodeToString(pub)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = devNull.Close() })

	tests := []struct {
		name string
		args []string
		want int
	}{
		{"sign with env key", []string{"sign", "-key-env", "TEST_RELSIGN_KEY", "-in", sums, "-out", sigPath}, exitOK},
		{"verify good signature", []string{"verify", "-pub", pubPath, "-in", sums, "-sig", sigPath}, exitOK},
		{"sign with unset env", []string{"sign", "-key-env", "TEST_RELSIGN_UNSET", "-in", sums, "-out", sigPath}, exitFail},
		{"sign without -out", []string{"sign", "-in", sums}, exitUsage},
		{"unknown command", []string{"frobnicate"}, exitUsage},
		{"no arguments", nil, exitUsage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := run(tt.args, devNull, devNull); got != tt.want {
				t.Errorf("run(%v) = %d, want %d", tt.args, got, tt.want)
			}
		})
	}

	// after the good round trip, tampering with the checked file must fail verification
	if err := os.WriteFile(sums, []byte("abd  cuisle.deb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := run([]string{"verify", "-pub", pubPath, "-in", sums, "-sig", sigPath}, devNull, devNull); got != exitFail {
		t.Errorf("verify of tampered file = %d, want %d", got, exitFail)
	}
}
