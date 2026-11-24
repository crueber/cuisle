// WP-T8: Attachments section (ux-spec §14.4 "Attachments section"; props.ts AttachmentsProps).
//
// SPEC-NOTE (DOM): the component renders its own section: <section data-section="attachments"> with a heading
//   (h2) whose accessible name is "ATTACHMENTS <n>" (n = items.length), case-insensitive. Empty `items` renders nothing.
//   The same data-section convention is used by IssueRows ("subtasks", "links") and Comments ("comments"), and
//   TicketContent's jump targets them.
// SPEC-NOTE (hooks): every tile and card carries data-attachment-id="<id>" and data-kind="image" | "file". These are
//   the only layout-free way to assert order in jsdom.
// SPEC-NOTE (order): images first, then files, each group keeping the order it arrived in (a stable partition by
//   `image`). Go already sends them that way, but the component must not rely on it (ux-spec §14.4: "followed by").
// SPEC-NOTE (images): an image tile is one <button> (name contains the file name) holding an <img alt="<name>">
//   whose src is exactly `thumbUrl` ("/media/{id}/thumb"): tiles never load the original. Clicking the tile calls
//   onOpenImage(id) and never onOpenFile.
// SPEC-NOTE (files): a file card is one <button> (name contains the file name) showing the upper-case extension block
//   (item.ext, e.g. "PDF"), the name and the meta line. Clicking calls onOpenFile(id) and never onOpenImage. File
//   cards draw no <img>.
// SPEC-NOTE (meta): "<size> · <INITIALS> · <D Mon>" (separator " · "). Size is decimal (1 KB = 1000 B, as in the
//   mockup's "412 KB"): >= 1000 B and < 1 MB is rounded KB without decimals when >= 100 ("183 KB"). The date is the
//   attachment's local calendar day as "18 Nov" (day without zero padding, English month abbreviation). This test
//   builds createdAt from local-time Date parts so it is timezone independent.
// SPEC-NOTE: no api calls: opening is delegated to the callbacks.
import { fireEvent, render, screen, within } from "@testing-library/preact";
import { describe, expect, it, vi } from "vitest";
import { setApi, type Api } from "../../api";
import type { AttachmentView } from "../../api/types";
import { Attachments } from "./Attachments";

const day = (d: number) => new Date(2026, 10, d, 12, 0, 0).getTime();

function att(over: Partial<AttachmentView> & { id: number; name: string }): AttachmentView {
  return {
    ext: "PNG",
    mime: "image/png",
    bytes: 182540,
    authorInitials: "WK",
    authorName: "Wren Kowalski",
    createdAt: day(18),
    image: true,
    width: 1200,
    height: 800,
    cached: "thumb",
    url: `/media/${over.id}`,
    thumbUrl: `/media/${over.id}/thumb`,
    ...over,
  };
}

const PNG1 = att({ id: 201, name: "unit-toggle-v3.png" });
const PNG2 = att({ id: 202, name: "conversion-flow.png", bytes: 47477, authorInitials: "IC", createdAt: day(17) });
const PDF = att({ id: 203, name: "unit-spec.pdf", ext: "PDF", mime: "application/pdf", bytes: 412000, authorInitials: "SM", createdAt: day(19), image: false, width: 0, height: 0, cached: "none" });
const CSV = att({ id: 204, name: "density-table.csv", ext: "CSV", mime: "text/csv", bytes: 18400, authorInitials: "NA", createdAt: day(18), image: false, width: 0, height: 0, cached: "none" });

function mount(items: AttachmentView[]) {
  const onOpenImage = vi.fn();
  const onOpenFile = vi.fn();
  const view = render(<Attachments items={items} onOpenImage={onOpenImage} onOpenFile={onOpenFile} />);
  return { onOpenImage, onOpenFile, ...view };
}
const ids = (root: ParentNode) => Array.from(root.querySelectorAll("[data-attachment-id]")).map((e) => e.getAttribute("data-attachment-id"));

describe("Attachments: section", () => {
  it("renders a section headed ATTACHMENTS with the item count", () => {
    const { container } = mount([PNG1, PNG2, PDF, CSV]);
    const sec = container.querySelector('section[data-section="attachments"]');
    expect(sec).not.toBeNull();
    expect(within(sec as HTMLElement).getByRole("heading", { name: /^attachments\s*4$/i })).toBeTruthy();
  });

  it("renders nothing when there are no attachments", () => {
    const view = mount([]);
    expect(view.container.querySelector("section")).toBeNull();
    expect(screen.queryByRole("heading", { name: /attachments/i })).toBeNull();
    view.rerender(<Attachments items={[PNG1]} onOpenImage={vi.fn()} onOpenFile={vi.fn()} />);
    expect(screen.getByRole("heading", { name: /^attachments\s*1$/i })).toBeTruthy(); // and it appears once there is one
  });
});

