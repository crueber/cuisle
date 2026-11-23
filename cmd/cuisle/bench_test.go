package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func fixedMem() memStats { return memStats{HeapAlloc: 12 << 20, HeapSys: 20 << 20} }

func TestBenchProbeFinish(t *testing.T) {
	loadOK := func(_ context.Context, n int) (int, int, error) { return n, 5, nil }
	tests := []struct {
		name     string
		report   bool // the frontend reports first-view
		load     viewLoader
		timeout  time.Duration
		wantCode int
		wantErr  string
	}{
		{"first view arrives, five views load", true, loadOK, time.Second, 0, ""},
		{"first view never arrives", false, loadOK, 20 * time.Millisecond, 1, "did not report first-view"},
		{"loading a view fails", true, func(context.Context, int) (int, int, error) { return 2, 5, errors.New("boom") }, time.Second, 1, "loading views: boom"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := newBenchProbe(time.Now().Add(-100 * time.Millisecond))
			p.Mark("db_ready")
			p.Mark("window_shown")
			if tc.report {
				p.Sink(map[string]float64{"other": 1}) // not the first-view mark: ignored
				p.Sink(map[string]float64{"first-view": 241})
				p.Sink(map[string]float64{"first-view": 999}) // a second report does not panic or change the first
			}
			rep, code := p.finish(context.Background(), tc.timeout, tc.load, fixedMem, true)
			if code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d (report %+v)", code, tc.wantCode, rep)
			}
			if tc.wantErr == "" && rep.Error != "" {
				t.Fatalf("unexpected error %q", rep.Error)
			}
			if tc.wantErr != "" && !bytes.Contains([]byte(rep.Error), []byte(tc.wantErr)) {
				t.Fatalf("error = %q, want it to contain %q", rep.Error, tc.wantErr)
			}
			if rep.DBReadyMs < 100 || rep.WindowShownMs < 100 {
				t.Errorf("marks are not measured from the given process start: db_ready=%v window_shown=%v", rep.DBReadyMs, rep.WindowShownMs)
			}
			if code != 0 {
				return
			}
			if rep.JSFirstViewMs != 241 || rep.FirstViewMs < 100 || rep.ViewsLoaded != benchViews || rep.Standups != 5 {
				t.Errorf("report = %+v", rep)
			}
			if rep.HeapAllocMB != 12 || rep.HeapOver || !rep.Fake {
				t.Errorf("heap/flags wrong: %+v", rep)
			}
		})
	}
}

func TestWriteReportIsOneJSONLine(t *testing.T) {
	var buf bytes.Buffer
	writeReport(&buf, benchReport{Error: "x", DBReadyMs: 1.5})
	out := buf.Bytes()
	if bytes.Count(out, []byte("\n")) != 1 || out[len(out)-1] != '\n' {
		t.Fatalf("want exactly one trailing newline, got %q", out)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	for _, k := range []string{"t_proc_start_ms", "t_db_ready_ms", "t_window_shown_ms", "t_first_view_ms", "heap_alloc_mb", "error"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing key %q in %s", k, out)
		}
	}
}

func TestBenchSeededIsFalseOutsideBenchMode(t *testing.T) {
	if benchSeeded(context.Background(), nil, nil) {
		t.Error("benchSeeded must be false without a probe")
	}
}
