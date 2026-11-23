package logx_test

// SPEC-NOTE: Redact's replacement text is unspecified; tests assert only that secret values are gone and that the
// surrounding, non-secret text (key names, other pairs, prose) is preserved.
// SPEC-NOTE: Sensitive key matching in Redact is case-insensitive ("Password=..." is redacted like "password=...").
// SPEC-NOTE: Query/plain style pairs end at "&" or whitespace; JSON style values end at the closing quote.
// SPEC-NOTE: NewRotatingWriter rotates BEFORE a write when current size + len(p) > maxBytes (strictly greater), so a
// file may hold exactly maxBytes. A write is never split across files. A single write larger than maxBytes is still
// written whole. The size of a pre-existing file counts when the writer is (re)opened. Rotation shifts path -> path.1
// -> ... -> path.<keep>; the oldest is dropped, so exactly `keep` rotated files exist at most.
// SPEC-NOTE: RedactingHandler redacts the values of sensitive keys inside groups and in With/WithAttrs attributes
// too, and treats an attribute holding an error as a string (its Error() text passes through Redact), so that
// logging err cannot leak a secret (AGENTS.md section 7).
// SPEC-NOTE: Sensitive-key attributes are replaced by the string "[redacted]" whatever their value kind.
// SPEC-NOTE: LevelFromEnv is exact-match on "debug" (other spellings, including "DEBUG", fall to Info).

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/crueber/cuisle/internal/logx"
)

// ---------------------------------------------------------------- LevelFromEnv

