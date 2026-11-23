// Combobox contract tests (architecture §4.6, ux-spec §8 last line, §9.3, §10; AGENTS invariant 4).
//
// SPEC-NOTE: Filtering keeps the original option order inside each group: prefix matches first (in source order), then
//   substring matches (in source order). The haystack is getLabel(o). Loaded (async) results are rendered as returned,
//   with no local re-filtering (the server already filtered).
// SPEC-NOTE: aria-selected="true" marks the COMMITTED value (the `selected` render state); the active option is conveyed
//   by aria-activedescendant on the input and by the `active` render state. Every option carries aria-selected.
// SPEC-NOTE: Opening: typing, ArrowDown, ArrowUp and Alt+ArrowDown open the list. Whether a plain click or focus opens it
//   is deliberately NOT specified. ArrowDown on a closed list opens it with the FIRST option active. Wrap-around of
//   ArrowUp on the first option and ArrowDown on the last is NOT specified.
// SPEC-NOTE: The second Esc is "an Esc pressed while the list is already closed". It calls onClear when `clearable` is
//   set, and never otherwise. The first Esc (closing an open list) never calls onClear.
// SPEC-NOTE: Below minChars the list does not show options and does not show the empty text; for `load`, the loader is
//   not called below minChars.
// SPEC-NOTE: Async with an empty query and minChars 0: opening with ArrowDown calls load("", signal) after the same
//   150 ms debounce ("opens with the full/initial list").
// SPEC-NOTE: The "Searching…" row appears only when a request is still pending after 200 ms. Whether those 200 ms count
//   from the keystroke or from the request start is ambiguous, so the tests assert only what both readings share:
//   absent 199 ms after the keystroke, present 350 ms after it while still pending, and never shown when the loader
//   resolves at the instant the request starts (150 ms after the keystroke).
// SPEC-NOTE: Error row: found by its Retry button (role=button, name /retry/i), no options shown, and the raw error
//   text ("boom") never appears in the DOM (AGENTS §7: no raw detail in user-facing messages). Clicking Retry calls
//   load again with the same query and focus stays in the input. The "Offline — showing cached" variant cannot be
//   signalled through the frozen props, so it is NOT tested here.
// SPEC-NOTE: Max 8 rows: the listbox carries data-max-rows="8" and a non-empty inline style.maxHeight (jsdom has no
//   layout). ALL options stay in the DOM (nothing hidden to fit; the list scrolls). position:fixed anchoring and
//   flip-above are layout and are not asserted here. The active option is scrolled with
//   scrollIntoView({block:"nearest"}); jsdom lacks scrollIntoView so the tests install a mock.
// SPEC-NOTE: variant="menu": renders exactly one button (before opening) whose content includes menuLabel and which has
//   aria-expanded. Clicking it opens the same role=listbox. After the click, typing filters (keystrokes are delivered
//   through user.keyboard, wherever the implementation puts focus), ArrowDown/Enter/Esc follow the field contract.
//   The first option is active when typing leaves matches.
// SPEC-NOTE: Disabled: the input (or the menu button) has the `disabled` property, and no key opens a list.

import { render, screen, within, act } from "@testing-library/preact";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import { useState } from "preact/hooks";
import { Combobox } from "./Combobox";
import type { ComboboxProps } from "./props";

interface Item {
  id: string;
  name: string;
}
const mk = (name: string): Item => ({ id: name.toLowerCase(), name });
const FRUIT = ["Pineapple", "Apricot", "Grape", "Apple", "Banana", "Cherry"].map(mk);
const [, APRICOT, GRAPE, APPLE, BANANA] = FRUIT as [Item, Item, Item, Item, Item, Item];

type Props = ComboboxProps<Item>;
function baseProps(over: Partial<Props> = {}): Props {
  return {
    id: "fruit",
    label: "Fruit",
    value: null,
    getKey: (o) => o.id,
    getLabel: (o) => o.name,
    options: FRUIT,
    onSelect: () => {},
    ...over,
  };
}

