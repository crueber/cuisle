// WP-T2: AdfDoc marks and inline nodes (ux-spec §14.4: strong/em/strike/underline/sub/sup, textColor, link, code,
// status, mention, date, emoji, inlineCard/blockCard; architecture §4.9).
//
// SPEC-NOTE (marks): native semantics: strong -> <strong>, em -> <em>, strike -> <s> (or <del>/<strike>), underline ->
//   <u>, sub -> <sub>, sup -> <sup>, code -> <code> (distinct from the codeBlock's pre > code), link -> <a href>. Marks
//   nest, so one text node can sit inside several of these.
// SPEC-NOTE (links): "link opens externally via the api" means the host's ctx.onOpenUrl(href) is called and the click's
//   default is prevented (the webview never navigates), also for modifier clicks. The <a> keeps its real href attribute
//   so it is focusable and has role=link. A link mark never calls ctx.onOpenIssue (only cards with issueId > 0 do).
// SPEC-NOTE (textColor): the clamp is verified through the rendered inline `style.color` of the element holding the text.
//   It must be an sRGB hex or rgb() string (jsdom drops oklch()), so the component converts back to sRGB. The tests do not
//   pin the exact output. They assert: contrast against the theme page background (design/tokens.json color.light.bg
//   #F6F4EE, color.dark.bg #121418) is >= 4.5 after the clamp; a colour that already passes is returned unchanged
//   (same RGB); a colour that fails changes; the hue is kept (HSL hue within 25 degrees). The same authored colour must
//   give different output per ctx.theme when it passes on one theme only.
// SPEC-NOTE (hooks): status/mention/date/emoji/card carry data-adf="<t>" on their root. Further attributes:
//   status data-lozenge=todo|prog|review|qa|done|alert; mention data-self="true" only for the connected user; date
//   data-past="true" only when Attrs.Past (the component never compares with the clock; Go computes Past); card
//   data-block="true" for blockCard. Visual rules (muted, inverted, alert-text) are CSS keyed on these.
// SPEC-NOTE (date): a date renders as <time dateTime="YYYY-MM-DD"> (itself or inside the data-adf root). The visible
//   format is not pinned (ux-spec only says "mono chip"), only that it is non-empty.
// SPEC-NOTE (mention): visible text is "@" + Attrs.Label (Go gives the name without "@"; mockup shows "@Silas Marchetti").
// SPEC-NOTE (cards): a card resolved to a synced issue (issueId > 0) shows the mono key, the summary and a status
//   lozenge (data-lozenge = statusLozenge, text = statusName) and a click calls ctx.onOpenIssue(issueId) (a number),
//   never onOpenUrl. A URL card shows host and path (no lozenge) and a click calls onOpenUrl(url).
import { describe, expect, it } from "vitest";
import { fireEvent, screen, within } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import {
  BG,
  adf,
  adfAll,
  contrast,
  doc,
  hue,
  interactive,
  makeCtx,
  mark,
  node,
  p,
  parseCssColor,
  renderDoc,
  text,
} from "./testdata/helpers";

describe("marks", () => {
  const cases: [string, string, string][] = [
    ["strong", "strong", "strong"],
    ["em", "em", "em"],
    ["underline", "u", "u"],
    ["sub", "sub", "sub"],
    ["sup", "sup", "sup"],
    ["code", "code", "code"],
  ];
  it.each(cases)("renders the %s mark as <%s>", (markType, _tag, selector) => {
    const { container } = renderDoc(doc(p(text("before "), text("marked", mark(markType)), text(" after"))));
    const el = screen.getByText("marked");
    expect(el.closest(selector)).not.toBeNull();
    expect(container.querySelector("pre")).toBeNull();
    expect(screen.getByText(/before/).closest(selector)).toBeNull();
  });

  it("renders the strike mark as <s>, <del> or <strike>", () => {
    renderDoc(doc(p(text("gone", mark("strike")), text(" kept"))));
    expect(screen.getByText("gone").closest("s, del, strike")).not.toBeNull();
    expect(screen.getByText(/kept/).closest("s, del, strike")).toBeNull();
  });

  it("nests several marks on one text", () => {
    renderDoc(doc(p(text("loud and slanted", mark("strong"), mark("em")))));
    const el = screen.getByText("loud and slanted");
    expect(el.closest("strong")).not.toBeNull();
    expect(el.closest("em")).not.toBeNull();
  });

  it("renders sub and sup with their text (g/cm3, H2O)", () => {
    renderDoc(doc(p(text("g/cm"), text("3", mark("sup")), text(" H"), text("2", mark("sub")), text("O"))));
    expect(screen.getByText("3").tagName).toBe("SUP");
    expect(screen.getByText("2").tagName).toBe("SUB");
  });
});