func TestLevelFromEnv(t *testing.T) {
	tests := []struct {
		in   string
		want slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"", slog.LevelInfo},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelInfo},
		{"trace", slog.LevelInfo},
		{"bogus", slog.LevelInfo},
		{"DEBUG", slog.LevelInfo},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("%q", tc.in), func(t *testing.T) {
			if got := logx.LevelFromEnv(tc.in); got != tc.want {
				t.Errorf("LevelFromEnv(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
	if logx.EnvLevel != "CUISLE_LOG" {
		t.Errorf("EnvLevel = %q", logx.EnvLevel)
	}
}

// ---------------------------------------------------------------- Redact

func TestRedact_RemovesSecretsAndKeepsSurroundingText(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		secrets []string // must not survive
		keep    []string // must survive
	}{
		{
			name:    "bearer header",
			in:      "Authorization: Bearer abc123.def456",
			secrets: []string{"abc123.def456"},
			keep:    []string{"Authorization"},
		},
		{
			name:    "basic header",
			in:      "Authorization: Basic dXNlcjpwYXNzd29yZA==",
			secrets: []string{"dXNlcjpwYXNzd29yZA=="},
			keep:    []string{"Authorization"},
		},
		{
			name:    "lower case header and scheme",
			in:      "authorization: bearer lowtoken99",
			secrets: []string{"lowtoken99"},
		},
		{
			name:    "header as JSON field",
			in:      `{"Authorization":"Bearer eyJhbGci.payload.sig","status":200}`,
			secrets: []string{"eyJhbGci.payload.sig"},
			keep:    []string{`"status":200`},
		},
		{
			name:    "header inside a longer dump",
			in:      "GET /rest/api/3/myself HTTP/1.1\nHost: acme.atlassian.net\nAuthorization: Basic Zm9vOmJhcg==\nAccept: application/json",
			secrets: []string{"Zm9vOmJhcg=="},
			keep:    []string{"GET /rest/api/3/myself", "Host: acme.atlassian.net", "Accept: application/json"},
		},
		{
			name:    "json token",
			in:      `{"user":"me","token":"tok-123-xyz","n":1}`,
			secrets: []string{"tok-123-xyz"},
			keep:    []string{`"user":"me"`, `"n":1`},
		},
		{
			name:    "json secret with space after colon",
			in:      `{"secret": "s3cr3t-value", "ok": true}`,
			secrets: []string{"s3cr3t-value"},
			keep:    []string{`"ok": true`},
		},
		{
			name:    "json password",
			in:      `{"password":"hunter2","name":"x"}`,
			secrets: []string{"hunter2"},
			keep:    []string{`"name":"x"`},
		},
		{
			name:    "query token in the middle",
			in:      "https://x.example/y?a=1&token=abc999&b=2",
			secrets: []string{"abc999"},
			keep:    []string{"https://x.example/y?a=1&", "b=2"},
		},
		{
			name:    "query secret at the end",
			in:      "https://x.example/y?a=1&secret=zzz777",
			secrets: []string{"zzz777"},
			keep:    []string{"a=1"},
		},
		{
			name:    "plain password pair ends at whitespace",
			in:      "login password=hunter2 next=word",
			secrets: []string{"hunter2"},
			keep:    []string{"login", "next=word"},
		},
		{
			name:    "capitalised key",
			in:      "Password=Hunter3 other=1",
			secrets: []string{"Hunter3"},
			keep:    []string{"other=1"},
		},
		{
			name:    "atlassian token in prose",
			in:      "jira said ATATT3xFfGF0Qx_1-abc=DEADBEEF is invalid",
			secrets: []string{"ATATT3xFfGF0Qx_1-abc=DEADBEEF"},
			keep:    []string{"jira said", "is invalid"},
		},
		{
			name:    "atlassian token alone",
			in:      "ATATT3xFfGF0000000000000000",
			secrets: []string{"ATATT3xFfGF0000000000000000"},
		},
		{
			name:    "several secrets in one string",
			in:      "Authorization: Bearer first111 token=second222 ATATTthird333 password=fourth444",
			secrets: []string{"first111", "second222", "ATATTthird333", "fourth444"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := logx.Redact(tc.in)
			for _, s := range tc.secrets {
				if strings.Contains(got, s) {
					t.Errorf("Redact(%q) = %q still contains secret %q", tc.in, got, s)
				}
			}
			for _, k := range tc.keep {
				if !strings.Contains(got, k) {
					t.Errorf("Redact(%q) = %q lost non-secret text %q", tc.in, got, k)
				}
			}
		})
	}
}

func TestRedact_LeavesOrdinaryTextAlone(t *testing.T) {
	for _, in := range []string{
		"",
		"sync finished: 12 issues, 0 errors in 450ms",
		"GET /rest/api/3/search/jql status=200 dur=12ms",
		"standup 3 opened board 42 (Kanban)",
		"https://acme.atlassian.net/rest/api/3/search?maxResults=100&fields=summary,status",
		`{"standup_id":3,"mode":"incremental","requests":6}`,
		"ATAT is not a token and neither is ATTAT",
	} {
		t.Run(in, func(t *testing.T) {
			if got := logx.Redact(in); got != in {
				t.Errorf("Redact(%q) = %q, want unchanged", in, got)
			}
		})
	}
}

// ---------------------------------------------------------------- RedactingHandler

// newCapture returns a logger that goes through RedactingHandler over a JSON handler into buf (no time field).
func newCapture(buf *bytes.Buffer, level slog.Level) *slog.Logger {
	inner := slog.NewJSONHandler(buf, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})
	return slog.New(logx.RedactingHandler(inner))
}

func decodeLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &m); err != nil {
		t.Fatalf("output is not one JSON object: %v\n%s", err, buf.String())
	}
	return m
}

func TestRedactingHandler_SensitiveKeysAreReplaced_CaseInsensitive(t *testing.T) {
	for _, key := range []string{
		"token", "Token", "TOKEN", "api_token", "accessToken",
		"secret", "Client_Secret",
		"password", "PassWord", "db_password",
		"authorization", "Authorization", "AUTHORIZATION", "proxy-authorization",
	} {
		t.Run(key, func(t *testing.T) {
			var buf bytes.Buffer
			newCapture(&buf, slog.LevelInfo).Info("msg", key, "the-actual-secret-value")
			m := decodeLine(t, &buf)
			if got := m[key]; got != "[redacted]" {
				t.Errorf("attr %q = %v, want \"[redacted]\" (line: %s)", key, got, buf.String())
			}
			if strings.Contains(buf.String(), "the-actual-secret-value") {
				t.Errorf("secret leaked: %s", buf.String())
			}
		})
	}
}

