// WP-T7 tests: Lightbox (ux-spec §14.7; props frozen in props.ts; architecture §4.9 "Lightbox": EnsureMedia on open when
// cached != "full" is an explicit network trigger).
//
// SPEC-NOTE: DOM HOOKS used by this file:
//   - Root: role="dialog" named "Image viewer".
//   - Top bar text: the issue key, the file name, "<w>×<h>" (only when both are known), the author name and the date as
//     "18 Nov" (local day, short month, no year), a size containing "KB" for a 182 540-byte file, the zoom readout and the
//     counter "1 / 2" (counter present even for a single image: "1 / 1").
//   - Zoom readout: "100%" when the image is shown at natural size, else "fit NN%" with NN = round(rendered width /
//     natural width × 100).
//   - Image: <img> with role "img" and name = the file name; its rendered size is its INLINE style width/height in px
//     (jsdom has no layout). src = attachment.url ("/media/201") when cached is "full", else attachment.thumbUrl until the
//     full image has been ensured.
//   - Fit rule: the image is never upscaled; it is capped to the viewport (window.innerWidth × innerHeight) minus the 64px
//     top bar, keeps its aspect ratio (within rounding), and is never taller than the viewport minus the bar nor wider
//     than the viewport. At 1920×1080 a 1200×720 image is exactly 1200×720. Unknown size (0×0): no inline size is set
//     and the readout is not asserted.
//   - Buttons: "Previous image" and "Next image" (56px, edges; absent for a single image; no wrap, so at the ends the
//     arrow keys and the button do nothing), "Open file" (calls onOpenFile(id)), "Close image" (calls onClose).
//   - Keys: ArrowRight / ArrowLeft call onIndex(index ± 1) and are default-prevented; Escape calls onClose once. Listeners
//     are on the document, so they work wherever focus is inside the page.
//   - Focus moves into the dialog when it opens.
//   - ensure(id) is called once when an image with cached != "full" becomes current (on mount or by stepping), never for a
//     "full" one; when it resolves the full src replaces the thumb; when it rejects the thumb stays, the dialog stays open
//     and Escape still closes.
import { act, fireEvent, render, screen, waitFor } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import mob1421 from "../../api/fixtures/ticket_mob1421.json";
import type { AttachmentView, TicketDetail } from "../../api/types";
import { Lightbox } from "./Lightbox";
import type { LightboxProps } from "./props";

const clone = <X,>(x: X): X => JSON.parse(JSON.stringify(x)) as X;
const D1421: TicketDetail = mob1421;
const ATT_201 = clone(D1421.attachments.find((a) => a.id === 201)) as AttachmentView; // 1200×720, cached full
const ATT_202 = clone(D1421.attachments.find((a) => a.id === 202)) as AttachmentView; // 1200×440, cached thumb
const nov18 = new Date(2026, 10, 18, 12, 0, 0).getTime();
const IMG_201: AttachmentView = { ...ATT_201, createdAt: nov18 };
const IMG_202: AttachmentView = { ...ATT_202, createdAt: nov18 };

function setViewport(w: number, h: number) {
  Object.defineProperty(window, "innerWidth", { configurable: true, writable: true, value: w });
  Object.defineProperty(window, "innerHeight", { configurable: true, writable: true, value: h });
}
beforeEach(() => setViewport(1920, 1080));
afterEach(() => setViewport(1024, 768));

function props(over: Partial<LightboxProps> = {}): LightboxProps {
  return {
    issueKey: "MOB-1421",
    images: [IMG_201, IMG_202],
    index: 0,
    onIndex: vi.fn(),
    onOpenFile: vi.fn(),
    ensure: vi.fn(async (id: number) => ({ ...(id === 202 ? IMG_202 : IMG_201), cached: "full" })),
    onClose: vi.fn(),
    ...over,
  };
}
function renderBox(over: Partial<LightboxProps> = {}) {
  const p = props(over);
  const view = render(<Lightbox {...p} />);
  return { ...view, p };
}
const img = (name: string) => screen.getByRole("img", { name }) as HTMLImageElement;
const px = (v: string) => Number.parseFloat(v);
const keyDown = (key: string) => fireEvent.keyDown(document.body, { key });

