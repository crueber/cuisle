// WP-T8: TicketContent, the scrolling content column of the ticket modal (ux-spec §14.4, §14.5, §14.9;
// props.ts TicketContentProps). It composes AnchorStrip, the description (real ADF), Attachments, IssueRows and
// Comments, and wires their callbacks to the AdfContext.
//
// SPEC-NOTE (sections): the column is [anchor strip] [updated bar] then sections in this DOM order, each a
//   <section data-section="<AnchorId>">: description, attachments, subtasks, links, comments. The description section
//   has NO heading of its own (the strip names it) and holds an AdfDoc at size "body" ([data-size="body"]). The other
//   four are drawn by Attachments, IssueRows and Comments (their heads are tested in their own files).
// SPEC-NOTE (strip): entries and counts are derived from `detail`, empty sections omitted (strip and column both):
//     DESCRIPTION            only when detail.description is non-null (a document with no nodes counts as empty);
//     ATTACHMENTS <n>        n = attachments.length, omitted at 0;
//     SUBTASKS <done>/<n>    from subtasksDone and subtasks.length, omitted when there are no subtasks;
//     LINKS <n>              n = rows over all linkGroups, omitted at 0;
//     COMMENTS <n>           n = comments.length; ALWAYS present (the composer lives there), "0" when empty.
//   The strip is the AnchorStrip nav (aria-label /sections/i). Pending comments are never counted.
// SPEC-NOTE (jump): clicking an entry calls scrollIntoView on that section's element (jsdom has none, so the test
//   installs a spy on Element.prototype and records `this`) and marks the entry aria-current="true" at once (the
//   click selects; a later scroll may move it). Initially the first entry is current. Scroll tracking itself (the
//   active entry following the reader) needs layout, so it is NOT tested here.
// SPEC-NOTE (updated bar, ux-spec §14.9): when `updated` is non-empty a role="status" element reads "Updated in Jira"
//   and names every part in `updated`, with a "Reload" button that calls onReload once. With [] there is no such bar.
// SPEC-NOTE (wiring through ctx): attachments and issue rows have no callbacks of their own in TicketContentProps, so
//   an image tile click -> ctx.onOpenImage(id); a file card click -> ctx.onOpenFile(id); an issue row with
//   issueId > 0 -> ctx.onOpenIssue(issueId); a row with issueId 0 -> ctx.onOpenUrl(row.url).
// SPEC-NOTE: rendering makes no api call (data comes from `detail`, local only).
import { fireEvent, render, screen, within } from "@testing-library/preact";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { setApi, type Api } from "../../api";
import type { TicketDetail } from "../../api/types";
import f1421 from "../../api/fixtures/ticket_mob1421.json";
import f1429 from "../../api/fixtures/ticket_mob1429.json";
import { doc, makeCtx, p, text } from "../../components/adf/testdata/helpers";
import { TicketContent } from "./TicketContent";
import type { CommentsProps, ModalGeometry, TicketContentProps } from "./props";

const D1421 = f1421 as unknown as TicketDetail;
const D1429 = f1429 as unknown as TicketDetail;

const GEOMETRY: ModalGeometry = { modalW: 1320, modalH: 1000, contentW: 880, railW: 440, short: false, narrow: false, stackedLabels: false };
const simple = (over: Partial<TicketDetail> = {}): TicketDetail => ({ ...D1421, description: doc(p(text("Home cooks want cups and spoons."))), ...over });

let scrolled: Element[] = [];
let apiCalls: string[] = [];
beforeEach(() => {
  scrolled = [];
  apiCalls = [];
  Element.prototype.scrollIntoView = vi.fn(function (this: Element) {
    scrolled.push(this);
  });
  setApi(new Proxy({}, { get: (_t, svc: string) => new Proxy({}, { get: (_s, m: string) => () => { apiCalls.push(`${svc}.${m}`); return Promise.reject(new Error("unstubbed")); } }) }) as unknown as Api);
});

function mount(detail: TicketDetail, over: Partial<TicketContentProps> = {}, commentOver: Partial<CommentsProps> = {}) {
  const ctx = makeCtx();
  const pr: TicketContentProps = {
    detail,
    geometry: GEOMETRY,
    ctx,
    updated: [],
    onReload: vi.fn(),
    comments: {
      comments: detail.comments,
      order: "newest",
      onOrder: vi.fn(),
      newSince: detail.newSince,
      pending: [],
      onRetry: vi.fn(),
      onEdit: vi.fn(),
      onDiscard: vi.fn(),
      composer: {
        draft: detail.draft,
        selfName: "Mock User",
        selfInitials: "MU",
        disabled: false,
        disabledReason: "",
        focusToken: 0,
        onChange: vi.fn(),
        onSend: vi.fn(),
        onBlur: vi.fn(),
        searchMentions: vi.fn(async () => []),
      },
      ctx,
      ...commentOver,
    },
    ...over,
  };
  const view = render(<TicketContent {...pr} />);
  return { ctx, pr, ...view };
}
const strip = () => within(screen.getByRole("navigation", { name: /sections/i })).getAllByRole("button").map((b) => (b.textContent ?? "").replace(/\s+/g, " ").trim().toUpperCase());
const sections = (root: ParentNode) => Array.from(root.querySelectorAll("section[data-section]")).map((s) => s.getAttribute("data-section"));

