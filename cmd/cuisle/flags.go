package main

import (
	"flag"
	"os"
	"strings"
)

// fakeFlag is --fake-jira[=path]: a bare --fake-jira selects the embedded fixture ("default"), --fake-jira=path a
// fixture file. It is a bool-style flag so the bare form parses.
type fakeFlag struct{ spec string }

func (f *fakeFlag) String() string { return f.spec }

func (f *fakeFlag) IsBoolFlag() bool { return true }

func (f *fakeFlag) Set(v string) error {
	switch strings.ToLower(v) {
	case "", "true", "1":
		f.spec = fakeDefault
	case "false", "0":
		f.spec = ""
	default:
		f.spec = v
	}
	return nil
}

// fakeDefault selects the embedded fixture (jiratest.DefaultFixture).
const fakeDefault = "default"

type options struct {
	fakeJira     string // "" = real Jira; fakeDefault or a fixture path
	benchStartup bool   // WP-35 implements the measurement
}

// parseOptions reads the flags and CUISLE_FAKE_JIRA (which `task dev` sets); a flag wins over the environment.
func parseOptions(args []string, getenv func(string) string) (options, error) {
	fs := flag.NewFlagSet("cuisle", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var fake fakeFlag
	fs.Var(&fake, "fake-jira", "run against the built-in fake Jira (optional fixture path; bare = embedded fixture)")
	bench := fs.Bool("bench-startup", false, "measure cold start, print JSON, exit")
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	opts := options{fakeJira: fake.spec, benchStartup: *bench}
	if opts.fakeJira == "" {
		if v := getenv("CUISLE_FAKE_JIRA"); v != "" {
			var env fakeFlag
			_ = env.Set(v)
			opts.fakeJira = env.spec
		}
	}
	return opts, nil
}