describe("top bar and image", () => {
  it("showsKeyFileNameDimensionsAuthorDateSizeAndCounter", () => {
    renderBox();
    const dialog = screen.getByRole("dialog", { name: "Image viewer" });
    const text = dialog.textContent ?? "";
    expect(text).toContain("MOB-1421");
    expect(text).toContain("unit-toggle-v3.png");
    expect(text).toContain("1200×720");
    expect(text).toContain("Wren Kowalski");
    expect(text).toContain("18 Nov");
    expect(text).toContain("KB");
    expect(screen.getByText("1 / 2")).toBeTruthy();
  });

  it("showsTheFullImageAtItsNaturalSizeWhenItFits_WithA100PercentReadout", () => {
    renderBox();
    const image = img("unit-toggle-v3.png");
    expect(image.getAttribute("src")).toBe("/media/201");
    expect(image.style.width).toBe("1200px");
    expect(image.style.height).toBe("720px");
    expect(screen.getByText("100%")).toBeTruthy();
  });

  it("neverUpscalesASmallImage", () => {
    const small: AttachmentView = { ...IMG_201, width: 400, height: 300 };
    renderBox({ images: [small] });
    const image = img("unit-toggle-v3.png");
    expect(image.style.width).toBe("400px");
    expect(image.style.height).toBe("300px");
    expect(screen.getByText("100%")).toBeTruthy();
  });

  it("capsAnImageThatIsTooBigToTheViewport_KeepingItsRatio_AndShowsFitPercent", () => {
    setViewport(800, 600);
    renderBox();
    const image = img("unit-toggle-v3.png");
    const w = px(image.style.width);
    const h = px(image.style.height);
    expect(w).toBeLessThan(1200);
    expect(w).toBeLessThanOrEqual(800);
    expect(h).toBeLessThanOrEqual(600 - 64);
    expect(Math.abs(w / h - 1200 / 720)).toBeLessThan(0.02);
    const pct = Math.round((w / 1200) * 100);
    expect(pct).toBeLessThan(100);
    expect(screen.getByText(`fit ${pct}%`)).toBeTruthy();
  });

  it("capsATallImageByHeight", () => {
    const tall: AttachmentView = { ...IMG_201, name: "tall.png", width: 1000, height: 3000 };
    renderBox({ images: [tall] });
    const image = img("tall.png");
    const w = px(image.style.width);
    const h = px(image.style.height);
    expect(h).toBeLessThanOrEqual(1080 - 64);
    expect(w).toBeLessThan(1000);
    expect(Math.abs(w / h - 1000 / 3000)).toBeLessThan(0.01);
    expect(screen.getByText(`fit ${Math.round((w / 1000) * 100)}%`)).toBeTruthy();
  });

  it("movesFocusIntoTheDialog", () => {
    renderBox();
    const dialog = screen.getByRole("dialog", { name: "Image viewer" });
    expect(dialog.contains(document.activeElement)).toBe(true);
  });
});

describe("navigation between images", () => {
  it("arrowRightAndLeftStepOneImage_AndArePreventedFromScrollingThePage", () => {
    const { p } = renderBox({ index: 1 });
    const notPreventedLeft = fireEvent.keyDown(document.body, { key: "ArrowLeft" });
    expect(notPreventedLeft).toBe(false);
    expect(p.onIndex).toHaveBeenCalledTimes(1);
    expect(p.onIndex).toHaveBeenLastCalledWith(0);
  });

  it("arrowRight_StepsForward_AndStopsAtTheLastImage", () => {
    const first = renderBox({ index: 0 });
    keyDown("ArrowRight");
    expect(first.p.onIndex).toHaveBeenCalledWith(1);
    first.unmount();

    const last = renderBox({ index: 1 });
    keyDown("ArrowRight");
    expect(last.p.onIndex).not.toHaveBeenCalled();
    expect(screen.getByText("2 / 2")).toBeTruthy();
  });

  it("arrowLeft_StopsAtTheFirstImage", () => {
    const { p } = renderBox({ index: 0 });
    keyDown("ArrowLeft");
    expect(p.onIndex).not.toHaveBeenCalled();
    keyDown("ArrowRight"); // control: the listener is alive
    expect(p.onIndex).toHaveBeenCalledWith(1);
  });

  it("theEdgeButtonsStepToo_AndDoNothingAtTheEnds", async () => {
    const user = userEvent.setup(); // user-event, like a real pointer, never clicks a disabled button
    const mid = renderBox({ index: 1 });
    await user.click(screen.getByRole("button", { name: "Previous image" }));
    expect(mid.p.onIndex).toHaveBeenCalledWith(0);
    await user.click(screen.getByRole("button", { name: "Next image" }));
    expect(mid.p.onIndex).toHaveBeenCalledTimes(1); // at the last image the next button does nothing
    mid.unmount();

    const start = renderBox({ index: 0 });
    await user.click(screen.getByRole("button", { name: "Previous image" }));
    expect(start.p.onIndex).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Next image" }));
    expect(start.p.onIndex).toHaveBeenCalledWith(1);
  });

  it("showsTheNextImageAndTheCounterWhenTheIndexChanges", () => {
    const p = props();
    const view = render(<Lightbox {...p} />);
    expect(screen.getByText("1 / 2")).toBeTruthy();
    view.rerender(<Lightbox {...p} index={1} />);
    expect(screen.getByText("2 / 2")).toBeTruthy();
    expect(img("conversion-flow.png")).toBeTruthy();
    expect(screen.queryByRole("img", { name: "unit-toggle-v3.png" })).toBeNull();
  });

  it("hasNoArrowButtonsForASingleImage", () => {
    renderBox({ images: [IMG_201] });
    expect(screen.getByRole("dialog", { name: "Image viewer" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Previous image" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Next image" })).toBeNull();
    expect(screen.getByText("1 / 1")).toBeTruthy();
  });
});