/** Stateful wrapper so multi-step sequences behave like the real (controlled) usage. */
function Harness(p: { init?: Item | null; props?: Partial<Props>; onSelect?: (o: Item) => void; onClear?: () => void }) {
  const [value, setValue] = useState<Item | null>(p.init ?? null);
  return (
    <div>
      <Combobox<Item>
        {...baseProps(p.props)}
        value={value}
        onSelect={(o) => {
          setValue(o);
          p.onSelect?.(o);
        }}
        onClear={() => {
          setValue(null);
          p.onClear?.();
        }}
      />
      <button type="button">outside</button>
    </div>
  );
}

const getInput = () => screen.getByRole("combobox", { name: "Fruit" }) as HTMLInputElement;
const optionNames = () => screen.queryAllByRole("option").map((o) => o.textContent?.trim());
const expanded = (el: HTMLElement) => el.getAttribute("aria-expanded");
const activeOption = (input: HTMLElement) => {
  const id = input.getAttribute("aria-activedescendant");
  return id ? document.getElementById(id) : null;
};

beforeEach(() => {
  (Element.prototype as unknown as { scrollIntoView: unknown }).scrollIntoView = vi.fn();
});
afterEach(() => {
  vi.useRealTimers();
  delete (Element.prototype as unknown as { scrollIntoView?: unknown }).scrollIntoView;
});

describe("Combobox ARIA", () => {
  it("exposes a closed combobox input with the accessible name and list autocomplete", () => {
    render(<Combobox<Item> {...baseProps()} />);
    const input = getInput();
    expect(input.tagName).toBe("INPUT");
    expect(input.getAttribute("aria-autocomplete")).toBe("list");
    expect(expanded(input)).toBe("false");
    expect(screen.queryByRole("listbox")).toBeNull();
  });

  it("opens a listbox wired by aria-controls with role=option rows when typing", async () => {
    const user = userEvent.setup();
    render(<Combobox<Item> {...baseProps()} />);
    const input = getInput();
    await user.type(input, "a");
    expect(expanded(input)).toBe("true");
    const listbox = screen.getByRole("listbox");
    expect(input.getAttribute("aria-controls")).toBe(listbox.id);
    expect(listbox.id).not.toBe("");
    const options = within(listbox).getAllByRole("option");
    expect(options.length).toBeGreaterThan(0);
    for (const o of options) {
      expect(o.id).not.toBe("");
      expect(["true", "false"]).toContain(o.getAttribute("aria-selected"));
    }
  });

  it("keeps focus in the input and points aria-activedescendant at the first option after typing", async () => {
    const user = userEvent.setup();
    render(<Combobox<Item> {...baseProps()} />);
    const input = getInput();
    await user.type(input, "ap");
    expect(document.activeElement).toBe(input);
    const first = screen.getAllByRole("option")[0] as HTMLElement;
    expect(input.getAttribute("aria-activedescendant")).toBe(first.id);
  });

  it("marks only the committed value as aria-selected", async () => {
    const user = userEvent.setup();
    render(<Combobox<Item> {...baseProps({ value: BANANA })} />);
    const input = getInput();
    expect(input.value).toBe("Banana");
    await user.keyboard("{Tab}"); // focus without relying on click-to-open
    expect(document.activeElement).toBe(input);
    await user.keyboard("{ArrowDown}");
    const selected = screen.getAllByRole("option").filter((o) => o.getAttribute("aria-selected") === "true");
    expect(selected.map((o) => o.textContent?.trim())).toEqual(["Banana"]);
  });

  it("passes invalid and describedBy through to the input", () => {
    render(<Combobox<Item> {...baseProps({ invalid: true, describedBy: "fruit-err" })} />);
    const input = getInput();
    expect(input.getAttribute("aria-invalid")).toBe("true");
    expect(input.getAttribute("aria-describedby")).toContain("fruit-err");
  });

  it("shows the visible label only when showLabel is set", () => {
    const { container, rerender } = render(<Combobox<Item> {...baseProps({ showLabel: true })} />);
    expect(getInput()).toBeTruthy();
    const visible = Array.from(container.querySelectorAll("label")).some((l) => l.textContent?.trim() === "Fruit");
    expect(visible).toBe(true);
    rerender(<Combobox<Item> {...baseProps({ showLabel: false })} />);
    expect(getInput()).toBeTruthy(); // still named (visually hidden or aria-label)
  });

  it("shows the placeholder and the committed label in the input", () => {
    const { rerender } = render(<Combobox<Item> {...baseProps({ placeholder: "Pick a fruit" })} />);
    expect(getInput().getAttribute("placeholder")).toBe("Pick a fruit");
    expect(getInput().value).toBe("");
    rerender(<Combobox<Item> {...baseProps({ value: APPLE })} />);
    expect(getInput().value).toBe("Apple");
  });
});

