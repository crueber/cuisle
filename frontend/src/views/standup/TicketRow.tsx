// Implementation: WP-24. One ticket as a whole-row button (ux-spec §5.4). Enter and click both come from the native button.
import { contentScale } from "../../state/app";
import { Icon } from "../../components/Icon";
import { PulseTrack } from "./PulseTrack";
import type { TicketRowProps } from "./props";

/** The issue-type icon, shared by the person rows and the whole-board rows (sized in CSS). */
export function TypeIcon({ type, done }: { type: string; done?: boolean }) {
  const kind = type === "story" || type === "bug" || type === "task" || type === "spike" ? type : "other";
  return <Icon name={kind} class={`ticket-icon type-${kind}${done ? " is-done-icon" : ""}`} />;
}

export function TicketRow({ ticket, density, trackW, keyCh, days, options, onOpen }: TicketRowProps) {
  const k = contentScale.value; // Large/TV multiplies the design px emitted inline
  const done = ticket.lane === "done";
  const alert = ticket.overLimit || ticket.blocked;
  const tier = density.row >= 46 ? "roomy" : density.row >= 38 ? "compact" : "tight";
  const cls = `ticket-row tier-${tier}${done ? " is-done" : ""}`;
  return (
    <button type="button" class={cls} style={`height:${density.row * k}px`} onClick={() => onOpen(ticket.key)}>
      <TypeIcon type={ticket.type} />
      <span class="ticket-key" style={`width:calc(${keyCh}ch + ${6 * k}px)`}>
        {ticket.key}
      </span>
      {ticket.blocked ? <span class="ticket-blocked">BLOCKED</span> : null}
      <span class="ticket-title" title={ticket.summary}>
        {ticket.summary}
      </span>
      <PulseTrack pulse={ticket.pulse} days={days} width={trackW} rowH={density.row} done={done} options={options} scale={k} />
      <span class="ticket-days" data-alert={alert ? "true" : "false"}>
        {`${Math.max(1, ticket.daysInLane)}d`}
      </span>
    </button>
  );
}
