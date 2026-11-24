// Configure > Status mapping section (ux-spec §9.4 Status mapping).
import { Combobox } from "../../../components/Combobox";
import { Icon } from "../../../components/Icon";
import type { MapLane } from "../../../api/types";
import { moveColumn, setColumnLane } from "../../../state/config";
import type { SectionProps } from "../props";

interface LaneOption {
  key: MapLane;
  label: string;
}

const LANE_OPTIONS: LaneOption[] = [
  { key: "backlog", label: "Backlog" },
  { key: "todo", label: "To Do" },
  { key: "prog", label: "In Progress" },
  { key: "review", label: "In Review" },
  { key: "qa", label: "QA" },
  { key: "done", label: "Done" },
  { key: "hidden", label: "Hidden" },
];

const byKey = (lane: string): LaneOption => LANE_OPTIONS.find((o) => o.key === lane) ?? LANE_OPTIONS[0]!;

export function StatusMappingSection({ config }: SectionProps) {
  // Lanes fed by more than one column: their tickets are grouped in the order set here (ux-spec §9.4).
  const shared = LANE_OPTIONS.filter((o) => o.key !== "hidden" && o.key !== "backlog")
    .map((o) => ({ ...o, cols: config.columns.filter((c) => c.lane === o.key).sort((a, b) => a.order - b.order) }))
    .filter((g) => g.cols.length > 1);
  return (
    <section class="cfg-section" aria-labelledby="cfg-h-mapping">
      <header class="cfg-section-head">
        <h2 id="cfg-h-mapping" class="cfg-section-title">Status mapping</h2>
        <span class="cfg-section-sub">board columns to lanes</span>
      </header>

      {config.board === null ? (
        <p class="cfg-hint">Choose a board in Source first. Its columns appear here so you can map them to lanes.</p>
      ) : (
        <>
          {config.unmappedStatuses.length > 0 ? (
            <p class="cfg-warning" role="note">
              {config.unmappedStatuses.length} {config.unmappedStatuses.length === 1 ? "status isn't" : "statuses aren't"} mapped:{" "}
              {config.unmappedStatuses.join(", ")}
            </p>
          ) : null}
          {config.columns.map((col) => (
            <div class="cfg-field cfg-map-field" key={col.columnName}>
              <span class="cfg-field-label">
                <span class="cfg-map-name">
                  <span class="cfg-swatch" data-lane={col.lane} aria-hidden="true" />
                  {col.columnName}
                </span>
                {col.statusNames.join(", ") !== col.columnName ? (
                  <span class="cfg-hint">{col.statusNames.join(", ")}</span>
                ) : null}
              </span>
              <div class="cfg-field-control cfg-map-control">
                <Combobox<LaneOption>
                  id={`cfg-map-${col.columnName.replace(/\s+/g, "-").toLowerCase()}`}
                  label={`Lane for ${col.columnName}`}
                  value={byKey(col.lane)}
                  options={LANE_OPTIONS}
                  getKey={(o) => o.key}
                  getLabel={(o) => o.label}
                  onSelect={(o) => setColumnLane(col.columnName, o.key)}
                />
              </div>
            </div>
          ))}
          {shared.length > 0 ? (
            <div class="cfg-map-order">
              <h3 class="cfg-field-label">Order within sections</h3>
              {shared.map((g) => (
                <ol class="personview-walk" key={g.key} aria-label={`${g.label} order`}>
                  {g.cols.map((col, i) => (
                    <li class="personview-row" key={col.columnName}>
                      <span class="personview-num">{i + 1}</span>{" "}
                      <span class="cfg-chevrons">
                        <button
                          type="button"
                          class="cfg-chevron"
                          aria-label={`Move ${col.columnName} up`}
                          disabled={i === 0}
                          onClick={() => moveColumn(col.columnName, i - 1)}
                        >
                          <Icon name="chevron-up" size={14} />
                        </button>
                        <button
                          type="button"
                          class="cfg-chevron"
                          aria-label={`Move ${col.columnName} down`}
                          disabled={i === g.cols.length - 1}
                          onClick={() => moveColumn(col.columnName, i + 1)}
                        >
                          <Icon name="chevron-down" size={14} />
                        </button>
                      </span>
                      <span class="personview-swatch" data-lane={g.key} aria-hidden="true" />
                      <span class="personview-name">{col.columnName}</span>
                      <span class="personview-jira">{g.label}</span>
                    </li>
                  ))}
                </ol>
              ))}
            </div>
          ) : null}
        </>
      )}
    </section>
  );
}