describe("Combobox static filtering", () => {
  it("lists every option, in source order, for an empty query when opened with ArrowDown", async () => {
    const user = userEvent.setup();
    render(<Combobox<Item> {...baseProps()} />);
    getInput().focus();
    await user.keyboard("{ArrowDown}");
    expect(optionNames()).toEqual(["Pineapple", "Apricot", "Grape", "Apple", "Banana", "Cherry"]);
  });

  it("puts prefix matches before substring matches, keeping source order inside each group", async () => {
    const user = userEvent.setup();
    render(<Combobox<Item> {...baseProps()} />);
    await user.type(getInput(), "ap");
    expect(optionNames()).toEqual(["Apricot", "Apple", "Pineapple", "Grape"]);
  });

  it("matches case-insensitively", async () => {
    const user = userEvent.setup();
    render(<Combobox<Item> {...baseProps()} />);
    await user.type(getInput(), "AP");
    expect(optionNames()).toEqual(["Apricot", "Apple", "Pineapple", "Grape"]);
  });

  it("shows the default empty text 'No matches' and no options when nothing matches", async () => {
    const user = userEvent.setup();
    render(<Combobox<Item> {...baseProps()} />);
    await user.type(getInput(), "zzz");
    expect(screen.queryAllByRole("option")).toHaveLength(0);
    expect(screen.getByText("No matches")).toBeTruthy();
  });

  it("shows a custom emptyText", async () => {
    const user = userEvent.setup();
    render(<Combobox<Item> {...baseProps({ emptyText: "Nobody by that name" })} />);
    await user.type(getInput(), "zzz");
    expect(screen.getByText("Nobody by that name")).toBeTruthy();
    expect(screen.queryByText("No matches")).toBeNull();
  });

  it("makes the first option active after every keystroke that changes the list", async () => {
    const user = userEvent.setup();
    render(<Combobox<Item> {...baseProps()} />);
    const input = getInput();
    await user.type(input, "a");
    await user.keyboard("{ArrowDown}{ArrowDown}");
    await user.keyboard("p");
    const first = screen.getAllByRole("option")[0] as HTMLElement;
    expect(first.textContent?.trim()).toBe("Apricot");
    expect(input.getAttribute("aria-activedescendant")).toBe(first.id);
  });

  it("uses renderOption with active and selected state", async () => {
    const user = userEvent.setup();
    render(
      <Combobox<Item>
        {...baseProps({
          value: APPLE,
          renderOption: (o, s) => (
            <span>
              {o.name}
              {s.active ? " [active]" : ""}
              {s.selected ? " [selected]" : ""}
            </span>
          ),
        })}
      />,
    );
    await user.keyboard("{Tab}");
    await user.keyboard("{ArrowDown}");
    const opts = screen.getAllByRole("option");
    expect(opts[0]?.textContent).toBe("Pineapple [active]");
    expect(opts.find((o) => o.textContent?.startsWith("Apple"))?.textContent).toBe("Apple [selected]");
  });
});

