// WP-T8: Comments section (ux-spec §14.5 "Comments"; props.ts CommentsProps, PendingComment).
//
// SPEC-NOTE (DOM): Comments renders <section data-section="comments"> headed (h2) "COMMENTS <n>" where n =
//   comments.length (pending comments are NOT counted; zero comments still renders the section, the heading "COMMENTS 0"
//   and the composer, because the composer is the way to add the first comment).
//   Inside it: the order Segmented (radiogroup named /order/i with radios "Newest" and "Oldest", aria-checked on the
//   current `order`), the Composer (real, fed `composer`), then the list. Hooks (jsdom has no CSS):
//     - each stored comment root: data-comment-id="<id>"; data-self="true" only when author.self (absent or "false"
//       otherwise); the author's name and initials are visible text; "edited" (exact text) only when edited; the
//       visibility text (e.g. "Developers only", case-insensitive, plus a lock icon) only when visibility != "";
//       the body is an AdfDoc at size "comment" ([data-size="comment"]); the meta line reads
//       "<Ddd D Mon HH:MM> · <age>" in local time, e.g. "Mon 23 Nov 08:55 · 29m" (24-hour, zero padded time);
//       age is the whole minutes (< 1 h), whole hours (< 1 d) or whole days since createdAt, taken from Date.now() as
//       CommentsProps has no clock (the test pins it with vi.setSystemTime);
//     - the standup marker: data-marker="standup", text "↑ <N> NEW SINCE STANDUP · FRI 20 NOV" (case-insensitive,
//       the day is newSince's local weekday, day and month; "↓" instead of "↑" in oldest-first order);
//     - each pending comment: data-pending-id="<localId>" and data-state="sending" | "failed".
// SPEC-NOTE (order): `comments` arrive oldest first. "newest" draws them reversed, "oldest" as given. ALL are drawn
//   (no "show older", no cap). The toggle calls onOrder with the clicked value only; persistence (app setting
//   ticket.comment_order) is the caller's, so this component makes no api call.
// SPEC-NOTE (marker): a comment is "new" when createdAt > newSince; N = how many are new. The marker is drawn only
//   when newSince > 0 and N > 0. Newest-first it sits BELOW the new comments (between the oldest new one and the
//   newest old one: ids 5,4,3,marker,2,1); oldest-first it sits ABOVE them (1,2,marker,3,4,5). If every comment is new
//   it is the last element newest-first and the first oldest-first. Pending comments never count.
// SPEC-NOTE (pending): pending comments are drawn at the newest end: first in newest-first order, last in oldest-first,
//   outside the marker logic. They show the composer's selfName and selfInitials as the author and the input text as
//   plain text. state "sending": no buttons; "sending…" (with the ellipsis character) appears on the author line only
//   after 250 ms (absent at 249 ms, present at 250 ms, measured from when the row appeared). state "failed": a line
//   "Not sent · <reason>" (the component adds the "Not sent · " prefix; `reason` is only the cause) and a separate
//   "Your text is kept" notice, then Retry / Edit / Discard buttons that call onRetry / onEdit / onDiscard with the
//   localId exactly once per click. NOTHING retries automatically: no timer ever calls onRetry.
// SPEC-NOTE: this suite gives the composer a real Composer; typing reaches composer.onChange.
import { act, fireEvent, render, screen, within } from "@testing-library/preact";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { CommentView } from "../../api/types";
import { doc, makeCtx, p, text } from "../../components/adf/testdata/helpers";
import { Comments } from "./Comments";
import type { CommentsProps, PendingComment } from "./props";

const at = (d: number, h: number, m: number) => new Date(2026, 10, d, h, m, 0).getTime();
const NOW = at(23, 9, 24);
const NEW_SINCE = at(20, 0, 0); // Fri 20 Nov 00:00 local

function cv(id: string, name: string, initials: string, createdAt: number, body: string, over: Partial<CommentView> = {}): CommentView {
  return {
    id,
    author: { accountId: name.toLowerCase().replace(/\W/g, "-"), name, initials, self: false },
    createdAt,
    updatedAt: createdAt,
    edited: false,
    visibility: "",
    body: doc(p(text(body))),
    ...over,
  };
}

