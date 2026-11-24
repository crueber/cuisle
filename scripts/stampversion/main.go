// Command stampversion writes a copy of build/windows/info.json with the release version in every version field,
// so the Windows exe's version info matches the release (the committed file keeps a 0.0.1 placeholder).
//
//	go run ./scripts/stampversion -version 1.2.3 -in build/windows/info.json -out bin/info.json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var numeric = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

func main() {
	version := flag.String("version", "", "numeric version, e.g. 1.2.3")
	in := flag.String("in", "", "info.json to read")
	out := flag.String("out", "", "file to write")
	flag.Parse()
	if err := run(*version, *in, *out); err != nil {
		fmt.Fprintln(os.Stderr, "stampversion:", err)
		os.Exit(1)
	}
}

func run(version, in, out string) error {
	if !numeric.MatchString(version) {
		return fmt.Errorf("version %q is not X.Y.Z", version)
	}
	b, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	stamped, err := stamp(b, version)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	return os.WriteFile(out, stamped, 0o644)
}

// stamp sets fixed.file_version/product_version and every info.<lang>.FileVersion/ProductVersion.
func stamp(b []byte, version string) ([]byte, error) {
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	if fixed, ok := doc["fixed"].(map[string]any); ok {
		fixed["file_version"], fixed["product_version"] = version, version
	}
	if info, ok := doc["info"].(map[string]any); ok {
		for _, v := range info {
			if lang, ok := v.(map[string]any); ok {
				lang["FileVersion"], lang["ProductVersion"] = version, version
			}
		}
	}
	return json.MarshalIndent(doc, "", "\t")
}