describe("Combobox keyboard", () => {
  it("opens with ArrowDown when closed, first option active, then moves down and up", async () => {
    const user = userEvent.setup();
    render(<Combobox<Item> {...baseProps()} />);
    const input = getInput();
    input.focus();
    expect(expanded(input)).toBe("false");
    await user.keyboard("{ArrowDown}");
    expect(expanded(input)).toBe("true");
    const opts = screen.getAllByRole("option");
    expect(activeOption(input)).toBe(opts[0]);
    await user.keyboard("{ArrowDown}");
    expect(activeOption(input)).toBe(opts[1]);
    await user.keyboard("{ArrowDown}");
    expect(activeOption(input)).toBe(opts[2]);
    await user.keyboard("{ArrowUp}");
    expect(activeOption(input)).toBe(opts[1]);
    expect(document.activeElement).toBe(input);
  });

  it("opens with ArrowUp when closed", async () => {
    const user = userEvent.setup();
    render(<Combobox<Item> {...baseProps()} />);
    const input = getInput();
    input.focus();
    await user.keyboard("{ArrowUp}");
    expect(expanded(input)).toBe("true");
    expect(screen.getAllByRole("option").length).toBe(FRUIT.length);
  });

  it("opens with Alt+ArrowDown", async () => {
    const user = userEvent.setup();
    render(<Combobox<Item> {...baseProps()} />);
    const input = getInput();
    input.focus();
    await user.keyboard("{Alt>}{ArrowDown}{/Alt}");
    expect(expanded(input)).toBe("true");
    expect(screen.getAllByRole("option").length).toBe(FRUIT.length);
  });

  it("moves to the first and last option with Home and End", async () => {
    const user = userEvent.setup();
    render(<Combobox<Item> {...baseProps()} />);
    const input = getInput();
    input.focus();
    await user.keyboard("{ArrowDown}{End}");
    const opts = screen.getAllByRole("option");
    expect(activeOption(input)).toBe(opts[opts.length - 1]);
    await user.keyboard("{Home}");
    expect(activeOption(input)).toBe(opts[0]);
  });

  it("scrolls the active option into view with block nearest", async () => {
    const user = userEvent.setup();
    render(<Combobox<Item> {...baseProps()} />);
    getInput().focus();
    await user.keyboard("{ArrowDown}{End}");
    const spy = Element.prototype.scrollIntoView as unknown as ReturnType<typeof vi.fn>;
    const args = spy.mock.calls.map((c) => c[0]);
    expect(args).toContainEqual({ block: "nearest" });
  });

  it("commits the active option with Enter, closes, and shows its label", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(<Harness onSelect={onSelect} />);
    const input = getInput();
    await user.type(input, "ba");
    await user.keyboard("{Enter}");
    expect(onSelect).toHaveBeenCalledTimes(1);
    expect(onSelect).toHaveBeenCalledWith(BANANA);
    expect(input.value).toBe("Banana");
    expect(expanded(input)).toBe("false");
    expect(screen.queryByRole("listbox")).toBeNull();
  });

  it("commits the arrow-selected option with Enter", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(<Harness onSelect={onSelect} />);
    getInput().focus();
    await user.keyboard("{ArrowDown}{ArrowDown}{ArrowDown}{Enter}");
    expect(onSelect).toHaveBeenCalledWith(GRAPE);
  });

  it("does nothing on Enter when closed or when there are no matches", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(<Harness onSelect={onSelect} />);
    const input = getInput();
    input.focus();
    await user.keyboard("{Enter}");
    await user.keyboard("zzz{Enter}");
    expect(document.activeElement).toBe(input);
    expect(onSelect).not.toHaveBeenCalled();
  });

  it("closes on Esc and restores the committed label", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(<Harness init={APPLE} onSelect={onSelect} />);
    const input = getInput();
    expect(input.value).toBe("Apple");
    await user.type(input, "zz");
    expect(input.value).toBe("Applezz");
    expect(expanded(input)).toBe("true");
    await user.keyboard("{Escape}");
    expect(expanded(input)).toBe("false");
    expect(input.value).toBe("Apple");
    expect(onSelect).not.toHaveBeenCalled();
  });

  it("calls onClear on the second Esc when clearable, not on the first", async () => {
    const user = userEvent.setup();
    const onClear = vi.fn();
    render(<Harness init={APPLE} props={{ clearable: true }} onClear={onClear} />);
    const input = getInput();
    input.focus();
    await user.keyboard("{ArrowDown}");
    expect(expanded(input)).toBe("true");
    await user.keyboard("{Escape}");
    expect(expanded(input)).toBe("false");
    expect(onClear).not.toHaveBeenCalled();
    await user.keyboard("{Escape}");
    expect(onClear).toHaveBeenCalledTimes(1);
    expect(input.value).toBe("");
  });

  it("never calls onClear on Esc when not clearable", async () => {
    const user = userEvent.setup();
    const onClear = vi.fn();
    render(<Harness init={APPLE} onClear={onClear} />);
    const input = getInput();
    input.focus();
    await user.keyboard("{ArrowDown}{Escape}{Escape}{Escape}");
    expect(onClear).not.toHaveBeenCalled();
    expect(input.value).toBe("Apple");
  });

  it("Tab closes the list without committing the active option", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(<Harness onSelect={onSelect} />);
    const input = getInput();
    await user.type(input, "ba");
    expect(screen.getAllByRole("option")[0]?.textContent?.trim()).toBe("Banana");
    await user.keyboard("{Tab}");
    expect(onSelect).not.toHaveBeenCalled();
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(input.value).toBe(""); // restored to the committed (empty) label
  });

  it("restores the committed label when the input loses focus", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(<Harness init={APPLE} onSelect={onSelect} />);
    const input = getInput();
    await user.type(input, "zz");
    expect(input.value).toBe("Applezz");
    await user.click(screen.getByRole("button", { name: "outside" }));
    expect(input.value).toBe("Apple");
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(onSelect).not.toHaveBeenCalled();
  });
});

