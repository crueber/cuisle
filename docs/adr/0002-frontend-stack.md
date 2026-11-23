# ADR 0002 — Frontend: Preact + @preact/signals, TypeScript, Vite, hand-written CSS

Status: Accepted

## Context
The brief asks for the lightest practical JS stack. The UI is two dense screens (Standup, Configure) with keyboard-driven
switching, one reusable combobox, reorderable lists, and ~500 rows worst case. Candidates: Preact + signals; lit-html +
signals-core; vanilla TS + a tiny reactive helper; Svelte 5.

## Decision
**Preact 10 + @preact/signals**, TypeScript (strict), Vite. No router, no CSS framework, no component library, no date
library. Runtime dependencies are exactly `preact`, `@preact/signals`, `@wailsio/runtime`.
State lives in module-level signals; view models arrive fully computed from Go (ADR 0008).
CSS is hand-written against custom properties generated from `design/tokens.json`; themes via `data-theme` on `<html>`.
Budgets: **JS ≤ 45 KB gzip, CSS ≤ 12 KB gzip**, enforced with `size-limit` in CI. (JS raised to 50 KB by ADR 0014.)

Why not the others:
- lit-html + signals-core: ~2 KB smaller, but no component/lifecycle model and untyped templates; the combobox and
  focus management become bespoke code.
- Vanilla TS: smallest runtime, largest amount of hand-rolled diffing/cleanup; highest defect risk in Configure.
- Svelte 5: larger and growing runtime+compiled output for ~40 components, extra compiler in the toolchain, no measurable
  win over signals for this UI.

## Consequences
- ~7 KB gzip of framework; signal-bound updates make speaker/standup switching sub-frame without virtualisation.
- JSX gives compile-time checking of component props and of the generated binding types.
- We own all components (Combobox, Segmented, Switch, Stepper, Toast) and their accessibility; axe checks run in CI.
- No virtualisation: correctness of "nothing is ever hidden" and the below-fold summary relies on real DOM heights;
  `content-visibility` is used as an optimisation with a non-supporting fallback.
