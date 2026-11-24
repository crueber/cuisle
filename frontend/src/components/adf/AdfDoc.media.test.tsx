// WP-T2: AdfDoc media, file cards and unknown nodes (ux-spec §14.4 rows mediaSingle / mediaGroup / unknown node,
// architecture §4.9 "Images are <img src=/media/{id}> ... onerror fallback to the thumb then a placeholder frame with
// 'Load image'").
//
// SPEC-NOTE (media src): the brief for this WP said "/media/{id}/thumb"; architecture §4.9 says the image is served from
//   /media/{id} with a fallback to the thumb. Both are encoded: Attrs.Cached "full" -> src ends with /media/{id};
//   Cached "thumb" -> src ends with /media/{id}/thumb. On an image `error` event a full image falls back to
//   /media/{id}/thumb, and a failing thumb falls back to the placeholder frame (no <img>, a "Load image" button).
//   Cached "none" (not missing) renders the placeholder straight away (no request is made for an uncached file).
//   The orchestrator should confirm this reading.
// SPEC-NOTE (media attrs): the <img> has explicit width/height attributes from Attrs.Width/Height (no layout shift),
//   loading="lazy" and a non-empty alt equal to Attrs.Caption or Attrs.Name (Attrs has no alt field). A caption renders
//   as text. The root carries data-adf="media". attachmentId is a decimal string in Attrs; every ctx callback receives a
//   number, so tests assert onOpenImage(201), not "201".
// SPEC-NOTE (media clicks): clicking the image (or the button/link wrapping it) calls ctx.onOpenImage(id). The
//   placeholder's "Load image" button calls ctx.onLoadImage(id) and nothing else. Attrs.Missing renders no <img> and a
//   link/button named /attachment unavailable/ that calls ctx.onOpenUrl(ctx.issueUrl); it calls neither onLoadImage
//   nor onOpenImage.
// SPEC-NOTE (file card): data-adf="file"; shows the file name and an upper-case extension block ("unit-spec.pdf" -> "PDF");
//   a click calls ctx.onOpenFile(id) (number). A Missing file shows "attachment unavailable" -> onOpenUrl(issueUrl).
// SPEC-NOTE (unknown): the brief said a muted "unsupported content" wrapper; ux-spec §14.4, architecture §4.9 and
//   props.ts say the node's text plus a muted "unsupported block ↗" that opens the ticket in Jira. The spec copy is
//   asserted: the root carries data-adf="unknown", the text is shown, and a link/button named /unsupported block/
//   calls ctx.onOpenUrl(ctx.issueUrl). This applies to Go's `unknown` node and to any `t` the table does not know.
import { describe, expect, it } from "vitest";
import { fireEvent, screen, within } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import { ISSUE_URL, adf, doc, interactive, makeCtx, node, p, renderDoc, text } from "./testdata/helpers";

const FULL = {
  attachmentId: "201",
  name: "unit-toggle-v3.png",
  mime: "image/png",
  bytes: 182540,
  width: 1200,
  height: 720,
  layout: "center",
  caption: "Unit toggle, v3: metric and US side by side",
  cached: "full",
  block: true,
};
const THUMB = { ...FULL, attachmentId: "202", name: "conversion-flow.png", width: 1200, height: 440, layout: "wide", caption: "", cached: "thumb" };

const imgs = (root: ParentNode = document.body) => Array.from(root.querySelectorAll("img"));
const wrapperOf = (img: HTMLElement) => (img.closest('button, a, [role="button"], [role="link"]') as HTMLElement | null) ?? img;

