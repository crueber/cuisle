// Shared Combobox: every dropdown in the app (architecture §4.6, ux-spec §9.3, §10; AGENTS invariant 4).
// WAI-ARIA 1.2 combobox with list autocomplete. Focus stays in the input. Popup is position:fixed (no portal).
import { useEffect, useLayoutEffect, useRef, useState } from "preact/hooks";
import type { ComboboxProps } from "./props";
import { filterOptions, popupPosition } from "./Combobox.filter";
import "./Combobox.css";

const DEBOUNCE_MS = 150;
const SEARCHING_MS = 200;
const MAX_ROWS = 8;

type LoadStatus = "idle" | "loading" | "ok" | "error";
type Pos = { top: number; left: number; width: number };

export function Combobox<T>(props: ComboboxProps<T>) {
  const {
    id,
    label,
    showLabel,
    value,
    getKey,
    getLabel,
    renderOption,
    options,
    load,
    onSelect,
    placeholder,
    minChars = 0,
    clearable,
    onClear,
    disabled,
    emptyText = "No matches",
    variant = "field",
    menuLabel,
    clearOnSelect,
    invalid,
    describedBy,
  } = props;

  const isMenu = variant === "menu";
  const isAsync = load !== undefined;
  const committed = value === null ? "" : getLabel(value);
  const listId = `${id}-listbox`;
  const optId = (i: number) => `${id}-opt-${i}`;

  const [text, setText] = useState(isMenu ? "" : committed);
  const [typed, setTyped] = useState(false);
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(-1);
  const [remote, setRemote] = useState<T[]>([]);
  const [status, setStatus] = useState<LoadStatus>("idle");
  const [searching, setSearching] = useState(false);
  const [pos, setPos] = useState<Pos>({ top: 0, left: 0, width: 0 });

  const inputRef = useRef<HTMLInputElement>(null);
  const fieldRef = useRef<HTMLDivElement>(null);
  const btnRef = useRef<HTMLButtonElement>(null);
  const popupRef = useRef<HTMLDivElement>(null);
  const listRef = useRef<HTMLUListElement>(null);
  const debounceRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const searchRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const abortRef = useRef<AbortController | null>(null);
  const seqRef = useRef(0);

  // The query only filters once the user has typed; opening shows the full (or initial) list.
  const q = typed ? text : "";
  const belowMin = q.length < minChars;
  const isOpen = open && !belowMin;
  const items: T[] = isAsync ? remote : filterOptions(options ?? [], getLabel, q);
  const act = active >= 0 && active < items.length ? active : -1;
  // The listbox element exists whenever the list is open (aria-controls must resolve), even with no options.
  const listShown = isOpen;
  const showPopup = isMenu ? open : isOpen;
  const showEmpty = items.length === 0 && (!isAsync || status === "ok");

  // Keep the committed label in sync while the user is not typing (field variant).
  useEffect(() => {
    if (!isMenu && !clearOnSelect && !typed) setText(committed);
  }, [committed]);

  // Scroll the active option into view, keyboard-driven only.
  useEffect(() => {
    if (!isOpen || act < 0) return;
    const el = listRef.current?.children[act] as HTMLElement | undefined;
    el?.scrollIntoView?.({ block: "nearest" });
  }, [act, isOpen]);

  // Menu variant: move focus into the search input when the list opens.
  useEffect(() => {
    if (isMenu && open) inputRef.current?.focus();
  }, [isMenu, open]);

  // Position the popup under its anchor (flips above when there is no room below).
  useLayoutEffect(() => {
    if (!showPopup) return;
    const anchor = isMenu ? btnRef.current : fieldRef.current;
    const pop = popupRef.current;
    if (!anchor || !pop) return;
    const r = anchor.getBoundingClientRect();
    const next = popupPosition(
      { top: r.top, bottom: r.bottom, left: r.left, width: r.width },
      pop.offsetHeight,
      window.innerHeight,
    );
    setPos((prev) =>
      prev.top === next.top && prev.left === next.left && prev.width === next.width ? prev : next,
    );
  });

  // Abort in-flight loads and clear timers on unmount.
  useEffect(() => () => stopLoads(), []);

  function stopLoads() {
    clearTimeout(debounceRef.current);
    clearTimeout(searchRef.current);
    abortRef.current?.abort();
    abortRef.current = null;
    seqRef.current += 1;
  }

  function runLoad(query: string) {
    if (!load) return;
    stopLoads();
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    const seq = seqRef.current;
    setStatus("loading");
    searchRef.current = setTimeout(() => {
      if (seqRef.current === seq) setSearching(true);
    }, SEARCHING_MS);
    load(query, ctrl.signal).then(
      (res) => {
        if (ctrl.signal.aborted || seqRef.current !== seq) return;
        clearTimeout(searchRef.current);
        setSearching(false);
        setRemote(res);
        setStatus("ok");
        setActive(res.length > 0 ? 0 : -1);
      },
      () => {
        // Raw error detail is deliberately not shown (AGENTS §7).
        if (ctrl.signal.aborted || seqRef.current !== seq) return;
        clearTimeout(searchRef.current);
        setSearching(false);
        setStatus("error");
      },
    );
  }

  function scheduleLoad(query: string) {
    if (!isAsync) return;
    stopLoads();
    setSearching(false);
    if (query.length < minChars) {
      setStatus("idle");
      setRemote([]);
      return;
    }
    debounceRef.current = setTimeout(() => runLoad(query), DEBOUNCE_MS);
  }

  function openList(last: boolean) {
    if (disabled) return;
    if (!open) {
      setOpen(true);
      scheduleLoad(q);
    }
    setActive(items.length === 0 ? -1 : last ? items.length - 1 : 0);
  }

  function closeList() {
    stopLoads();
    setSearching(false);
    setOpen(false);
    setTyped(false);
    setActive(-1);
    setText(isMenu || clearOnSelect ? "" : committed);
  }

  function commit(o: T) {
    stopLoads();
    setSearching(false);
    onSelect(o);
    setOpen(false);
    setTyped(false);
    setActive(-1);
    setText(isMenu || clearOnSelect ? "" : getLabel(o));
    if (isMenu) btnRef.current?.focus();
  }

  function clearValue() {
    onClear?.();
    setText("");
    setTyped(false);
  }

  function onTyped(v: string) {
    setText(v);
    setTyped(true);
    setOpen(true);
    setActive(0);
    scheduleLoad(v);
  }

  function retry() {
    runLoad(q);
    inputRef.current?.focus();
  }

  function onKeyDown(e: KeyboardEvent) {
    const it = act >= 0 ? items[act] : undefined;
    switch (e.key) {
      case "ArrowDown":
        e.preventDefault();
        if (!isOpen) openList(false);
        else setActive(Math.min(act + 1, items.length - 1));
        return;
      case "ArrowUp":
        e.preventDefault();
        if (!isOpen) openList(true);
        else setActive(Math.max(act - 1, 0));
        return;
      case "Home":
        if (isOpen) {
          e.preventDefault();
          setActive(items.length === 0 ? -1 : 0);
        }
        return;
      case "End":
        if (isOpen) {
          e.preventDefault();
          setActive(items.length === 0 ? -1 : items.length - 1);
        }
        return;
      case "Enter":
        if (isOpen && it !== undefined) {
          e.preventDefault();
          commit(it);
        }
        return;
      case "Escape":
        if (open) {
          e.preventDefault();
          closeList();
          if (isMenu) btnRef.current?.focus();
        } else if (clearable) {
          e.preventDefault();
          clearValue();
        }
        return;
      case "Tab":
        if (open) closeList();
        return;
      default:
        return;
    }
  }

  function onButtonKeyDown(e: KeyboardEvent) {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      if (!open) openList(e.key === "ArrowUp");
    } else if (e.key === "Escape" && !open && clearable) {
      e.preventDefault();
      clearValue();
    }
  }

  const renderInput = (panel: boolean) => (
    <input
      ref={inputRef}
      id={panel ? `${id}-search` : id}
      type="text"
      role="combobox"
      class="cbx-input"
      autoComplete="off"
      spellcheck={false}
      placeholder={placeholder}
      value={text}
      disabled={disabled}
      aria-label={panel ? label : undefined}
      aria-autocomplete="list"
      aria-expanded={isOpen || (isMenu && open) ? "true" : "false"}
      aria-controls={listShown ? listId : undefined}
      aria-activedescendant={listShown && act >= 0 ? optId(act) : undefined}
      aria-invalid={invalid ? "true" : undefined}
      aria-describedby={describedBy}
      onInput={(e) => onTyped(e.currentTarget.value)}
      onKeyDown={onKeyDown}
      onClick={() => {
        // Mouse users expect a dropdown to open on click (the keyboard contract is unchanged).
        if (!open) openList(false);
      }}
      onBlur={() => {
        if (open) closeList();
      }}
    />
  );

  const popup = showPopup ? (
    <div
      ref={popupRef}
      class="cbx-popup"
      style={{ top: pos.top, left: pos.left, width: pos.width }}
      onMouseDown={(e) => e.preventDefault()} // keep focus in the input on clicks inside the popup
    >
      {isMenu ? renderInput(true) : null}
      {listShown ? (
        <ul
          ref={listRef}
          id={listId}
          role="listbox"
          aria-label={label}
          class="cbx-list"
          data-max-rows={String(MAX_ROWS)}
          style={{ maxHeight: `calc(${MAX_ROWS} * var(--size-control-height))` }}
        >
          {items.map((o, i) => {
            const selected = value !== null && getKey(o) === getKey(value);
            const isAct = i === act;
            return (
              <li
                key={getKey(o)}
                id={optId(i)}
                role="option"
                aria-selected={selected ? "true" : "false"}
                data-active={isAct ? "true" : "false"}
                class="cbx-option"
                onClick={() => commit(o)}
                onMouseEnter={() => setActive(i)}
              >
                {renderOption ? renderOption(o, { active: isAct, selected }) : getLabel(o)}
              </li>
            );
          })}
        </ul>
      ) : null}
      {isOpen && status === "error" ? (
        <div class="cbx-row cbx-error">
          <span>Couldn't load results.</span>
          <button type="button" class="cbx-retry" onClick={retry}>
            Retry
          </button>
        </div>
      ) : null}
      {isOpen && status !== "error" && searching ? (
        <div class="cbx-row cbx-muted" role="status">
          Searching…
        </div>
      ) : null}
      {isOpen && status !== "error" && !searching && showEmpty ? (
        <div class="cbx-row cbx-muted">{emptyText}</div>
      ) : null}
    </div>
  ) : null;

  if (isMenu) {
    return (
      <div class="cbx" data-variant="menu">
        <button
          ref={btnRef}
          type="button"
          class="cbx-menu-btn"
          id={id}
          aria-haspopup="listbox"
          aria-expanded={open ? "true" : "false"}
          aria-controls={listShown ? listId : undefined}
          disabled={disabled}
          onMouseDown={(e) => e.preventDefault()} // focus moves to the search input once opened
          onClick={() => {
            if (open) closeList();
            else openList(false);
          }}
          onKeyDown={onButtonKeyDown}
        >
          {menuLabel}
        </button>
        {popup}
      </div>
    );
  }

  return (
    <div class="cbx" data-variant="field">
      <label for={id} class={showLabel ? "cbx-label" : "cbx-sr"}>
        {label}
      </label>
      <div ref={fieldRef} class="cbx-field">
        {renderInput(false)}
      </div>
      {popup}
    </div>
  );
}
