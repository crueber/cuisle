# Handoff: ticket modal (F12), the next feature after v1

**Read this first, then [README.md](README.md) in this folder for the detailed steps.**

## Where things stand
- v1 is complete. Every work package (01–38) is merged and every budget is green. What remains for v1 is CI secrets,
  UAT and the M8 release. That work is independent of this feature and stays tracked in `CLAUDE.md`.
- Chris designed a new feature with Claude (design chat) and Fable (design advisor): **open any ticket inside Cuisle as
  a modal.** It shows the description rendered from Jira's rich text, images included, every comment, and a dense field
  rail with custom fields. It also allows four light edits: status, assignee, flag and add comment.
- Everything for it is in `docs/features/ticket-modal/` and `design/mockups/ticket-*.png`. These files are **untracked**
  in the working tree. They were dropped in from outside the repo, and nothing tracked was modified.

## What it looks like (Read these PNGs before writing any UI)
All screens use the Saffron sample data over the person view. They are the visual contract; `ux-spec §14` (after the
merge) is the written one. Where the two disagree, the spec wins.

| File | Shows |
|---|---|
| `design/mockups/ticket-1920x1080.png` | **The main reference.** MOB-1421 at 1920×1080: header (type icon, mono key, parent crumb, "as of · synced", `‹ 7 / 20 ›` stepper, Open in Jira, ✕), summary, pulse strip, anchor strip, ADF description (link card, mention, info panel, task list, table, image, code block, warning panel, decision, expand), and the field rail (State / Details / Custom fields with sentence-case labels and a "Not set:" line). |
| `design/mockups/ticket-edit-states-1920x1080.png` | MOB-1429 (blocked bug), scrolled to Comments. Shows the Status combobox open (in-app transitions; "screen · ↗ Jira" ones muted below a divider), Flag on with BLOCKED, the composer with text, a comment *sending…*, a comment that **failed** (Retry / Edit / Discard), and the "↑ 3 NEW SINCE STANDUP" marker. |
| `design/mockups/ticket-lightbox-1920x1080.png` | The image lightbox: solid top bar with file meta, `100%`, `1 / 2`, Open file and ✕. The image is never upscaled; ← → step through images. |
| `design/mockups/ticket-1440x900.png` | The same layout at 1440. |
| `design/mockups/ticket-1280x720.png` | Short mode (`vh < 800`): smaller header and no keyboard footer. |
| `design/mockups/ticket-1024x640.png` | Smallest window: 700px content column; rail labels stack and short fields pair two-up. |

There is **one layout**. A side sheet and a three-column variant were explored and rejected as too busy, so don't
build them or add a wide breakpoint. The live design canvas (Round 6, same screens, scrollable) is https://claude.ai/artifact/ByQrCUJ3PUPfcjdwKG9ASg#page-r6.
Chris can open it; you work from the PNGs.

## Do this, in order
1. **Commit the package as it is:** `docs/features/ticket-modal/` plus `design/mockups/ticket-*.png`, with the message
   `docs: ticket modal feature package (F12)`.
2. **Merge the docs** (README Step 1).
   - `git apply --3way` each patch.
   - Expect 1 trivial conflict in `AGENTS.md` and 3 in `docs/architecture.md`; the README says exactly how to resolve
     each.
   - Move the ADR to `docs/adr/0015-…`.
   - Sanity-check names and paths against the code, then commit.
3. **Add milestone M9 and work packages T1–T9** to `docs/roadmap.md` and `docs/agent-plan.md`, and update the
   ownership file (README Steps 2–3).
4. **Freeze the contracts:**
   - `internal/adf` node types
   - `internal/media.Cache`
   - `view.TicketDetail`
   - `TicketService`
   - the `source.Source` content and write methods
   - the new migration
   Then dispatch T1–T5 in parallel, then T6 alongside T7 and T8, then T9. Use the same gate, reviewer and worktree
   rules as v1.
5. **Add `- [ ] M9 Ticket modal (F12, ADR 0015)` to the `CLAUDE.md` progress list.** Tick it when M9's DoD holds.

## Chris's decisions (2026-11-23), already written into the patches
- **JS budget: raised to ≤ 70 KB gz for all JS.** Up from ADR 0014's 50 KB; the modal and ADF renderer are expected to
  add about 20 KB.
  - Amend ADR 0014 (status line + a dated note) and the size-limit config.
  - Code-splitting `views/ticket` + `components/adf` into a chunk prefetched at idle is recommended (cold start stays
    flat) but not required.
- **Deleting synced content.**
  - Never automatic.
  - Disconnect keeps content, and offers an **unchecked** "Also delete synced content (2.4 GB)" only when local content
    is over `storage.cleanup_offer_mb` (default 2048 MB, editable in Display → Storage).
  - History rows are never deleted. Details are in `ux-spec §14.11`; the API is `ConnectionService.Disconnect(ctx,
    deleteContent bool)`.
- **Layout:** A ("Docket") only.

## Ground rules that still apply
- AGENTS.md invariants. After the merge these include the new #13 (Jira writes are explicit user actions only) and #14
  (Cuisle renders ADF itself; never `innerHTML` or `renderedFields`).
- Opening the modal makes **zero** network requests. Writes are never retried automatically.
- Exports, logs and crash output never contain comment bodies.
- Push to Forgejo only when Chris asks. Never publish to GitHub.