describe("media (mediaSingle)", () => {
  it("renders a full image from /media/{id} with explicit size, lazy loading and alt", () => {
    renderDoc(doc(node("media", FULL)));
    expect(adf(document.body, "media")).not.toBeNull();
    const [img] = imgs();
    expect(img).toBeTruthy();
    expect(img?.getAttribute("src")).toMatch(/\/media\/201$/);
    expect(img?.getAttribute("width")).toBe("1200");
    expect(img?.getAttribute("height")).toBe("720");
    expect(img?.getAttribute("loading")).toBe("lazy");
    const alt = img?.getAttribute("alt") ?? "";
    expect(alt).not.toBe("");
    expect([FULL.caption, FULL.name]).toContain(alt);
  });

  it("renders a thumb-cached image from /media/{id}/thumb", () => {
    renderDoc(doc(node("media", THUMB)));
    const [img] = imgs();
    expect(img?.getAttribute("src")).toMatch(/\/media\/202\/thumb$/);
    expect(img?.getAttribute("alt")).toBe("conversion-flow.png");
  });

  it("renders the caption as text", () => {
    renderDoc(doc(node("media", FULL)));
    expect(screen.getByText(FULL.caption)).toBeTruthy();
    expect(imgs().length).toBe(1);
  });

  it("opens the lightbox with the numeric attachment id on click", async () => {
    const ctx = makeCtx();
    renderDoc(doc(node("media", FULL)), ctx);
    await userEvent.click(wrapperOf(imgs()[0] as HTMLElement));
    expect(ctx.onOpenImage).toHaveBeenCalledTimes(1);
    expect(ctx.onOpenImage).toHaveBeenCalledWith(201);
    expect(ctx.onOpenUrl).not.toHaveBeenCalled();
    expect(ctx.onLoadImage).not.toHaveBeenCalled();
  });

  it("falls back from the full image to the thumb on error, then to the placeholder with Load image", async () => {
    const ctx = makeCtx();
    renderDoc(doc(node("media", FULL)), ctx);
    const first = imgs()[0] as HTMLElement;
    expect(first.getAttribute("src")).toMatch(/\/media\/201$/);
    fireEvent.error(first);
    const second = imgs()[0] as HTMLElement;
    expect(second.getAttribute("src")).toMatch(/\/media\/201\/thumb$/);
    fireEvent.error(second);
    expect(imgs().length).toBe(0);
    await userEvent.click(screen.getByRole("button", { name: /load image/i }));
    expect(ctx.onLoadImage).toHaveBeenCalledTimes(1);
    expect(ctx.onLoadImage).toHaveBeenCalledWith(201);
    expect(ctx.onOpenImage).not.toHaveBeenCalled();
  });

  it("shows the placeholder (no <img>) when a thumb fails", () => {
    renderDoc(doc(node("media", THUMB)));
    fireEvent.error(imgs()[0] as HTMLElement);
    expect(imgs().length).toBe(0);
    expect(screen.getByRole("button", { name: /load image/i })).toBeTruthy();
  });

  it("renders an uncached image as a placeholder with Load image and makes no image request", async () => {
    const ctx = makeCtx();
    renderDoc(doc(node("media", { ...FULL, attachmentId: "205", cached: "none" })), ctx);
    expect(adf(document.body, "media")).not.toBeNull();
    expect(imgs().length).toBe(0);
    await userEvent.click(screen.getByRole("button", { name: /load image/i }));
    expect(ctx.onLoadImage).toHaveBeenCalledWith(205);
  });

  it("renders a Missing image as 'attachment unavailable' linking to the issue in Jira", async () => {
    const ctx = makeCtx();
    renderDoc(doc(node("media", { ...FULL, missing: true, cached: "none", attachmentId: "" })), ctx);
    expect(adf(document.body, "media")).not.toBeNull();
    expect(imgs().length).toBe(0);
    // The click bubbles, so it works whether the text is the link itself or sits inside it.
    fireEvent.click(screen.getByText(/attachment unavailable/i));
    expect(ctx.onOpenUrl).toHaveBeenCalledWith(ISSUE_URL);
    expect(ctx.onLoadImage).not.toHaveBeenCalled();
    expect(ctx.onOpenImage).not.toHaveBeenCalled();
  });

  it("renders an inline (non-block) image as an <img> inside the paragraph", () => {
    renderDoc(doc(p(text("icon "), node("media", { ...THUMB, block: false, caption: "" }))));
    const [img] = imgs();
    expect(img?.getAttribute("src")).toMatch(/\/media\/202\/thumb$/);
    expect(img?.closest("p")).not.toBeNull();
  });
});

