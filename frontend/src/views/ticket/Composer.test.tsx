/* eslint-disable @typescript-eslint/no-explicit-any */
// WP-T8: Composer (ux-spec §14.5 "Composer", "Sending"; props.ts ComposerProps).
//
// SPEC-NOTE (layering, needs orchestrator confirmation): the frozen ComposerProps are fully prop driven, and drafts live
//   in state/ticket*.ts (WP-T7, F12-freeze "Ownership gaps"). So the Composer NEVER calls the api itself. The 400 ms
//   draft debounce, TicketService.SaveDraft, "flush on blur/close" and clearing the draft after a successful
//   AddComment are T7's state and are NOT tested here. What IS pinned is the prop protocol the state layer needs:
//     - onChange(input) fires immediately, once per edit, carrying the full CommentInput (text + mentions). It is
//       NOT debounced inside the Composer: the state layer debounces and flushes it on onBlur;
//     - onBlur() fires once when the text box loses focus;
//     - the text box shows `draft.text`, and re-shows it whenever the `draft` prop changes (draft survives reopening;
//       the parent clearing the draft after a successful send empties the box; "Edit" on a failed comment puts its
//       text back by changing the prop).
//   A test installs a recording Api that rejects everything and asserts no call is made.
// SPEC-NOTE (DOM): the text box is a <textarea> (role textbox) with accessible name matching /comment/i; Send is a
//   button named /send/i, disabled when the trimmed text is empty or when `disabled`. The hint line reads
//   "Ctrl+Enter to send · posts to Jira as <selfName>" (both texts must be present; matched separately). The initials
//   badge shows selfInitials.
// SPEC-NOTE (send): Ctrl+Enter or Cmd+Enter (keydown, key "Enter") inside the text box, or a Send click, calls
//   onSend({text, mentions}) EXACTLY once per gesture; plain Enter and Shift+Enter never send (they insert a newline).
//   Empty or whitespace-only text never sends, from either path. A disabled composer never sends. The Composer does
//   not clear its own text on send (the parent owns the draft). onSend receives the text as typed (not trimmed).
// SPEC-NOTE (disabled): `disabled` disables the text box and Send and shows `disabledReason` as visible text.
// SPEC-NOTE (focus): bumping `focusToken` (key C) moves focus into the text box; an unchanged token never steals focus.
// SPEC-NOTE (mentions): typing "@" in the text box opens the shared Combobox (invariant 4) labelled /mention|people/i,
//   filled from `searchMentions`: its `load(query, signal)` delegates to searchMentions(query, signal) unchanged.
//   Selecting a person replaces the "@" with "@<Name>" (followed by a space) at the caret, and calls onChange with the
//   new text and a Mention {start, end, accountId, name} where start/end are RUNE offsets (End exclusive) covering the
//   "@" and the name, as in the MOB-1429 fixture (62..74 for "@Jonas Reyes"). The picker closes after selection.
//   Without an "@" no picker is drawn. The Combobox is a recording fake here (its DOM is specified by its own tests).
// SPEC-NOTE (mentions, keep): editing text AFTER an existing mention keeps that mention in onChange's mentions.
import { act, fireEvent, render, screen } from "@testing-library/preact";
import { useState } from "preact/hooks";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setApi, type Api } from "../../api";
import type { CommentInput, UserOption } from "../../api/types";
import { Composer } from "./Composer";
import type { ComposerProps } from "./props";

const reg = vi.hoisted(() => ({ last: null as any, count: 0 }));
vi.mock("../../components/Combobox", async () => {
  const { h } = await import("preact");
  return {
    Combobox: (p: any) => {
      reg.last = p;
      reg.count++;
      return h("input", { role: "combobox", "aria-label": p.label, readOnly: true, value: "" });
    },
  };
});

const JONAS: UserOption = { accountId: "jonas", displayName: "Jonas Reyes", initials: "JR", onBoard: false };
const EMPTY: CommentInput = { text: "", mentions: [] };

