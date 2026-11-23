package file_test

// SPEC-NOTE (WP-21 file sink):
//  1. Validate rejects a filename that is absolute or whose cleaned form escapes the directory ("../x", "a/../../x")
//     with a validation error, Field "filename". Validate does not require dir to exist. A blank/missing dir is a
//     validation error with Field "dir"; overwrite must be "true" or "false" (Field "overwrite") when present.
//  2. Deliver never writes outside dir whatever the config says (Deliver may be handed an unvalidated config, and
//     {standupKey} comes from data): it must either return an error or write a file that lives directly in dir. These
//     tests accept both but forbid escape. For overwrite=false the suffix goes before the extension: name.json,
//     name-2.json, name-3.json. A missing/empty "filename" uses the schema default; a missing "overwrite" means true.
//  3. Deliver creates dir if it is missing is NOT asserted. Receipt.Ref is absolute and refers to the written file
//     (compared with symlinks resolved, because macOS temp dirs live behind a symlink).
//  4. Capabilities: Current=true, Formats=["json"], NeedsSecret=false. Idempotent and Range are not asserted.

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/export"
	"github.com/crueber/cuisle/internal/sink"
	"github.com/crueber/cuisle/internal/sink/file"
)

const defaultName = "cuisle-{standupKey}-{date}.json"

func delivery(raw string) sink.Delivery {
	return sink.Delivery{
		Key:        "k",
		Attempt:    1,
		Raw:        []byte(raw),
		Document:   export.Document{Schema: export.Schema},
		StandupKey: "MOB",
		Date:       "2026-11-23",
	}
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

func mustRead(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func realPath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", p, err)
	}
	return r
}

func TestKindAndMetadata(t *testing.T) {
	s := file.New()
	if s.Kind() != "file" || file.Kind != "file" {
		t.Errorf("Kind = %q / %q", s.Kind(), file.Kind)
	}
	if s.DisplayName() == "" {
		t.Error("DisplayName is empty")
	}
	c := s.Capabilities()
	if !c.Current || c.NeedsSecret || !reflect.DeepEqual(c.Formats, []string{"json"}) {
		t.Errorf("Capabilities = %+v", c)
	}
}

func TestConfigSchema_FieldsAndDefaults(t *testing.T) {
	f := file.New().ConfigSchema()
	if len(f) != 3 {
		t.Fatalf("fields = %+v, want dir, filename, overwrite", f)
	}
	by := map[string]sink.Field{}
	var keys []string
	for _, x := range f {
		by[x.Key] = x
		keys = append(keys, x.Key)
		if x.Label == "" {
			t.Errorf("field %q has no label", x.Key)
		}
	}
	if !reflect.DeepEqual(keys, []string{"dir", "filename", "overwrite"}) {
		t.Errorf("field order = %v", keys)
	}
	if d := by["dir"]; d.Type != "path" || !d.Required {
		t.Errorf("dir = %+v, want required path", d)
	}
	if fn := by["filename"]; fn.Type != "text" || fn.Required || fn.Default != defaultName {
		t.Errorf("filename = %+v, want optional text defaulting to %q", fn, defaultName)
	}
	if o := by["overwrite"]; o.Type != "bool" || o.Required || o.Default != "true" {
		t.Errorf("overwrite = %+v, want bool default true", o)
	}
}

func TestValidate_Table(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name      string
		cfg       map[string]string
		wantField string // "" = valid
	}{
		{"dir only", map[string]string{"dir": dir}, ""},
		{"all fields", map[string]string{"dir": dir, "filename": "standup-{date}.json", "overwrite": "false"}, ""},
		{"dir not yet existing", map[string]string{"dir": filepath.Join(dir, "later")}, ""},
		{"missing dir", map[string]string{}, "dir"},
		{"blank dir", map[string]string{"dir": "   "}, "dir"},
		{"bad overwrite", map[string]string{"dir": dir, "overwrite": "maybe"}, "overwrite"},
		{"filename parent traversal", map[string]string{"dir": dir, "filename": "../evil.json"}, "filename"},
		{"filename nested traversal", map[string]string{"dir": dir, "filename": "a/../../evil.json"}, "filename"},
		{"filename absolute", map[string]string{"dir": dir, "filename": filepath.Join(dir, "abs.json")}, "filename"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := file.New().Validate(tc.cfg)
			if tc.wantField == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !apperr.Is(err, apperr.Validation) {
				t.Fatalf("err = %v (code %q), want validation", err, apperr.CodeOf(err))
			}
			if f := apperr.From(err).Field; f != tc.wantField {
				t.Errorf("Field = %q, want %q", f, tc.wantField)
			}
		})
	}
}

