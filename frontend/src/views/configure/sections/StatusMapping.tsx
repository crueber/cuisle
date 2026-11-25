// Configure > Status mapping section (ux-spec §9.4 Status mapping). The lanes come from Go (config.laneOptions):
// backlog, the built-in working lanes, the lanes the user named, done, hidden. Typing a new name in a column's lane box
// offers "Create lane “…”"; the Lanes list renames any working lane or Done and deletes the user's own lanes.
import { useState } from "preact/hooks";
import type { LaneInfo } from "../../../api/types";
import { Combobox } from "../../../components/Combobox";
import { Icon } from "../../../components/Icon";
import { TextField } from "../../../components/TextField";
import { createLane, deleteLane, laneError, moveColumn, renameLane, setColumnLane } from "../../../state/config";
import { hueOf } from "../../../state/lanes";
import { fieldError } from "../FieldError";
import type { SectionProps } from "../props";

const laneKey = (o: LaneInfo) => o.lane;
const laneLabel = (o: LaneInfo) => o.label;
const slug = (s: string) => s.replace(/\s+/g, "-").toLowerCase();

function Swatch({ lane, cls = "cfg-swatch" }: { lane: string; cls?: string }) {
  return <span class={cls} data-lane={lane} data-hue={hueOf(lane)} aria-hidden="true" />;
}

export function StatusMappingSection({ config }: SectionProps) {
  const options = config.laneOptions;
  const byLane = (lane: string) => options.find((o) => o.lane === lane) ?? null;
  // Lanes fed by more than one column: their tickets are grouped in the order set here (ux-spec §9.4).
  const shared = options
    .filter((o) => o.kind === "working" || o.kind === "done")
    .map((o) => ({ ...o, cols: config.columns.filter((c) => c.lane === o.lane).sort((a, b) => a.order - b.order) }))
    .filter((g) => g.cols.length > 1);
  const named = options.filter((o) => o.kind === "working" || o.kind === "done");
  const laneErr = laneError(config.id);
  const [deleting, setDeleting] = useState<string | null>(null);
  const [moveTo, setMoveTo] = useState<LaneInfo | null>(null);
  const startDelete = (l: LaneInfo) => {
    if (config.columns.some((c) => c.lane === l.lane)) {
      setDeleting(l.lane);
      setMoveTo(null);
    } else deleteLane(l.lane, "");
  };

  return (
    <section class="cfg-section" aria-labelledby="cfg-h-mapping">
      <header class="cfg-section-head">
        <h2 id="cfg-h-mapping" class="cfg-section-title">Status mapping</h2>
        <span class="cfg-section-sub">board columns to lanes, including lanes you name yourself</span>
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
          <p class="cfg-hint">To add a lane, type its name in a column's lane box and choose Create lane.</p>
          {laneErr ? (
            <p class="tf-error" role="alert">
              {laneErr}
            </p>
          ) : null}
          {config.columns.map((col) => (
            <div class="cfg-field cfg-map-field" key={col.columnName}>
              <span class="cfg-field-label">
                <span class="cfg-map-name">
                  <Swatch lane={col.lane} />
                  {col.columnName}
                </span>
                {col.statusNames.join(", ") !== col.columnName ? (
                  <span class="cfg-hint">{col.statusNames.join(", ")}</span>
                ) : null}
              </span>
              <div class="cfg-field-control cfg-map-control">
                <Combobox<LaneInfo>
                  id={`cfg-map-${slug(col.columnName)}`}
                  label={`Lane for ${col.columnName}`}
                  value={byLane(col.lane)}
                  options={options}
                  getKey={laneKey}
                  getLabel={laneLabel}
                  onSelect={(o) => setColumnLane(col.columnName, o.lane)}
                  onCreate={(name) => createLane(name, col.columnName)}
                  createLabel={(name) => `Create lane “${name}”`}
                />
              </div>
            </div>
          ))}
          <div class="cfg-map-order">
            <h3 class="cfg-field-label">Lanes</h3>
            {named.map((l) => (
              <div key={l.lane}>
                <div class="cfg-field cfg-map-field" data-lane-row={l.lane}>
                  <span class="cfg-field-label">
                    <span class="cfg-map-name">
                      <Swatch lane={l.lane} />
                      {l.label}
                    </span>
                    <span class="cfg-hint">{l.columns.length > 0 ? l.columns.join(", ") : "no columns yet"}</span>
                  </span>
                  <div class="cfg-field-control cfg-map-control">
                    <TextField
                      id={`cfg-lane-${l.lane}`}
                      label={`Name of the ${l.custom ? l.label : l.defaultLabel} lane`}
                      value={l.label}
                      placeholder={l.defaultLabel}
                      maxLength={40}
                      error={fieldError(config.id, `label:${l.lane}`)}
                      onInput={(v) => renameLane(l.lane, v)}
                      onCommit={(v) => v.trim() === "" && renameLane(l.lane, "", true)}
                    />
                    {l.custom ? (
                      <button type="button" class="cfg-btn" aria-label={`Delete ${l.label}`} onClick={() => startDelete(l)}>
                        Delete
                      </button>
                    ) : null}
                  </div>
                </div>
                {deleting === l.lane ? (
                  <div class="cfg-field cfg-map-field" role="group" aria-label={`Delete ${l.label}: choose where its columns go`}>
                    <span class="cfg-field-label">Move its columns to</span>
                    <div class="cfg-field-control cfg-map-control">
                      <Combobox<LaneInfo>
                        id={`cfg-lane-move-${l.lane}`}
                        label={`Move the columns of ${l.label} to`}
                        value={moveTo}
                        options={options.filter((o) => o.lane !== l.lane)}
                        getKey={laneKey}
                        getLabel={laneLabel}
                        onSelect={setMoveTo}
                        placeholder="Choose a lane"
                      />
                      <button
                        type="button"
                        class="cfg-btn"
                        disabled={moveTo === null}
                        onClick={() => {
                          if (moveTo) deleteLane(l.lane, moveTo.lane);
                          setDeleting(null);
                        }}
                      >
                        Delete lane
                      </button>
                      <button type="button" class="cfg-btn" onClick={() => setDeleting(null)}>
                        Cancel
                      </button>
                    </div>
                  </div>
                ) : null}
              </div>
            ))}
          </div>
          {shared.length > 0 ? (
            <div class="cfg-map-order">
              <h3 class="cfg-field-label">Order within sections</h3>
              {shared.map((g) => (
                <ol class="personview-walk" key={g.lane} aria-label={`${g.label} order`}>
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
                      <Swatch lane={g.lane} cls="personview-swatch" />
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