describe("Attachments: order", () => {
  it("draws images first, then files, keeping each group's order", () => {
    const { container } = mount([PDF, PNG1, CSV, PNG2]);
    expect(ids(container)).toEqual(["201", "202", "203", "204"]);
  });

  it("marks tiles as images and cards as files", () => {
    const { container } = mount([PNG1, PDF]);
    expect(container.querySelector('[data-attachment-id="201"]')?.getAttribute("data-kind")).toBe("image");
    expect(container.querySelector('[data-attachment-id="203"]')?.getAttribute("data-kind")).toBe("file");
  });
});

describe("Attachments: image tiles", () => {
  it("shows the thumbnail through /media/{id}/thumb with the file name as alt text", () => {
    mount([PNG1, PNG2]);
    expect(screen.getByRole("img", { name: "unit-toggle-v3.png" }).getAttribute("src")).toBe("/media/201/thumb");
    expect(screen.getByRole("img", { name: "conversion-flow.png" }).getAttribute("src")).toBe("/media/202/thumb");
  });

  it("uses the thumb even when the original is already cached (never loads the full image in the grid)", () => {
    mount([att({ id: 201, name: "unit-toggle-v3.png", cached: "full" })]);
    expect(screen.getByRole("img", { name: "unit-toggle-v3.png" }).getAttribute("src")).toBe("/media/201/thumb");
  });

  it("shows name and the size, author initials and day meta line", () => {
    const { container } = mount([PNG1]);
    const tile = container.querySelector('[data-attachment-id="201"]') as HTMLElement;
    expect(within(tile).getByText("unit-toggle-v3.png")).toBeTruthy();
    expect((tile.textContent ?? "").replace(/\s+/g, " ")).toMatch(/183 KB\s*·\s*WK\s*·\s*18 Nov/);
  });

  it("opens the lightbox callback with the attachment id on click", () => {
    const { onOpenImage, onOpenFile } = mount([PNG1, PNG2]);
    fireEvent.click(screen.getByRole("button", { name: /conversion-flow\.png/ }));
    expect(onOpenImage).toHaveBeenCalledTimes(1);
    expect(onOpenImage).toHaveBeenCalledWith(202);
    expect(onOpenFile).not.toHaveBeenCalled();
  });
});

describe("Attachments: file cards", () => {
  it("shows the extension block, name and meta line, with no image", () => {
    const { container } = mount([PDF]);
    const card = container.querySelector('[data-attachment-id="203"]') as HTMLElement;
    expect(within(card).getByText("PDF")).toBeTruthy();
    expect(within(card).getByText("unit-spec.pdf")).toBeTruthy();
    expect((card.textContent ?? "").replace(/\s+/g, " ")).toMatch(/412 KB\s*·\s*SM\s*·\s*19 Nov/);
    expect(card.querySelector("img")).toBeNull();
  });

  it("opens the file through onOpenFile (download and open), never the lightbox", () => {
    const { onOpenImage, onOpenFile } = mount([PNG1, PDF, CSV]);
    fireEvent.click(screen.getByRole("button", { name: /density-table\.csv/ }));
    expect(onOpenFile).toHaveBeenCalledTimes(1);
    expect(onOpenFile).toHaveBeenCalledWith(204);
    expect(onOpenImage).not.toHaveBeenCalled();
  });
});

describe("Attachments: no side effects", () => {
  it("makes no api calls and calls no callback by rendering", () => {
    const calls: string[] = [];
    setApi(new Proxy({}, { get: (_t, svc: string) => new Proxy({}, { get: (_s, m: string) => () => { calls.push(`${svc}.${m}`); return Promise.reject(new Error("unstubbed")); } }) }) as unknown as Api);
    const { onOpenImage, onOpenFile } = mount([PNG1, PDF]);
    expect(screen.getByRole("button", { name: /unit-spec\.pdf/ })).toBeTruthy(); // it did render
    expect(onOpenImage).not.toHaveBeenCalled();
    expect(onOpenFile).not.toHaveBeenCalled();
    expect(calls).toEqual([]);
  });
});