const C1 = cv("1", "Silas Marchetti", "SM", at(18, 14, 0), "First body");
const C2 = cv("2", "Noor Abadi", "NA", at(19, 9, 0), "Second body");
const C3 = cv("3", "Wren Kowalski", "WK", at(20, 9, 30), "Third body", { edited: true });
const C4 = cv("4", "Mock User", "MU", at(23, 6, 24), "Fourth body", { visibility: "Developers only", author: { accountId: "mock-user", name: "Mock User", initials: "MU", self: true } });
const C5 = cv("5", "Iris Calloway", "IC", at(23, 8, 55), "Fifth body");
const ALL = [C1, C2, C3, C4, C5];

function pending(over: Partial<PendingComment> = {}): PendingComment {
  return { localId: "L1", input: { text: "Raised in standup: moving this to the top", mentions: [] }, createdAt: Date.now(), state: "sending", reason: "", ...over };
}

function props(over: Partial<CommentsProps> = {}): CommentsProps {
  return {
    comments: ALL,
    order: "newest",
    onOrder: vi.fn(),
    newSince: NEW_SINCE,
    pending: [],
    onRetry: vi.fn(),
    onEdit: vi.fn(),
    onDiscard: vi.fn(),
    composer: {
      draft: { text: "", mentions: [] },
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
    ctx: makeCtx(),
    ...over,
  };
}

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(NOW);
});
afterEach(() => {
  vi.useRealTimers();
});

const mount = (over: Partial<CommentsProps> = {}) => {
  const pr = props(over);
  const view = render(<Comments {...pr} />);
  return { pr, ...view };
};
/** Document order of everything the list draws: comment ids, pending ids (as "pending:<id>"), and the marker. */
const sequence = (root: ParentNode) =>
  Array.from(root.querySelectorAll("[data-comment-id], [data-pending-id], [data-marker='standup']")).map((e) =>
    e.hasAttribute("data-marker") ? "marker" : e.hasAttribute("data-comment-id") ? (e.getAttribute("data-comment-id") as string) : `pending:${e.getAttribute("data-pending-id")}`,
  );
const comment = (id: string) => document.querySelector(`[data-comment-id="${id}"]`) as HTMLElement;

describe("Comments: section and order toggle", () => {
  it("renders the section headed COMMENTS with the stored count (pending excluded)", () => {
    const { container } = mount({ pending: [pending()] });
    const sec = container.querySelector('section[data-section="comments"]') as HTMLElement;
    expect(sec).not.toBeNull();
    expect(within(sec).getByRole("heading", { name: /^comments\s*5$/i })).toBeTruthy();
  });

  it("with no comments still shows the heading, the composer and no marker", () => {
    mount({ comments: [] });
    expect(screen.getByRole("heading", { name: /^comments\s*0$/i })).toBeTruthy();
    expect(screen.getByRole("textbox", { name: /comment/i })).toBeTruthy();
    expect(document.querySelector("[data-marker]")).toBeNull();
  });

  it("offers Newest / Oldest with the current order selected", () => {
    mount({ order: "oldest" });
    const group = screen.getByRole("radiogroup", { name: /order/i });
    expect(within(group).getByRole("radio", { name: "Oldest" }).getAttribute("aria-checked")).toBe("true");
    expect(within(group).getByRole("radio", { name: "Newest" }).getAttribute("aria-checked")).toBe("false");
  });

  it("defaults to newest first in the radiogroup when order is newest", () => {
    mount({ order: "newest" });
    expect(screen.getByRole("radio", { name: "Newest" }).getAttribute("aria-checked")).toBe("true");
  });

  it("calls onOrder with the clicked order and nothing else (persisting is the caller's)", () => {
    const { pr } = mount({ order: "newest" });
    fireEvent.click(screen.getByRole("radio", { name: "Oldest" }));
    expect(pr.onOrder).toHaveBeenCalledTimes(1);
    expect(pr.onOrder).toHaveBeenCalledWith("oldest");
  });

  it("does not call onOrder when the current order is clicked again", () => {
    const { pr } = mount({ order: "newest" });
    fireEvent.click(screen.getByRole("radio", { name: "Newest" }));
    expect(pr.onOrder).not.toHaveBeenCalled();
  });
});