func TestDeliver_WritesRawIntoDirWithDefaultFilename(t *testing.T) {
	dir := t.TempDir()
	const raw = "{\n  \"schema\": \"cuisle.export.v1\"\n}\n"
	rc, err := file.New().Deliver(context.Background(), withCfg(delivery(raw), map[string]string{"dir": dir}))
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	want := "cuisle-MOB-2026-11-23.json"
	if got := names(t, dir); !reflect.DeepEqual(got, []string{want}) {
		t.Fatalf("dir = %v, want only %s (no temp files left)", got, want)
	}
	if got := mustRead(t, filepath.Join(dir, want)); got != raw {
		t.Errorf("content = %q, want the frozen Raw bytes", got)
	}
	if !filepath.IsAbs(rc.Ref) {
		t.Errorf("Receipt.Ref = %q, want an absolute path", rc.Ref)
	}
	if got, w := realPath(t, rc.Ref), realPath(t, filepath.Join(dir, want)); got != w {
		t.Errorf("Receipt.Ref resolves to %q, want %q", got, w)
	}
}

func TestDeliver_TemplatedFilename(t *testing.T) {
	dir := t.TempDir()
	cfg := map[string]string{"dir": dir, "filename": "{standupKey}_on_{date}.json"}
	if _, err := file.New().Deliver(context.Background(), withCfg(delivery("{}"), cfg)); err != nil {
		t.Fatal(err)
	}
	if got := names(t, dir); !reflect.DeepEqual(got, []string{"MOB_on_2026-11-23.json"}) {
		t.Errorf("dir = %v", got)
	}
}

func TestDeliver_EmptyFilenameUsesDefault(t *testing.T) {
	dir := t.TempDir()
	cfg := map[string]string{"dir": dir, "filename": ""}
	if _, err := file.New().Deliver(context.Background(), withCfg(delivery("{}"), cfg)); err != nil {
		t.Fatal(err)
	}
	if got := names(t, dir); !reflect.DeepEqual(got, []string{"cuisle-MOB-2026-11-23.json"}) {
		t.Errorf("dir = %v", got)
	}
}

func TestDeliver_OverwriteDefaultTrue_ReplacesContent(t *testing.T) {
	dir := t.TempDir()
	s := file.New()
	cfg := map[string]string{"dir": dir}
	if _, err := s.Deliver(context.Background(), withCfg(delivery("first"), cfg)); err != nil {
		t.Fatal(err)
	}
	rc, err := s.Deliver(context.Background(), withCfg(delivery("second"), cfg))
	if err != nil {
		t.Fatal(err)
	}
	if got := names(t, dir); !reflect.DeepEqual(got, []string{"cuisle-MOB-2026-11-23.json"}) {
		t.Fatalf("dir = %v, want a single file", got)
	}
	if got := mustRead(t, rc.Ref); got != "second" {
		t.Errorf("content = %q, want second", got)
	}
}

func TestDeliver_OverwriteFalse_AddsNumericSuffixBeforeExtension(t *testing.T) {
	dir := t.TempDir()
	s := file.New()
	cfg := map[string]string{"dir": dir, "overwrite": "false"}
	var refs []string
	for _, body := range []string{"one", "two", "three"} {
		rc, err := s.Deliver(context.Background(), withCfg(delivery(body), cfg))
		if err != nil {
			t.Fatal(err)
		}
		refs = append(refs, rc.Ref)
	}
	want := []string{"cuisle-MOB-2026-11-23-2.json", "cuisle-MOB-2026-11-23-3.json", "cuisle-MOB-2026-11-23.json"}
	if got := names(t, dir); !reflect.DeepEqual(got, want) {
		t.Fatalf("dir = %v, want %v", got, want)
	}
	for i, w := range []string{"cuisle-MOB-2026-11-23.json", "cuisle-MOB-2026-11-23-2.json", "cuisle-MOB-2026-11-23-3.json"} {
		if filepath.Base(refs[i]) != w {
			t.Errorf("ref %d = %q, want base %q", i, refs[i], w)
		}
	}
	if got := mustRead(t, filepath.Join(dir, "cuisle-MOB-2026-11-23.json")); got != "one" {
		t.Errorf("original file was modified: %q", got)
	}
	if got := mustRead(t, filepath.Join(dir, "cuisle-MOB-2026-11-23-3.json")); got != "three" {
		t.Errorf("third file content = %q", got)
	}
}