describe("Combobox mouse", () => {
  it("commits an option on click and keeps focus in the input", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(<Harness onSelect={onSelect} />);
    const input = getInput();
    await user.type(input, "ap");
    await user.click(screen.getByRole("option", { name: "Grape" }));
    expect(onSelect).toHaveBeenCalledTimes(1);
    expect(onSelect).toHaveBeenCalledWith(GRAPE);
    expect(input.value).toBe("Grape");
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(document.activeElement).toBe(input);
  });

  it("moves the active option on hover without committing", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(<Harness onSelect={onSelect} />);
    const input = getInput();
    await user.type(input, "ap");
    const opts = screen.getAllByRole("option");
    await user.hover(opts[2] as HTMLElement);
    expect(input.getAttribute("aria-activedescendant")).toBe((opts[2] as HTMLElement).id);
    expect(onSelect).not.toHaveBeenCalled();
    expect(document.activeElement).toBe(input);
  });
});

describe("Combobox minChars, disabled, clearOnSelect", () => {
  it("shows no options (and no empty text) below minChars, then lists at minChars", async () => {
    const user = userEvent.setup();
    render(<Combobox<Item> {...baseProps({ minChars: 2 })} />);
    const input = getInput();
    await user.type(input, "a");
    expect(screen.queryAllByRole("option")).toHaveLength(0);
    expect(screen.queryByText("No matches")).toBeNull();
    await user.type(input, "p");
    expect(optionNames()).toEqual(["Apricot", "Apple", "Pineapple", "Grape"]);
  });

  it("does not open on ArrowDown when below minChars", async () => {
    const user = userEvent.setup();
    render(<Combobox<Item> {...baseProps({ minChars: 2 })} />);
    const input = getInput();
    input.focus();
    await user.keyboard("{ArrowDown}");
    expect(screen.queryAllByRole("option")).toHaveLength(0);
    expect(document.activeElement).toBe(input);
  });

  it("is inert when disabled", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(<Combobox<Item> {...baseProps({ disabled: true, onSelect })} />);
    const input = getInput();
    expect(input.disabled).toBe(true);
    await user.click(input);
    await user.keyboard("{ArrowDown}ap{Enter}");
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(screen.queryAllByRole("option")).toHaveLength(0);
    expect(onSelect).not.toHaveBeenCalled();
  });

  it("clears the input after commit when clearOnSelect is set", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(<Harness props={{ clearOnSelect: true }} onSelect={onSelect} />);
    const input = getInput();
    await user.type(input, "ch");
    await user.keyboard("{Enter}");
    expect(onSelect).toHaveBeenCalledWith(FRUIT[5]);
    expect(input.value).toBe("");
    expect(expanded(input)).toBe("false");
    expect(document.activeElement).toBe(input);
  });
});

describe("Combobox max rows", () => {
  const MANY = Array.from({ length: 12 }, (_, i) => mk(`Item ${String(i + 1).padStart(2, "0")}`));

  it("caps the visible height at 8 rows but keeps every option in the DOM", async () => {
    const user = userEvent.setup();
    render(<Combobox<Item> {...baseProps({ options: MANY })} />);
    getInput().focus();
    await user.keyboard("{ArrowDown}");
    const listbox = screen.getByRole("listbox");
    expect(within(listbox).getAllByRole("option")).toHaveLength(12);
    expect(listbox.getAttribute("data-max-rows")).toBe("8");
    expect(listbox.style.maxHeight).not.toBe("");
  });
});