func TestRedactingHandler_SensitiveKeyWithNonStringValue(t *testing.T) {
	var buf bytes.Buffer
	newCapture(&buf, slog.LevelInfo).Info("msg", "token", 123456789, "password", []byte("hunter2"))
	if strings.Contains(buf.String(), "123456789") || strings.Contains(buf.String(), "hunter2") {
		t.Errorf("secret leaked: %s", buf.String())
	}
	m := decodeLine(t, &buf)
	if m["token"] != "[redacted]" || m["password"] != "[redacted]" {
		t.Errorf("sensitive attrs not replaced: %s", buf.String())
	}
}

func TestRedactingHandler_OrdinaryAttributesPassThrough(t *testing.T) {
	var buf bytes.Buffer
	newCapture(&buf, slog.LevelInfo).Info("sync done", "standup_id", 3, "mode", "incremental", "ok", true, "dur_ms", 450.5)
	m := decodeLine(t, &buf)
	if m["msg"] != "sync done" || m["level"] != "INFO" {
		t.Errorf("msg/level = %v/%v", m["msg"], m["level"])
	}
	if m["standup_id"] != float64(3) || m["mode"] != "incremental" || m["ok"] != true || m["dur_ms"] != 450.5 {
		t.Errorf("attrs changed: %s", buf.String())
	}
}

func TestRedactingHandler_StringValuesAndMessageGoThroughRedact(t *testing.T) {
	var buf bytes.Buffer
	newCapture(&buf, slog.LevelInfo).Info("rejected ATATT3xFfGF0msgsecret", "detail", "calling ?token=qs-secret-1&x=2 failed", "note", "hello")
	out := buf.String()
	for _, s := range []string{"ATATT3xFfGF0msgsecret", "qs-secret-1"} {
		if strings.Contains(out, s) {
			t.Errorf("secret %q leaked: %s", s, out)
		}
	}
	m := decodeLine(t, &buf)
	if m["note"] != "hello" {
		t.Errorf("ordinary string changed: %v", m["note"])
	}
	if d, _ := m["detail"].(string); !strings.Contains(d, "x=2") {
		t.Errorf("non-secret text lost: %q", d)
	}
}

func TestRedactingHandler_ErrorValuesAreRedacted(t *testing.T) {
	var buf bytes.Buffer
	newCapture(&buf, slog.LevelInfo).Error("request failed", "err", errors.New("401 for Authorization: Bearer errsecret123"))
	if strings.Contains(buf.String(), "errsecret123") {
		t.Errorf("secret in error leaked: %s", buf.String())
	}
}

func TestRedactingHandler_WithAttrsAndGroupsAreRedacted(t *testing.T) {
	var buf bytes.Buffer
	l := newCapture(&buf, slog.LevelInfo).With("token", "with-secret-1", "src", "jira")
	l.WithGroup("http").Info("call", "password", "group-secret-2", slog.Group("hdr", "Authorization", "Basic grp-secret-3"), "status", 200)
	out := buf.String()
	for _, s := range []string{"with-secret-1", "group-secret-2", "grp-secret-3"} {
		if strings.Contains(out, s) {
			t.Errorf("secret %q leaked: %s", s, out)
		}
	}
	m := decodeLine(t, &buf)
	if m["src"] != "jira" {
		t.Errorf("With attr src lost: %s", out)
	}
	httpGroup, ok := m["http"].(map[string]any)
	if !ok || httpGroup["status"] != float64(200) {
		t.Errorf("group attrs lost: %s", out)
	}
}

