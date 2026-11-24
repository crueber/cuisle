# WP-T8: ticket content UI, ticket fields, storage, disconnect offer

Implements the Haiku-side UI for ux-spec §14.4, §14.5, §14.8, §14.9 and §14.11 against the frozen T8 tests.

## Files
- `frontend/src/views/ticket/`: `TicketContent`, `AnchorStrip`, `Attachments`, `IssueRows`, `Comments`, `Composer`
  (each with its `.css`). `TicketContent.css` also owns the shared section head (`tk-*`).
- `frontend/src/views/configure/sections/`: `TicketFields` (+ `.css`), `Storage` (+ `.css`; exports `formatBytes`),
  `Connection.tsx` and `Connection.css` (Disconnect confirm reads sizes and offers "Also delete synced content").

## Decisions
- Composer is prop driven and never calls the api (F12 freeze). Mentions carry across edits by common prefix and suffix.
- The "@" picker opens when "@" is typed and closes on select, Escape, or when the "@" is edited away.
- The strip's active entry follows the reader with an IntersectionObserver (guarded for jsdom): the first section in DOM
  order that intersects a band just below the strip. Clicks set it at once. Unverified in a real browser (jsdom has no layout).
- Sizes follow the Configure convention: font sizes and key dimensions use `calc(var(--scale) * …)`, so Large (TV) text scales.
  Column padding steps 32/24/20 with the modal width. Visual sign-off against the mockups is still pending (UAT).
- The Composer's "@" picker is live only while its "@" is still in the draft.
- Ticket fields: pin, unpin and hide show the server's result. A reorder shows at once and returns to the last saved order
  on failure. The hide switch's visible name is hidden with CSS. The accessible name stays "Hide <field>".
- Disconnect: the size lookup starts synchronously on click. A late answer is dropped after Cancel or a re-open.
- Sizes use decimal units. The cleanup cap is stored in MiB and shown in GB (1 GB = 1024 MB).

## Gates (this branch)
- Vitest: `src/views/ticket` + `src/views/configure` 404/404. Full suite 1588/1588 (79 files).
- `tsc --noEmit`, `eslint src scripts tests`, `vite build`: green.
- size-limit: JS 46.09 kB gz (limit 70), CSS 10.55 kB gz (limit 12), fonts 118.83 kB (limit 160). `.size-limit.json` untouched.
