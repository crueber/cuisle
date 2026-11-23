package app_test

// Orchestrator-owned bridge test (jira-sync.md §6, testing.md §3): the MOB fixture synced through the real stack
// (fake Jira → jira adapter → sync → cache → view → app) must produce exactly testdata/view_mob.golden.json, and the
// Bootstrap that /boot.js serves must match testdata/bootstrap_mob.golden.json. These are the payloads the frontend
// renders, so they are also replayed in Playwright. Regenerate deliberately with:
//
//	go test -tags purego ./internal/app -run TestGolden -update-golden
//
// and review the diff. Lists and maps must never be null (architecture §3.2).

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crueber/cuisle/internal/app"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite internal/app/testdata/*.golden.json")

// nullable lists the JSON keys whose value may legitimately be null (pointer fields in the view models).
var nullable = map[string]bool{
	"sprint": true, "view": true, "window": true, "lastDelivery": true, "space": true, "board": true,
	"filterCheck": true,
}

func TestGolden_SVCViewAndBootstrap(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()

	v := ta.view(id)
	checkGolden(t, "view_mob.golden.json", v, ta.Fake.URL)

	b := ta.bootstrap()
	// The toolchain and platform the test runs on are not part of the contract (CI is Linux, development is macOS).
	b.Build.GoVersion, b.Build.OS, b.Build.Arch = "go", "any", "any"
	checkGolden(t, "bootstrap_mob.golden.json", b, ta.Fake.URL)
}

// checkGolden compares v's indented JSON with testdata/name. fakeURL (random port per run) is replaced by a stable
// placeholder so the golden is deterministic.
func checkGolden(t *testing.T, name string, v any, fakeURL string) {
	t.Helper()
	got, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", name, err)
	}
	got = bytes.ReplaceAll(got, []byte(fakeURL), []byte("https://fake-jira.test"))
	got = append(got, '\n')

	var tree any
	if err := json.Unmarshal(got, &tree); err != nil {
		t.Fatalf("re-decode %s: %v", name, err)
	}
	if bad := findNulls(tree, ""); len(bad) > 0 {
		t.Errorf("%s has null values outside pointer fields (lists/maps must be empty, not null): %s", name, strings.Join(bad, ", "))
	}

	path := filepath.Join("testdata", name)
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden %s missing; run go test -tags purego ./internal/app -run TestGolden -update-golden", path)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from %s; rerun with -update-golden and review the diff", name, path)
	}
}

func findNulls(v any, path string) []string {
	var out []string
	switch x := v.(type) {
	case map[string]any:
		for k, c := range x {
			p := path + "." + k
			if c == nil {
				if !nullable[k] {
					out = append(out, p)
				}
				continue
			}
			out = append(out, findNulls(c, p)...)
		}
	case []any:
		for i, c := range x {
			if c == nil {
				out = append(out, path+"["+itoa(i)+"]")
				continue
			}
			out = append(out, findNulls(c, path+"["+itoa(i)+"]")...)
		}
	}
	return out
}

func itoa(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}

// TestNoNullLists_AllBoundReturns walks every other bound method's JSON result for null lists/maps: the frontend
// strips `| null` only at the type level, so a nil nested slice would crash a Configure section at runtime.
func TestNoNullLists_AllBoundReturns(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	sc := ta.saveFileSink(id, "manual")
	if _, err := ta.Svc.Sink.Send(ctx, sc.ID, id, app.SendRequest{Kind: "current"}); err != nil {
		t.Fatalf("Sink.Send: %v", err)
	}
	check := func(name string, v any, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		b, mErr := json.Marshal(v)
		if mErr != nil {
			t.Fatalf("%s marshal: %v", name, mErr)
		}
		var tree any
		_ = json.Unmarshal(b, &tree)
		if bad := findNulls(tree, name); len(bad) > 0 {
			t.Errorf("%s returns null lists/maps: %s", name, strings.Join(bad, ", "))
		}
	}
	cfg, err := ta.Svc.Config.Get(ctx, id)
	check("Config.Get", cfg, err)
	tabs, err := ta.Svc.Standup.List(ctx)
	check("Standup.List", tabs, err)
	kinds, err := ta.Svc.Sink.Kinds(ctx)
	check("Sink.Kinds", kinds, err)
	sinks, err := ta.Svc.Sink.List(ctx)
	check("Sink.List", sinks, err)
	ob, err := ta.Svc.Sink.Outbox(ctx, sc.ID, 10)
	check("Sink.Outbox", ob, err)
	sp, err := ta.Svc.Connection.SearchSpaces(ctx, "")
	check("Connection.SearchSpaces", sp, err)
	bo, err := ta.Svc.Connection.SearchBoards(ctx, ta.Fx.scrumProject.Key, "")
	check("Connection.SearchBoards", bo, err)
	us, err := ta.Svc.Connection.SearchUsers(ctx, id, "")
	check("Connection.SearchUsers", us, err)
	fc, err := ta.Svc.Connection.ValidateFilter(ctx, id, "labels = standup")
	check("Connection.ValidateFilter", fc, err)
	st, err := ta.Svc.Sync.Status(ctx)
	check("Sync.Status", st, err)
	dg, err := ta.Svc.App.Diagnostics(ctx)
	check("App.Diagnostics", dg, err)
	conn, err := ta.Svc.Connection.Connection(ctx)
	check("Connection.Connection", conn, err)
	draft, err := ta.Svc.Config.Create(ctx)
	check("Config.Create (draft)", draft, err)
}
