// Command layercheck enforces the package dependency table of architecture.md §2 (AGENTS.md invariant 7).
// It runs `go list -json ./...` (with -tags purego, cgo off) and fails on any forbidden internal or Wails import.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
)

const mod = "github.com/crueber/cuisle"
const wails = "github.com/wailsapp/wails/v3"

// allowed lists the internal packages each package may import (relative to mod). Packages not listed here may
// import nothing internal unless matched by a prefix rule below. Test files are checked too.
var allowed = map[string][]string{
	"internal/model":                {},
	"internal/pulse":                {"internal/model"},
	"internal/apperr":               {},
	"internal/buildinfo":            {},
	"internal/paths":                {"internal/apperr"},
	"internal/logx":                 {"internal/apperr"},
	"internal/store":                {"internal/apperr"},
	"internal/store/storetest":      {"internal/store"},
	"internal/secrets":              {},
	"internal/settings":             {"internal/store", "internal/model", "internal/apperr"},
	"internal/cache":                {"internal/store", "internal/model", "internal/apperr"},
	"internal/source":               {"internal/model", "internal/apperr"},
	"internal/source/jira":          {"internal/source", "internal/model", "internal/apperr", "internal/adf", "internal/source/jira/limiter"}, // adf: comment bodies (F12, jira-sync §2.15); limiter: WP-T0
	"internal/source/jira/jiratest": {},
	"internal/sync":                 {"internal/source", "internal/cache", "internal/settings", "internal/secrets", "internal/model", "internal/store", "internal/apperr"},
	"internal/history":              {"internal/cache", "internal/store", "internal/settings", "internal/model", "internal/apperr"},
	"internal/adf":                  {"internal/model"},
	"internal/media":                {"internal/model", "internal/store", "internal/paths", "internal/apperr"},
	"internal/view":                 {"internal/model", "internal/cache", "internal/settings", "internal/pulse", "internal/adf"},
	"internal/export":               {"internal/view", "internal/model", "internal/pulse"},
	"internal/sink":                 {"internal/export", "internal/model", "internal/secrets", "internal/store", "internal/apperr"},
	"internal/sink/file":            {"internal/sink", "internal/export", "internal/model", "internal/apperr"},
	"internal/relsign":              {},
	"internal/update":               {"internal/relsign", "internal/apperr"}, // auto-update (ADR 0016)
	"internal/relsign/cmd/relsign":  {"internal/relsign"},
}

// testOnly extends the allowance for _test.go files (fixtures and helpers are fine in tests).
var testOnly = map[string][]string{
	"internal/settings":    {"internal/store/storetest"},
	"internal/cache":       {"internal/store/storetest", "internal/settings"},
	"internal/store":       {"internal/store/storetest"},
	"internal/history":     {"internal/store/storetest", "internal/sync", "internal/source", "internal/source/jira", "internal/source/jira/jiratest", "internal/secrets", "internal/view", "internal/pulse"},
	"internal/sync":        {"internal/store/storetest", "internal/source/jira", "internal/source/jira/jiratest", "internal/view", "internal/pulse", "internal/history"},
	"internal/view":        {"internal/store/storetest", "internal/sync", "internal/source", "internal/source/jira", "internal/source/jira/jiratest", "internal/secrets", "internal/store", "internal/apperr"},
	"internal/sink":        {"internal/store/storetest", "internal/view", "internal/pulse", "internal/sink/file"},
	"internal/sink/file":   {"internal/view", "internal/pulse"},
	"internal/export":      {},
	"internal/media":       {"internal/store/storetest"},
	"internal/source/jira": {"internal/source/jira/jiratest"},
}

type pkg struct {
	ImportPath   string
	Imports      []string
	TestImports  []string
	XTestImports []string
}

func main() {
	cmd := exec.Command("go", "list", "-tags", "purego", "-json", "./...")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "layercheck: go list:", err)
		os.Exit(2)
	}
	var violations []string
	dec := json.NewDecoder(&out)
	for {
		var p pkg
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			fmt.Fprintln(os.Stderr, "layercheck: decode:", err)
			os.Exit(2)
		}
		violations = append(violations, check(p)...)
	}
	sort.Strings(violations)
	if len(violations) > 0 {
		fmt.Fprintln(os.Stderr, "layercheck: forbidden imports (architecture.md §2):")
		for _, v := range violations {
			fmt.Fprintln(os.Stderr, "  "+v)
		}
		os.Exit(1)
	}
	fmt.Println("layercheck: ok")
}

func rel(path string) (string, bool) {
	if path == mod {
		return "", true
	}
	if strings.HasPrefix(path, mod+"/") {
		return strings.TrimPrefix(path, mod+"/"), true
	}
	return "", false
}

func check(p pkg) []string {
	self, ok := rel(p.ImportPath)
	if !ok {
		return nil
	}
	var v []string
	isCmdCuisle := self == "cmd/cuisle"
	isApp := self == "internal/app"
	for _, set := range []struct {
		imports []string
		test    bool
	}{{p.Imports, false}, {p.TestImports, true}, {p.XTestImports, true}} {
		for _, imp := range set.imports {
			if strings.HasPrefix(imp, wails) && !isCmdCuisle && !isApp {
				v = append(v, fmt.Sprintf("%s imports Wails (%s): only internal/app and cmd/cuisle may", self, imp))
				continue
			}
			r, ok := rel(imp)
			if !ok || r == self {
				continue
			}
			if r == "internal/app" && !isCmdCuisle {
				v = append(v, fmt.Sprintf("%s imports internal/app: nothing imports app", self))
				continue
			}
			if isCmdCuisle || strings.HasPrefix(self, "scripts/") || strings.HasPrefix(self, "cmd/fixturegen") {
				continue
			}
			if r == "frontend" {
				v = append(v, fmt.Sprintf("%s imports frontend: only cmd/cuisle may", self))
				continue
			}
			if isApp {
				if r == "internal/source/jira" || r == "internal/sink/file" || r == "internal/source/jira/jiratest" {
					if !set.test {
						v = append(v, fmt.Sprintf("internal/app imports concrete adapter %s: only cmd/cuisle may", r))
					}
				}
				continue
			}
			if !permitted(self, r, set.test) {
				v = append(v, fmt.Sprintf("%s imports %s%s", self, r, map[bool]string{true: " (test)", false: ""}[set.test]))
			}
		}
	}
	return v
}

func permitted(self, imp string, test bool) bool {
	list, known := allowed[self]
	if !known {
		return false // every internal package must be listed
	}
	for _, a := range list {
		if a == imp {
			return true
		}
	}
	if test {
		for _, a := range testOnly[self] {
			if a == imp {
				return true
			}
		}
	}
	return false
}
