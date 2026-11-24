// WP-10: Icon (ux-spec §5.4: 18px, stroke 2.4, colour via the token; architecture §4.6: inline SVG).
//
// SPEC-NOTE: every assertion first requires an <svg> to exist (the stub renders null), so each test fails until the
//   component renders.
// SPEC-NOTE: size is asserted on the root <svg> `width`/`height` attributes; strokeWidth on the svg markup as a
//   `stroke-width` attribute (on the root svg or any descendant, so a <g> or sprite wrapper is fine). Colour comes from
//   `currentColor`, so the output must never contain a literal hex colour.
// SPEC-NOTE: with a `title` the svg has role="img" and a <title> child with that text and no aria-hidden="true";
//   without a title it has aria-hidden="true", no <title> and no role="img".
// SPEC-NOTE: an empty-string title counts as no title (aria-hidden, no <title>).
// SPEC-NOTE (invariant 11, sizes scale with --scale): size is pinned as the px `width`/`height` attributes (default 18),
//   as the IconProps contract says "px". Scaling for Large (TV) is then the caller's job (pass size × scale, or CSS
//   `width/height: calc(var(--scale) * …)` overriding the attributes); the orchestrator should confirm this reading.
// SPEC-NOTE: every icon must render distinct markup (guards a placeholder glyph for all names).
import { render } from "@testing-library/preact";
import { describe, expect, it } from "vitest";
import { Icon } from "./Icon";
import type { IconName } from "./props";

// Record<IconName, true> makes tsc fail if props.ts gains or loses a name without this list following.
const NAME_SET: Record<IconName, true> = {
  story: true,
  bug: true,
  task: true,
  spike: true,
  other: true,
  sliders: true,
  resync: true,
  close: true,
  plus: true,
  "chevron-down": true,
  "chevron-up": true,
  board: true,
  external: true,
  download: true,
};
const NAMES = Object.keys(NAME_SET) as IconName[];

function renderSvg(props: Parameters<typeof Icon>[0]): SVGSVGElement {
  const { container } = render(<Icon {...props} />);
  const svg = container.querySelector("svg");
  expect(svg, `no <svg> rendered for ${props.name}`).not.toBeNull();
  return svg as SVGSVGElement;
}

describe("Icon: renders every name", () => {
  it.each(NAMES)("renders an <svg> for %s", (name) => {
    const svg = renderSvg({ name });
    expect(svg.tagName.toLowerCase()).toBe("svg");
    expect(svg.getAttribute("viewBox")).toBeTruthy();
  });

  it("renders distinct markup for every name", () => {
    const seen = new Map<string, IconName>();
    for (const name of NAMES) {
      const html = renderSvg({ name }).innerHTML;
      expect(html.length, `${name} rendered an empty svg`).toBeGreaterThan(0);
      expect(seen.get(html), `${name} duplicates ${seen.get(html)}`).toBeUndefined();
      seen.set(html, name);
    }
  });
});

describe("Icon: size and stroke", () => {
  it("defaults to 18px", () => {
    const svg = renderSvg({ name: "story" });
    expect(svg.getAttribute("width")).toBe("18");
    expect(svg.getAttribute("height")).toBe("18");
  });

  it("applies a custom size to width and height", () => {
    const svg = renderSvg({ name: "bug", size: 32 });
    expect(svg.getAttribute("width")).toBe("32");
    expect(svg.getAttribute("height")).toBe("32");
  });

  it("defaults the stroke width to 2.4", () => {
    const svg = renderSvg({ name: "task" });
    expect(svg.outerHTML).toContain('stroke-width="2.4"');
  });

  it("applies a custom stroke width and drops the default", () => {
    const svg = renderSvg({ name: "task", strokeWidth: 1.5 });
    expect(svg.outerHTML).toContain('stroke-width="1.5"');
    expect(svg.outerHTML).not.toContain('stroke-width="2.4"');
  });

  it("takes its colour from currentColor and contains no raw hex", () => {
    for (const name of NAMES) {
      const html = renderSvg({ name }).outerHTML;
      expect(html, name).toContain("currentColor");
      expect(html, name).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    }
  });

  it("passes the class through to the svg", () => {
    const svg = renderSvg({ name: "close", class: "row-icon" });
    expect(svg.classList.contains("row-icon")).toBe(true);
  });
});

describe("Icon: accessibility", () => {
  it("is aria-hidden without a title and has no <title> or img role", () => {
    for (const name of NAMES) {
      const svg = renderSvg({ name });
      expect(svg.getAttribute("aria-hidden"), name).toBe("true");
      expect(svg.getAttribute("role"), name).not.toBe("img");
      expect(svg.querySelector("title"), name).toBeNull();
    }
  });

  it('with a title it is role="img" with a <title> child and is not aria-hidden', () => {
    const svg = renderSvg({ name: "board", title: "Partial sync" });
    expect(svg.getAttribute("role")).toBe("img");
    expect(svg.getAttribute("aria-hidden")).not.toBe("true");
    const title = svg.querySelector("title");
    expect(title).not.toBeNull();
    expect(title?.textContent).toBe("Partial sync");
  });

  it("exposes the title as the accessible image name", () => {
    const { getByRole } = render(<Icon name="resync" title="Resync (R)" />);
    expect(getByRole("img", { name: "Resync (R)" })).toBeTruthy();
  });

  it("treats an empty title like no title", () => {
    const svg = renderSvg({ name: "close", title: "" });
    expect(svg.getAttribute("aria-hidden")).toBe("true");
    expect(svg.querySelector("title")).toBeNull();
  });
});
