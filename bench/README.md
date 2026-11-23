# Benchmarks and baselines

`baseline.txt` is the committed output of the Go benchmarks (architecture.md section 6.1), recorded on an Apple M4 Max
(macOS, arm64). Absolute numbers are machine specific: compare relative change on the same machine, and let CI
compare against a baseline recorded on the CI runner.

| Benchmark | Package | Budget |
|---|---|---|
| `BenchmarkBootstrap/L` (open DB + migrate check + Bootstrap, dataset L) | `internal/app` | <= 60 ms/op |
| `BenchmarkStandupView/L` (load + build, dataset L) | `internal/app` | <= 15 ms/op |
| `BenchmarkStandupView` (pure `view.Build`, 400 tickets, 3 years of history on a third) | `internal/view` | <= 15 ms/op, <= 2 MB/op |
| `BenchmarkConfigUpdate` (one Configure save, file DB) | `internal/settings` | <= 10 ms/op |
| `BenchmarkOpenExisting` (reopen a migrated file DB) | `internal/store` | floor of Bootstrap |

Dataset "L" is seeded by `internal/app/bench_test.go`: 5 standups, 400 board tickets plus a 60-row backlog slice in the
visible one, a 28-day pulse, and about 3 000 closed issues of history (3 years) on the visible standup.

## Regenerate the baseline

```
CGO_ENABLED=0 go test -tags purego -run '^$' -bench . -benchmem -count 6 \
  ./internal/view ./internal/settings ./internal/app ./internal/store > bench/baseline.txt
```

(`task bench` writes `bench/latest.txt` with the same flags; add `./internal/app` to its package list so Bootstrap is
included.) Review the diff before committing a new baseline and say why it moved.

## Compare with benchstat

benchstat is optional tooling, not a dependency: `go install golang.org/x/perf/cmd/benchstat@latest`.

```
benchstat bench/baseline.txt bench/latest.txt
```

## Cold start (`--bench-startup`)

`cuisle --bench-startup` opens the window, waits for the frontend's `first-view` report (`AppService.ReportPerf`),
loads five views, prints one JSON line on stdout and exits 0 (or prints `{"error": ...}` and exits 1 after 15 s).
It never syncs. Times are milliseconds since process start (`t_proc_start_ms` is the Unix epoch in ms);
`js_first_view_ms` is the page's own `performance.now()` at the mark.

Against dataset L (write it once; the data dir then holds a `cuisle.db`):

```
(cd frontend && ./node_modules/.bin/vite build) && go build -o /tmp/cuisle ./cmd/cuisle
CUISLE_BENCH_SEED_DIR=$(mktemp -d) CGO_ENABLED=0 go test -tags purego -run TestWriteBenchDataset ./internal/app   # note the dir
CUISLE_DATA_DIR=<that dir> /tmp/cuisle --bench-startup
```

With `--fake-jira` on a fresh data dir the fake's standups are created first (about 1.5 s of loopback calls, before the
window opens), so that run's `t_window_shown_ms` is inflated and its views are empty (no sync runs). Run it a second time
on the same dir (seeding is skipped) or use dataset L for representative numbers.
