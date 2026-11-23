# Feature package: ticket modal (F12)

> Start with [HANDOFF.md](HANDOFF.md): where things stand, the screenshots, and the order of work. This file has the
> detailed steps.

**For Claude Code (orchestrator).** Chris has asked for a new feature on top of the finished v1. This folder holds
everything needed to build it. Nothing here has been merged into the living docs yet: merging is step 1 below, because
the docs moved on (Saffron data, Wails 3.1, ADR 0014 JS budget) after this design was drafted.

## What Chris asked for
Open any ticket inside Cuisle as a modal. It shows the summary, the description, and every comment. Beside them is a
dense field panel, "like Jira's but without the wasted whitespace". The panel must render Jira's rich text properly,
including images and attachments, and render custom fields correctly.

His decisions (2026-11-23):
- **Data:** sync it all up front. Descriptions, comments, field values and attachment images are stored locally, so the
  modal opens instantly and offline.
- **Editing:** read plus light edits only: change status (transitions), change assignee, flag or unflag, add a comment.
  All other changes go through Open in Jira.
- **Layout:** A, "Docket", only. It was reviewed with Fable, the design advisor. A side sheet and a three-column
  variant were rejected as too busy.
- **JS budget:** raised from 50 KB to ≤ 70 KB gz for all JS (ADR 0014 to be amended).
- **Deleting synced content:** only offered at Disconnect, and only past the storage cap (default 2 GB; ux-spec §14.11).

## Contents
| Path | What |
|---|---|
| `0015-ticket-detail-content-and-light-writes.md` | The ADR. Move it to `docs/adr/`. Your ADR 0014 (JS budget) already holds 0014. |
| `patches/*.patch` | Doc changes as unified diffs against **`6e762c4`** (the docs baseline commit). Apply each with `git apply --3way`. |
| `fixtures/media/*.png` | The three images in the mockups. Use them as jiratest media fixtures for `MOB-1421` / `MOB-1429`. |
| `../../design/mockups/ticket-*.png` | The approved screens: 1920, 1440, 1280 and 1024, plus edit states and the lightbox. |

Design canvas (Round 6): https://claude.ai/artifact/ByQrCUJ3PUPfcjdwKG9ASg#page-r6

## Step 1: merge the docs (orchestrator, before any WP)
```sh
git apply --3way docs/features/ticket-modal/patches/<file>.patch   # one at a time, in this order:
#   docs_ux-spec.md  docs_product-spec.md  docs_data-model.md  docs_jira-sync.md  docs_testing.md
#   docs_adr_0002-frontend-stack.md  docs_adr_0008-view-models-in-go.md  design_tokens.json
#   docs_architecture.md  AGENTS.md
cp docs/features/ticket-modal/0015-*.md docs/adr/
cp docs/features/ticket-modal/fixtures/media/*.png internal/source/jira/jiratest/testdata/media/   # or wherever J §6 says
```

Dry-run results against `db470d3`:
- **Clean 3-way:** ux-spec, product-spec, data-model, jira-sync, testing, ADR 0002, ADR 0008, `tokens.json` (light and
  dark key sets stay identical).
- **`AGENTS.md`, 1 conflict:** the ADR range. Take `docs/adr/0001…0015`.
- **`docs/architecture.md`, 3 conflicts.** Resolve them like this:
  1. **`Source` interface:** keep HEAD's `Hydrate(ctx, ids, progress ProgressFunc)`, `Meta()` and `Requests()`. Append
     the patch's *Content* block (`Fields`, `FetchMedia`, `GetIssue`) and *Writes* block (`Transitions`, `Transition`,
     `Assign`, `SetFlag`, `AddComment`). `GetIssue` should take the same progress-less path.
  2. **View-model types:** keep HEAD's "Principal types… Where they live (frozen in Wave 0)" paragraph. Insert the
     patch's `TicketDetail` block (types, `BuildTicket` rules, the light-write path) **before** it. Put the ticket types
     in `internal/view/contract.go` beside `TicketView`, following HEAD's rule.
  3. **Budgets: decided by Chris.**
     - Keep HEAD's single all-JS row, and change it to **≤ 70 KB gz** (raised from 50 KB for F12, 2026-11-23).
     - Keep the patch's new rows: modal open ≤ 100 ms with zero network, step ≤ 50 ms, optimistic write ≤ 32 ms.
     - Amend ADR 0014 to say the same.
