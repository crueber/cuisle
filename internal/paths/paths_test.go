package paths_test

// SPEC-NOTE: Resolve is computed on the host, so for GOOS="windows" run on a non-Windows host the separator
// produced by the implementation may be "/" or "\". Tests compare after normalising "\" to "/" (norm()).
// SPEC-NOTE: Resolve returns a non-nil error when it needs the home directory (darwin; linux without XDG vars)
// and Env.Home is empty, and no CUISLE_DATA_DIR override is set. The override does not need Home.
// SPEC-NOTE: An empty-string XDG var is treated as unset (XDG Base Directory spec). Getenv returns "" for unset vars.
// SPEC-NOTE: Windows with LOCALAPPDATA unset is unspecified; the test only requires an error OR a result under Home.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/crueber/cuisle/internal/paths"
)

func norm(s string) string { return strings.ReplaceAll(s, `\`, "/") }

func envOf(goos, home string, vars map[string]string) paths.Env {
	return paths.Env{
		GOOS: goos,
		Home: home,
		Getenv: func(k string) string {
			return vars[k]
		},
	}
}

func TestResolve_PerOS(t *testing.T) {
	tests := []struct {
		name     string
		env      paths.Env
		wantData string
		wantLogs string
	}{
		{
			name:     "darwin",
			env:      envOf("darwin", "/Users/me", nil),
			wantData: "/Users/me/Library/Application Support/Cuisle",
			wantLogs: "/Users/me/Library/Logs/Cuisle",
		},
		{
			name:     "darwin ignores XDG vars",
			env:      envOf("darwin", "/Users/me", map[string]string{"XDG_DATA_HOME": "/x/data", "XDG_STATE_HOME": "/x/state"}),
			wantData: "/Users/me/Library/Application Support/Cuisle",
			wantLogs: "/Users/me/Library/Logs/Cuisle",
		},
		{
			name:     "windows uses LOCALAPPDATA",
			env:      envOf("windows", `C:\Users\me`, map[string]string{"LOCALAPPDATA": `C:\Users\me\AppData\Local`}),
			wantData: "C:/Users/me/AppData/Local/Cuisle",
			wantLogs: "C:/Users/me/AppData/Local/Cuisle/logs",
		},
		{
			name:     "linux XDG unset",
			env:      envOf("linux", "/home/me", nil),
			wantData: "/home/me/.local/share/cuisle",
			wantLogs: "/home/me/.local/state/cuisle",
		},
		{
			name:     "linux XDG empty strings count as unset",
			env:      envOf("linux", "/home/me", map[string]string{"XDG_DATA_HOME": "", "XDG_STATE_HOME": ""}),
			wantData: "/home/me/.local/share/cuisle",
			wantLogs: "/home/me/.local/state/cuisle",
		},
		{
			name:     "linux XDG both set",
			env:      envOf("linux", "/home/me", map[string]string{"XDG_DATA_HOME": "/srv/data", "XDG_STATE_HOME": "/srv/state"}),
			wantData: "/srv/data/cuisle",
			wantLogs: "/srv/state/cuisle",
		},
		{
			name:     "linux only XDG_DATA_HOME set",
			env:      envOf("linux", "/home/me", map[string]string{"XDG_DATA_HOME": "/srv/data"}),
			wantData: "/srv/data/cuisle",
			wantLogs: "/home/me/.local/state/cuisle",
		},
		{
			name:     "linux only XDG_STATE_HOME set",
			env:      envOf("linux", "/home/me", map[string]string{"XDG_STATE_HOME": "/srv/state"}),
			wantData: "/home/me/.local/share/cuisle",
			wantLogs: "/srv/state/cuisle",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := paths.Resolve(tc.env)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if norm(got.Data) != tc.wantData {
				t.Errorf("Data = %q, want %q", got.Data, tc.wantData)
			}
			if norm(got.Logs) != tc.wantLogs {
				t.Errorf("Logs = %q, want %q", got.Logs, tc.wantLogs)
			}
		})
	}
}

func TestResolve_DataDirOverride_AppliesOnEveryOS(t *testing.T) {
	for _, goos := range []string{"darwin", "windows", "linux"} {
		t.Run(goos, func(t *testing.T) {
			env := envOf(goos, "/home/me", map[string]string{
				paths.EnvDataDir: "/tmp/cuisle-test",
				"LOCALAPPDATA":   `C:\ignored`,
				"XDG_DATA_HOME":  "/ignored",
				"XDG_STATE_HOME": "/ignored",
			})
			got, err := paths.Resolve(env)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if norm(got.Data) != "/tmp/cuisle-test" {
				t.Errorf("Data = %q, want /tmp/cuisle-test", got.Data)
			}
			if norm(got.Logs) != "/tmp/cuisle-test/logs" {
				t.Errorf("Logs = %q, want /tmp/cuisle-test/logs", got.Logs)
			}
		})
	}
}

func TestResolve_DataDirOverride_DoesNotNeedHome(t *testing.T) {
	got, err := paths.Resolve(envOf("linux", "", map[string]string{paths.EnvDataDir: "/tmp/x"}))
	if err != nil {
		t.Fatalf("Resolve with override and empty home: %v", err)
	}
	if norm(got.Data) != "/tmp/x" {
		t.Errorf("Data = %q", got.Data)
	}
}

func TestResolve_MissingHome_IsAnError(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			if _, err := paths.Resolve(envOf(goos, "", nil)); err == nil {
				t.Fatal("expected an error when Home is empty and no override or XDG vars are set")
			}
		})
	}
}

func TestResolve_WindowsWithoutLocalAppData_ErrorsOrFallsBackUnderHome(t *testing.T) {
	got, err := paths.Resolve(envOf("windows", `C:\Users\me`, nil))
	if err != nil {
		return
	}
	if !strings.HasPrefix(norm(got.Data), "C:/Users/me") {
		t.Errorf("Data = %q, want a path under the home dir", got.Data)
	}
}

func TestResolve_DoesNotTouchFilesystem(t *testing.T) {
	d := filepath.Join(t.TempDir(), "never", "created")
	got, err := paths.Resolve(envOf(runtime.GOOS, "/home/me", map[string]string{paths.EnvDataDir: d}))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for _, p := range []string{got.Data, got.Logs} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("Resolve must not create %q (stat err = %v)", p, err)
		}
	}
}

func TestPaths_DB_JoinsDataAndDBFile(t *testing.T) {
	if paths.DBFile != "cuisle.db" {
		t.Fatalf("DBFile = %q", paths.DBFile)
	}
	p := paths.Paths{Data: filepath.Join(string(filepath.Separator)+"d", "data"), Logs: "/ignored"}
	want := filepath.Join(p.Data, "cuisle.db")
	if got := p.DB(); got != want {
		t.Errorf("DB() = %q, want %q", got, want)
	}
}

func TestDefault_CreatesDirectoriesUnderOverride(t *testing.T) {
	d := filepath.Join(t.TempDir(), "data")
	t.Setenv(paths.EnvDataDir, d)

	got, err := paths.Default()
	if err != nil {
		t.Fatalf("Default: %v", err)
	}
	if got.Data != d {
		t.Errorf("Data = %q, want %q", got.Data, d)
	}
	if want := filepath.Join(d, "logs"); got.Logs != want {
		t.Errorf("Logs = %q, want %q", got.Logs, want)
	}
	for _, p := range []string{got.Data, got.Logs} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %q: %v", p, err)
		}
		if !fi.IsDir() {
			t.Errorf("%q is not a directory", p)
		}
		if runtime.GOOS != "windows" {
			if perm := fi.Mode().Perm(); perm != 0o700 {
				t.Errorf("%q perm = %o, want 700", p, perm)
			}
		}
	}
	if want := filepath.Join(d, "cuisle.db"); got.DB() != want {
		t.Errorf("DB() = %q, want %q", got.DB(), want)
	}
}

func TestDefault_IsIdempotentAndKeepsExistingFiles(t *testing.T) {
	d := filepath.Join(t.TempDir(), "data")
	t.Setenv(paths.EnvDataDir, d)
	first, err := paths.Default()
	if err != nil {
		t.Fatalf("Default: %v", err)
	}
	keep := filepath.Join(first.Data, "cuisle.db")
	if err := os.WriteFile(keep, []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := paths.Default()
	if err != nil {
		t.Fatalf("second Default: %v", err)
	}
	if second != first {
		t.Errorf("second = %+v, first = %+v", second, first)
	}
	b, err := os.ReadFile(keep)
	if err != nil || string(b) != "precious" {
		t.Errorf("existing file changed: %q, %v", b, err)
	}
}
