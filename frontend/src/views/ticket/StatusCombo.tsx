// StatusCombo: the rail's Status control (ux-spec §14.6 "Status combobox, open state"; props frozen in props.ts).
// Built on the shared Combobox (AGENTS invariant 4) using its controlled open / onOpenChange props: each open starts a
// session whose transition list is loaded and ordered once (typing filters it locally), and picking a needs-screen or
// offline option returns `false` from onSelect so the field keeps showing the current status.
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "preact/hooks";
import { hueOf } from "../../state/lanes";
import type { TransitionOption } from "../../api/types";
import { Combobox } from "../../components/Combobox";
import { filterOptions } from "../../components/Combobox.filter";
import type { StatusComboProps } from "./props";
import "./StatusCombo.css";

const CURRENT = "__current__";

interface Session {
  ctrl: AbortController;
  promise: Promise<TransitionOption[]> | null;
  firstRowId: string;
  firstScreenId: string;
}

const label = (o: TransitionOption): string => o.toStatusName || o.name;

/** Normal transitions first, then the ones that need a Jira screen; each group keeps the order it came in. */
export function orderTransitions(items: readonly TransitionOption[]): TransitionOption[] {
  return [...items.filter((o) => !o.needsScreen), ...items.filter((o) => o.needsScreen)];
}

export function StatusCombo(props: StatusComboProps) {
  const { id, statusName, lane, open, onOpenChange, disabled, offline, saving, load, onSelect, onOpenInJira } = props;
  const session = useRef<Session | null>(null);
  const [listOffline, setListOffline] = useState(false);

  const value = useMemo<TransitionOption>(
    () => ({ id: CURRENT, name: statusName, toStatusName: statusName, toLane: lane, needsScreen: false, url: "" }),
    [statusName, lane],
  );

  const startSession = () => {
    session.current?.ctrl.abort();
    session.current = { ctrl: new AbortController(), promise: null, firstRowId: "", firstScreenId: "" };
    setListOffline(false);
  };
  const endSession = () => {
    session.current?.ctrl.abort();
    session.current = null;
    setListOffline(false);
  };

  // Sync the session when controlled `open` changes externally (e.g. the S key or parent closing it).
  useLayoutEffect(() => {
    if (open === undefined) return;
    if (open) {
      if (!session.current) startSession();
    } else if (session.current) {
      endSession();
    }
  }, [open]);

  useEffect(() => () => session.current?.ctrl.abort(), []);

  const handleOpenChange = (next: boolean) => {
    if (next) startSession();
    else endSession();
    onOpenChange?.(next);
  };

  const loadOptions = (query: string): Promise<TransitionOption[]> => {
    let s = session.current;
    if (!s) s = session.current = { ctrl: new AbortController(), promise: null, firstRowId: "", firstScreenId: "" };
    const mine = s;
    mine.promise ??= load(mine.ctrl.signal).then(
      (res) => {
        const ordered = orderTransitions(res.items);
        mine.firstRowId = ordered[0]?.id ?? "";
        mine.firstScreenId = ordered.find((x) => x.needsScreen)?.id ?? "";
        if (session.current === mine) setListOffline(res.offline);
        return ordered;
      },
      (err: unknown) => {
        mine.promise = null; // "Retry" in the Combobox loads again
        throw err;
      },
    );
    return mine.promise.then((ordered) => filterOptions(ordered, label, query));
  };

  const pick = (o: TransitionOption): boolean | void => {
    if (o.id === CURRENT) return false;
    if (offline || listOffline || o.needsScreen) {
      onOpenInJira(o.url);
      return false; // keep showing the current status in the field
    }
    onSelect(o);
  };

  return (
    <div class="sc" data-lane-now={lane}>
      <span class="sc-now" data-lane={lane} data-hue={hueOf(lane)} aria-hidden="true" />
      <Combobox<TransitionOption>
        id={id}
        label="Status"
        value={value}
        open={open}
        onOpenChange={handleOpenChange}
        getKey={(o) => o.id}
        getLabel={label}
        load={(q) => loadOptions(q)}
        onSelect={pick}
        disabled={disabled || saving}
        renderOption={(o) => (
          <span
            class="sc-row"
            data-divider={o.needsScreen && o.id === session.current?.firstScreenId ? "true" : undefined}
            data-muted={o.needsScreen ? "true" : undefined}
          >
            {(offline || listOffline) && o.id === session.current?.firstRowId ? <span class="sc-offline">Offline · open in Jira</span> : null}
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