func TestRedactingHandler_EnabledDelegatesToInnerLevel(t *testing.T) {
	var buf bytes.Buffer
	l := newCapture(&buf, slog.LevelInfo)
	if l.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("debug must be disabled when the inner handler is at info")
	}
	if !l.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("info must be enabled")
	}
	l.Debug("hidden")
	if buf.Len() != 0 {
		t.Errorf("debug record was written: %s", buf.String())
	}
	var buf2 bytes.Buffer
	if !newCapture(&buf2, slog.LevelDebug).Enabled(context.Background(), slog.LevelDebug) {
		t.Error("debug must be enabled at debug level")
	}
}

// ---------------------------------------------------------------- NewRotatingWriter

func chunk(n int, id byte) []byte {
	return append(bytes.Repeat([]byte{id}, n-1), '\n')
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func TestRotatingWriter_AppendsWithoutRotatingBelowLimit(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cuisle.log")
	w, err := logx.NewRotatingWriter(p, 100, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []byte("abc") {
		n, err := w.Write(chunk(30, id))
		if err != nil || n != 30 {
			t.Fatalf("Write = %d, %v", n, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	want := string(chunk(30, 'a')) + string(chunk(30, 'b')) + string(chunk(30, 'c'))
	if got := readFile(t, p); got != want {
		t.Errorf("content = %q, want %q", got, want)
	}
	if exists(p + ".1") {
		t.Error("rotated file exists before the limit was exceeded")
	}
}

func TestRotatingWriter_FileMayHoldExactlyMaxBytes_NextWriteRotates(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cuisle.log")
	w, err := logx.NewRotatingWriter(p, 90, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	for _, id := range []byte("abc") {
		if _, err := w.Write(chunk(30, id)); err != nil {
			t.Fatal(err)
		}
	}
	if exists(p + ".1") {
		t.Fatal("rotated at exactly maxBytes; only exceeding it should rotate")
	}
	if _, err := w.Write(chunk(30, 'd')); err != nil {
		t.Fatal(err)
	}
	want1 := string(chunk(30, 'a')) + string(chunk(30, 'b')) + string(chunk(30, 'c'))
	if got := readFile(t, p+".1"); got != want1 {
		t.Errorf(".1 = %q, want %q", got, want1)
	}
	if got := readFile(t, p); got != string(chunk(30, 'd')) {
		t.Errorf("current = %q", got)
	}
}

func TestRotatingWriter_KeepsExactlyKeepRotatedFiles_DropsOldest(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cuisle.log")
	w, err := logx.NewRotatingWriter(p, 100, 2)
	if err != nil {
		t.Fatal(err)
	}
	// 30-byte writes, 3 per file: writes 1-3 | 4-6 | 7-9 | 10.
	for i := 0; i < 10; i++ {
		if _, err := w.Write(chunk(30, byte('a'+i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	three := func(a, b, c byte) string { return string(chunk(30, a)) + string(chunk(30, b)) + string(chunk(30, c)) }
	if got, want := readFile(t, p), string(chunk(30, 'j')); got != want {
		t.Errorf("current = %q, want %q", got, want)
	}
	if got, want := readFile(t, p+".1"), three('g', 'h', 'i'); got != want {
		t.Errorf(".1 = %q, want %q", got, want)
	}
	if got, want := readFile(t, p+".2"), three('d', 'e', 'f'); got != want {
		t.Errorf(".2 = %q, want %q", got, want)
	}
	if exists(p + ".3") {
		t.Error(".3 exists but keep is 2")
	}
	// Nothing else in the directory.
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 3 {
		t.Errorf("directory has %d entries, want 3", len(entries))
	}
}

func TestRotatingWriter_KeepOne(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cuisle.log")
	w, err := logx.NewRotatingWriter(p, 50, 1)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ { // 30 bytes each, one per file
		if _, err := w.Write(chunk(30, byte('a'+i))); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()
	if got := readFile(t, p); got != string(chunk(30, 'e')) {
		t.Errorf("current = %q", got)
	}
	if got := readFile(t, p+".1"); got != string(chunk(30, 'd')) {
		t.Errorf(".1 = %q", got)
	}
	if exists(p + ".2") {
		t.Error(".2 exists but keep is 1")
	}
}

func TestRotatingWriter_ExistingFileSizeCountsOnReopen(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cuisle.log")
	w, err := logx.NewRotatingWriter(p, 100, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(chunk(60, 'a')); err != nil {
		t.Fatal(err)
	}
	w.Close()

	w2, err := logx.NewRotatingWriter(p, 100, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w2.Write(chunk(60, 'b')); err != nil {
		t.Fatal(err)
	}
	w2.Close()
	if got := readFile(t, p+".1"); got != string(chunk(60, 'a')) {
		t.Errorf(".1 = %q, want the first run's data", got)
	}
	if got := readFile(t, p); got != string(chunk(60, 'b')) {
		t.Errorf("current = %q", got)
	}
}

func TestRotatingWriter_ReopenAppendsToExistingFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cuisle.log")
	for _, id := range []byte("ab") {
		w, err := logx.NewRotatingWriter(p, 1000, 2)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(chunk(10, id)); err != nil {
			t.Fatal(err)
		}
		w.Close()
	}
	if got, want := readFile(t, p), string(chunk(10, 'a'))+string(chunk(10, 'b')); got != want {
		t.Errorf("content = %q, want %q", got, want)
	}
}

func TestRotatingWriter_OversizedWriteIsNotSplitOrDropped(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cuisle.log")
	w, err := logx.NewRotatingWriter(p, 100, 2)
	if err != nil {
		t.Fatal(err)
	}
	big := chunk(250, 'x')
	n, err := w.Write(big)
	if err != nil || n != len(big) {
		t.Fatalf("Write = %d, %v", n, err)
	}
	w.Close()
	if got := readFile(t, p); got != string(big) {
		t.Errorf("current file lost or split the oversized write: len %d", len(got))
	}
}

func TestRotatingWriter_ConcurrentWritesKeepLinesWhole(t *testing.T) {
	p := filepath.Join(t.TempDir(), "cuisle.log")
	w, err := logx.NewRotatingWriter(p, 500, 3)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(id byte) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if _, err := w.Write(chunk(20, id)); err != nil {
					t.Errorf("Write: %v", err)
					return
				}
			}
		}('a' + byte(g))
	}
	wg.Wait()
	w.Close()
	for _, f := range []string{p, p + ".1", p + ".2", p + ".3"} {
		if !exists(f) {
			continue
		}
		for _, line := range strings.Split(strings.TrimSuffix(readFile(t, f), "\n"), "\n") {
			if len(line) != 19 || strings.Trim(line, line[:1]) != "" {
				t.Errorf("%s has a torn line %q", filepath.Base(f), line)
			}
		}
	}
	if exists(p + ".4") {
		t.Error(".4 exists but keep is 3")
	}
}

// ---------------------------------------------------------------- New (end to end)

func readLines(t *testing.T, p string) []map[string]any {
	t.Helper()
	f, err := os.Open(p)
	if err != nil {
		t.Fatalf("open %s: %v", p, err)
	}
	defer f.Close()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("log line is not JSON: %v\n%s", err, sc.Text())
		}
		out = append(out, m)
	}
	return out
}

func TestNew_WritesJSONLinesToCuisleLog(t *testing.T) {
	dir := t.TempDir()
	l, c, err := logx.New(logx.Options{Dir: dir, Level: slog.LevelInfo})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	l.Info("sync finished", "standup_id", 3, "mode", "incremental")
	l.Warn("slow", "dur_ms", 1200)
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	lines := readLines(t, filepath.Join(dir, "cuisle.log"))
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	if lines[0]["msg"] != "sync finished" || lines[0]["level"] != "INFO" || lines[0]["standup_id"] != float64(3) || lines[0]["mode"] != "incremental" {
		t.Errorf("line 0 = %v", lines[0])
	}
	if _, ok := lines[0]["time"]; !ok {
		t.Error("line 0 has no time field")
	}
	if lines[1]["msg"] != "slow" || lines[1]["level"] != "WARN" {
		t.Errorf("line 1 = %v", lines[1])
	}
}

func TestNew_RedactionTest_NoSecretReachesTheLogFile(t *testing.T) {
	dir := t.TempDir()
	l, c, err := logx.New(logx.Options{Dir: dir, Level: slog.LevelDebug})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	const (
		apiToken = "ATATT3xFfGF0RealLookingAtlassianToken_123=ABCDEF01"
		bearer   = "bearer-secret-xyz"
		basic    = "dXNlcjpzdXBlcnNlY3JldA=="
		pw       = "hunter2-pw"
		qs       = "qs-secret-777"
		kc       = "keychain-secret-555"
	)
	l.Info("connecting with "+apiToken, "token", apiToken)
	l.Debug("request", "Authorization", "Bearer "+bearer, "headers", "Authorization: Basic "+basic)
	l.Error("login failed", "password", pw, "url", "https://acme.atlassian.net/x?token="+qs+"&ok=1", "err", errors.New("secret="+kc))
	l.With("api_token", apiToken).Info("with attrs")
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	raw := readFile(t, filepath.Join(dir, "cuisle.log"))
	for _, s := range []string{apiToken, "RealLookingAtlassianToken", bearer, basic, pw, qs, kc} {
		if strings.Contains(raw, s) {
			t.Errorf("secret %q found in cuisle.log:\n%s", s, raw)
		}
	}
	if !strings.Contains(raw, "login failed") || !strings.Contains(raw, "ok=1") {
		t.Errorf("non-secret content missing from log:\n%s", raw)
	}
}

func TestNew_LevelFiltersDebugAtInfo(t *testing.T) {
	for _, tc := range []struct {
		level     slog.Level
		wantDebug bool
	}{{slog.LevelInfo, false}, {slog.LevelDebug, true}} {
		t.Run(tc.level.String(), func(t *testing.T) {
			dir := t.TempDir()
			l, c, err := logx.New(logx.Options{Dir: dir, Level: tc.level})
			if err != nil {
				t.Fatal(err)
			}
			l.Debug("dbg-line")
			l.Info("info-line")
			c.Close()
			raw := readFile(t, filepath.Join(dir, "cuisle.log"))
			if got := strings.Contains(raw, "dbg-line"); got != tc.wantDebug {
				t.Errorf("debug present = %v, want %v", got, tc.wantDebug)
			}
			if !strings.Contains(raw, "info-line") {
				t.Error("info line missing")
			}
		})
	}
}

func TestNew_RotatesUsingOptions(t *testing.T) {
	dir := t.TempDir()
	l, c, err := logx.New(logx.Options{Dir: dir, Level: slog.LevelInfo, MaxBytes: 400, MaxFiles: 2})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		l.Info("filler line to force rotation", "i", i)
	}
	c.Close()
	base := filepath.Join(dir, "cuisle.log")
	for _, f := range []string{base, base + ".1", base + ".2"} {
		if !exists(f) {
			t.Errorf("%s missing", filepath.Base(f))
		}
	}
	if exists(base + ".3") {
		t.Error("cuisle.log.3 exists but MaxFiles is 2")
	}
}

func TestNew_ZeroOptionsUseDefaultsAndCloserIsUsable(t *testing.T) {
	dir := t.TempDir()
	l, c, err := logx.New(logx.Options{Dir: dir})
	if err != nil {
		t.Fatalf("New with zero MaxBytes/MaxFiles: %v", err)
	}
	var _ io.Closer = c
	l.Info("hello")
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !strings.Contains(readFile(t, filepath.Join(dir, "cuisle.log")), "hello") {
		t.Error("record not flushed to cuisle.log")
	}
	// Defaults are 5 MB x 3: a few hundred bytes must never rotate.
	if exists(filepath.Join(dir, "cuisle.log.1")) {
		t.Error("rotated with default limits after one line")
	}
}
