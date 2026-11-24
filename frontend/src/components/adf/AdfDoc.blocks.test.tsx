// WP-T2: AdfDoc block nodes (ux-spec §14.4 node table, architecture §2.4 and §4.9).
//
// SPEC-NOTE (hooks): CSS never loads in jsdom, so every visual rule is asserted through an attribute hook, in the spirit
//   of `data-lane`. The hooks this file pins, which the implementer must emit exactly:
//   - the root element of `decision`, `panel` and `expand` nodes carries data-adf="decision|panel|expand"
//     (the same data-adf="<normalised t>" convention is used for the inline nodes in the other test files);
//   - panels also carry data-panel="info|note|success|warning|error", role="note" and an aria-label naming the type
//     (matched by /info/i etc.), because colour is never the only signal (ux-spec §10);
//   - the <li> of a taskItem carries data-done="true" when done (muted, never struck through); it must not be "true"
//     when not done;
//   - the root element AdfDoc renders (the first child of the container) carries data-size="body|comment|field"
//     (default "body") and the `class` prop.
// SPEC-NOTE (semantics): headings 1-6 are asserted with getByRole("heading", {level}) so native <hN> and aria-level
//   both pass. Task items expose a read-only checkbox: a native <input type=checkbox> (disabled) or role=checkbox with
//   aria-checked, with a non-empty accessible name (axe rule "label"; e.g. aria-labelledby the item text); clicking it
//   never changes the state. Tables use <table>/<tr>/<th>/<td> with colSpan/rowSpan, and
//   colwidth[0] lands as an inline `width` (or min-width) in px on the cell. An expand's toggle is a <button> with
//   aria-expanded; its body is either absent or inside [hidden]/aria-hidden while collapsed.
// SPEC-NOTE (E key): ctx.expandAll = {open, token}: a token the component has not seen forces every expand to `open`;
//   re-rendering with the SAME token never undoes a manual toggle.
// SPEC-NOTE (not asserted): `clampLines` ("Show all", ux-spec §14.6) needs real layout and is not testable in jsdom;
//   `maxW` is a per-node prop and is exercised only through the media tests.
import { describe, expect, it } from "vitest";
import { screen, within } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import {
  adf,
  doc,
  isChecked,
  makeCtx,
  node,
  p,
  renderDoc,
  shown,
  text,
  mark,
} from "./testdata/helpers";

describe("AdfDoc root", () => {
  it("renders one root element carrying data-size=body by default and the class prop", () => {
    const { root } = renderDoc(doc(p(text("Hello"))), makeCtx(), { class: "desc extra" });
    expect(root).not.toBeNull();
    expect(root?.getAttribute("data-size")).toBe("body");
    expect(root?.classList.contains("desc")).toBe(true);
    expect(root?.classList.contains("extra")).toBe(true);
    expect(screen.getByText("Hello")).toBeTruthy();
  });

  it.each(["body", "comment", "field"] as const)("passes size=%s through as data-size", (size) => {
    const { root } = renderDoc(doc(p(text("Sized"))), makeCtx(), { size });
    expect(screen.getByText("Sized")).toBeTruthy();
    expect(root?.getAttribute("data-size")).toBe(size);
  });

  it("renders an empty document as an empty root without throwing", () => {
    const { root } = renderDoc(doc());
    expect(root).not.toBeNull();
    expect(root?.textContent).toBe("");
  });

  it("does not render Doc.u (the unsupported counter) as content", () => {
    const { root } = renderDoc({ c: [p(text("Only this"))], u: 3 });
    expect(root?.textContent).toBe("Only this");
  });
});