- **`README.md`:** no patch, because you rewrote it. Add one bullet to "What it is": *"**Open any ticket in place.** A
  dense panel shows the description (images included), every comment, and all fields including custom ones. Move,
  reassign, flag or comment without leaving standup."* Add a screenshot once it is built.
- **`roadmap.md`, `agent-plan.md`, `CLAUDE.md`:** no patch, because the waves are done. Add the milestone and work
  packages below instead.

After merging, sanity-check the text against reality:
- Paths (`internal/view/contract.go`, `api/` facade).
- Names (the row type `TicketView` stays; the modal payload is `TicketDetail`).
- The migration policy. The patch folds the new tables into `0001_init` because nothing had shipped when it was
  drafted. If a release or real user DB exists by then, put them in a new `000N_ticket_content.sql` instead.
- Commit as `docs: ticket modal (F12, ADR 0015)`.

## Step 2: milestone to add to `docs/roadmap.md`
**M9: Ticket modal (F12, ADR 0015).** This milestone is backend + UI; M8 (release) is independent.

1. **Adapter** (J §2.2, §2.8–2.10, §2.14, §2.15):
   - Field registry and kind mapping; `*all` hydrate; comment bodies; `FetchMedia`.
   - Writes: `Transitions`, `Transition`, `Assign`, `SetFlag`, `AddComment`, and `GetIssue`.
   - jiratest content, media, writes and faults. The fixture `MOB-1421` covers every ADF node and field kind.
2. **Sync:**
   - `content` and `media` phases.
   - One-issue apply for write re-fetches, which records history like a sync.
   - Never overwrite newer data.
3. **Backend:**
   - `internal/adf` and `internal/media` (cache, 320×200 thumbnails, cap, eviction).
   - `view.BuildTicket` → `TicketDetail`; `TicketService`; field prefs; media cache settings.
   - The `/media/{id}[/thumb]` routes.
   - Events `ticket:updated`, `ticket:write_failed`, `media:ready`.
4. **UI** (ux-spec §14):
   - Modal geometry at 4 sizes; header with pulse strip and stepper.
   - Anchor strip; ADF components; attachments, subtasks and links.
   - Comments with composer, draft, sending/failed states and the standup marker.
   - Field rail with Not-set folding and the Status/Assignee comboboxes and Flag switch; lightbox.
   - The "Updated in Jira · Reload" bar; row hover/focus; live update of the row behind the modal.
   - Configure → Ticket fields; Display → Storage (size, Clear image cache, cleanup-offer cap); Disconnect's
     over-cap delete offer (U §14.11).

**DoD:**
- Parity with `ticket-*.png` at all four sizes.
- `TicketService.Get` makes no network request; modal open p95 < 100 ms on the fixture.
- **ADF and field coverage:** an ADF golden covers every node in U §14.4; a field-kind matrix covers every kind in
  U §14.6.
- **Writes:** success / failure / revert / no auto-retry / history-recorded tests pass against jiratest.
- **Drafts:** a draft survives a restart.
- **Privacy:** exports and logs contain no comment bodies (scan tests).
- **Quality gates:** axe is clean (modal, combobox, lightbox), and every budget is green.