describe("Comments: drawing order and completeness", () => {
  it("newest first reverses the oldest-first input", () => {
    const { container } = mount({ order: "newest", newSince: 0 });
    expect(sequence(container)).toEqual(["5", "4", "3", "2", "1"]);
  });

  it("oldest first keeps the input order", () => {
    const { container } = mount({ order: "oldest", newSince: 0 });
    expect(sequence(container)).toEqual(["1", "2", "3", "4", "5"]);
  });

  it("draws every comment: no cap and no show-older control", () => {
    const many = Array.from({ length: 60 }, (_, i) => cv(String(1000 + i), `Person ${i}`, "PP", at(1, 9, 0) + i * 60_000, `Body ${i}`));
    const { container } = mount({ comments: many, newSince: 0 });
    expect(sequence(container)).toHaveLength(60);
    expect(screen.queryByRole("button", { name: /older|more|show/i })).toBeNull();
    expect(screen.getByRole("heading", { name: /^comments\s*60$/i })).toBeTruthy();
  });
});

describe("Comments: one comment", () => {
  it("shows author name, initials, body at comment size, and the date and age line", () => {
    mount();
    const c5 = comment("5");
    expect(within(c5).getByText("Iris Calloway")).toBeTruthy();
    expect(within(c5).getByText("IC")).toBeTruthy();
    expect(within(c5).getByText("Fifth body")).toBeTruthy();
    expect(c5.querySelector('[data-size="comment"]')).not.toBeNull();
    expect((c5.textContent ?? "").replace(/\s+/g, " ")).toMatch(/Mon 23 Nov 08:55\s*·\s*29m/);
  });

  it("ages in whole hours and whole days", () => {
    mount();
    expect((comment("4").textContent ?? "").replace(/\s+/g, " ")).toMatch(/Mon 23 Nov 06:24\s*·\s*3h/);
    expect((comment("2").textContent ?? "").replace(/\s+/g, " ")).toMatch(/Thu 19 Nov 09:00\s*·\s*4d/);
  });

  it("labels only the edited comment as edited", () => {
    mount();
    expect(within(comment("3")).getByText(/^edited$/i)).toBeTruthy();
    for (const id of ["1", "2", "4", "5"]) expect(within(comment(id)).queryByText(/^edited$/i)).toBeNull();
  });

  it("shows the restricted-visibility tag only on the restricted comment", () => {
    mount();
    expect(within(comment("4")).getByText(/developers only/i)).toBeTruthy();
    for (const id of ["1", "2", "3", "5"]) expect(within(comment(id)).queryByText(/developers only/i)).toBeNull();
  });

  it("highlights only the connected user's own comments", () => {
    mount();
    expect(comment("4").getAttribute("data-self")).toBe("true");
    for (const id of ["1", "2", "3", "5"]) expect(comment(id).getAttribute("data-self")).not.toBe("true");
  });
});

