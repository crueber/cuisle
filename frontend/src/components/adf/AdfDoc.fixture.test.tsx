// WP-T2: AdfDoc safety (invariant 14, never innerHTML) and the full MOB-1421 description render summary.
//
// SPEC-NOTE (safety): "no innerHTML" is asserted three ways: (1) every string a node can carry (text, code, titles,
//   labels, names, captions, hosts) containing markup renders as literal text and creates no <b>/<i>/<script>/<img>
//   elements and no script side effect; (2) a static scan of the production files in this directory forbids
//   dangerouslySetInnerHTML, innerHTML, outerHTML, insertAdjacentHTML, document.write, eval and new Function; (3) the
//   same scan forbids raw hex colours in components and CSS (tokens only, invariant 11), with contrast.ts exempt
//   because the textColor clamp must parse and emit authored colours. Scan (2) and (3) pass against the stub (there is
//   nothing to forbid yet); they are regression guards, and the behavioural tests are what fail until WP-T2 lands.
// SPEC-NOTE (snapshot): a toMatchSnapshot() recorded against a null stub would be an empty, meaningless golden and
//   __snapshots__/ is outside this WP's allowed paths. Instead the full MOB-1421 description (the fixture
//   frontend/src/api/fixtures/ticket_mob1421.json, every node of ux-spec §14.4) is rendered and compared to a literal
//   structural summary written from the fixture (headings, checkbox states, table shape, media srcs, chips, hooks, the
//   collapsed expand). WP-T2 may add a toMatchSnapshot() after its first green run; that golden needs review.
// SPEC-NOTE (a11y): axe-core runs on the full render with the rules that need real layout (color-contrast, region)
//   switched off; the Playwright axe job covers contrast in both themes.
import { readdirSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import axe from "axe-core";
import { screen } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import ticket1421 from "../../api/fixtures/ticket_mob1421.json";
import type { AdfDoc as AdfDocT } from "../../api/types";
import { adf, adfAll, doc, interactive, isChecked, makeCtx, mark, node, p, renderDoc, shown, text } from "./testdata/helpers";

const EVIL = "<b>x</b><i>y</i><img src=x onerror=alert(1)><script>window.__pwn=1</script>";

describe("markup in content is always text", () => {
  it("renders a literal <b>x</b> in a paragraph as text, not a <b> element", () => {
    const { container } = renderDoc(doc(p(text("<b>x</b>"))));
    expect(screen.getByText("<b>x</b>")).toBeTruthy();
    expect(container.querySelector("b")).toBeNull();
  });

  it("never turns any string a node carries into elements", () => {
    const ctx = makeCtx();
    const d = doc(
      p(text(EVIL)),
      node("h", { level: 2 }, text(EVIL)),
      p(text(EVIL, mark("strong"), mark("em"))),
      { t: "code", x: EVIL, a: { lang: EVIL } },
      node("quote", null, p(text(EVIL))),
      node("ul", null, node("li", null, p(text(EVIL)))),
      node("task", null, node("taskItem", { done: true }, text(EVIL))),
      node("expand", { title: EVIL }, p(text(EVIL))),
      node("panel", { panel: "info" }, p(text(EVIL))),
      node("table", null, node("tr", null, node("td", null, p(text(EVIL))))),
      p(
        node("status", { label: EVIL, lozenge: "done" }),
        node("mention", { accountId: "a", label: EVIL }),
        node("emoji", { shortName: EVIL }),
        node("card", { issueId: 1, key: EVIL, summary: EVIL, statusName: EVIL, statusLozenge: "prog" }),
        node("card", { url: "https://x.example/y", host: EVIL, path: EVIL }),
      ),
      node("media", { attachmentId: "9", name: EVIL, caption: EVIL, width: 10, height: 10, cached: "full", block: true }),
      node("file", { attachmentId: "10", name: EVIL, cached: "none" }),
      { t: "unknown", x: EVIL },
    );
    const { container } = renderDoc(d, ctx);
    // positive: the strings really are on the page
    expect(screen.getAllByText(EVIL, { exact: false }).length).toBeGreaterThanOrEqual(8);
    // negative: no element was created from them
    expect(container.querySelector("b, i, script")).toBeNull();
    const srcs = Array.from(container.querySelectorAll("img")).map((i) => i.getAttribute("src") ?? "");
    expect(srcs.length).toBe(1);
    expect(srcs[0]).toMatch(/\/media\/9$/);
    expect((window as unknown as { __pwn?: number }).__pwn).toBeUndefined();
  });
});

describe("production files in components/adf", () => {
  // vitest runs from frontend/ (import.meta.url is not a file: URL under jsdom).
  const dir = resolve(process.cwd(), "src/components/adf") + "/";
  const files = readdirSync(dir).filter((f) => /\.(tsx?|css)$/.test(f) && !/\.test\.tsx?$/.test(f) && f !== "props.ts");
  const stripComments = (s: string) => s.replace(/\/\*[\s\S]*?\*\//g, "").replace(/(^|[^:])\/\/.*$/gm, "$1");

  it("includes AdfDoc.tsx (the root component named by props.ts)", () => {
    expect(files).toContain("AdfDoc.tsx");
  });

  it.each(["dangerouslySetInnerHTML", "innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval(", "new Function"])(
    "never uses %s",
    (banned) => {
      for (const f of files) expect(stripComments(readFileSync(dir + f, "utf8")), `${f} uses ${banned}`).not.toContain(banned);
    },
  );

  it("has no raw hex colours outside contrast.ts (tokens only)", () => {
    for (const f of files.filter((n) => n !== "contrast.ts")) {
      expect(stripComments(readFileSync(dir + f, "utf8")), `${f} has a raw hex colour`).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    }
  });
});

describe("MOB-1421 description (every node of ux-spec §14.4)", () => {
  const description = ticket1421.description as unknown as AdfDocT;
  const level = (el: HTMLElement) => Number(el.getAttribute("aria-level") ?? el.tagName.slice(1));

  it("renders the expected structure", () => {
    const ctx = makeCtx();
    const { container, root } = renderDoc(description, ctx);
    expect(root).not.toBeNull();

    const summary = {
      headings: screen.getAllByRole("heading").map((h) => [level(h), h.textContent]),
      checkboxes: screen.getAllByRole("checkbox").map(isChecked),
      statuses: adfAll(container, "status")
        .filter((el) => !el.closest('[data-adf="card"]'))
        .map((el) => [el.textContent?.trim(), el.getAttribute("data-lozenge")]),
      mentions: adfAll(container, "mention").map((el) => [el.textContent, el.getAttribute("data-self") === "true"]),
      dates: adfAll(container, "date").map((el) => [
        (el.matches("time") ? el : el.querySelector("time"))?.getAttribute("datetime"),
        el.getAttribute("data-past") === "true",
      ]),
      emoji: adfAll(container, "emoji").map((el) => el.textContent),
      cards: adfAll(container, "card").map((el) => el.getAttribute("data-block") === "true"),
      images: Array.from(container.querySelectorAll("img")).map((i) => i.getAttribute("src")?.replace(/^.*(\/media\/)/, "$1")),
      files: adfAll(container, "file").length,
      unknown: adfAll(container, "unknown").length,
      table: {
        tr: container.querySelectorAll("table tr").length,
        th: container.querySelectorAll("table th").length,
        td: container.querySelectorAll("table td").length,
      },
      blockquotes: container.querySelectorAll("blockquote").length,
      separators: screen.getAllByRole("separator").length,
      codeBlocks: container.querySelectorAll("pre code").length,
      decisions: adfAll(container, "decision").length,
      panels: screen.getAllByRole("note").map((n) => n.getAttribute("data-panel")),
      expands: screen.getAllByRole("button", { name: /density coverage/i }).map((b) => b.getAttribute("aria-expanded")),
      orderedItems: container.querySelectorAll("ol > li").length,
      nestedBullets: container.querySelectorAll("ol ul > li").length,
    };

    expect(summary).toEqual({
      headings: [
        [2, "Acceptance criteria"],
        [3, "Conversion rules"],
        [4, "Edge cases"],
      ],
      checkboxes: [true, true, true, false, false],
      statuses: [
        ["METRIC", "done"],
        ["US", "prog"],
        ["NEW", "prog"],
      ],
      mentions: [["@Silas Marchetti", false]],
      dates: [
        ["2026-12-01", false],
        ["2026-11-18", true],
      ],
      emoji: ["\u{1f9c1}"],
      cards: [false, false],
      images: ["/media/201", "/media/202/thumb"],
      files: 1,
      unknown: 1,
      table: { tr: 5, th: 8, td: 12 },
      blockquotes: 1,
      separators: 1,
      codeBlocks: 1,
      decisions: 1,
      panels: ["info"],
      expands: ["false"],
      orderedItems: 2,
      nestedBullets: 2,
    });
  });

  it("shows the visible prose and keeps the collapsed expand body out of sight", () => {
    const { container } = renderDoc(description);
    expect(shown(container, "Admin › Ingredients")).toBe(true);
    expect(shown(container, "rounding rules")).toBe(true);
    expect(shown(container, "Unit toggle, v3: metric and US side by side")).toBe(true);
    expect(shown(container, "unit-spec.pdf")).toBe(true);
    expect(shown(container, /Embedded Loom video/)).toBe(true);
    expect(shown(container, /Measured on 1,200 recipes/)).toBe(false);
    expect(container.querySelector("pre code")?.textContent).toMatch(/^fun convert\(amount: Double/);
  });

  it("reveals the expand body (with its colour mark, line break and code mark) when opened", async () => {
    const { container } = renderDoc(description);
    await userEvent.click(screen.getByRole("button", { name: /density coverage/i }));
    expect(shown(container, /Measured on 1,200 recipes/)).toBe(true);
    const six = screen.getByText("6%");
    expect(six.closest("[style]")).not.toBeNull();
    expect(screen.getByText("./gradlew :recipes:densityReport").closest("code")).not.toBeNull();
    expect(adf(container, "expand")?.querySelector("br")).not.toBeNull();
  });

  it("routes the interactive nodes through the context", async () => {
    const ctx = makeCtx();
    const { container } = renderDoc(description, ctx);
    await userEvent.click(screen.getByRole("link", { name: "rounding rules" }));
    expect(ctx.onOpenUrl).toHaveBeenLastCalledWith("https://docs.example.com/recipes/rounding");
    const [issueCard, urlCard] = adfAll(container, "card") as [HTMLElement, HTMLElement];
    await userEvent.click(interactive(issueCard));
    expect(ctx.onOpenIssue).toHaveBeenLastCalledWith(900);
    await userEvent.click(interactive(urlCard));
    expect(ctx.onOpenUrl).toHaveBeenLastCalledWith("https://docs.example.com/recipes/units-v2");
  });

  it("has no axe violations (layout-dependent rules off)", async () => {
    const { container } = renderDoc(description);
    expect(screen.getAllByRole("heading").length).toBe(3);
    const results = await axe.run(container, { rules: { "color-contrast": { enabled: false }, region: { enabled: false } } });
    expect(results.violations.map((v) => v.id)).toEqual([]);
  });
});