## Step 3: work packages (same gate and dispatch template as agent-plan §3 and §5)
| WP | Model | Owns | Depends | Reads | Done when |
|---|---|---|---|---|---|
| T1 | Haiku | `internal/adf` | contracts | U §14.4; A (adf); J §2.14 | every ADF fixture normalises to the golden tree; media and inline-card resolution; unknown-node fallback |
| T2 | Haiku | `frontend/src/components/adf/*` | contracts | U §14.4; A §4.9 | one component per node; render snapshots; textColor contrast clamp (light and dark) |
| T3 | Haiku | `internal/media` | contracts | A (media); D §2.5a | paths, thumbnails, cap and LRU eviction, thumbnails never evicted, clear-cache |
| T4 | Haiku | `internal/source/jira/{fields,content,media}*.go` + jiratest content/media | contracts | J §2.2, §2.8–2.10, §2.14 | kind mapping table; `*all` hydrate; bodies; attachment and thumbnail fetch |
| T5 | Haiku | `internal/source/jira/writes*.go` + jiratest writes/faults | contracts | J §2.15; ADR 0015 | needs-screen rule; assign; flag; comment ADF body; 400/401/403/409 mapping |
| T6 | **Sonnet** | `internal/view/ticket*.go`, `internal/sync/oneissue*.go`, `internal/cache/ticket*.go`, migration | T1, T3, T4, T5 | U §14; D §2.5a, Q9; J §4.8 | golden `ticket_mob1421.golden.json` == mock fixture; Not-set folding; prefs order; one-issue apply records history; Get makes no network call |
| T7 | Haiku | `frontend/src/views/ticket/{TicketModal,TicketHeader,FieldRail,StatusCombo,Lightbox}*` | T2 | U §14.1–14.3, §14.6–14.10 | geometry at 4 sizes; stepping and Esc unwinding; optimistic save and revert; no upscaling |
| T8 | Haiku | `frontend/src/views/ticket/{TicketContent,AnchorStrip,Attachments,IssueRows,Comments,Composer}*`, `configure/sections/{TicketFields,Storage}*`, Disconnect confirm | T2 | U §14.4–14.5, §14.8, §14.11 | anchor strip; sending/failed; draft autosave; marker; every comment drawn; field prefs and storage cap autosave; delete offer only over the cap |
| T9 | Haiku → reviewer | `internal/app` (TicketService, routes, events, `Disconnect(ctx, deleteContent)` + storage usage), `cmd/cuisle`, bindings, `api/mock.ts` ticket fixtures | T6, T7, T8 | A §3; U §14.11 | wired end to end in `--fake-jira`; no-network-without-trigger test extended; Playwright screenshots vs `ticket-*.png`; axe |

T1–T5 run in parallel, then T6 alongside T7 and T8 (frontend against `api/mock.ts`), then T9.

**Before dispatching:** freeze the contracts first (adf node types, `media.Cache`, `TicketDetail`, `TicketService`,
`Source` content and write methods). Update `docs/agent-plan.ownership.txt` with the T rows.

## Step 4: update `CLAUDE.md` and `AGENTS.md` §10
- Add `- [ ] M9 Ticket modal (F12, ADR 0015)` to the progress list.
- Log the decision: "2026-11-23: ticket modal with full content sync and light writes (ADR 0015); layout A".
- Log the decisions: JS budget 70 KB, and delete only offered at Disconnect past the storage cap (already in the
  AGENTS patch).

## Decisions Fable made while drafting (surface to Chris if you change any)
- The modal payload is named `TicketDetail`, because `TicketView` is the row.
- A transition "needs a screen" only when its screen has a required field without a default; optional-only screens
  transition directly.
- Thumbnails are 320×200 and never evicted. Full images download after the record apply, so the first open may show
  placeholders. The media cache defaults to 1 GB.
- Comments deleted in Jira are hidden but kept (`deleted_at`).
- Writes are sent once: no 429 or 5xx retry.
- The full-sync time budget rises from 25 s to 60 s (content phase).
- Comments are newest first by default (app setting `ticket.comment_order`), and all are drawn.