let apiCalls: string[] = [];
beforeEach(() => {
  reg.last = null;
  reg.count = 0;
  apiCalls = [];
  setApi(new Proxy({}, { get: (_t, svc: string) => new Proxy({}, { get: (_s, m: string) => () => { apiCalls.push(`${svc}.${m}`); return Promise.reject(new Error("unstubbed")); } }) }) as unknown as Api);
  vi.useFakeTimers();
});
afterEach(() => {
  vi.useRealTimers();
});

function props(over: Partial<ComposerProps> = {}): ComposerProps {
  return {
    draft: EMPTY,
    selfName: "Mock User",
    selfInitials: "MU",
    disabled: false,
    disabledReason: "",
    focusToken: 0,
    onChange: vi.fn(),
    onSend: vi.fn(),
    onBlur: vi.fn(),
    searchMentions: vi.fn(async () => [JONAS]),
    ...over,
  };
}

/** The parent's role: owns the draft, feeds onChange back in. */
function Harness(p: { init?: CommentInput; over?: Partial<ComposerProps>; spy: { change: any; send: any } }) {
  const [draft, setDraft] = useState<CommentInput>(p.init ?? EMPTY);
  return (
    <Composer
      {...props(p.over)}
      draft={draft}
      onChange={(i) => {
        p.spy.change(i);
        setDraft(i);
      }}
      onSend={(i) => p.spy.send(i)}
    />
  );
}
const harness = (init?: CommentInput, over?: Partial<ComposerProps>) => {
  const spy = { change: vi.fn(), send: vi.fn() };
  const view = render(<Harness init={init} over={over} spy={spy} />);
  return { spy, ...view };
};

const box = () => screen.getByRole("textbox", { name: /comment/i }) as HTMLTextAreaElement;
const send = () => screen.getByRole("button", { name: /send/i }) as HTMLButtonElement;
const type = (value: string) => fireEvent.input(box(), { target: { value } });
const ctrlEnter = () => fireEvent.keyDown(box(), { key: "Enter", ctrlKey: true });
const metaEnter = () => fireEvent.keyDown(box(), { key: "Enter", metaKey: true });

describe("Composer: layout", () => {
  it("shows the initials badge, the text box, Send and the posts-as hint", () => {
    render(<Composer {...props()} />);
    expect(box().tagName).toBe("TEXTAREA");
    expect(screen.getByText("MU")).toBeTruthy();
    expect(screen.getByText(/ctrl\+enter to send/i)).toBeTruthy();
    expect(screen.getByText(/posts to jira as Mock User/i)).toBeTruthy();
    expect(send()).toBeTruthy();
  });

  it("shows the draft text it is given (a draft survives reopening)", () => {
    render(<Composer {...props({ draft: { text: "Unblocked once PR-9 merges.", mentions: [] } })} />);
    expect(box().value).toBe("Unblocked once PR-9 merges.");
  });

  it("re-shows the box content when the draft prop changes (Edit on a failed comment, or cleared after a send)", () => {
    const view = render(<Composer {...props()} />);
    expect(box().value).toBe("");
    view.rerender(<Composer {...props({ draft: { text: "put back by Edit", mentions: [] } })} />);
    expect(box().value).toBe("put back by Edit");
    view.rerender(<Composer {...props({ draft: EMPTY })} />);
    expect(box().value).toBe("");
  });
});

describe("Composer: typing autosaves through onChange", () => {
  it("calls onChange with the full input on every edit, immediately (the state layer debounces)", () => {
    const { spy } = harness();
    type("H");
    expect(spy.change).toHaveBeenLastCalledWith({ text: "H", mentions: [] });
    type("Hi there");
    expect(spy.change).toHaveBeenCalledTimes(2);
    expect(spy.change).toHaveBeenLastCalledWith({ text: "Hi there", mentions: [] });
    expect(box().value).toBe("Hi there");
  });

  it("does nothing by itself as time passes: no extra onChange, no send, no api call", () => {
    const { spy } = harness();
    type("draft text");
    act(() => { vi.advanceTimersByTime(60_000); });
    expect(spy.change).toHaveBeenCalledTimes(1);
    expect(spy.send).not.toHaveBeenCalled();
    expect(apiCalls).toEqual([]);
  });

  it("calls onBlur once when the text box loses focus", () => {
    const onBlur = vi.fn();
    render(<Composer {...props({ onBlur })} />);
    fireEvent.focus(box());
    expect(onBlur).not.toHaveBeenCalled();
    fireEvent.blur(box());
    expect(onBlur).toHaveBeenCalledTimes(1);
  });

  it("never calls the api itself", () => {
    const { spy } = harness();
    type("hello");
    ctrlEnter();
    expect(spy.send).toHaveBeenCalledTimes(1);
    expect(apiCalls).toEqual([]);
  });
});