describe("paragraph and hardBreak", () => {
  it("renders a paragraph as a <p> and keeps document order", () => {
    renderDoc(doc(p(text("First")), p(text("Second"))));
    const first = screen.getByText("First");
    const second = screen.getByText("Second");
    expect(first.closest("p")).not.toBeNull();
    expect(second.closest("p")).not.toBeNull();
    expect(first.compareDocumentPosition(second) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("renders a hardBreak as a <br> inside the paragraph, between the texts", () => {
    const { container } = renderDoc(doc(p(text("line one"), node("br"), text("line two"))));
    const para = container.querySelector("p") as HTMLElement;
    const br = para.querySelector("br");
    expect(br).not.toBeNull();
    const kids = Array.from(para.childNodes);
    const at = kids.findIndex((k) => k === br || k.contains(br));
    expect(at).toBeGreaterThan(0);
    expect(
      kids
        .slice(0, at)
        .map((k) => k.textContent)
        .join(""),
    ).toBe("line one");
    expect(
      kids
        .slice(at + 1)
        .map((k) => k.textContent)
        .join(""),
    ).toBe("line two");
  });
});

describe("heading", () => {
  it.each([1, 2, 3, 4, 5, 6])("renders level %i as a heading of that level with its text", (level) => {
    renderDoc(doc(node("h", { level }, text(`Heading ${level}`))));
    expect(screen.getByRole("heading", { level, name: `Heading ${level}` })).toBeTruthy();
  });

  it("renders marks inside a heading", () => {
    const { container } = renderDoc(doc(node("h", { level: 2 }, text("Plain "), text("bold", mark("strong")))));
    const h2 = screen.getByRole("heading", { level: 2 });
    expect(h2.textContent).toBe("Plain bold");
    expect(container.querySelector("h2 strong")?.textContent).toBe("bold");
  });
});

describe("bulletList and orderedList", () => {
  it("renders a bullet list as <ul><li> with the item text", () => {
    const { container } = renderDoc(
      doc(node("ul", null, node("li", null, p(text("alpha"))), node("li", null, p(text("beta"))))),
    );
    const items = container.querySelectorAll("ul > li");
    expect(items.length).toBe(2);
    expect(items[0]?.textContent).toBe("alpha");
    expect(items[1]?.textContent).toBe("beta");
  });

  it("renders an ordered list as <ol><li>", () => {
    const { container } = renderDoc(doc(node("ol", { order: 1 }, node("li", null, p(text("one"))), node("li", null, p(text("two"))))));
    expect(container.querySelectorAll("ol > li").length).toBe(2);
  });

  it("starts an ordered list at Attrs.Order when it is not 1", () => {
    const { container } = renderDoc(doc(node("ol", { order: 5 }, node("li", null, p(text("fifth"))))));
    expect(screen.getByText("fifth")).toBeTruthy();
    expect(container.querySelector("ol")?.getAttribute("start")).toBe("5");
  });

  it("treats Order 0 or 1 as the default start", () => {
    const { container } = renderDoc(doc(node("ol", { order: 0 }, node("li", null, p(text("zero"))))));
    expect(screen.getByText("zero")).toBeTruthy();
    const start = container.querySelector("ol")?.getAttribute("start");
    expect(start === null || start === "1").toBe(true);
  });

  it("nests a list inside a list item", () => {
    const { container } = renderDoc(
      doc(
        node(
          "ol",
          { order: 1 },
          node("li", null, p(text("outer")), node("ul", null, node("li", null, p(text("inner"))))),
        ),
      ),
    );
    expect(container.querySelector("ol > li > ul > li")?.textContent).toBe("inner");
    expect(screen.getByText("outer")).toBeTruthy();
  });
});

describe("taskList", () => {
  const taskDoc = () =>
    doc(
      node(
        "task",
        null,
        node("taskItem", { done: true }, text("Shipped")),
        node("taskItem", null, text("Pending")),
        node("taskItem", { done: true }, text("Also shipped, with "), node("status", { label: "OK", lozenge: "done" })),
      ),
    );

  it("renders a read-only checkbox per item with the right checked state, in order", () => {
    renderDoc(taskDoc());
    const boxes = screen.getAllByRole("checkbox");
    expect(boxes.map(isChecked)).toEqual([true, false, true]);
    expect(screen.getByText("Shipped")).toBeTruthy();
    expect(screen.getByText("Pending")).toBeTruthy();
  });

  it("never lets the reader change a checkbox", async () => {
    renderDoc(taskDoc());
    const boxes = screen.getAllByRole("checkbox");
    const before = boxes.map(isChecked);
    for (const b of boxes) await userEvent.click(b);
    expect(screen.getAllByRole("checkbox").map(isChecked)).toEqual(before);
    const readonly = (el: Element) =>
      (el as HTMLInputElement).disabled === true ||
      el.getAttribute("aria-disabled") === "true" ||
      el.getAttribute("aria-readonly") === "true";
    expect(boxes.every(readonly)).toBe(true);
  });

  it("marks done items data-done=true (muted) without any strikethrough, and leaves open items unmarked", () => {
    const { container } = renderDoc(taskDoc());
    const items = Array.from(container.querySelectorAll("li"));
    expect(items.length).toBe(3);
    expect(items.map((li) => li.getAttribute("data-done") === "true")).toEqual([true, false, true]);
    expect(container.querySelector("s, del, strike")).toBeNull();
  });

  it("renders inline nodes inside a task item", () => {
    const { container } = renderDoc(taskDoc());
    const item = container.querySelectorAll("li")[2] as HTMLElement;
    expect(within(item).getByText("OK")).toBeTruthy();
  });
});

describe("decision", () => {
  it("renders its content with a DECISION label on a decision root", () => {
    renderDoc(doc(node("decision", null, p(text("Store the preference server-side")))));
    const root = adf(document.body, "decision");
    expect(root).not.toBeNull();
    expect(within(root as HTMLElement).getByText("Store the preference server-side")).toBeTruthy();
    expect(within(root as HTMLElement).getByText(/^decision$/i)).toBeTruthy();
  });
});

describe("blockquote and rule", () => {
  it("renders a quote as <blockquote> containing its paragraphs", () => {
    const { container } = renderDoc(doc(node("quote", null, p(text("A quoted line")))));
    expect(container.querySelector("blockquote p")?.textContent).toBe("A quoted line");
  });

  it("renders a rule as a separator", () => {
    renderDoc(doc(p(text("above")), node("rule"), p(text("below"))));
    expect(screen.getByRole("separator")).toBeTruthy();
  });
});

describe("codeBlock", () => {
  const CODE = "fun convert(a: Double): Double {\n    return a * 2 // <b>not html</b>\n}";

  it("renders the code text verbatim (newlines and indentation kept) inside pre > code", () => {
    const { container } = renderDoc(doc({ t: "code", x: CODE, a: { lang: "kotlin" } }));
    const code = container.querySelector("pre code");
    expect(code).not.toBeNull();
    expect(code?.textContent).toBe(CODE);
    expect(container.querySelector("pre b")).toBeNull();
  });

  it("shows the language as a tab label", () => {
    renderDoc(doc({ t: "code", x: "x = 1", a: { lang: "python" } }));
    expect(screen.getByText("x = 1")).toBeTruthy();
    expect(screen.getByText(/^python$/i)).toBeTruthy();
  });

  it("shows no language tab when none is authored", () => {
    const { container } = renderDoc(doc({ t: "code", x: "plain text" }));
    expect(container.querySelector("pre code")?.textContent).toBe("plain text");
    expect(container.querySelector("pre")?.parentElement?.textContent).toBe("plain text");
  });
});

describe("panel", () => {
  const KINDS = ["info", "note", "success", "warning", "error"] as const;

  it.each(KINDS)("renders a %s panel with an accessible label, data-panel and its content", (kind) => {
    renderDoc(doc(node("panel", { panel: kind }, p(text(`Body of ${kind}`)))));
    const panel = screen.getByRole("note", { name: new RegExp(kind, "i") });
    expect(panel.getAttribute("data-panel")).toBe(kind);
    expect(adf(document.body, "panel")).toBe(panel);
    expect(within(panel).getByText(`Body of ${kind}`)).toBeTruthy();
  });

  it("gives each panel kind a distinct accessible name", () => {
    renderDoc(doc(...KINDS.map((k) => node("panel", { panel: k }, p(text(`b-${k}`))))));
    const names = screen.getAllByRole("note").map((n) => n.getAttribute("aria-label"));
    expect(names.length).toBe(5);
    expect(new Set(names).size).toBe(5);
  });

  it("renders blocks nested in a panel", () => {
    const { container } = renderDoc(
      doc(node("panel", { panel: "info" }, p(text("intro")), node("ul", null, node("li", null, p(text("point")))))),
    );
    expect(container.querySelector('[role="note"] ul > li')?.textContent).toBe("point");
  });
});

describe("table", () => {
  const tableDoc = () =>
    doc(
      node(
        "table",
        null,
        node(
          "tr",
          { header: true },
          node("th", { colwidth: [210] }, p(text("Type"))),
          node("th", { colspan: 2, colwidth: [146, 260] }, p(text("Both"))),
        ),
        node(
          "tr",
          null,
          node("th", { rowspan: 2 }, p(text("Row head"))),
          node("td", null, p(text("a1"))),
          node("td", null, p(text("a2"))),
        ),
        node("tr", null, node("td", null, p(text("b1"))), node("td", null, p(text("b2")))),
      ),
    );

  it("renders a <table> with header cells as <th> and body cells as <td>", () => {
    const { container } = renderDoc(tableDoc());
    expect(container.querySelector("table")).not.toBeNull();
    expect(container.querySelectorAll("tr").length).toBe(3);
    expect(container.querySelectorAll("th").length).toBe(3);
    expect(container.querySelectorAll("td").length).toBe(4);
    expect(screen.getAllByRole("columnheader").some((c) => c.textContent === "Type")).toBe(true);
    expect(screen.getAllByRole("cell").map((c) => c.textContent)).toEqual(["a1", "a2", "b1", "b2"]);
  });

  it("honours colspan and rowspan", () => {
    renderDoc(tableDoc());
    const both = screen.getByText("Both").closest("th") as HTMLTableCellElement;
    expect(both.colSpan).toBe(2);
    const rowHead = screen.getByText("Row head").closest("th") as HTMLTableCellElement;
    expect(rowHead.rowSpan).toBe(2);
    const plain = screen.getByText("a1").closest("td") as HTMLTableCellElement;
    expect(plain.colSpan).toBe(1);
    expect(plain.rowSpan).toBe(1);
  });

  it("applies the authored colwidth as a px width on the cell", () => {
    renderDoc(tableDoc());
    const th = screen.getByText("Type").closest("th") as HTMLElement;
    expect(th.style.width === "210px" || th.style.minWidth === "210px").toBe(true);
  });

  it("renders marks and inline nodes inside cells", () => {
    const { container } = renderDoc(
      doc(node("table", null, node("tr", null, node("td", null, p(text("x "), node("status", { label: "NEW", lozenge: "prog" })))))),
    );
    expect(within(container.querySelector("td") as HTMLElement).getByText("NEW")).toBeTruthy();
  });
});

describe("expand and nestedExpand", () => {
  const expandDoc = (collapsed: boolean | undefined) =>
    doc(node("expand", { title: "Density coverage", collapsed }, p(text("Measured on 1,200 recipes"))));

  it("renders a collapsed expand as a closed toggle button with the title and no visible body", () => {
    const { container } = renderDoc(expandDoc(true));
    const btn = screen.getByRole("button", { name: /density coverage/i });
    expect(btn.getAttribute("aria-expanded")).toBe("false");
    expect(adf(container, "expand")).not.toBeNull();
    expect(shown(container, "Measured on 1,200 recipes")).toBe(false);
  });

  it("renders an expand authored open with its body visible", () => {
    const { container } = renderDoc(expandDoc(undefined));
    const btn = screen.getByRole("button", { name: /density coverage/i });
    expect(btn.getAttribute("aria-expanded")).toBe("true");
    expect(shown(container, "Measured on 1,200 recipes")).toBe(true);
  });

  it("toggles open and closed on click", async () => {
    const { container } = renderDoc(expandDoc(true));
    const btn = screen.getByRole("button", { name: /density coverage/i });
    await userEvent.click(btn);
    expect(btn.getAttribute("aria-expanded")).toBe("true");
    expect(shown(container, "Measured on 1,200 recipes")).toBe(true);
    await userEvent.click(btn);
    expect(btn.getAttribute("aria-expanded")).toBe("false");
    expect(shown(container, "Measured on 1,200 recipes")).toBe(false);
  });

  it("toggles from the keyboard", async () => {
    renderDoc(expandDoc(true));
    const btn = screen.getByRole("button", { name: /density coverage/i });
    btn.focus();
    await userEvent.keyboard("{Enter}");
    expect(btn.getAttribute("aria-expanded")).toBe("true");
  });

  it("renders a nested expand (inside a table cell) as its own toggle", () => {
    const { container } = renderDoc(
      doc(
        node(
          "table",
          null,
          node("tr", null, node("td", null, node("expand", { title: "Inner", collapsed: true }, p(text("hidden inner"))))),
        ),
      ),
    );
    expect(container.querySelector("td")?.contains(screen.getByRole("button", { name: /inner/i }))).toBe(true);
    expect(shown(container, "hidden inner")).toBe(false);
  });

  describe("expandAll (the E key)", () => {
    const twoExpands = () =>
      doc(
        node("expand", { title: "One", collapsed: true }, p(text("body one"))),
        node("expand", { title: "Two" }, p(text("body two"))),
      );
    const states = () => screen.getAllByRole("button").map((b) => b.getAttribute("aria-expanded"));

    it("a new token opens every expand, then a newer token closes every expand", () => {
      const base = makeCtx();
      const r = renderDoc(twoExpands(), base);
      expect(states()).toEqual(["false", "true"]);
      r.rerenderDoc({ ctx: { ...base, expandAll: { open: true, token: 1 } } });
      expect(states()).toEqual(["true", "true"]);
      expect(shown(r.container, "body one")).toBe(true);
      r.rerenderDoc({ ctx: { ...base, expandAll: { open: false, token: 2 } } });
      expect(states()).toEqual(["false", "false"]);
      expect(shown(r.container, "body two")).toBe(false);
    });

    it("re-rendering with the same token keeps a manual toggle", async () => {
      const base = makeCtx();
      const r = renderDoc(twoExpands(), base);
      r.rerenderDoc({ ctx: { ...base, expandAll: { open: true, token: 7 } } });
      await userEvent.click(screen.getAllByRole("button")[0] as HTMLElement);
      expect(states()).toEqual(["false", "true"]);
      r.rerenderDoc({ ctx: { ...base, expandAll: { open: true, token: 7 } } });
      expect(states()).toEqual(["false", "true"]);
    });
  });
});
