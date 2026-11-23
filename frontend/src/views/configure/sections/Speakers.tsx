// Implementation: WP-27. Props are frozen in props.ts.
// Configure > Speakers (ux-spec §9.4 Speakers, §9.3 ordered lists). Mutations go through state/config.ts.
import { useEffect, useState } from "preact/hooks";
import { api } from "../../../api";
import type { SpeakerConfig, SpeakerSort, UnassignedPosition, UserOption } from "../../../api/types";
import { Checkbox } from "../../../components/Checkbox";
import type { SegmentedOption } from "../../../components/props";
import { Combobox } from "../../../components/Combobox";
import { Icon } from "../../../components/Icon";
import { Segmented } from "../../../components/Segmented";
import { Switch } from "../../../components/Switch";
import { addSpeaker, moveSpeaker, removeSpeaker, saveField, setSpeakerIncluded, sortSpeakers } from "../../../state/config";
import type { SectionProps } from "../props";
import "./Speakers.css";

const SHUFFLED_NOTE_MS = 4000;

const UNASSIGNED_OPTIONS: SegmentedOption<UnassignedPosition>[] = [
  { value: "first", label: "First" },
  { value: "last", label: "Last" },
  { value: "skip", label: "Skip" },
];

const SORT_BUTTONS: { mode: SpeakerSort; label: string }[] = [
  { mode: "shuffle", label: "Shuffle" },
  { mode: "az", label: "A–Z" },
  { mode: "active", label: "Most active" },
];

export function SpeakersSection({ config }: SectionProps) {
  const [shuffled, setShuffled] = useState(false);

  // UI-only transient note; it clears itself after a few seconds. No network work is scheduled here.
  useEffect(() => {
    if (!shuffled) return;
    const t = setTimeout(() => setShuffled(false), SHUFFLED_NOTE_MS);
    return () => clearTimeout(t);
  }, [shuffled]);

  const heading = (sub: string) => (
    <header class="cfg-section-head">
      <h2 id="cfg-h-speakers" class="cfg-section-title">Speakers</h2>
      {sub ? <span class="cfg-section-sub">{sub}</span> : null}
    </header>
  );

  if (config.board === null) {
    return (
      <section class="cfg-section speakers" aria-labelledby="cfg-h-speakers">
        {heading("listed once a board is picked")}
        <p class="speakers-empty">Pick a board in Source first. Its speakers appear here once it is chosen.</p>
      </section>
    );
  }

  const speakers = config.speakers;
  let rank = 0;
  const numbers = speakers.map((s) => (s.included ? ++rank : null));

  const move = (s: SpeakerConfig, index: number, delta: number) => {
    const to = index + delta;
    if (to < 0 || to >= speakers.length) return;
    moveSpeaker(s.accountId, to);
  };

  const sort = (mode: SpeakerSort) => {
    setShuffled(mode === "shuffle");
    sortSpeakers(mode);
  };

  const searchUsers = (query: string): Promise<UserOption[]> => api().connection.searchUsers(config.id, query);

  return (
    <section class="cfg-section speakers" aria-labelledby="cfg-h-speakers">
      {heading(`${rank} of ${speakers.length} in this standup`)}

      <div class="speakers-toolbar">
        {SORT_BUTTONS.map((b) => (
          <button key={b.mode} type="button" class="cfg-btn speakers-sort" onClick={() => sort(b.mode)}>
            {b.label}
          </button>
        ))}
        <Switch
          id="speakers-reshuffle-daily"
          label="Reshuffle daily"
          checked={config.reshuffleDaily}
          onChange={(v) => saveField("reshuffleDaily", v)}
        />
        {shuffled && <span class="speakers-note" role="status">Shuffled just now</span>}
      </div>

      <ol class="speakers-list">
        {speakers.map((s, i) => (
          <li
            key={s.accountId}
            class="speakers-row"
            data-included={s.included ? "true" : "false"}
            onKeyDown={(e) => {
              if (!e.altKey) return;
              if (e.key === "ArrowUp") {
                e.preventDefault();
                move(s, i, -1);
              } else if (e.key === "ArrowDown") {
                e.preventDefault();
                move(s, i, 1);
              }
            }}
          >
            <span class="speakers-num">{numbers[i] ?? "—"}</span>{" "}
            <span class="cfg-chevrons">
              <button
                type="button"
                class="cfg-chevron"
                aria-label={`Move ${s.name} up`}
                disabled={i === 0}
                onClick={() => move(s, i, -1)}
              >
                <Icon name="chevron-up" size={14} />
              </button>
              <button
                type="button"
                class="cfg-chevron"
                aria-label={`Move ${s.name} down`}
                disabled={i === speakers.length - 1}
                onClick={() => move(s, i, 1)}
              >
                <Icon name="chevron-down" size={14} />
              </button>
            </span>
            <Checkbox
              id={`speakers-include-${s.accountId}`}
              label={`Include ${s.name}`}
              checked={s.included}
              onChange={(v) => setSpeakerIncluded(s.accountId, v)}
            />
            <span class="speakers-person">
              <span class="speakers-name">{s.name}</span>
              {s.note !== "" && <span class="speakers-note-text">{s.note}</span>}
            </span>
            <button type="button" class="speakers-remove" aria-label={`Remove ${s.name}`} onClick={() => removeSpeaker(s.accountId)}>
              Remove
            </button>
          </li>
        ))}
      </ol>

      <div class="speakers-unassigned">
        <span class="speakers-unassigned-label">Unassigned as a speaker</span>
        <Segmented<UnassignedPosition>
          id="speakers-unassigned"
          label="Unassigned as a speaker"
          value={config.unassignedPosition === "first" || config.unassignedPosition === "skip" ? config.unassignedPosition : "last"}
          options={UNASSIGNED_OPTIONS}
          onChange={(v) => saveField("unassignedPosition", v)}
        />
      </div>

      <div class="speakers-add">
        <Combobox<UserOption>
          id="speakers-add"
          label="Add someone from this space…"
          placeholder="Add someone from this space…"
          value={null}
          getKey={(u) => u.accountId}
          getLabel={(u) => u.displayName}
          load={searchUsers}
          onSelect={(u) => addSpeaker(u)}
          clearOnSelect
        />
      </div>

      <p class="speakers-footnote">Listed: anyone with tickets on this board in the last 30 days.</p>
    </section>
  );
}