describe("Comments: new-since-standup marker", () => {
  it("newest first: sits below the comments newer than the standup, reading ↑ 3 NEW SINCE STANDUP · FRI 20 NOV", () => {
    const { container } = mount({ order: "newest" });
    expect(sequence(container)).toEqual(["5", "4", "3", "marker", "2", "1"]);
    const m = container.querySelector("[data-marker='standup']") as HTMLElement;
    expect((m.textContent ?? "").replace(/\s+/g, " ").trim()).toMatch(/^↑\s*3 NEW SINCE STANDUP\s*·\s*FRI 20 NOV$/i);
  });

  it("oldest first: sits above the new comments and the arrow flips", () => {
    const { container } = mount({ order: "oldest" });
    expect(sequence(container)).toEqual(["1", "2", "marker", "3", "4", "5"]);
    const m = container.querySelector("[data-marker='standup']") as HTMLElement;
    expect((m.textContent ?? "").replace(/\s+/g, " ").trim()).toMatch(/^↓\s*3 NEW SINCE STANDUP\s*·\s*FRI 20 NOV$/i);
  });

  it("when every comment is new the marker is the last newest-first and the first oldest-first", () => {
    const early = at(17, 0, 0);
    const a = mount({ order: "newest", newSince: early });
    expect(sequence(a.container)).toEqual(["5", "4", "3", "2", "1", "marker"]);
    a.unmount();
    const b = mount({ order: "oldest", newSince: early });
    expect(sequence(b.container)).toEqual(["marker", "1", "2", "3", "4", "5"]);
  });

  it("is absent when newSince is 0", () => {
    const { container } = mount({ newSince: 0 });
    expect(sequence(container)).toEqual(["5", "4", "3", "2", "1"]);
    expect(container.querySelector("[data-marker]")).toBeNull();
  });

  it("is absent when no comment is newer than newSince", () => {
    const { container } = mount({ newSince: at(24, 0, 0) });
    expect(sequence(container)).toEqual(["5", "4", "3", "2", "1"]);
  });

  it("counts a comment created exactly at newSince as old", () => {
    const edge = cv("6", "Edge Case", "EC", NEW_SINCE, "On the boundary");
    const { container } = mount({ comments: [C1, C2, edge, C5], order: "oldest" });
    expect(sequence(container)).toEqual(["1", "2", "6", "marker", "5"]);
    expect((container.querySelector("[data-marker]")?.textContent ?? "")).toMatch(/1 NEW SINCE STANDUP/i);
  });
});

describe("Comments: pending (sending)", () => {
  it("draws the pending comment at the newest end, outside the marker", () => {
    const a = mount({ order: "newest", pending: [pending()] });
    expect(sequence(a.container)).toEqual(["pending:L1", "5", "4", "3", "marker", "2", "1"]);
    expect(within(a.container.querySelector("[data-marker]") as HTMLElement).getByText(/3 NEW/i)).toBeTruthy();
    a.unmount();
    const b = mount({ order: "oldest", pending: [pending()] });
    expect(sequence(b.container)).toEqual(["1", "2", "marker", "3", "4", "5", "pending:L1"]);
  });

  it("shows the author as the connected user and the text as plain text, state sending, with no buttons", () => {
    mount({ pending: [pending()] });
    const row = document.querySelector('[data-pending-id="L1"]') as HTMLElement;
    expect(row.getAttribute("data-state")).toBe("sending");
    expect(within(row).getByText("Mock User")).toBeTruthy();
    expect(within(row).getByText("MU")).toBeTruthy();
    expect(within(row).getByText("Raised in standup: moving this to the top")).toBeTruthy();
    expect(within(row).queryAllByRole("button")).toEqual([]);
  });

  it("shows 'sending…' only after 250 ms", () => {
    mount({ pending: [pending()] });
    const row = () => document.querySelector('[data-pending-id="L1"]') as HTMLElement;
    expect(within(row()).queryByText(/sending…/)).toBeNull();
    act(() => { vi.advanceTimersByTime(249); });
    expect(within(row()).queryByText(/sending…/)).toBeNull();
    act(() => { vi.advanceTimersByTime(1); });
    expect(within(row()).getByText(/sending…/)).toBeTruthy();
  });
});

describe("Comments: pending (sent, but the refresh failed)", () => {
  it("shows the text as sent (no buttons, no failure) with the local-copy note", () => {
    mount({ pending: [pending({ state: "sent" })] });
    const row = document.querySelector('[data-pending-id="L1"]') as HTMLElement;
    expect(row.getAttribute("data-state")).toBe("sent");
    expect(within(row).getByText("Raised in standup: moving this to the top")).toBeTruthy();
    expect(within(row).getByText("Couldn't refresh · showing your comment locally")).toBeTruthy();
    expect(within(row).queryByText(/Not sent/)).toBeNull();
    expect(within(row).queryByRole("button")).toBeNull();
    expect(within(row).queryByText(/sending…/)).toBeNull();
  });
});

