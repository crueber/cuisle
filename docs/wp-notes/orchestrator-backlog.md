# Orchestrator backlog (follow-ups found while gating WPs)

Items to dispatch to the WP-38 bug pool or fold into a later WP brief.

- **jiratest Materialize, morning runs:** today's events are stamped 10:00 / 11:00 local. Before 10:00 they are in the
  future, so pulse ignores them and `task dev` shows no marks for today in the morning. Clamp day-0 event times to
  `min(slot, Now − 1 min)` (fixture.go, WP-07 code).
- **store.OpenMemory:** two pools over a shared-cache memory DB can hit SQLITE_LOCKED under concurrent read and write.
  Watch for it in the sync/app integration tests; switch tests to `storetest.OpenFile` if it bites.
- **storetest.Checksum:** rows are read with no ORDER BY. That's fine for "unchanged" comparisons, but don't use it to
  compare DBs built in different orders.
- **jira client error Detail:** transport errors carry the url.Error text, which includes the JQL query. That's OK at
  debug, but the UI's "Details" view could show it. Consider redacting `jql=` in Detail (errors.go, WP-06 code).
- **WP-32 brief:** export.Meta.Timezone must be a named IANA location (time.Local.String() is "Local"). Resolve it
  from `TZ` or `/etc/localtime`, falling back to UTC. sink.ListConfigs has no keyring, so the app fills
  SinkConfig.HasSecret from the keyring. Use app.NewSinkBuild/SinkNotifier for the dispatcher wiring.
- **WP-29 brief:** recover panics in run goroutines and map them to apperr internal (the architecture requires it, and
  the tests abort otherwise).
- **WP-15 brief (done):** currentPerson resolves excluded speakers.
- **Stepper button names** are "Decrease"/"Increase". If two steppers share a screen (Pulse limits), consider
  "Decrease <label>" for a11y. That needs a test change, so it would go through a contract decision.
- **file sink mode:** exports are written 0600 (CreateTemp). Chmod to 0644 before rename so other tools can read them (file.go, WP-21 code).
- **file sink dir:** Deliver requires an existing folder (sink_failed "The folder does not exist."). Configure should validate it or offer to create it.
- **view alloc budget:** BenchmarkStandupView is 1.96 MB/op against a 2 MB budget (2% headroom). WP-35 should trim allocations (pre-size slices, reuse windows).
- **WP-28 follow-ups:** Export delete is immediate and Undo re-creates the sink with a new id and no secret. The Connect "Create an API token" link needs AppService.OpenURL-style handling in the webview (only OpenIssue exists). After Disconnect the app stays on Configure.

## Visual fix list from the first mock-mode screenshots (for WP-33/WP-36)
Person view at 1920×1080 against person-1920x1080.png:
- BLOCKED tag sits at the far right of the title cell. It belongs right after the title text (ux-spec §5.4).
- Side-gutter heading "IN PROGRESS 5" wraps. The gutter heading must use --font-size-gutter-head (12px bold caps) and
  fit in the 118px gutter.
- Axis labels: weekends in --color-rule-mid and Mondays bold --color-fg (ux-spec §3). Today they're all the same.
- Rail Unassigned entry shows "··" initials. The mockup shows only "Unassigned" plus the count.
- Rail footer should read "1–N switch standup" with N = number of tabs.
- The mock fixture's pulse window is 28 days (the sample axis). The mockups use 21. Fine once the Go golden view
  replaces view_mob.json.
Configure at 1920×1080 against configure-1920x1080.png:
- Duplicate labels: the field row label plus the component's own visible label ("Name / Name"; checkbox
  "Include X" next to the name). Sections should hide component labels visually (keep the accessible name).
- Display/Connection/Export section headings are oversized display type. Use the same section heading style as
  Source/Speakers (ux-spec §9.3).