describe("link mark", () => {
  const HREF = "https://docs.example.com/recipes/rounding";
  const linkDoc = () => doc(p(text("see "), text("rounding rules", mark("link", HREF)), text(".")));

  it("renders an <a> with the href as a link role and the link text", () => {
    renderDoc(linkDoc());
    const a = screen.getByRole("link", { name: "rounding rules" });
    expect(a.getAttribute("href")).toBe(HREF);
  });

  it("opens through ctx.onOpenUrl and prevents navigation", () => {
    const ctx = makeCtx();
    renderDoc(linkDoc(), ctx);
    const a = screen.getByRole("link", { name: "rounding rules" });
    const notPrevented = fireEvent.click(a);
    expect(notPrevented).toBe(false);
    expect(ctx.onOpenUrl).toHaveBeenCalledTimes(1);
    expect(ctx.onOpenUrl).toHaveBeenCalledWith(HREF);
    expect(ctx.onOpenIssue).not.toHaveBeenCalled();
  });

  it("also prevents navigation for modifier clicks", () => {
    const ctx = makeCtx();
    renderDoc(linkDoc(), ctx);
    const a = screen.getByRole("link", { name: "rounding rules" });
    expect(fireEvent.click(a, { metaKey: true })).toBe(false);
    expect(fireEvent.click(a, { ctrlKey: true })).toBe(false);
  });

  it("opens from the keyboard (Enter on the focused link)", async () => {
    const ctx = makeCtx();
    renderDoc(linkDoc(), ctx);
    screen.getByRole("link", { name: "rounding rules" }).focus();
    await userEvent.keyboard("{Enter}");
    expect(ctx.onOpenUrl).toHaveBeenCalledWith(HREF);
  });

  it("combines with other marks", () => {
    renderDoc(doc(p(text("bold link", mark("strong"), mark("link", HREF)))));
    const el = screen.getByText("bold link");
    expect(el.closest("a")).not.toBeNull();
    expect(el.closest("strong")).not.toBeNull();
  });
});

