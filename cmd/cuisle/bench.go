package main

// The --bench-startup measurement (architecture.md section 6.1): Go records process start, database ready and window
// shown; the frontend reports its first-view mark through AppService.ReportPerf (Deps.PerfSink); then the process
// loads five views, reads the heap, prints ONE JSON object on stdout and exits (0, or 1 with an "error" field when the
// first-view mark does not arrive in time). Nothing here touches the network and bench mode never syncs.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"runtime"
	"sync"
	"time"

	"github.com/crueber/cuisle/internal/app"
	"github.com/crueber/cuisle/internal/buildinfo"
)

// procStart is the earliest instant Go code can capture (package initialisation).
var procStart = time.Now()

// benchTimeout is how long the first-view mark may take before bench mode fails.
const benchTimeout = 15 * time.Second

// benchViews is how many views are loaded before the heap is read (architecture 6.1: "after loading 5 views").
const benchViews = 5

// Budgets from architecture 6.1 (macOS/Linux cold start, Go heap steady state).
const (
	budgetFirstViewMs = 500
	budgetHeapMB      = 40
)

// benchReport is the JSON line printed by --bench-startup. Times are milliseconds since process start unless the
// name says otherwise. js_first_view_ms is the page's own performance.now() at the mark (relative to the page's
// time origin, so it is not comparable to the Go times); t_first_view_ms is when Go received the mark.
type benchReport struct {
	Error         string  `json:"error,omitempty"`
	ProcStartMs   int64   `json:"t_proc_start_ms"` // Unix epoch milliseconds
	DBReadyMs     float64 `json:"t_db_ready_ms"`
	WindowShownMs float64 `json:"t_window_shown_ms"`
	FirstViewMs   float64 `json:"t_first_view_ms"`
	JSFirstViewMs float64 `json:"js_first_view_ms"`
	ViewsLoaded   int     `json:"views_loaded"`
	ViewsLoadMs   float64 `json:"views_load_ms"`
	HeapAllocMB   float64 `json:"heap_alloc_mb"`
	HeapSysMB     float64 `json:"heap_sys_mb"`
	Standups      int     `json:"standups"`
	Fake          bool    `json:"fake_jira"`
	Version       string  `json:"version"`
	GoOS          string  `json:"goos"`
	GoArch        string  `json:"goarch"`
	CPUs          int     `json:"cpus"`
	BudgetFirstMs float64 `json:"budget_first_view_ms"`
	BudgetHeapMB  float64 `json:"budget_heap_mb"`
	OverBudget    bool    `json:"first_view_over_budget"`
	HeapOver      bool    `json:"heap_over_budget"`
}

// benchProbe collects the marks. It is safe for concurrent use: PerfSink runs on a Wails IPC goroutine, the window
// events on the UI thread.
type benchProbe struct {
	start time.Time

	mu    sync.Mutex
	marks map[string]time.Duration
	js    float64

	first     chan struct{}
	firstOnce sync.Once
}

func newBenchProbe(start time.Time) *benchProbe {
	return &benchProbe{start: start, marks: map[string]time.Duration{}, first: make(chan struct{})}
}

// Mark records the first time name is seen (a window can be shown more than once).
func (p *benchProbe) Mark(name string) {
	p.mu.Lock()
	if _, ok := p.marks[name]; !ok {
		p.marks[name] = time.Since(p.start)
	}
	p.mu.Unlock()
}