- Speaker include checkboxes render as solid black squares (checkbox styling).
- Pulse live preview is cramped and its backdrop misaligned.
- Status limits Steppers wrap awkwardly in the 3-column layout.
- **Tab/rail accessible names:** they read "1SVC — Saffron Mobile" because the visible children have no text space between them. Add a real space text node between the number box and the key/name (and in rail initials/count) so the name reads "1 MOB — Saffron Mobile". Update the aria-labels and run Tabs/Rail tests plus the a11y suite.

## From WP-32 (app wiring)
- cache.Trim is not called at startup or after a successful apply (data-model §5 says both). Wire it into cmd/cuisle
  startup and the sync AfterSync hook.
- boot.js returns exactly `window.__CUISLE_BOOT__ = …;`, so the frontend applies data-theme from boot.settings. The
  native window's BackgroundColour comes from the theme, so there's no white flash. Update architecture §4.7 ("set by
  boot.js") to match, or have the middleware append the theme line and adjust the boot test.
- Window geometry is saved only at shutdown (lost on a crash). Consider saving on resize/move, debounced.
- cache.UpsertBoards overwrites board.source_meta with search results (no Meta). It's harmless today; keep existing
  meta on conflict.
- Startup failures before the window (DB error, newer schema) print to stderr and exit 1. The spec wants a clear
  dialog for "newer schema" (architecture §6.3).
- **JS bundle budget:** 43.06 KB gz of 45 KB. Before adding UI, look for dead weight (bindings index re-exports, the Wails runtime surface used, mock code fully tree-shaken). WP-35/36.
- **Mock fixture view_mob.json** is JS-generated (28 days, no Go semantics). testing §3 wants it to equal the Go golden (internal/app/testdata/view_mob.golden.json). Swap it in WP-33 and make mock.test.ts tolerate the Go data.
- **Days-in-lane semantics (decision for Chris):** jira-sync §5.4 gives 5d for a ticket whose lane occupies 6 day columns (entered 5 days ago), and the prototype/mockups show 6d. The spec formula is implemented.
- **Sync trigger "config":** SyncService.Start(ctx, id, mode) has no trigger, so syncs started by leaving Configure are recorded as "manual". The banner rules (banners only for user-started syncs) are unaffected, but sync_run.trigger and sync:started.trigger never say "config". Options: accept and document, or add a bound StartFromConfig(id) (contract change + bindings).
- **SearchBoards** probes each "simple" board for sprint support (up to ~200 requests per typeahead). Consider probing lazily at SetBoard only.
- **SetBoard** calls Reference() and discards the resulting meta (field ids). Persist via connection meta or skip.
- **sync.Orchestrator side state:** the closed/starting flags live in a package-level sync.Map (orchExt) because the frozen struct had no fields for them. Add real fields to internal/sync/contract.go at the next contract revision.

## Final polish (2026-11-23)
- JS budget: terser (`build.minify: "terser"`, dev dependency only) plus TicketRow's TypeIcon reusing `Icon`.
  `IconName` (frozen contract, components/props.ts) dropped ten unused names (warning, check, grip, trash, copy,
  shuffle, key, external, folder, search); re-add one when a screen needs it. `pure_getters` stays off, because a
  bare signal `.value` read subscribes.
- staticcheck has not run yet (a broken mise shim locally; CI cannot start without the `cuisle-ci` image). The first
  green CI run may report findings.
- Real-stack smoke of the terser bundle (server mode, fake Jira): first full sync about 15 s at 10 req/s, whole board
  67 rows, autosave survives reload, File sink delivers a valid `cuisle.export.v1`, no console errors. Packaged
  cold start with 5 seeded standups: 363–398 ms to first view (budget 500), heap under 1 MB.
- `--fake-jira` is a bool-style flag (`--fake-jira` or `--fake-jira=PATH`). `--fake-jira default --bench-startup` reads
  `default` as a positional argument and silently ignores `--bench-startup`.
- JS budget raised to 50 KB gz (ADR 0014, maintainer's choice) at 45.33 KB, after status ordering, speaker removal and
  sync progress landed.