describe("color mark (textColor contrast clamp)", () => {
  // Colours are written as numbers: the components/** lint rule forbids "#rrggbb" string literals (tokens only).
  const css = (n: number): string => "#" + n.toString(16).padStart(6, "0");
  const rgb = (n: number): [number, number, number] => [(n >> 16) & 255, (n >> 8) & 255, n & 255];
  const BROWN_RED = 0xbf2600; // 5.47:1 on the light page, 3.07:1 on the dark page
  const DEEP_BLUE = 0x0747a6; // 7.76:1 light, 2.16:1 dark
  const PALE_YELLOW = 0xffe380; // 1.15:1 light, 14.5:1 dark
  const AMBER = 0xffc400; // 1.45:1 light
  const MINT = 0x79f2c0; // 1.25:1 light
  const NAVY = 0x172b4d; // 1.31:1 dark
  const LIGHT_GREY = 0xcccccc;

  function renderedColor(authored: number, theme: "light" | "dark") {
    const { unmount } = renderDoc(doc(p(text("tinted", mark("color", css(authored))))), makeCtx({ theme }));
    const el = screen.getByText("tinted");
    const holder = el.style.color ? el : (el.closest("[style]") as HTMLElement | null);
    expect(holder?.style.color, `no inline colour on the text for ${css(authored)} (${theme})`).toBeTruthy();
    const out = parseCssColor((holder as HTMLElement).style.color);
    unmount();
    return out;
  }

  it("changes nothing when the colour already meets 4.5:1 (brown-red on light)", () => {
    expect(renderedColor(BROWN_RED, "light")).toEqual(rgb(BROWN_RED));
  });

  it("keeps a dark colour unchanged on the light theme (deep blue)", () => {
    expect(renderedColor(DEEP_BLUE, "light")).toEqual(rgb(DEEP_BLUE));
  });

  it("keeps a light colour unchanged on the dark theme (pale yellow)", () => {
    expect(renderedColor(PALE_YELLOW, "dark")).toEqual(rgb(PALE_YELLOW));
  });

  it.each([
    ["pale yellow", PALE_YELLOW, "light"],
    ["amber", AMBER, "light"],
    ["mint", MINT, "light"],
    ["deep blue", DEEP_BLUE, "dark"],
    ["brown-red", BROWN_RED, "dark"],
    ["navy", NAVY, "dark"],
  ] as const)("adjusts %s on the %s theme to >= 4.5:1, keeping its hue", (_name, authored, theme) => {
    const out = renderedColor(authored, theme);
    expect(contrast(out, BG[theme])).toBeGreaterThanOrEqual(4.5);
    expect(out).not.toEqual(rgb(authored));
    const d = Math.abs(hue(out) - hue(rgb(authored)));
    expect(Math.min(d, 360 - d)).toBeLessThanOrEqual(25);
  });

  it("gives one authored colour different output on each theme when it only passes on one", () => {
    expect(renderedColor(DEEP_BLUE, "light")).not.toEqual(renderedColor(DEEP_BLUE, "dark"));
  });

  it("adjusts a neutral grey too (on light)", () => {
    const out = renderedColor(LIGHT_GREY, "light");
    expect(contrast(out, BG.light)).toBeGreaterThanOrEqual(4.5);
  });

  it("keeps the text itself and any other marks", () => {
    renderDoc(doc(p(text("tinted bold", mark("strong"), mark("color", css(PALE_YELLOW))))));
    const el = screen.getByText("tinted bold");
    expect(el.closest("strong")).not.toBeNull();
    expect(el.closest("[style]")).not.toBeNull();
  });
});

describe("status lozenge", () => {
  it.each(["todo", "prog", "review", "qa", "done", "alert"])("renders the %s lozenge with its label and data-lozenge", (lozenge) => {
    renderDoc(doc(p(text("state "), node("status", { label: "METRIC", lozenge }))));
    const el = adf(document.body, "status") as HTMLElement;
    expect(el).not.toBeNull();
    expect(el.textContent?.trim()).toBe("METRIC");
    expect(el.getAttribute("data-lozenge")).toBe(lozenge);
  });
});

describe("mention", () => {
  it("renders @label on a mention root and leaves other people's mentions un-inverted", () => {
    renderDoc(doc(p(text("owner "), node("mention", { accountId: "silas", label: "Silas Marchetti" }))));
    const el = adf(document.body, "mention") as HTMLElement;
    expect(el).not.toBeNull();
    expect(el.textContent).toBe("@Silas Marchetti");
    expect(el.getAttribute("data-self")).not.toBe("true");
  });

  it("marks a mention of the connected user data-self=true", () => {
    renderDoc(doc(p(node("mention", { accountId: "me", label: "Dev User", self: true }))));
    const el = adf(document.body, "mention") as HTMLElement;
    expect(el.textContent).toBe("@Dev User");
    expect(el.getAttribute("data-self")).toBe("true");
  });
});

describe("date", () => {
  it("renders a <time> carrying the ISO date and some visible text", () => {
    renderDoc(doc(p(text("starts "), node("date", { date: "2026-12-01" }))));
    const root = adf(document.body, "date") as HTMLElement;
    expect(root).not.toBeNull();
    const time = (root.matches("time") ? root : root.querySelector("time")) as HTMLTimeElement | null;
    expect(time?.getAttribute("datetime")).toBe("2026-12-01");
    expect((root.textContent ?? "").trim().length).toBeGreaterThan(0);
    expect(root.getAttribute("data-past")).not.toBe("true");
  });

  it("marks a past date data-past=true (alert text via CSS), from Attrs.Past alone", () => {
    renderDoc(doc(p(node("date", { date: "2026-11-18", past: true }), text(" "), node("date", { date: "2099-01-01" }))));
    const [past, future] = adfAll(document.body, "date");
    expect(past?.getAttribute("data-past")).toBe("true");
    expect(future?.getAttribute("data-past")).not.toBe("true");
  });
});

