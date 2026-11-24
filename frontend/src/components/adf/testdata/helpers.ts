// Shared builders and helpers for the WP-T2 ADF component tests. No production code.
import { h } from "preact";
import { render } from "@testing-library/preact";
import { vi, type Mock } from "vitest";
import type { AdfDoc as AdfDocT, AdfNode, AdfMark, AdfAttrs } from "../../../api/types";
import { AdfDoc } from "../AdfDoc";
import type { AdfContext, AdfDocProps } from "../props";

export const ISSUE_URL = "https://example.atlassian.net/browse/MOB-1421";

// ── node builders ──
export const text = (x: string, ...m: AdfMark[]): AdfNode => (m.length ? { t: "text", x, m } : { t: "text", x });
export const mark = (t: string, v?: string): AdfMark => (v === undefined ? { t } : { t, v });
export const node = (t: string, a?: AdfAttrs | null, ...c: AdfNode[]): AdfNode => {
  const n: AdfNode = { t };
  if (a) n.a = a;
  if (c.length) n.c = c;
  return n;
};
export const p = (...c: AdfNode[]): AdfNode => node("p", null, ...c);
export const doc = (...c: AdfNode[]): AdfDocT => ({ c });

// ── context with spies ──
export interface MockCtx extends AdfContext {
  onOpenIssue: Mock;
  onOpenUrl: Mock;
  onOpenImage: Mock;
  onOpenFile: Mock;
  onLoadImage: Mock;
}

export function makeCtx(over: Partial<AdfContext> = {}): MockCtx {
  return {
    onOpenIssue: vi.fn(),
    onOpenUrl: vi.fn(),
    onOpenImage: vi.fn(),
    onOpenFile: vi.fn(),
    onLoadImage: vi.fn(),
    issueUrl: ISSUE_URL,
    theme: "light",
    ...over,
  } as MockCtx;
}

export function renderDoc(d: AdfDocT, ctx: AdfContext = makeCtx(), extra: Partial<AdfDocProps> = {}) {
  const props: AdfDocProps = { doc: d, ctx, ...extra };
  const result = render(h(AdfDoc, props));
  return {
    ...result,
    root: result.container.firstElementChild as HTMLElement | null,
    rerenderDoc: (next: Partial<AdfDocProps>) => result.rerender(h(AdfDoc, { ...props, ...next })),
  };
}

// ── DOM helpers ──
/** The first element carrying data-adf="<t>" (the per-node testing/styling hook; see SPEC-NOTEs in the test files). */
export const adf = (root: ParentNode, t: string): HTMLElement | null => root.querySelector(`[data-adf="${t}"]`);
export const adfAll = (root: ParentNode, t: string): HTMLElement[] => Array.from(root.querySelectorAll(`[data-adf="${t}"]`));

const INTERACTIVE = 'a, button, [role="button"], [role="link"]';
/** The element a user clicks: the node root itself when interactive, else its first interactive descendant. */
export function interactive(el: HTMLElement): HTMLElement {
  if (el.matches(INTERACTIVE)) return el;
  const inner = el.querySelector<HTMLElement>(INTERACTIVE);
  if (!inner) throw new Error(`no interactive element in <${el.tagName.toLowerCase()} data-adf=${el.getAttribute("data-adf")}>`);
  return inner;
}

/** True when the text is rendered (own text of some element) and not inside a hidden / aria-hidden subtree. */
export function shown(root: ParentNode, textMatch: string | RegExp): boolean {
  const all = Array.from(root.querySelectorAll<HTMLElement>("*")).filter((el) => {
    const own = Array.from(el.childNodes)
      .filter((c) => c.nodeType === 3)
      .map((c) => c.textContent ?? "")
      .join("");
    return typeof textMatch === "string" ? own.trim() === textMatch : textMatch.test(own);
  });
  return all.some((el) => !el.closest('[hidden], [aria-hidden="true"]'));
}

export function isChecked(el: Element): boolean {
  return (el as HTMLInputElement).checked === true || el.getAttribute("aria-checked") === "true";
}

// ── colour maths (WCAG 2.x), for the textColor clamp ──
export type RGB = [number, number, number];
export function parseCssColor(s: string): RGB {
  const hex = /^#([0-9a-f]{6})$/i.exec(s.trim());
  if (hex) {
    const v = hex[1] as string;
    return [parseInt(v.slice(0, 2), 16), parseInt(v.slice(2, 4), 16), parseInt(v.slice(4, 6), 16)];
  }
  const rgb = /^rgba?\(\s*(\d+)[,\s]+(\d+)[,\s]+(\d+)/i.exec(s.trim());
  if (rgb) return [Number(rgb[1]), Number(rgb[2]), Number(rgb[3])];
  throw new Error(`textColor output must be an sRGB hex or rgb() colour, got "${s}"`);
}
const lin = (c: number) => {
  const v = c / 255;
  return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
};
export const luminance = ([r, g, b]: RGB) => 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b);
export function contrast(a: RGB, b: RGB): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x) as [number, number];
  return (hi + 0.05) / (lo + 0.05);
}
export function hue([r, g, b]: RGB): number {
  const [R, G, B] = [r / 255, g / 255, b / 255];
  const max = Math.max(R, G, B);
  const d = max - Math.min(R, G, B);
  if (d === 0) return 0;
  const hh = max === R ? ((G - B) / d) % 6 : max === G ? (B - R) / d + 2 : (R - G) / d + 4;
  return (hh * 60 + 360) % 360;
}
/** Page backgrounds the clamp runs against (design/tokens.json color.light.bg / color.dark.bg). */
export const BG: Record<"light" | "dark", RGB> = { light: [0xf6, 0xf4, 0xee], dark: [0x12, 0x14, 0x18] };
