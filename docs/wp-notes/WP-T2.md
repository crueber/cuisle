# WP-T2: ADF renderer components

Implements `frontend/src/components/adf/` against the frozen `props.ts` and the four `AdfDoc.*.test.tsx` specs.

## Layout
- `AdfDoc.tsx`: root (`data-size`, `class`), optional `clampLines` with "Show all" (measured after render, no timers).
- `AdfNode.tsx`: the node switch (one case per `adf.Node.t`); unknown types fall through to `Unknown`. Also `plainText`
  for task checkbox names.
- `marks.tsx`: text marks; links and `Unknown`/file/media "open" calls go through `openLink` (preventDefault + ctx).
- `contrast.ts`: `clampTextColor(authored, theme)`: keeps the hue, steps HSL lightness until 4.5:1 against the page
  background (mirrors `design/tokens.json` bg). Memoised per colour × theme. The only file allowed to hold colour maths.
- `blocks.tsx`: `Expand` (collapsed body is not rendered; `ctx.expandAll` token semantics).
- `cards.tsx`: issue card (button, `onOpenIssue`), URL card (anchor, `onOpenUrl`), `Unknown`.
- `media.tsx`: `Media` (full → thumb → placeholder "Load image", `attachment unavailable` for Missing) and `FileCard`.
  The parent keys `Media` by attachment and `cached`, so an `EnsureMedia` refresh restarts the fallback chain.
- `inline.tsx`: status, mention, date (`<time dateTime>`, shown as "1 Dec 2026" in UTC), emoji.
- `AdfDoc.css`: tokens only, sizes via `calc(var(--scale) * …)`, no `#` characters.

## Decisions
- Expand bodies are not rendered while collapsed (rather than `hidden`), so role queries and axe see only visible content.
- Task checkboxes are disabled inputs with `aria-label` from the item text.
- Panel kinds not in the five known values render as `note`.
- Table cell width is the sum of `colwidth` for the cell (colspan-aware).
- Clamp "Show all" is shown only when the clamped box overflows (or while expanded).