describe("open file and close", () => {
  it("openFileCallsOnOpenFileWithTheCurrentAttachment", () => {
    const { p } = renderBox({ index: 1 });
    fireEvent.click(screen.getByRole("button", { name: "Open file" }));
    expect(p.onOpenFile).toHaveBeenCalledTimes(1);
    expect(p.onOpenFile).toHaveBeenCalledWith(202);
  });

  it("escapeClosesOnce_AndTheCloseButtonToo", () => {
    const { p } = renderBox();
    keyDown("Escape");
    expect(p.onClose).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole("button", { name: "Close image" }));
    expect(p.onClose).toHaveBeenCalledTimes(2);
  });

  it("stopsListeningToKeysAfterItUnmounts", () => {
    const { p, unmount } = renderBox();
    keyDown("Escape");
    expect(p.onClose).toHaveBeenCalledTimes(1);
    unmount();
    keyDown("Escape");
    keyDown("ArrowRight");
    expect(p.onClose).toHaveBeenCalledTimes(1);
    expect(p.onIndex).not.toHaveBeenCalled();
  });
});

describe("ensure: the full image is an explicit network trigger", () => {
  it("doesNotEnsureAnImageThatIsAlreadyFullyCached", () => {
    const { p } = renderBox({ index: 0 });
    expect(img("unit-toggle-v3.png").getAttribute("src")).toBe("/media/201");
    expect(p.ensure).not.toHaveBeenCalled();
  });

  it("ensuresAThumbOnlyImageOnOpen_ShowsTheThumbFirst_ThenTheFullImage", async () => {
    const { p } = renderBox({ index: 1 });
    expect(p.ensure).toHaveBeenCalledTimes(1);
    expect(p.ensure).toHaveBeenCalledWith(202);
    await waitFor(() => expect(img("conversion-flow.png").getAttribute("src")).toBe("/media/202"));
  });

  it("showsTheThumbWhileEnsuringIsPending", async () => {
    let resolve!: (a: AttachmentView) => void;
    const ensure = vi.fn(() => new Promise<AttachmentView>((r) => (resolve = r)));
    renderBox({ index: 1, ensure });
    expect(img("conversion-flow.png").getAttribute("src")).toBe("/media/202/thumb");
    await act(async () => resolve({ ...IMG_202, cached: "full" }));
    expect(img("conversion-flow.png").getAttribute("src")).toBe("/media/202");
  });

  it("ensuresWhenSteppingOntoAThumbOnlyImage_AndOnlyOnce", async () => {
    const p = props();
    const view = render(<Lightbox {...p} index={0} />);
    expect(p.ensure).not.toHaveBeenCalled();
    view.rerender(<Lightbox {...p} index={1} />);
    expect(p.ensure).toHaveBeenCalledTimes(1);
    expect(p.ensure).toHaveBeenCalledWith(202);
    view.rerender(<Lightbox {...p} index={0} />);
    await waitFor(() => expect(img("unit-toggle-v3.png")).toBeTruthy());
    expect(p.ensure).toHaveBeenCalledTimes(1);
  });

  it("keepsTheThumbAndStaysOpen_WhenEnsureFails_AndEscapeStillCloses", async () => {
    const ensure = vi.fn(async () => Promise.reject(new Error("media unavailable")));
    const { p } = renderBox({ index: 1, ensure });
    await waitFor(() => expect(ensure).toHaveBeenCalledTimes(1));
    await act(async () => undefined);
    expect(screen.getByRole("dialog", { name: "Image viewer" })).toBeTruthy();
    expect(img("conversion-flow.png").getAttribute("src")).toBe("/media/202/thumb");
    keyDown("Escape");
    expect(p.onClose).toHaveBeenCalledTimes(1);
  });
});
