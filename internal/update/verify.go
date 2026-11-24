package update

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/crueber/cuisle/internal/relsign"
)

// ErrVerify is wrapped by every verification failure: a missing or malformed SHA256SUMS.txt, a file that is not
// listed, a checksum that does not match, or a signature that is missing or does not match.
var ErrVerify = errors.New("update: the download could not be verified")

// ParseSums reads `sha256sum` output: "<64 hex>  <name>" or "<64 hex> *<name>" per line. Names are bare file
// names (release.yml and ci.yml write them from the download folder).
func ParseSums(data []byte) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		sum, name, ok := strings.Cut(line, " ")
		if !ok || len(sum) != 64 {
			return nil, fmt.Errorf("%w: malformed line in %s", ErrVerify, SumsName)
		}
		if _, err := hex.DecodeString(sum); err != nil {
			return nil, fmt.Errorf("%w: malformed checksum in %s", ErrVerify, SumsName)
		}
		name = strings.TrimLeft(name, " ")
		name = strings.TrimPrefix(name, "*")
		name = strings.TrimPrefix(name, "./")
		if name == "" {
			return nil, fmt.Errorf("%w: malformed line in %s", ErrVerify, SumsName)
		}
		sum = strings.ToLower(sum)
		if prev, dup := out[name]; dup && prev != sum {
			return nil, fmt.Errorf("%w: %s lists %s twice with different checksums", ErrVerify, SumsName, name)
		}
		out[name] = sum
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrVerify, err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: %s is empty", ErrVerify, SumsName)
	}
	return out, nil
}

// CheckSum compares the hex SHA-256 of a downloaded file with its SHA256SUMS.txt entry.
func CheckSum(sums map[string]string, name, gotHex string) error {
	want, ok := sums[name]
	if !ok {
		return fmt.Errorf("%w: %s is not listed in %s", ErrVerify, name, SumsName)
	}
	if !strings.EqualFold(want, gotHex) {
		return fmt.Errorf("%w: checksum of %s does not match", ErrVerify, name)
	}
	return nil
}

// VerifySums checks SHA256SUMS.txt.sig (base64 text written by relsign, or the raw 64 bytes) against the public
// key compiled into this build.
func VerifySums(pubKey string, sums, sig []byte) error {
	raw := sig
	if len(sig) != ed25519.SignatureSize {
		clean := strings.Join(strings.Fields(string(sig)), "")
		dec, err := base64.StdEncoding.DecodeString(clean)
		if err != nil {
			return fmt.Errorf("%w: %s is not a signature", ErrVerify, SigName)
		}
		raw = dec
	}
	if err := relsign.Verify(pubKey, sums, raw); err != nil {
		return fmt.Errorf("%w: %s does not match", ErrVerify, SigName)
	}
	return nil
}