describe("Combobox async load", () => {
  function deferred<T>() {
    let resolve!: (v: T) => void;
    let reject!: (e: unknown) => void;
    const promise = new Promise<T>((res, rej) => {
      resolve = res;
      reject = rej;
    });
    return { promise, resolve, reject };
  }
  type Req = { query: string; signal: AbortSignal; d: ReturnType<typeof deferred<Item[]>> };
  function makeLoader() {
    const reqs: Req[] = [];
    const load = vi.fn((query: string, signal: AbortSignal) => {
      const d = deferred<Item[]>();
      reqs.push({ query, signal, d });
      return d.promise;
    });
    return { load, reqs };
  }
  function setup() {
    vi.useFakeTimers();
    return userEvent.setup({ advanceTimers: vi.advanceTimersByTime, delay: null });
  }
  const tick = (ms: number) =>
    act(async () => {
      await vi.advanceTimersByTimeAsync(ms);
    });
  const asyncProps = (load: Props["load"]) => baseProps({ options: undefined, load });

  it("debounces load by 150 ms and calls it once with the final query and a signal", async () => {
    const user = setup();
    const { load } = makeLoader();
    render(<Combobox<Item> {...asyncProps(load)} />);
    await user.type(getInput(), "abc");
    await tick(149);
    expect(load).not.toHaveBeenCalled();
    await tick(1);
    expect(load).toHaveBeenCalledTimes(1);
    expect(load.mock.calls[0]?.[0]).toBe("abc");
    expect(load.mock.calls[0]?.[1]).toBeInstanceOf(AbortSignal);
  });

  it("restarts the debounce on each keystroke", async () => {
    const user = setup();
    const { load } = makeLoader();
    render(<Combobox<Item> {...asyncProps(load)} />);
    const input = getInput();
    await user.type(input, "a");
    await tick(100);
    await user.type(input, "b");
    await tick(100);
    expect(load).not.toHaveBeenCalled();
    await tick(50);
    expect(load).toHaveBeenCalledTimes(1);
    expect(load.mock.calls[0]?.[0]).toBe("ab");
  });

  it("loads the initial list with an empty query when opened with ArrowDown", async () => {
    const user = setup();
    const { load } = makeLoader();
    render(<Combobox<Item> {...asyncProps(load)} />);
    getInput().focus();
    await user.keyboard("{ArrowDown}");
    await tick(150);
    expect(load).toHaveBeenCalledTimes(1);
    expect(load.mock.calls[0]?.[0]).toBe("");
  });

  it("renders the loaded results as returned, without local re-filtering", async () => {
    const user = setup();
    const { load, reqs } = makeLoader();
    render(<Combobox<Item> {...asyncProps(load)} />);
    await user.type(getInput(), "q");
    await tick(150);
    await act(async () => {
      reqs[0]?.d.resolve([mk("Zebra"), mk("Yak")]);
    });
    await tick(0);
    expect(optionNames()).toEqual(["Zebra", "Yak"]);
  });

  it("aborts the previous request's signal when a new request starts", async () => {
    const user = setup();
    const { load, reqs } = makeLoader();
    render(<Combobox<Item> {...asyncProps(load)} />);
    const input = getInput();
    await user.type(input, "a");
    await tick(150);
    expect(reqs).toHaveLength(1);
    expect(reqs[0]?.signal.aborted).toBe(false);
    await user.type(input, "b");
    await tick(150);
    expect(reqs).toHaveLength(2);
    expect(reqs[0]?.signal.aborted).toBe(true);
    expect(reqs[1]?.signal.aborted).toBe(false);
  });

  it("drops a stale response that arrives after a newer request started", async () => {
    const user = setup();
    const { load, reqs } = makeLoader();
    render(<Combobox<Item> {...asyncProps(load)} />);
    const input = getInput();
    await user.type(input, "a");
    await tick(150);
    await user.type(input, "b");
    await tick(150);
    expect(reqs).toHaveLength(2);
    await act(async () => {
      reqs[0]?.d.resolve([APPLE]); // stale
    });
    await tick(0);
    expect(optionNames()).not.toContain("Apple");
    await act(async () => {
      reqs[1]?.d.resolve([BANANA]);
    });
    await tick(0);
    expect(optionNames()).toEqual(["Banana"]);
  });

  it("keeps the newer results when a stale response resolves last", async () => {
    const user = setup();
    const { load, reqs } = makeLoader();
    render(<Combobox<Item> {...asyncProps(load)} />);
    const input = getInput();
    await user.type(input, "a");
    await tick(150);
    await user.type(input, "b");
    await tick(150);
    await act(async () => {
      reqs[1]?.d.resolve([BANANA]);
    });
    await tick(0);
    await act(async () => {
      reqs[0]?.d.resolve([APPLE]);
    });
    await tick(0);
    expect(optionNames()).toEqual(["Banana"]);
  });

  it("makes the first loaded option active and commits it with Enter", async () => {
    const user = setup();
    const onSelect = vi.fn();
    const { load, reqs } = makeLoader();
    render(<Combobox<Item> {...asyncProps(load)} onSelect={onSelect} />);
    const input = getInput();
    await user.type(input, "a");
    await tick(150);
    await act(async () => {
      reqs[0]?.d.resolve([APPLE, APRICOT]);
    });
    await tick(0);
    const first = screen.getAllByRole("option")[0] as HTMLElement;
    expect(input.getAttribute("aria-activedescendant")).toBe(first.id);
    await user.keyboard("{Enter}");
    expect(onSelect).toHaveBeenCalledWith(APPLE);
  });

  it("shows a 'Searching…' row only after 200 ms while a request is pending", async () => {
    const user = setup();
    const { load } = makeLoader();
    render(<Combobox<Item> {...asyncProps(load)} />);
    await user.type(getInput(), "a");
    await tick(199);
    expect(screen.queryByText(/Searching…/)).toBeNull();
    await tick(151); // 350 ms after the keystroke, request still pending
    expect(screen.getByText(/Searching…/)).toBeTruthy();
  });

  it("never shows 'Searching…' when the loader resolves as the request starts", async () => {
    const user = setup();
    const load = vi.fn(async () => [APPLE]);
    render(<Combobox<Item> {...asyncProps(load)} />);
    await user.type(getInput(), "a");
    await tick(150);
    expect(load).toHaveBeenCalledTimes(1);
    expect(screen.queryByText(/Searching…/)).toBeNull();
    await tick(500);
    expect(screen.queryByText(/Searching…/)).toBeNull();
    expect(optionNames()).toEqual(["Apple"]);
  });

  it("replaces the 'Searching…' row with the results once the request resolves", async () => {
    const user = setup();
    const { load, reqs } = makeLoader();
    render(<Combobox<Item> {...asyncProps(load)} />);
    await user.type(getInput(), "a");
    await tick(400);
    expect(screen.getByText(/Searching…/)).toBeTruthy();
    await act(async () => {
      reqs[0]?.d.resolve([APPLE]);
    });
    await tick(0);
    expect(screen.queryByText(/Searching…/)).toBeNull();
    expect(optionNames()).toEqual(["Apple"]);
  });

  it("shows an error row with a Retry button on rejection, and retries with the same query", async () => {
    const user = setup();
    const { load, reqs } = makeLoader();
    render(<Combobox<Item> {...asyncProps(load)} />);
    const input = getInput();
    await user.type(input, "ap");
    await tick(150);
    await act(async () => {
      reqs[0]?.d.reject(new Error("boom"));
    });
    await tick(0);
    const retry = screen.getByRole("button", { name: /retry/i });
    expect(screen.queryAllByRole("option")).toHaveLength(0);
    expect(document.body.textContent).not.toContain("boom");
    await user.click(retry);
    await tick(150); // retry may go through the same debounce or fire at once; both are done by now
    expect(load).toHaveBeenCalledTimes(2);
    expect(load.mock.calls[1]?.[0]).toBe("ap");
    expect(document.activeElement).toBe(input);
    await act(async () => {
      reqs[1]?.d.resolve([APPLE]);
    });
    await tick(0);
    expect(optionNames()).toEqual(["Apple"]);
    expect(screen.queryByRole("button", { name: /retry/i })).toBeNull();
  });

  it("does not call load below minChars", async () => {
    const user = setup();
    const { load } = makeLoader();
    render(<Combobox<Item> {...baseProps({ options: undefined, load, minChars: 3 })} />);
    const input = getInput();
    await user.type(input, "ab");
    await tick(500);
    expect(load).not.toHaveBeenCalled();
    await user.type(input, "c");
    await tick(150);
    expect(load).toHaveBeenCalledTimes(1);
    expect(load.mock.calls[0]?.[0]).toBe("abc");
  });
});