func TestDeliver_OverwriteExplicitTrue_ReplacesContent(t *testing.T) {
	dir := t.TempDir()
	s := file.New()
	cfg := map[string]string{"dir": dir, "overwrite": "true"}
	for _, body := range []string{"a", "b"} {
		if _, err := s.Deliver(context.Background(), withCfg(delivery(body), cfg)); err != nil {
			t.Fatal(err)
		}
	}
	got := names(t, dir)
	if len(got) != 1 || mustRead(t, filepath.Join(dir, got[0])) != "b" {
		t.Errorf("dir = %v", got)
	}
}

func TestDeliver_NeverEscapesDir(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "out")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(parent, "abs-escape.json")
	cases := []struct {
		name   string
		cfgFn  string // filename config ("" = default)
		keyOvr string // StandupKey override
	}{
		{"parent traversal", "../escape.json", ""},
		{"nested traversal", "a/../../escape.json", ""},
		{"absolute path", abs, ""},
		{"traversal in standup key", "", "../../escape"},
		{"separator in standup key", "", "x/y"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := delivery("payload")
			if tc.keyOvr != "" {
				d.StandupKey = tc.keyOvr
			}
			cfg := map[string]string{"dir": dir}
			if tc.cfgFn != "" {
				cfg["filename"] = tc.cfgFn
			}
			rc, err := file.New().Deliver(context.Background(), withCfg(d, cfg))

			// Nothing new may appear next to dir, and dir must hold only regular files directly inside it.
			if got := names(t, parent); !reflect.DeepEqual(got, []string{"out"}) {
				t.Fatalf("parent dir contains %v (escape or leftover); err=%v", got, err)
			}
			ents, rerr := os.ReadDir(dir)
			if rerr != nil {
				t.Fatal(rerr)
			}
			for _, e := range ents {
				if e.IsDir() {
					t.Errorf("unexpected sub-directory %q created in dir", e.Name())
				}
				if strings.HasSuffix(e.Name(), ".tmp") || strings.Contains(e.Name(), "tmp") {
					t.Errorf("temp file left behind: %q", e.Name())
				}
			}
			if err != nil {
				return // rejecting is acceptable
			}
			if len(ents) != 1 {
				t.Fatalf("success must write exactly one file in dir, got %d", len(ents))
			}
			if filepath.Dir(realPath(t, rc.Ref)) != realPath(t, dir) {
				t.Errorf("Receipt.Ref %q is not directly inside %q", rc.Ref, dir)
			}
			if _, err := os.Stat(abs); err == nil {
				t.Error("absolute filename was honoured")
			}
		})
	}
}

func TestDeliver_TargetIsDirectory_ErrorsAndLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "cuisle-MOB-2026-11-23.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := file.New().Deliver(context.Background(), withCfg(delivery("x"), map[string]string{"dir": dir}))
	if err == nil {
		t.Fatal("expected an error when the target name is a directory")
	}
	if got := names(t, dir); !reflect.DeepEqual(got, []string{"cuisle-MOB-2026-11-23.json"}) {
		t.Errorf("dir = %v, want only the pre-existing directory (temp file must be cleaned up)", got)
	}
}

func TestDeliver_MissingDir_Errors(t *testing.T) {
	_, err := file.New().Deliver(context.Background(), withCfg(delivery("x"), map[string]string{}))
	if err == nil {
		t.Fatal("expected an error when dir is not configured")
	}
}

func withCfg(d sink.Delivery, cfg map[string]string) sink.Delivery {
	d.Config = cfg
	return d
}
