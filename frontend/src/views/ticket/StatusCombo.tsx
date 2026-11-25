// StatusCombo: the rail's Status control (ux-spec §14.6 "Status combobox, open state"; props frozen in props.ts).
// It is the shared Combobox (AGENTS invariant 4). The Combobox has no controlled-open prop and no open callback, so:
//   - the open state is observed from the input's aria-expanded (a MutationObserver on our wrapper) and reported through
//     onOpenChange; each open starts a "session" whose transition list is loaded once (typing filters it locally);
//   - the `open` prop opens it by focusing + clicking the input (the Combobox's own mouse path) and closes it with an
//     Escape keydown on the input (its own keyboard path);
//   - these effects are LAYOUT effects on purpose: they must run in the same microtask as the render, so a quick user
//     action cannot slip in between a state change and the effect that echoes it (a late echo would reopen or close it);
//   - after a pick that must not change the field (needs-screen, offline) the Combobox is remounted with a `key` so it
//     shows the current status again.
import { useLayoutEffect, useMemo, useRef, useState } from "preact/hooks";
import { hueOf } from "../../state/lanes";
import type { TransitionList, TransitionOption } from "../../api/types";
import { Combobox } from "../../components/Combobox";
import { filterOptions } from "../../components/Combobox.filter";
import type { StatusComboProps } from "./props";
import "./StatusCombo.css";

const CURRENT = "__current__";

interface Session {
  ctrl: AbortController;
  promise: Promise<TransitionList> | null;
  list: TransitionList | null;
}

const label = (o: TransitionOption): string => o.toStatusName || o.name;

/** Normal transitions first, then the ones that need a Jira screen; each group keeps the order it came in. */
export function orderTransitions(items: readonly TransitionOption[]): TransitionOption[] {
  return [...items.filter((o) => !o.needsScreen), ...items.filter((o) => o.needsScreen)];
}

export function StatusCombo(props: StatusComboProps) {
  const { id, statusName, lane, open, disabled, offline, saving, load, onSelect, onOpenInJira } = props;
  const wrapRef = useRef<HTMLDivElement>(null);
  const session = useRef<Session | null>(null);
  const reported = useRef(false);
  const prevOpen = useRef<boolean | undefined>(undefined);
  const refocus = useRef(false);
  const latest = useRef(props);
  latest.current = props;
  const [remount, setRemount] = useState(0);
  const [listOffline, setListOffline] = useState(false);

  const value = useMemo<TransitionOption>(
    () => ({ id: CURRENT, name: statusName, toStatusName: statusName, toLane: lane, needsScreen: false, url: "" }),
    [statusName, lane],
  );

  const input = (): HTMLInputElement | null => wrapRef.current?.querySelector<HTMLInputElement>("input[role=combobox]") ?? null;
  const expanded = (): boolean => input()?.getAttribute("aria-expanded") === "true";

  // Observe open / close from the DOM: each open is a new session (the list loads once per open).
  useLayoutEffect(() => {
    const wrap = wrapRef.current;
    if (!wrap) return;
    const sync = () => {
      const now = expanded();
      if (now === reported.current) return;
      reported.current = now;
      if (now) {
        session.current?.ctrl.abort();
        session.current = { ctrl: new AbortController(), promise: null, list: null };
      } else {
        session.current?.ctrl.abort();
        session.current = null;
      }
      setListOffline(false);
      latest.current.onOpenChange?.(now);
    };
    const mo = new MutationObserver(sync);
    mo.observe(wrap, { subtree: true, childList: true, attributes: true, attributeFilter: ["aria-expanded"] });
    return () => {
      mo.disconnect();
      session.current?.ctrl.abort();
    };
  }, []);

  // Controlled open (the S key): only a CHANGE of the prop acts.
  useLayoutEffect(() => {
    if (open === undefined) return;
    const was = prevOpen.current;
    prevOpen.current = open;
    if (open === was) return;
    const el = input();
    if (!el || el.disabled) return;
    if (open && !expanded()) {
      el.focus();
      el.click();
    } else if (!open && was !== undefined && expanded()) {
      el.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: false, cancelable: true }));
    }
  }, [open]);

  // After a remount the new input takes the focus the old one had.
  useLayoutEffect(() => {
    if (refocus.current) {
      refocus.current = false;
      input()?.focus();
    }
  }, [remount]);

  const loadOptions = (query: string): Promise<TransitionOption[]> => {
    let s = session.current;
    if (!s) s = session.current = { ctrl: new AbortController(), promise: null, list: null };
    const mine = s;
    mine.promise ??= load(mine.ctrl.signal).then(
      (res) => {
        mine.list = res;
        if (session.current === mine) setListOffline(res.offline);
        return res;
      },
      (err: unknown) => {
        mine.promise = null; // "Retry" in the Combobox loads again
        throw err;
      },
    );
    return mine.promise.then((res) => filterOptions(orderTransitions(res.items), label, query));
  };

  const pick = (o: TransitionOption) => {
    if (o.id === CURRENT) return;
    if (offline || listOffline) {
      onOpenInJira(o.url);
    } else if (o.needsScreen) {
      onOpenInJira(o.url);
    } else {
      onSelect(o);
      return;
    }
    // The field must keep showing the CURRENT status (not the unchosen target): remount it.
    refocus.current = true;
    setRemount((n) => n + 1);
  };

  const firstScreen = (o: TransitionOption): boolean => {
    const items = session.current?.list ? orderTransitions(session.current.list.items) : [];
    return o.needsScreen && items.find((x) => x.needsScreen)?.id === o.id;
  };
  const firstRow = (o: TransitionOption): boolean => {
    const items = session.current?.list ? orderTransitions(session.current.list.items) : [];
    return items[0]?.id === o.id;
  };

  return (
    <div class="sc" ref={wrapRef} data-lane-now={lane}>
      <span class="sc-now" data-lane={lane} data-hue={hueOf(lane)} aria-hidden="true" />
      <Combobox<TransitionOption>
        key={remount}
        id={id}
        label="Status"
        value={value}
        getKey={(o) => o.id}
        getLabel={label}
        load={(q) => loadOptions(q)}
        onSelect={pick}
        disabled={disabled || saving}
        renderOption={(o) => (
          <span class="sc-row" data-divider={firstScreen(o) ? "true" : undefined} data-muted={o.needsScreen ? "true" : undefined}>
            {(offline || listOffline) && firstRow(o) ? <span class="sc-offline">Offline · open in Jira</span> : null}
            <span class="sc-opt">
              <span class="sc-sw" data-lane={o.toLane} data-hue={hueOf(o.toLane)} aria-hidden="true" />
              <span class="sc-name">{`→ ${o.name}`}</span>
              {o.needsScreen ? (
                <span class="sc-screen" data-needs-screen="true">
                  screen · ↗ Jira
                </span>
              ) : null}
            </span>
          </span>
        )}
      />
    </div>
  );
}