describe("Combobox variant=menu", () => {
  const menuProps = (over: Partial<Props> = {}) =>
    baseProps({ variant: "menu", menuLabel: "Pick fruit", value: APPLE, ...over });

  it("renders a single button showing menuLabel, closed, with no listbox", () => {
    render(<Combobox<Item> {...menuProps()} />);
    const btn = screen.getByRole("button");
    expect(btn.textContent).toContain("Pick fruit");
    expect(expanded(btn)).toBe("false");
    expect(screen.queryByRole("listbox")).toBeNull();
  });

  it("opens the same listbox of options when the button is clicked", async () => {
    const user = userEvent.setup();
    render(<Combobox<Item> {...menuProps()} />);
    const btn = screen.getByRole("button");
    await user.click(btn);
    expect(expanded(btn)).toBe("true");
    expect(optionNames()).toEqual(["Pineapple", "Apricot", "Grape", "Apple", "Banana", "Cherry"]);
    const selected = screen.getAllByRole("option").filter((o) => o.getAttribute("aria-selected") === "true");
    expect(selected.map((o) => o.textContent?.trim())).toEqual(["Apple"]);
  });

  it("filters by typing after opening and commits with Enter", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(<Combobox<Item> {...menuProps({ onSelect })} />);
    await user.click(screen.getByRole("button"));
    await user.keyboard("ban");
    expect(optionNames()).toEqual(["Banana"]);
    await user.keyboard("{Enter}");
    expect(onSelect).toHaveBeenCalledTimes(1);
    expect(onSelect).toHaveBeenCalledWith(BANANA);
    expect(screen.queryByRole("listbox")).toBeNull();
  });

  it("commits a clicked option and closes", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(<Combobox<Item> {...menuProps({ onSelect })} />);
    await user.click(screen.getByRole("button"));
    await user.click(screen.getByRole("option", { name: "Cherry" }));
    expect(onSelect).toHaveBeenCalledWith(FRUIT[5]);
    expect(screen.queryByRole("listbox")).toBeNull();
  });

  it("closes on Esc without selecting", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(<Combobox<Item> {...menuProps({ onSelect })} />);
    await user.click(screen.getByRole("button"));
    expect(screen.getByRole("listbox")).toBeTruthy();
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(onSelect).not.toHaveBeenCalled();
  });

  it("is inert when disabled", async () => {
    const user = userEvent.setup();
    render(<Combobox<Item> {...menuProps({ disabled: true })} />);
    const btn = screen.getByRole("button") as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    await user.click(btn);
    expect(screen.queryByRole("listbox")).toBeNull();
  });
});