describe("Comments: pending (failed)", () => {
  const failed = () => pending({ state: "failed", reason: "couldn't reach Jira (offline)" });

  it("shows Not sent · reason, that the text is kept, and the text itself", () => {
    mount({ pending: [failed()] });
    const row = document.querySelector('[data-pending-id="L1"]') as HTMLElement;
    expect(row.getAttribute("data-state")).toBe("failed");
    expect((row.textContent ?? "").replace(/\s+/g, " ")).toMatch(/Not sent\s*·\s*couldn't reach Jira \(offline\)/);
    expect(within(row).getByText(/your text is kept/i)).toBeTruthy();
    expect(within(row).getByText("Raised in standup: moving this to the top")).toBeTruthy();
    expect(within(row).queryByText(/sending…/)).toBeNull();
  });

  it("offers Retry, Edit and Discard, each calling back with the localId exactly once", () => {
    const { pr } = mount({ pending: [failed()] });
    const row = document.querySelector('[data-pending-id="L1"]') as HTMLElement;
    fireEvent.click(within(row).getByRole("button", { name: "Retry" }));
    expect(pr.onRetry).toHaveBeenCalledTimes(1);
    expect(pr.onRetry).toHaveBeenCalledWith("L1");
    fireEvent.click(within(row).getByRole("button", { name: "Edit" }));
    expect(pr.onEdit).toHaveBeenCalledTimes(1);
    expect(pr.onEdit).toHaveBeenCalledWith("L1");
    fireEvent.click(within(row).getByRole("button", { name: "Discard" }));
    expect(pr.onDiscard).toHaveBeenCalledTimes(1);
    expect(pr.onDiscard).toHaveBeenCalledWith("L1");
    expect(pr.onRetry).toHaveBeenCalledTimes(1);
  });

  it("never retries by itself: no onRetry as time passes", () => {
    const { pr } = mount({ pending: [failed()] });
    expect(screen.getByRole("button", { name: "Retry" })).toBeTruthy(); // the failed state is really drawn
    act(() => { vi.advanceTimersByTime(10 * 60_000); });
    expect(pr.onRetry).not.toHaveBeenCalled();
    expect(pr.onEdit).not.toHaveBeenCalled();
    expect(pr.onDiscard).not.toHaveBeenCalled();
  });

  it("routes each of several failed comments by its own localId", () => {
    const { pr } = mount({
      pending: [pending({ localId: "A", state: "failed", reason: "offline" }), pending({ localId: "B", state: "failed", reason: "offline", input: { text: "second try", mentions: [] } })],
    });
    const rowB = document.querySelector('[data-pending-id="B"]') as HTMLElement;
    fireEvent.click(within(rowB).getByRole("button", { name: "Discard" }));
    expect(pr.onDiscard).toHaveBeenCalledTimes(1);
    expect(pr.onDiscard).toHaveBeenCalledWith("B");
  });
});

describe("Comments: composer", () => {
  it("renders the composer and wires typing to composer.onChange", () => {
    const { pr } = mount();
    const box = screen.getByRole("textbox", { name: /comment/i });
    fireEvent.input(box, { target: { value: "hello" } });
    expect(pr.composer.onChange).toHaveBeenLastCalledWith({ text: "hello", mentions: [] });
  });

  it("wires Ctrl+Enter to composer.onSend once", () => {
    const composer = { ...props().composer, draft: { text: "ship it", mentions: [] } };
    mount({ composer });
    fireEvent.keyDown(screen.getByRole("textbox", { name: /comment/i }), { key: "Enter", ctrlKey: true });
    expect(composer.onSend).toHaveBeenCalledTimes(1);
    expect(composer.onSend).toHaveBeenCalledWith({ text: "ship it", mentions: [] });
  });
});
