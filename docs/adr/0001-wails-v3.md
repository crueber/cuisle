# ADR 0001 — Go + Wails v3 desktop shell

Status: Accepted (decided by the owner; recorded here)

## Context
Cuisle is a single-user desktop tool for running standups from Jira boards. The owner is fluent in Go; the app must ship
for macOS, Windows and Linux from milestone 1, start instantly from a local cache, and stay small.

## Decision
Build on **Wails v3** (stable; pinned to **v3.1.0**, see Spike results): Go backend, OS webview frontend, one window. Pin the exact Wails version in `go.mod`
and the identical `@wailsio/runtime` version in `frontend/package.json`; upgrade both together, deliberately.
Use Wails **services** as the only Go↔JS API (`architecture.md` §3.2), Wails **events** for Go→JS notifications (§3.3),
generated TypeScript bindings, and the Wails Taskfile build/packaging flow. Only `cmd/cuisle` and `internal/app` import Wails.

## Consequences
- Small binary, no bundled browser; rendering engine differs per OS (WKWebView, WebView2, WebKitGTK) → CSS/JS must target
  the weakest (WebKitGTK) and the packaged Linux build is smoke-tested in CI.
- cgo is required on macOS and Linux, so each OS builds on a native runner (no cross-compile matrix from one host).
- Wails-specific API names in the docs must be verified against the pinned version in a first spike (risk R1).
- Domain packages stay framework-free and testable with plain `go test`.

## Spike results (M0, 2026-11-23, Wails v3.1.0)
- **Pin:** `github.com/wailsapp/wails/v3 v3.1.0`, `wails3` CLI v3.1.0 (`go install …/cmd/wails3@v3.1.0`) and
  `@wailsio/runtime 3.1.0` (exact, no caret). v3.1.0 is the newest stable release all three share, so it replaces the
  earlier "3.0.x" wording.
- **Verified in a running app (macOS WKWebView):** `application.NewService`, `Options.MarshalError` (JSON arrives as
  `RuntimeError.cause`), `AssetOptions.Middleware` (served `/boot.js`), ctx injection plus cancellation
  (`CancellablePromise.cancel()` cancels the Go ctx, and JS sees `CancelError`), `application.RegisterEvent[T]` typed events,
  and `wails3 generate bindings -clean=true -ts -i -d frontend/src/bindings`.
- **cgo:** Wails' build guard rejects `CGO_ENABLED=0` unless `-tags purego` is set. Invariant 9 is therefore
  `CGO_ENABLED=0 go test -tags purego ./internal/...`. Release builds on macOS and Linux still use cgo.
- **Generated bindings are committed** (CI checks drift), because agent worktrees are fresh checkouts.
- Wails logs every bound-method error message at ERR, so `apperr.Error.Error()` excludes `Detail`.
- **Linux uses GTK3 / WebKitGTK 4.1** (confirmed by Chris, 2026-11-23). Wails 3.1 defaults to GTK4 / webkitgtk-6.0, so
  Linux builds and the CI image's `wails3` CLI use `-tags gtk3`. This matches the Ubuntu 22.04 glibc baseline in
  architecture §6.5.