// Sink is the Deps.PerfSink: it notes the frontend's first-view mark and wakes finish.
func (p *benchProbe) Sink(marks map[string]float64) {
	v, ok := marks["first-view"]
	if !ok {
		return
	}
	p.firstOnce.Do(func() {
		p.Mark("first_view")
		p.mu.Lock()
		p.js = v
		p.mu.Unlock()
		close(p.first)
	})
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// memStats is the slice of runtime.MemStats the report uses (injected so tests need no GC).
type memStats struct{ HeapAlloc, HeapSys uint64 }

func readMem() memStats {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return memStats{HeapAlloc: m.HeapAlloc, HeapSys: m.HeapSys}
}

// viewLoader loads n views and reports how many it loaded and how many standups exist.
type viewLoader func(ctx context.Context, n int) (loaded, standups int, err error)

// finish waits for the first-view mark (up to timeout), loads views, and returns the report and the exit code.
func (p *benchProbe) finish(ctx context.Context, timeout time.Duration, load viewLoader, mem func() memStats, fake bool) (benchReport, int) {
	rep := benchReport{
		ProcStartMs: p.start.UnixMilli(), Fake: fake, Version: buildinfo.Version, GoOS: runtime.GOOS, GoArch: runtime.GOARCH,
		CPUs: runtime.NumCPU(), BudgetFirstMs: budgetFirstViewMs, BudgetHeapMB: budgetHeapMB,
	}
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case <-p.first:
	case <-t.C:
		p.fill(&rep)
		rep.Error = fmt.Sprintf("the frontend did not report first-view within %v", timeout)
		return rep, 1
	case <-ctx.Done():
		p.fill(&rep)
		rep.Error = "interrupted before the frontend reported first-view"
		return rep, 1
	}
	begin := time.Now()
	loaded, standups, err := load(ctx, benchViews)
	p.fill(&rep)
	rep.ViewsLoaded, rep.Standups, rep.ViewsLoadMs = loaded, standups, ms(time.Since(begin))
	if err != nil {
		rep.Error = "loading views: " + err.Error()
		return rep, 1
	}
	m := mem()
	rep.HeapAllocMB = float64(m.HeapAlloc) / (1 << 20)
	rep.HeapSysMB = float64(m.HeapSys) / (1 << 20)
	rep.OverBudget = rep.FirstViewMs > rep.BudgetFirstMs
	rep.HeapOver = rep.HeapAllocMB > rep.BudgetHeapMB
	return rep, 0
}

func (p *benchProbe) fill(r *benchReport) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r.DBReadyMs = ms(p.marks["db_ready"])
	r.WindowShownMs = ms(p.marks["window_shown"])
	r.FirstViewMs = ms(p.marks["first_view"])
	r.JSFirstViewMs = p.js
}

// loadViews is the production loader: it calls StandupService.View n times, cycling through the configured standups,
// which is what switching tabs does (cached data, no sync, no network).
func loadViews(svcs *app.Services) viewLoader {
	return func(ctx context.Context, n int) (int, int, error) {
		tabs, err := svcs.Standup.List(ctx)
		if err != nil {
			return 0, 0, err
		}
		var ids []int64
		for _, t := range tabs {
			if t.Configured {
				ids = append(ids, t.ID)
			}
		}
		if len(ids) == 0 {
			return 0, len(tabs), nil
		}
		for i := 0; i < n; i++ {
			if _, err := svcs.Standup.View(ctx, ids[i%len(ids)]); err != nil {
				return i, len(tabs), err
			}
		}
		return n, len(tabs), nil
	}
}

// writeReport prints the report as one JSON line.
func writeReport(w io.Writer, r benchReport) {
	b, err := json.Marshal(r)
	if err != nil {
		b = []byte(`{"error":"cannot encode the report"}`)
	}
	_, _ = fmt.Fprintf(w, "%s\n", b)
}

// benchSeeded reports whether a bench run over --fake-jira can skip seeding the fake: the data dir already has
// standups (a fake's port changes on every launch, so reconnecting to a seeded dir would be refused, and bench mode
// does not need the fake once there is data to show). It is false outside bench mode.
func benchSeeded(ctx context.Context, svcs *app.Services, probe *benchProbe) bool {
	if probe == nil {
		return false
	}
	tabs, err := svcs.Standup.List(ctx)
	return err == nil && len(tabs) > 0
}