describe("Composer: sending", () => {
  it("disables Send while the text is empty or whitespace only", () => {
    const view = render(<Composer {...props()} />);
    expect(send().disabled).toBe(true);
    view.rerender(<Composer {...props({ draft: { text: "   \n ", mentions: [] } })} />);
    expect(send().disabled).toBe(true);
    view.rerender(<Composer {...props({ draft: { text: "ok", mentions: [] } })} />);
    expect(send().disabled).toBe(false);
  });

  it("Ctrl+Enter sends exactly once with the typed text", () => {
    const { spy } = harness();
    type("Looks good to me");
    ctrlEnter();
    expect(spy.send).toHaveBeenCalledTimes(1);
    expect(spy.send).toHaveBeenCalledWith({ text: "Looks good to me", mentions: [] });
  });

  it("Cmd+Enter sends exactly once too", () => {
    const { spy } = harness();
    type("From a Mac");
    metaEnter();
    expect(spy.send).toHaveBeenCalledTimes(1);
    expect(spy.send).toHaveBeenCalledWith({ text: "From a Mac", mentions: [] });
  });

  it("the Send button sends exactly once", () => {
    const { spy } = harness();
    type("Clicked");
    fireEvent.click(send());
    expect(spy.send).toHaveBeenCalledTimes(1);
    expect(spy.send).toHaveBeenCalledWith({ text: "Clicked", mentions: [] });
  });

  it("plain Enter and Shift+Enter do not send", () => {
    const { spy } = harness();
    type("one line");
    fireEvent.keyDown(box(), { key: "Enter" });
    fireEvent.keyDown(box(), { key: "Enter", shiftKey: true });
    expect(spy.send).not.toHaveBeenCalled();
    expect(box().value).toBe("one line"); // and the text is intact
  });

  it("does not send empty text from either path", () => {
    const { spy } = harness();
    expect(send().disabled).toBe(true);
    ctrlEnter();
    fireEvent.click(send());
    type("   ");
    ctrlEnter();
    expect(spy.send).not.toHaveBeenCalled();
  });

  it("sends the mentions the draft carries", () => {
    const text = "Unblocked once PR-9 merges. I'll re-test on the Android beta. @Jonas Reyes can you pair at 2?";
    const mentions = [{ start: 62, end: 74, accountId: "jonas", name: "Jonas Reyes" }];
    const onSend = vi.fn();
    render(<Composer {...props({ draft: { text, mentions }, onSend })} />);
    ctrlEnter();
    expect(onSend).toHaveBeenCalledTimes(1);
    expect(onSend).toHaveBeenCalledWith({ text, mentions });
  });
});

describe("Composer: after a successful send the parent clears the draft", () => {
  it("empties the box and disables Send again when the draft prop goes empty", () => {
    const onSend = vi.fn();
    const view = render(<Composer {...props({ draft: { text: "sent text", mentions: [] }, onSend })} />);
    ctrlEnter();
    expect(onSend).toHaveBeenCalledTimes(1);
    expect(box().value).toBe("sent text"); // not cleared until the parent says so
    view.rerender(<Composer {...props({ draft: EMPTY, onSend })} />);
    expect(box().value).toBe("");
    expect(send().disabled).toBe(true);
    expect(onSend).toHaveBeenCalledTimes(1);
  });
});

