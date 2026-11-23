// Command checkownership reads changed paths (one per line) on stdin and fails if any is outside the WP's allowlist in
// docs/agent-plan.ownership.txt or touches a frozen file. Usage: git diff --name-only main...wp/NN | scripts/agent/check-ownership WP-NN
package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
)

var frozen = []string{
	"internal/**/contract*.go", "internal/model/**", "internal/app/types.go", "internal/app/deps.go",
	"internal/store/migrations/0001_init.sql", "frontend/src/api/types.ts", "frontend/src/api/index.ts",
	"frontend/src/api/errors.ts", "frontend/src/api/events.ts", "frontend/src/components/props.ts",
	"frontend/src/views/*/props.ts", "go.mod", "go.sum", "frontend/package.json", "frontend/package-lock.json",
	"frontend/src/bindings/**", "AGENTS.md", "CLAUDE.md", "Taskfile.yml", "frontend/src/App.tsx", "frontend/src/main.tsx",
}

func glob(g string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(g); i++ {
		c := g[i]
		switch {
		case c == '*' && i+1 < len(g) && g[i+1] == '*':
			b.WriteString(".*")
			i++
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: check-ownership WP-NN < paths")
		os.Exit(2)
	}
	wp := os.Args[1]
	f, err := os.Open("docs/agent-plan.ownership.txt")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	var allow []*regexp.Regexp
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == wp {
			allow = append(allow, glob(fields[1]))
		}
	}
	f.Close()
	if len(allow) == 0 {
		fmt.Fprintf(os.Stderr, "check-ownership: no allowlist for %s\n", wp)
		os.Exit(2)
	}
	allow = append(allow, glob("docs/wp-notes/"+wp+".md"))
	var frozenRe []*regexp.Regexp
	for _, g := range frozen {
		frozenRe = append(frozenRe, glob(g))
	}
	bad := 0
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		p := strings.TrimSpace(in.Text())
		if p == "" {
			continue
		}
		isFrozen := false
		for _, r := range frozenRe {
			if r.MatchString(p) {
				isFrozen = true
			}
		}
		ok := false
		for _, r := range allow {
			if r.MatchString(p) {
				ok = true
			}
		}
		switch {
		case isFrozen:
			fmt.Printf("FROZEN   %s\n", p)
			bad++
		case !ok:
			fmt.Printf("OUTSIDE  %s\n", p)
			bad++
		}
	}
	if bad > 0 {
		fmt.Fprintf(os.Stderr, "check-ownership: %d path(s) violate %s's allowlist\n", bad, wp)
		os.Exit(1)
	}
	fmt.Printf("check-ownership: %s ok\n", wp)
}