describe("file card (file)", () => {
  const FILE = { attachmentId: "203", name: "unit-spec.pdf", mime: "application/pdf", bytes: 412000, cached: "none" };

  it("shows the name and an upper-case extension block", () => {
    renderDoc(doc(node("file", FILE)));
    const card = adf(document.body, "file") as HTMLElement;
    expect(card).not.toBeNull();
    expect(within(card).getByText("unit-spec.pdf")).toBeTruthy();
    expect(within(card).getByText("PDF")).toBeTruthy();
  });

  it("derives the extension block per file (notes.txt -> TXT)", () => {
    renderDoc(doc(node("file", { ...FILE, attachmentId: "205", name: "rounding-notes.txt", mime: "text/plain" })));
    expect(within(adf(document.body, "file") as HTMLElement).getByText("TXT")).toBeTruthy();
  });

  it("downloads and opens the file with the numeric id on click", async () => {
    const ctx = makeCtx();
    renderDoc(doc(node("file", FILE)), ctx);
    await userEvent.click(interactive(adf(document.body, "file") as HTMLElement));
    expect(ctx.onOpenFile).toHaveBeenCalledTimes(1);
    expect(ctx.onOpenFile).toHaveBeenCalledWith(203);
    expect(ctx.onOpenImage).not.toHaveBeenCalled();
  });

  it("renders a Missing file as 'attachment unavailable' linking to the issue in Jira", () => {
    const ctx = makeCtx();
    renderDoc(doc(node("file", { ...FILE, missing: true, attachmentId: "" })), ctx);
    const card = adf(document.body, "file") as HTMLElement;
    expect(card).not.toBeNull();
    fireEvent.click(within(card).getByText(/attachment unavailable/i));
    expect(ctx.onOpenUrl).toHaveBeenCalledWith(ISSUE_URL);
    expect(ctx.onOpenFile).not.toHaveBeenCalled();
  });
});

describe("unknown node", () => {
  it("renders its text on an unknown root with a muted 'unsupported block' link", () => {
    renderDoc(doc({ t: "unknown", x: "Embedded Loom video: unit toggle walkthrough" }));
    const root = adf(document.body, "unknown") as HTMLElement;
    expect(root).not.toBeNull();
    expect(root.textContent).toContain("Embedded Loom video: unit toggle walkthrough");
    expect(within(root).getByText(/unsupported block/i)).toBeTruthy();
  });

  it("opens the ticket in Jira through ctx.onOpenUrl(issueUrl)", async () => {
    const ctx = makeCtx({ issueUrl: "https://example.atlassian.net/browse/MOB-9" });
    renderDoc(doc({ t: "unknown", x: "macro" }), ctx);
    await userEvent.click(interactive(adf(document.body, "unknown") as HTMLElement));
    expect(ctx.onOpenUrl).toHaveBeenCalledTimes(1);
    expect(ctx.onOpenUrl).toHaveBeenCalledWith("https://example.atlassian.net/browse/MOB-9");
    expect(ctx.onOpenIssue).not.toHaveBeenCalled();
  });

  it("treats a node type the table does not know like an unknown node", () => {
    renderDoc(doc({ t: "futureNodeType", x: "from the future" }));
    const root = adf(document.body, "unknown") as HTMLElement;
    expect(root).not.toBeNull();
    expect(root.textContent).toContain("from the future");
    expect(within(root).getByText(/unsupported block/i)).toBeTruthy();
  });

  it("keeps rendering the rest of the document around it", () => {
    renderDoc(doc(p(text("before")), { t: "unknown", x: "mystery" }, p(text("after"))));
    expect(screen.getByText("before")).toBeTruthy();
    expect(screen.getByText("after")).toBeTruthy();
    expect(adf(document.body, "unknown")?.textContent).toContain("mystery");
  });
});
