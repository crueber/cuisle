// Configure > Source section (ux-spec §9.4 Source, §9.5; architecture §5.3).
import { api } from "../../../api";
import type { BoardOption, SpaceOption, StandupConfig } from "../../../api/types";
import { Combobox } from "../../../components/Combobox";
import { Segmented } from "../../../components/Segmented";
import { TextField } from "../../../components/TextField";
import { standups } from "../../../state/app";
import { copySettingsFrom, saveField, setBoard, setSpace, validateFilter } from "../../../state/config";
import { flush } from "../../../state/save";
import { fieldError } from "../FieldError";
import type { SectionProps } from "../props";

type Scope = StandupConfig["scope"];

/** "Start from" options: the static Defaults entry plus one copy entry per other configured standup. */
type StartFrom = { key: string; label: string; copyFrom: number | null };

export function SourceSection({ config }: SectionProps) {
  const isKanban = config.board?.type === "kanban";
  const scope: Scope = isKanban ? ("board" as Scope) : config.scope;
  const errors = config.filterCheck && !config.filterCheck.ok ? config.filterCheck.errors : [];

  const startOptions: StartFrom[] = [
    { key: "defaults", label: "Defaults", copyFrom: null },
    ...standups.value
      .filter((t) => t.id !== config.id && t.configured)
      .map((t) => ({ key: `copy:${t.id}`, label: `Copy ${t.name}`, copyFrom: t.id })),
  ];

  return (
    <section class="cfg-section" aria-labelledby="cfg-h-source">
      <header class="cfg-section-head">
        <h2 id="cfg-h-source" class="cfg-section-title">Source</h2>
        <span class="cfg-section-sub">where this standup's tickets come from</span>
      </header>

      <div class="cfg-field">
        <span class="cfg-field-label">Name</span>
        <div class="cfg-field-control">
          <TextField
            id="cfg-source-name"
            label="Name"
            value={config.name}
            error={fieldError(config.id, "name")}
            onInput={(v) => saveField("name", v, { text: true })}
            onCommit={() => void flush()}
          />
        </div>
      </div>

      {config.draft ? (
        <div class="cfg-field">
          <span class="cfg-field-label">Start from</span>
          <div class="cfg-field-control">
            <Combobox<StartFrom>
              id="cfg-source-start"
              label="Start from"
              value={null}
              options={startOptions}
              getKey={(o) => o.key}
              getLabel={(o) => o.label}
              placeholder="Defaults"
              onSelect={(o) => {
                if (o.copyFrom !== null) void copySettingsFrom(o.copyFrom);
              }}
            />
          </div>
        </div>
      ) : null}

      <div class="cfg-field">
        <span class="cfg-field-label">Space</span>
        <div class="cfg-field-control">
          <Combobox<SpaceOption>
            id="cfg-source-space"
            label="Space"
            value={config.space}
            getKey={(o) => o.id}
            getLabel={(o) => `${o.name} (${o.key})`}
            load={(q) => api().connection.searchSpaces(q)}
            onSelect={(o) => setSpace(o)}
            placeholder="Search spaces"
          />
        </div>
      </div>

      <div class="cfg-field">
        <span class="cfg-field-label">Board</span>
        <div class="cfg-field-control">
          <Combobox<BoardOption>
            key={config.space?.key ?? ""}
            id="cfg-source-board"
            label="Board"
            value={config.board}
            getKey={(o) => o.id}
            getLabel={(o) => o.name}
            load={(q) => api().connection.searchBoards(config.space?.key ?? "", q)}
            onSelect={(o) => setBoard(o)}
            disabled={!config.space}
            placeholder={config.space ? "Search boards" : "Pick a space first"}
          />
        </div>
      </div>

      <div class="cfg-field">
        <span class="cfg-field-label">Scope</span>
        <div class="cfg-field-control">
          <Segmented<Scope>
            id="cfg-source-scope"
            label="Scope"
            value={scope}
            options={[
              { value: "sprint" as Scope, label: "Active sprint", disabled: isKanban },
              { value: "board" as Scope, label: "Whole board" },
            ]}
            onChange={(v) => saveField("scope", v)}
          />
          {isKanban ? <p class="cfg-hint">Kanban boards have no sprints, so this covers the whole board.</p> : null}
        </div>
      </div>

      <div class="cfg-field">
        <span class="cfg-field-label">Extra filter</span>
        <div class="cfg-field-control">
          <TextField
            id="cfg-source-filter"
            label="Extra filter"
            value={config.extraFilter}
            mono
            placeholder="labels = standup"
            hint="JQL added to the board query"
            error={errors.length > 0 ? errors.join(" ") : fieldError(config.id, "extraFilter")}
            onInput={(v) => saveField("extraFilter", v, { text: true })}
            onCommit={() => {
              void flush();
              validateFilter();
            }}
          />
        </div>
      </div>
    </section>
  );
}
