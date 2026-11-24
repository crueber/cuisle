package update

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testSHA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestNSISScript_WaitsChecksTheHashRunsSilentlyRelaunchesAndQuotes(t *testing.T) {
	inst := `C:\Users\O’Brien\AppData\Local\Temp\cuisle-update-1\Cuisle-v1.3.0-windows-amd64-setup.exe`
	got := nsisScript(99, inst, strings.ToUpper(testSHA), `C:\Program Files\crueber\Cuisle\cuisle.exe`)
	quoted := `'C:\Users\O’’Brien\AppData\Local\Temp\cuisle-update-1\Cuisle-v1.3.0-windows-amd64-setup.exe'`
	for _, want := range []string{
		"Wait-Process -Id 99 -Timeout 60",
		"$h = (Get-FileHash -LiteralPath " + quoted + " -Algorithm SHA256).Hash",
		"if ($h -and $h.ToLower() -eq '" + testSHA + "') { Start-Process -FilePath " + quoted + " -ArgumentList '/S' -Verb RunAs -Wait }",
		`Start-Process -FilePath 'C:\Program Files\crueber\Cuisle\cuisle.exe'`,
		"Remove-Item -LiteralPath",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("script lacks %q:\n%s", want, got)
		}
	}
	if !(strings.Index(got, "Wait-Process") < strings.Index(got, "Get-FileHash") && strings.Index(got, "Get-FileHash") < strings.Index(got, "/S")) {
		t.Error("order must be: wait for the app, check the hash, then run the installer")
	}
}

func TestPSQuote_AllSingleQuoteForms(t *testing.T) {
	for in, want := range map[string]string{
		`a'b`:      `'a''b'`,
		"a\u2018b": "'a\u2018\u2018b'",
		"a\u2019b": "'a\u2019\u2019b'",
		"a\u201Ab": "'a\u201A\u201Ab'",
		"a\u201Bb": "'a\u201B\u201Bb'",
		`C:\plain`: `'C:\plain'`,
		`"double"`: `'"double"'`,
	} {
		if got := psQuote(in); got != want {
			t.Errorf("psQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSystemNSIS_UsesTheAbsolutePowerShellAndNeedsAHash(t *testing.T) {
	if got := PowerShellPath(func(k string) string { return map[string]string{"SystemRoot": `D:\WINDOWS\`}[k] }); got != `D:\WINDOWS\System32\WindowsPowerShell\v1.0\powershell.exe` {
		t.Fatalf("path %q", got)
	}
	if got := PowerShellPath(func(string) string { return "" }); got != `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe` {
		t.Fatalf("default %q", got)
	}
	run := &fakeRunner{}
	s := &System{GOOS: "windows", Exe: `C:\x\cuisle.exe`, PID: 5, Run: run, PowerShell: `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`}
	if err := s.Install(context.Background(), KindNSIS, `C:\t\setup.exe`, testSHA); err != nil {
		t.Fatal(err)
	}
	if len(run.starts) != 1 || run.starts[0][0] != s.PowerShell || len(run.runs) != 0 {
		t.Fatalf("starts %v runs %v", run.starts, run.runs)
	}
	if err := s.Install(context.Background(), KindNSIS, `C:\t\setup.exe`, ""); err == nil {
		t.Fatal("ran an installer without a checksum")
	}
}

func TestRelaunchHelpersWaitAtMostAMinute(t *testing.T) {
	if !strings.Contains(relaunchWait, `-lt 300`) || !strings.Contains(relaunchWait, "sleep 0.2") {
		t.Fatalf("unbounded wait: %s", relaunchWait)
	}
}

// When the new app can't move in and the old one can't be moved back, the old one is left in the staging folder
// (not deleted) and the error says where.
func TestSystemDMG_FailedRestoreKeepsThePreviousApp(t *testing.T) {
	apps := t.TempDir()
	exe := makeBundle(t, apps, "Cuisle.app")
	run := &fakeRunner{dmgApp: "cuisle.app"}
	s := &System{GOOS: "darwin", Exe: exe, PID: 1, Run: run}
	calls := 0
	rename = func(from, to string) error {
		calls++
		if calls >= 2 { // the new app moving in, and the old one moving back
			return errors.New("EIO")
		}
		return os.Rename(from, to)
	}
	t.Cleanup(func() { rename = os.Rename })
	err := s.Install(context.Background(), KindDMG, filepath.Join(t.TempDir(), "x.dmg"), testSHA)
	if !errors.Is(err, ErrRestoreFailed) || !strings.Contains(err.Error(), "previous.app") {
		t.Fatalf("err %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(apps, ".cuisle-update-*", "previous.app", "Contents", "MacOS", "cuisle"))
	if len(matches) != 1 {
		t.Fatal("the previous app was deleted")
	}
	if b, _ := os.ReadFile(matches[0]); string(b) != "old" {
		t.Fatal("previous app damaged")
	}
	if len(run.starts) != 0 {
		t.Fatal("relaunched")
	}
}

func TestAppImagePath_ResolvesSymlinks(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "Cuisle-x86_64.AppImage")
	_ = os.WriteFile(real, []byte("x"), 0o755)
	link := filepath.Join(dir, "cuisle")
	if err := os.Symlink(real, link); err != nil {
		t.Skip(err)
	}
	want, _ := filepath.EvalSymlinks(real)
	if got := AppImagePath(func(string) string { return link }); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if AppImagePath(func(string) string { return "relative/x.AppImage" }) != "" {
		t.Fatal("relative path accepted")
	}
}