describe("TicketContent: anchor strip counts (golden fixtures)", () => {
  it("MOB-1421: DESCRIPTION · ATTACHMENTS 5 · SUBTASKS 3/5 · LINKS 2 · COMMENTS 7", () => {
    mount(D1421);
    expect(strip()).toEqual(["DESCRIPTION", "ATTACHMENTS 5", "SUBTASKS 3/5", "LINKS 2", "COMMENTS 7"]);
  });

  it("MOB-1429: no Subtasks entry; ATTACHMENTS 1 · LINKS 1 · COMMENTS 6", () => {
    mount(D1429);
    expect(strip()).toEqual(["DESCRIPTION", "ATTACHMENTS 1", "LINKS 1", "COMMENTS 6"]);
  });

  it("does not count pending comments", () => {
    const pending = [{ localId: "L1", input: { text: "in flight", mentions: [] }, createdAt: Date.now(), state: "sending" as const, reason: "" }];
    mount(D1429, {}, { pending });
    expect(strip()).toEqual(["DESCRIPTION", "ATTACHMENTS 1", "LINKS 1", "COMMENTS 6"]);
  });

  it("omits every empty section but keeps Comments at 0", () => {
    mount(simple({ attachments: [], subtasks: [], subtasksDone: 0, linkGroups: [], comments: [] }));
    expect(strip()).toEqual(["DESCRIPTION", "COMMENTS 0"]);
  });

  it("omits Description when there is none, and the first entry is then current", () => {
    mount(simple({ description: null }));
    expect(strip()[0]).toBe("ATTACHMENTS 5");
    const current = within(screen.getByRole("navigation", { name: /sections/i })).getAllByRole("button").filter((b) => b.getAttribute("aria-current") === "true");
    expect(current).toHaveLength(1);
    expect((current[0]?.textContent ?? "").toUpperCase()).toContain("ATTACHMENTS");
  });

  it("counts link rows over all groups", () => {
    const row = (key: string, id: number) => ({ issueId: id, key, summary: key, typeKind: "task", statusName: "To Do", statusLozenge: "todo", assigneeInitials: "", url: `https://x/${key}`, done: false });
    mount(simple({ linkGroups: [{ label: "BLOCKS", rows: [row("A-1", 1), row("A-2", 2)] }, { label: "RELATES TO", rows: [row("A-3", 3)] }] }));
    expect(strip()).toContain("LINKS 3");
  });
});

describe("TicketContent: sections", () => {
  it("draws description, attachments, subtasks, links and comments in that order", () => {
    const { container } = mount(simple());
    expect(sections(container)).toEqual(["description", "attachments", "subtasks", "links", "comments"]);
  });

  it("omits the sections that are empty, keeping the order of the rest", () => {
    const { container } = mount(simple({ attachments: [], subtasks: [], subtasksDone: 0 }));
    expect(sections(container)).toEqual(["description", "links", "comments"]);
  });

  it("renders the description as ADF at body size without a heading of its own", () => {
    const { container } = mount(simple());
    const sec = container.querySelector('section[data-section="description"]') as HTMLElement;
    expect(within(sec).getByText("Home cooks want cups and spoons.")).toBeTruthy();
    expect(sec.querySelector('[data-size="body"]')).not.toBeNull();
    expect(within(sec).queryByRole("heading", { name: /description/i })).toBeNull();
  });

  it("renders the full MOB-1421 description (every ADF node type) and its comments", () => {
    const { container } = mount(D1421);
    const sec = container.querySelector('section[data-section="description"]') as HTMLElement;
    expect(within(sec).getByText(/Home cooks in the US want cups and spoons/)).toBeTruthy();
    expect(sec.querySelector("table")).not.toBeNull();
    expect(container.querySelectorAll("[data-comment-id]")).toHaveLength(7);
  });

  it("draws the comments in the order the Comments props ask for", () => {
    const ids = (root: ParentNode) => Array.from(root.querySelectorAll("[data-comment-id]")).map((e) => e.getAttribute("data-comment-id"));
    const newest = mount(D1429, {}, { order: "newest" });
    expect(ids(newest.container)).toEqual(["10206", "10205", "10204", "10203", "10202", "10201"]);
    newest.unmount();
    const oldest = mount(D1429, {}, { order: "oldest" });
    expect(ids(oldest.container)).toEqual(["10201", "10202", "10203", "10204", "10205", "10206"]);
  });
});