describe("Combobox accessibility (axe)", () => {
  async function violations(container: Element) {
    const results = await axe.run(container);
    return results.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.html).join(" | ")}`);
  }

  it("has no axe violations when closed (field)", async () => {
    const { container } = render(<Combobox<Item> {...baseProps({ value: APPLE })} />);
    expect(getInput()).toBeTruthy();
    expect(await violations(container)).toEqual([]);
  });

  it("has no axe violations when open (field)", async () => {
    const user = userEvent.setup();
    const { container } = render(<Combobox<Item> {...baseProps({ value: APPLE })} />);
    getInput().focus();
    await user.keyboard("{ArrowDown}");
    expect(screen.getByRole("listbox")).toBeTruthy();
    expect(await violations(container)).toEqual([]);
  });

  it("has no axe violations when open with no matches", async () => {
    const user = userEvent.setup();
    const { container } = render(<Combobox<Item> {...baseProps()} />);
    await user.type(getInput(), "zzz");
    expect(screen.getByText("No matches")).toBeTruthy();
    expect(await violations(container)).toEqual([]);
  });

  it("has no axe violations for the menu variant, closed and open", async () => {
    const user = userEvent.setup();
    const { container } = render(<Combobox<Item> {...baseProps({ variant: "menu", menuLabel: "Pick fruit", value: APPLE })} />);
    const btn = screen.getByRole("button");
    expect(await violations(container)).toEqual([]);
    await user.click(btn);
    expect(screen.getByRole("listbox")).toBeTruthy();
    expect(await violations(container)).toEqual([]);
  });
});