describe("Composer: disabled", () => {
  it("disables the box and Send, shows the reason, and never sends", () => {
    const onSend = vi.fn();
    render(<Composer {...props({ draft: { text: "can't send this", mentions: [] }, disabled: true, disabledReason: "You can't comment on this issue", onSend })} />);
    expect(box().disabled).toBe(true);
    expect(send().disabled).toBe(true);
    expect(screen.getByText("You can't comment on this issue")).toBeTruthy();
    fireEvent.click(send());
    ctrlEnter();
    expect(onSend).not.toHaveBeenCalled();
  });

  it("shows no reason text when enabled", () => {
    render(<Composer {...props({ disabledReason: "should stay hidden" })} />);
    expect(screen.queryByText("should stay hidden")).toBeNull();
    expect(box().disabled).toBe(false);
  });
});

describe("Composer: focus token", () => {
  it("focuses the box when focusToken is bumped, and not otherwise", () => {
    const view = render(<Composer {...props({ focusToken: 0 })} />);
    expect(document.activeElement).not.toBe(box());
    view.rerender(<Composer {...props({ focusToken: 1 })} />);
    expect(document.activeElement).toBe(box());
    (document.activeElement as HTMLElement).blur();
    view.rerender(<Composer {...props({ focusToken: 1 })} />);
    expect(document.activeElement).not.toBe(box());
    view.rerender(<Composer {...props({ focusToken: 2 })} />);
    expect(document.activeElement).toBe(box());
  });
});

describe("Composer: @mentions", () => {
  it("opens no picker until an @ is typed", () => {
    render(<Composer {...props()} />);
    expect(screen.queryByRole("combobox")).toBeNull();
    type("plain words");
    expect(screen.queryByRole("combobox")).toBeNull();
  });

  it("opens the people Combobox on @, loading through searchMentions unchanged", async () => {
    const searchMentions = vi.fn(async (_q: string, _s: AbortSignal) => [JONAS]);
    harness(undefined, { searchMentions });
    type("Hi @");
    expect(screen.getByRole("combobox", { name: /mention|people/i })).toBeTruthy();
    const ctl = new AbortController();
    const got = await reg.last.load("jo", ctl.signal);
    expect(searchMentions).toHaveBeenCalledWith("jo", ctl.signal);
    expect(got).toEqual([JONAS]);
    expect(reg.last.getLabel(JONAS)).toContain("Jonas Reyes");
    expect(reg.last.getKey(JONAS)).toBeTruthy();
  });

  it("inserts the chosen person as @Name and records a mention with rune offsets", () => {
    const { spy } = harness();
    type("Hi @");
    act(() => reg.last.onSelect(JONAS));
    const last = spy.change.mock.calls.at(-1)?.[0] as CommentInput;
    expect(last.text.startsWith("Hi @Jonas Reyes")).toBe(true);
    expect(last.mentions).toEqual([{ start: 3, end: 15, accountId: "jonas", name: "Jonas Reyes" }]);
    expect(box().value).toBe(last.text);
    expect(screen.queryByRole("combobox")).toBeNull(); // the picker closes
  });

  it("counts offsets in runes, not UTF-16 units (an emoji before the @ shifts by one)", () => {
    const { spy } = harness();
    type("\u{1F600} @"); // 😀, space, @  -> the @ is the 3rd rune, the 4th UTF-16 unit
    act(() => reg.last.onSelect(JONAS));
    const last = spy.change.mock.calls.at(-1)?.[0] as CommentInput;
    expect(last.text.startsWith("\u{1F600} @Jonas Reyes")).toBe(true);
    expect(last.mentions).toEqual([{ start: 2, end: 14, accountId: "jonas", name: "Jonas Reyes" }]);
  });

  it("selecting a mention does not send anything", () => {
    const { spy } = harness();
    type("@");
    act(() => reg.last.onSelect(JONAS));
    expect(spy.send).not.toHaveBeenCalled();
  });

  it("keeps an existing mention when text is added after it", () => {
    const text = "@Jonas Reyes can you pair";
    const mentions = [{ start: 0, end: 12, accountId: "jonas", name: "Jonas Reyes" }];
    const { spy } = harness({ text, mentions });
    type(`${text} at 2?`);
    const last = spy.change.mock.calls.at(-1)?.[0] as CommentInput;
    expect(last.text).toBe("@Jonas Reyes can you pair at 2?");
    expect(last.mentions).toEqual(mentions);
  });
});
