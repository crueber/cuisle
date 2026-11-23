// Command relsign signs and verifies release files with ed25519 (see internal/relsign, ADR 0013).
//
//	relsign sign   [-key-env RELEASE_SIGNING_KEY] -in SHA256SUMS.txt -out SHA256SUMS.txt.sig
//	relsign verify -pub release.pub              -in SHA256SUMS.txt -sig SHA256SUMS.txt.sig
//
// The private key is read from an environment variable, never from argv. The signature file holds the
// base64 of the raw signature with a trailing newline. Exit codes: 0 ok, 1 failure, 2 usage.
package main

import (
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/crueber/cuisle/internal/relsign"
)

const (
	exitOK    = 0
	exitFail  = 1
	exitUsage = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr *os.File) int {
	if len(args) == 0 {
		usage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "sign":
		return sign(args[1:], stdout, stderr)
	case "verify":
		return verify(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		usage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "relsign: unknown command %q\n", args[0])
		usage(stderr)
		return exitUsage
	}
}

func usage(w *os.File) {
	fmt.Fprintln(w, "usage:")
	fmt.Fprintln(w, "  relsign sign   [-key-env NAME] -in FILE -out FILE.sig   (default key env: RELEASE_SIGNING_KEY)")
	fmt.Fprintln(w, "  relsign verify -pub FILE       -in FILE -sig FILE.sig")
}

func sign(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("sign", flag.ContinueOnError)
	fs.SetOutput(stderr)
	keyEnv := fs.String("key-env", "RELEASE_SIGNING_KEY", "environment variable holding the private key (base64 seed/key or PKCS#8 PEM)")
	in := fs.String("in", "", "file to sign")
	out := fs.String("out", "", "signature file to write (base64)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *in == "" || *out == "" {
		fmt.Fprintln(stderr, "relsign sign: -in and -out are required")
		return exitUsage
	}

	keyText := os.Getenv(*keyEnv)
	if strings.TrimSpace(keyText) == "" {
		fmt.Fprintf(stderr, "relsign sign: environment variable %s is empty\n", *keyEnv)
		return exitFail
	}
	data, err := os.ReadFile(*in)
	if err != nil {
		fmt.Fprintf(stderr, "relsign sign: read %s: %v\n", *in, err)
		return exitFail
	}
	sig, err := relsign.Sign(keyText, data)
	if err != nil {
		fmt.Fprintf(stderr, "relsign sign: %v\n", err)
		return exitFail
	}
	if err := os.WriteFile(*out, []byte(base64.StdEncoding.EncodeToString(sig)+"\n"), 0o644); err != nil {
		fmt.Fprintf(stderr, "relsign sign: write %s: %v\n", *out, err)
		return exitFail
	}
	fmt.Fprintf(stdout, "signed %s -> %s\n", *in, *out)
	return exitOK
}

func verify(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	pub := fs.String("pub", "", "file holding the public key (base64 or PKIX PEM)")
	in := fs.String("in", "", "file that was signed")
	sigPath := fs.String("sig", "", "signature file (base64)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *pub == "" || *in == "" || *sigPath == "" {
		fmt.Fprintln(stderr, "relsign verify: -pub, -in and -sig are required")
		return exitUsage
	}

	pubText, err := os.ReadFile(*pub)
	if err != nil {
		fmt.Fprintf(stderr, "relsign verify: read %s: %v\n", *pub, err)
		return exitFail
	}
	data, err := os.ReadFile(*in)
	if err != nil {
		fmt.Fprintf(stderr, "relsign verify: read %s: %v\n", *in, err)
		return exitFail
	}
	sigText, err := os.ReadFile(*sigPath)
	if err != nil {
		fmt.Fprintf(stderr, "relsign verify: read %s: %v\n", *sigPath, err)
		return exitFail
	}
	sig, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(string(sigText)), ""))
	if err != nil {
		fmt.Fprintf(stderr, "relsign verify: signature file is not base64: %v\n", err)
		return exitFail
	}
	if err := relsign.Verify(string(pubText), data, sig); err != nil {
		if errors.Is(err, relsign.ErrInvalidSignature) {
			fmt.Fprintf(stderr, "relsign verify: %s does NOT match %s\n", *sigPath, *in)
		} else {
			fmt.Fprintf(stderr, "relsign verify: %v\n", err)
		}
		return exitFail
	}
	fmt.Fprintf(stdout, "signature OK: %s\n", *in)
	return exitOK
}
