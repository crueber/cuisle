// Implementation: WP-27. Props are frozen in props.ts.
// Configure > Person view (ux-spec §9.4 Person view, §9.3 ordered lists). Mutations go through state/config.ts.
import type { RowsMode, WalkLane } from "../../../api/types";
import { hueOf } from "../../../state/lanes";
import { Icon } from "../../../components/Icon";
import type { SegmentedOption } from "../../../components/props";
import { Segmented } from "../../../components/Segmented";
import { saveField, setWalkOrder } from "../../../state/config";
import type { SectionProps } from "../props";
import "./PersonView.css";

const ROWS_OPTIONS: SegmentedOption<RowsMode>[] = [
  { value: "fit", label: "Fit when possible" },
  { value: "roomy", label: "Always roomy" },
];

function sameOrder(a: WalkLane[], b: WalkLane[]): boolean {
  return a.length === b.length && a.every((l, i) => l === b[i]);
}

function subtitleFor(order: WalkLane[], ltr: WalkLane[], rtl: WalkLane[], first: string, last: string): string {
  if (sameOrder(order, rtl)) return `Right to left (${last} first)`;
  if (sameOrder(order, ltr)) return `Left to right (${first} first)`;
  return "Custom order";
}

export function PersonViewSection({ config }: SectionProps) {
  // Every working lane, custom lanes included, in walk order. The presets follow the board's column order (from Go).
  const walk = config.lanes.filter((l) => l.lane !== "done");
  const order: WalkLane[] = walk.map((l) => l.lane);
  const ltr = config.walkLeftToRight.length === order.length ? config.walkLeftToRight : order;
  const rtl = [...ltr].reverse();
  const labelOf = (lane: string | undefined) => walk.find((l) => l.lane === lane)?.label ?? "";
  const done = config.laneOptions.find((o) => o.lane === "done");

  const move = (index: number, delta: number) => {
    const to = index + delta;
    if (to < 0 || to >= order.length) return;
    const next = [...order];
    const a = next[index];
    const b = next[to];
    if (a === undefined || b === undefined) return;
    next[index] = b;
    next[to] = a;
    setWalkOrder(next);
  };

  return (
    <section class="cfg-section personview" aria-labelledby="cfg-h-person">
      <header class="cfg-section-head">
        <h2 id="cfg-h-person" class="cfg-section-title">Person view</h2>
        <span class="cfg-section-sub">{subtitleFor(order, ltr, rtl, labelOf(ltr[0]), labelOf(rtl[0]))}</span>
      </header>

      <div class="cfg-field personview-walk-head">
        <span class="cfg-field-label">Walk order</span>
        <div class="cfg-field-control personview-presets">
          <button type="button" class="cfg-btn" onClick={() => setWalkOrder(rtl)}>
            Right to left
          </button>
          <button type="button" class="cfg-btn" onClick={() => setWalkOrder(ltr)}>
            Left to right
          </button>
        </div>
      </div>

      <ol class="personview-walk">
        {walk.map((l, i) => (
          <li
            key={l.lane}
            class="personview-row"
            onKeyDown={(e) => {
              if (!e.altKey) return;
              if (e.key === "ArrowUp") {
                e.preventDefault();
                move(i, -1);
              } else if (e.key === "ArrowDown") {
                e.preventDefault();
                move(i, 1);
              }
            }}
          >
            <span class="personview-num">{i + 1}</span>{" "}
            <span class="cfg-chevrons">
              <button
                type="button"
                class="cfg-chevron"
                aria-label={`Move ${l.label} up`}
                disabled={i === 0}
                onClick={() => move(i, -1)}
              >
                <Icon name="chevron-up" size={14} />
              </button>
              <button
                type="button"
                class="cfg-chevron"
                aria-label={`Move ${l.label} down`}
                disabled={i === walk.length - 1}
                onClick={() => move(i, 1)}
              >
                <Icon name="chevron-down" size={14} />
              </button>
            </span>
            <span class="personview-swatch" data-lane={l.lane} data-hue={hueOf(l.lane)} aria-hidden="true" />
            <span class="personview-name">{l.label}</span>
            {l.columns.length > 0 ? <span class="personview-jira">{`Jira: ${l.columns.join(", ")}`}</span> : null}
          </li>
        ))}
      </ol>

      <div class="personview-done">
        <span class="personview-swatch" data-lane="done" aria-hidden="true" />
        <span class="personview-name">{done?.label ?? "Done"}</span>
        <p class="personview-locked">Always last, so it's first below the fold.</p>
      </div>

      <div class="cfg-field">
        <span class="cfg-field-label">
          Rows
          <span class="cfg-hint personview-hint">Tighten before scrolling</span>
        </span>
        <div class="cfg-field-control">
        <Segmented<RowsMode>
          id="personview-rows"
          label="Rows"
          value={config.rowsMode === "roomy" ? "roomy" : "fit"}
          options={ROWS_OPTIONS}
          onChange={(v) => saveField("rowsMode", v)}
        />
        </div>
      </div>
    </section>
  );
}
