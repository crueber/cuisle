// Implementation: WP-27. Props are frozen in props.ts.
// Configure > Whole board (ux-spec §9.4 Whole board). Mutations go through state/config.ts.
import type { DoneWindow } from "../../../api/types";
import type { SegmentedOption } from "../../../components/props";
import { Segmented } from "../../../components/Segmented";
import { Stepper } from "../../../components/Stepper";
import { saveField } from "../../../state/config";
import { FieldError } from "../FieldError";
import type { SectionProps } from "../props";
import "./WholeBoard.css";

export function WholeBoardSection({ config }: SectionProps) {
  const kanban = config.board?.type === "kanban";
  const doneOptions: SegmentedOption<DoneWindow>[] = [
    { value: "sprint", label: "Sprint", disabled: kanban },
    { value: "14d", label: "14 days" },
    { value: "yesterday", label: "Since yesterday" },
  ];

  return (
    <section class="cfg-section wholeboard" aria-labelledby="cfg-h-board">
      <header class="cfg-section-head">
        <h2 id="cfg-h-board" class="cfg-section-title">Whole board</h2>
      </header>

      <div class="cfg-field">
        <span class="cfg-field-label">Done window</span>
        <div class="cfg-field-control">
          <Segmented<DoneWindow>
            id="wholeboard-done-window"
            label="Done window"
            value={config.doneWindow === "14d" || config.doneWindow === "yesterday" ? config.doneWindow : "sprint"}
            options={doneOptions}
            onChange={(v) => saveField("doneWindow", v)}
          />
        </div>
      </div>

      <div class="cfg-field">
        <span class="cfg-field-label">Backlog rows</span>
        <div class="cfg-field-control">
          <Stepper
            id="wholeboard-backlog-rows"
            label="Backlog rows"
            value={config.backlogRows}
            min={0}
            max={50}
            onChange={(n) => saveField("backlogRows", n)}
          />
          <FieldError standupId={config.id} field="backlogRows" />
          <p class="wholeboard-hint">top of the ranked backlog · 0 turns it off</p>
        </div>
      </div>
    </section>
  );
}
