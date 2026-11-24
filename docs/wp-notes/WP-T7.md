# WP-T7: ticket modal shell (state, header, field rail, status combo, lightbox, modal)

Implements ux-spec §14.1–14.3, §14.6, §14.7, §14.9, §14.10, §14.12 and §14.13 against the frozen T7 tests. The modal is not
mounted in the app yet (WP-T9 does that), so nothing here changes the running app.

## Files
- `frontend/src/state/ticket.ts`: session (open, step, back stack), the active detail, optimistic writes, the Refresh state
  machine, pending comments and draft autosave.
- `frontend/src/views/ticket/`: `TicketModal` (+ `modalGeometry`), `TicketHeader`, `FieldRail`, `StatusCombo`, `Lightbox`,
  each with its `.css`, plus `TicketModal.util.ts` (250 ms hook, `H:mm`, `openLink`).

## Decisions
- **Per-issue UI state.** Write phases, pending comments, the Retry closures and the Refresh machine are kept per issue and
  published for the active one. A write that settles after the user stepped away fixes its own cached rows but never touches the
  visible detail, and its failure state is not shown on the other ticket. The Refresh cooldown survives stepping away and back.
- **Optimistic writes** patch the detail and every cached `StandupView` row of that issue before the first await; the revert is
  the exact inverse (captured previous values), so a failure is deep-equal to before. Success replaces the detail and the rows
  with the server's answer (the row keeps its own pulse when the view's `pulseDays` differs from the detail's).
- **Sending a comment** clears the composer (the text lives in the pending bubble) and cancels any pending draft save so it can't
  resurrect the text. A failed send stores the text with `SaveDraft` so it survives a restart. Edit puts it back (kept in front of
  newer composer text, mentions shifted); Discard deletes the stored draft unless the composer holds something newer.
- **No setInterval** (lint bans it): the one-second countdown is a self-rescheduling `setTimeout`, recomputed from instants.
- **Opening a combobox programmatically.** The shared Combobox has no open prop or callback. `StatusCombo` observes `aria-expanded`
  with a MutationObserver (each open is a session: the transition list loads once, typing filters locally), opens by focus + click and
  closes with an Escape keydown on its input, and remounts the Combobox (`key`) after a needs-screen or offline pick so it shows the
  current status again. These effects are **layout effects**: a deferred effect let a quick second click slip in before the echo of
  the first close and re-close the list (a real flake caught by the tests).
- **Offline note** ("Offline · open in Jira") lives inside the first option's content (the list stays flat, option count unchanged).
  An offline pick opens the ticket in Jira and never writes.
- **Keys** are one `document` listener that ignores keys from outside the dialog, with modifiers, from text controls (except Esc), or
  already default-prevented (a combobox that closed itself), and everything while the lightbox is open. Esc order: combobox,
  lightbox, `?` popover, composer blur (focus stays in the dialog), then close.
- **Narrow windows** (< 900 CSS px): `FieldRail layout="strip"` is the State strip; the other groups render as a second
  `FieldRail` with an extra `rest` prop (root `data-layout="inline"`, so there is still no `data-layout="column"`) at the head of
  the content column. `props.ts` is untouched.
- **Geometry** follows the spec table exactly at the four rows (`mx` 100 / 32 / 12, `my` 40 / 32 / 24 / 12; width capped at 1240
  below 1440 so it is monotonic); content column is the nearest smaller row's value, at most `modal − 292`; rail = the rest − 8.
- **Lightbox** fits to the window minus the 64px bar, 64px each side (edge buttons) and 48px below; no caption (attachments carry
  none). `media:ready` events patch the open detail's attachment `cached`, so a Lightbox `ensure` isn't repeated.
- **Links.** `ctx.onOpenUrl` / needs-screen URLs: an `/browse/KEY` URL goes through `app.openIssue(key)`; any other http(s)/mailto
  link uses `@wailsio/runtime` `Browser.OpenURL` (already a runtime dep). There is no facade method for arbitrary URLs; WP-T9 may
  want one.
- **Offline** is `detail.can.reason === "offline"` (the modal has no offline prop).
- **Comment order** goes through `state/app.setAppSetting("ticket.comment_order", …)` (save queue + settings signal).
- **Scrim click** does not close the modal (the spec names only ✕ and Esc).
- Focus: the dialog takes focus on mount and the opener gets it back on unmount.

## Gates (this branch)
- Vitest: `src/views/ticket` + `src/state` 617/617 (27 files, all 12 WP-T7 files included), run 6× plus once under load: no flake.
  Full suite 1855/1855 (91 files). `tsc --noEmit`, `eslint src scripts tests`, `vite build`: green.
- size-limit (modal not mounted, so unchanged): JS 46.09 kB gz (limit 70), CSS 10.55 kB gz (limit 12), fonts 118.83 kB (160).
- **Measured with the modal mounted** (temporary entry importing it; not committed): T8 content alone is JS 53.18 / CSS 13.30 kB
  gz; with T7 on top JS 63.01 kB (T7 ≈ +9.8, under 70) and CSS 15.68 kB (T7 ≈ +2.4). **The CSS budget (12 kB gz) is already over
  with T8 alone**; WP-T9 needs the orchestrator to raise it or trim CSS when it mounts the modal.