describe("TicketContent: jump and active section", () => {
  it("scrolls the section into view and marks its entry current when an entry is clicked", () => {
    const { container } = mount(simple());
    fireEvent.click(screen.getByRole("button", { name: /^attachments\s*5$/i }));
    expect(scrolled).toEqual([container.querySelector('section[data-section="attachments"]')]);
    const current = within(screen.getByRole("navigation", { name: /sections/i })).getAllByRole("button").filter((b) => b.getAttribute("aria-current") === "true");
    expect(current).toHaveLength(1);
    expect((current[0]?.textContent ?? "").toUpperCase()).toContain("ATTACHMENTS");
  });

  it("starts on Description", () => {
    mount(simple());
    expect(screen.getByRole("button", { name: /^description$/i }).getAttribute("aria-current")).toBe("true");
  });

  it("jumps to comments, links and subtasks too", () => {
    const { container } = mount(simple());
    fireEvent.click(screen.getByRole("button", { name: /^comments\s*7$/i }));
    fireEvent.click(screen.getByRole("button", { name: /^links\s*2$/i }));
    fireEvent.click(screen.getByRole("button", { name: /^subtasks\s*3\/5$/i }));
    expect(scrolled).toEqual([
      container.querySelector('section[data-section="comments"]'),
      container.querySelector('section[data-section="links"]'),
      container.querySelector('section[data-section="subtasks"]'),
    ]);
    expect(screen.getByRole("button", { name: /^subtasks\s*3\/5$/i }).getAttribute("aria-current")).toBe("true");
    expect(screen.getByRole("button", { name: /^comments\s*7$/i }).getAttribute("aria-current")).not.toBe("true");
  });
});

describe("TicketContent: updated bar", () => {
  it("is absent when nothing changed under the reader", () => {
    mount(simple(), { updated: [] });
    expect(screen.getByRole("navigation", { name: /sections/i })).toBeTruthy(); // rendered
    expect(screen.queryByRole("status")).toBeNull();
    expect(screen.queryByRole("button", { name: /reload/i })).toBeNull();
  });

  it("names the changed parts and offers Reload", () => {
    const { pr } = mount(simple(), { updated: ["description", "comments", "attachments"] });
    const bar = screen.getByRole("status");
    expect(bar.textContent).toMatch(/Updated in Jira/);
    for (const part of ["description", "comments", "attachments"]) expect(bar.textContent?.toLowerCase()).toContain(part);
    fireEvent.click(within(bar).getByRole("button", { name: /reload/i }));
    expect(pr.onReload).toHaveBeenCalledTimes(1);
  });

  it("sits between the strip and the description", () => {
    const { container } = mount(simple(), { updated: ["description"] });
    const nav = screen.getByRole("navigation", { name: /sections/i });
    const bar = screen.getByRole("status");
    const desc = container.querySelector('section[data-section="description"]') as HTMLElement;
    expect(nav.compareDocumentPosition(bar) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(bar.compareDocumentPosition(desc) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });
});

describe("TicketContent: opening things goes through ctx", () => {
  it("an unsynced subtask opens in the browser through ctx.onOpenUrl", () => {
    const { ctx } = mount(simple());
    fireEvent.click(screen.getByRole("button", { name: /MOB-1422/ }));
    expect(ctx.onOpenUrl).toHaveBeenCalledTimes(1);
    expect(ctx.onOpenUrl).toHaveBeenCalledWith("https://example.atlassian.net/browse/MOB-1422");
    expect(ctx.onOpenIssue).not.toHaveBeenCalled();
  });

  it("a synced link opens in the modal stack through ctx.onOpenIssue", () => {
    const { ctx } = mount(simple());
    fireEvent.click(screen.getByRole("button", { name: /MOB-1436/ }));
    expect(ctx.onOpenIssue).toHaveBeenCalledTimes(1);
    expect(ctx.onOpenIssue).toHaveBeenCalledWith(111);
    expect(ctx.onOpenUrl).not.toHaveBeenCalled();
  });

  it("an unsynced link (issueId 0) opens in the browser", () => {
    const { ctx } = mount(D1429);
    fireEvent.click(screen.getByRole("button", { name: /PLAT-444/ }));
    expect(ctx.onOpenUrl).toHaveBeenCalledTimes(1);
    expect(ctx.onOpenIssue).not.toHaveBeenCalled();
  });

  it("an image tile opens the lightbox through ctx.onOpenImage", () => {
    const { ctx } = mount(simple());
    fireEvent.click(screen.getByRole("button", { name: /unit-toggle-v3\.png/ }));
    expect(ctx.onOpenImage).toHaveBeenCalledTimes(1);
    expect(ctx.onOpenImage).toHaveBeenCalledWith(201);
    expect(ctx.onOpenFile).not.toHaveBeenCalled();
  });

  it("a file card downloads and opens through ctx.onOpenFile", () => {
    const { ctx } = mount(simple());
    fireEvent.click(screen.getByRole("button", { name: /unit-spec\.pdf/ }));
    expect(ctx.onOpenFile).toHaveBeenCalledTimes(1);
    expect(ctx.onOpenFile).toHaveBeenCalledWith(203);
    expect(ctx.onOpenImage).not.toHaveBeenCalled();
  });
});

describe("TicketContent: purity", () => {
  it("makes no api call by rendering or jumping", () => {
    mount(simple());
    fireEvent.click(screen.getByRole("button", { name: /^links\s*2$/i }));
    expect(screen.getByRole("button", { name: /^links\s*2$/i })).toBeTruthy();
    expect(apiCalls).toEqual([]);
  });
});