describe("emoji", () => {
  it("renders the native glyph", () => {
    renderDoc(doc(p(node("emoji", { emoji: "🧁", shortName: ":cupcake:" }))));
    const el = adf(document.body, "emoji") as HTMLElement;
    expect(el.textContent).toBe("🧁");
  });

  it("falls back to the shortName text when there is no glyph", () => {
    renderDoc(doc(p(node("emoji", { shortName: ":custom_blob:" }))));
    expect((adf(document.body, "emoji") as HTMLElement).textContent).toBe(":custom_blob:");
  });
});

describe("card", () => {
  const issueCard = (block = false) =>
    node("card", {
      issueId: 900,
      key: "MOB-1200",
      summary: "Recipe units v2",
      typeKind: "epic",
      statusName: "In Progress",
      statusLozenge: "prog",
      url: "https://example.atlassian.net/browse/MOB-1200",
      block,
    });
  const urlCard = (block = false) =>
    node("card", { url: "https://docs.example.com/recipes/units-v2", host: "docs.example.com", path: "/recipes/units-v2", block });

  it("shows key, summary and the status lozenge for a synced issue", () => {
    renderDoc(doc(p(text("Part of "), issueCard())));
    const card = adf(document.body, "card") as HTMLElement;
    expect(within(card).getByText("MOB-1200")).toBeTruthy();
    expect(within(card).getByText("Recipe units v2")).toBeTruthy();
    const lozenge = card.querySelector("[data-lozenge]") as HTMLElement;
    expect(lozenge.getAttribute("data-lozenge")).toBe("prog");
    expect(lozenge.textContent?.trim()).toBe("In Progress");
    expect(card.getAttribute("data-block")).not.toBe("true");
  });

  it("opens a synced issue in the modal with the numeric id, never the browser", async () => {
    const ctx = makeCtx();
    renderDoc(doc(p(issueCard())), ctx);
    await userEvent.click(interactive(adf(document.body, "card") as HTMLElement));
    expect(ctx.onOpenIssue).toHaveBeenCalledTimes(1);
    expect(ctx.onOpenIssue).toHaveBeenCalledWith(900);
    expect(ctx.onOpenUrl).not.toHaveBeenCalled();
  });

  it("shows host and path (no status lozenge) for a URL card", () => {
    renderDoc(doc(p(text("Notes: "), urlCard())));
    const card = adf(document.body, "card") as HTMLElement;
    expect(card.textContent).toContain("docs.example.com");
    expect(card.textContent).toContain("/recipes/units-v2");
    expect(card.querySelector("[data-lozenge]")).toBeNull();
  });

  it("opens a URL card in the OS browser through ctx.onOpenUrl, not in the modal", async () => {
    const ctx = makeCtx();
    renderDoc(doc(p(urlCard())), ctx);
    const target = interactive(adf(document.body, "card") as HTMLElement);
    expect(fireEvent.click(target)).toBe(false); // an <a> must not navigate the webview
    expect(ctx.onOpenUrl).toHaveBeenCalledWith("https://docs.example.com/recipes/units-v2");
    expect(ctx.onOpenIssue).not.toHaveBeenCalled();
  });

  it("renders a blockCard (Attrs.Block) as a card marked data-block=true, issue and URL alike", () => {
    renderDoc(doc(issueCard(true), urlCard(true)));
    const cards = adfAll(document.body, "card");
    expect(cards.length).toBe(2);
    expect(cards.every((c) => c.getAttribute("data-block") === "true")).toBe(true);
    expect(within(cards[0] as HTMLElement).getByText("MOB-1200")).toBeTruthy();
    expect((cards[1] as HTMLElement).textContent).toContain("docs.example.com");
  });
});
