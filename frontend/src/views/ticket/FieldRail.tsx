// FieldRail: the ticket modal's field rail (ux-spec §14.6) or, in "strip" layout, only its State group (§14.2 narrow).
// Go decides which rows exist (empty fields are folded into `notSet`); this component only draws them. Status and
// Assignee are the shared Combobox, Flag is the shared Switch. Writes are optimistic upstream; here a write shows
// "saving…" after 250 ms and, on failure, its reason with Retry.
import type { ComponentChildren } from "preact";
import { useLayoutEffect, useRef, useState } from "preact/hooks";
import type { PersonRef, RailGroup, RailRow, UserOption } from "../../api/types";
import { AdfDoc } from "../../components/adf/AdfDoc";
import type { AdfContext } from "../../components/adf/props";
import { Combobox } from "../../components/Combobox";
import { Icon } from "../../components/Icon";
import { Switch } from "../../components/Switch";
import type { EditField, FieldRailProps, WriteState } from "./props";
import { StatusCombo } from "./StatusCombo";
import { useAfter } from "./TicketModal.util";
import "./FieldRail.css";

const UNASSIGNED: UserOption = { accountId: "", displayName: "Unassigned", initials: "", onBoard: false };
/** Kinds whose values are short enough to pair two-up when the labels stack (Priority | Points, Sprint | Due, …). */
const PAIRABLE = new Set(["option", "number", "date", "datetime", "sprint", "versions", "time"]);

function WriteNote({ state, onRetry }: { state: WriteState; onRetry: () => void }) {
  const late = useAfter(state.phase === "saving", 250);
  if (state.phase === "failed") {
    return (
      <div class="fr-fail" role="alert">
        <span class="fr-fail-text">{state.reason}</span>
        <button type="button" class="fr-retry" onClick={onRetry}>
          Retry
        </button>
      </div>
    );
  }
  return late ? <span class="fr-saving">saving…</span> : null;
}

function Initials({ text }: { text: string }) {
  return (
    <span class="fr-initials" aria-hidden="true">
      {text}
    </span>
  );
}

function People({ users }: { users: PersonRef[] }) {
  return (
    <span class="fr-people">
      {users.map((u) => (
        <span class="fr-person" key={u.accountId}>
          <Initials text={u.initials} />
          <span>{u.name}</span>
        </span>
      ))}
    </span>
  );
}

function Chips({ items }: { items: string[] }) {
  return (
    <span class="fr-chips">
      {items.map((t) => (
        <span class="chip" key={t}>
          {t}
        </span>
      ))}
    </span>
  );
}

/** The plain value of a non-editable row, by kind (ux-spec §14.6 table). */
function Value({ row, ctx, onOpenUrl }: { row: RailRow; ctx: AdfContext; onOpenUrl: (url: string) => void }) {
  const items = row.items ?? [];
  const users = row.users ?? [];
  switch (row.kind) {
    case "labels":
    case "components":
    case "versions":
    case "options":
      return <Chips items={items} />;
    case "cascade":
      return <span>{items.join(" › ")}</span>;
    case "user":
    case "users":
      return <People users={users} />;
    case "group":
    case "groups":
      return (
        <span class="fr-names">
          {(items.length > 0 ? items : [row.text]).map((n) => (
            <span class="fr-name" key={n}>
              {n}
            </span>
          ))}
        </span>
      );
    case "team":
    case "project":
      return (
        <span class="fr-name">
          <Icon name="board" size={16} class="fr-name-icon" />
          <span>{row.text}</span>
        </span>
      );
    case "url":
      return (
        <a
          class="fr-link"
          href={row.url}
          title={row.url}
          onClick={(e) => {
            e.preventDefault(); // the webview never navigates
            onOpenUrl(row.url);
          }}
        >
          {row.urlLabel || row.url}
        </a>
      );
    case "time": {
      const pct = Math.round((row.progress ?? 0) * 100);
      return (
        <span class="fr-time">
          <span>{row.text}</span>
          <span class="fr-bar" role="progressbar" aria-label={row.label} aria-valuemin={0} aria-valuemax={100} aria-valuenow={pct}>
            <span class="fr-bar-fill" style={{ width: `${pct}%` }} />
          </span>
        </span>
      );
    }
    case "paragraph":
      return row.rich ? <AdfDoc doc={row.rich} ctx={ctx} size="field" clampLines={6} /> : <span>{row.text}</span>;
    case "parent":
      return (
        <span class="fr-parent">
          <Icon name="spike" size={16} class="fr-parent-icon" />
          <span class="fr-key">{row.text}</span>
          {row.sub ? <span class="fr-parent-name">{row.sub}</span> : null}
        </span>
      );
    case "sprint":
      return (
        <span>
          <span>{row.text}</span>
          {row.sub ? <span class="fr-sub">{row.sub}</span> : null}
        </span>
      );
    case "commits":
      return (
        <span>
          <span>{row.text}</span>
          {row.sub ? <span class="fr-sub">{`last ${row.sub.replace(/^last\s+/, "")}`}</span> : null}
        </span>
      );
    case "pr":
      return (
        <span class="fr-pr">
          {items[0] ? (
            <span class="fr-pr-state" data-pr={items[0]}>
              {items[0]}
            </span>
          ) : null}
          <span>{row.text}</span>
          {row.sub ? <span class="fr-sub">{row.sub}</span> : null}
        </span>
      );
    case "watchers":
      return <span>{row.number !== null ? String(row.number) : row.text}</span>;
    case "unknown":
      return <span>{row.raw || row.text}</span>;
    default:
      return <span>{row.text || (row.number !== null ? String(row.number) : row.raw)}</span>;
  }
}

function Row({ row, children, pair }: { row: RailRow; children: ComponentChildren; pair?: boolean }) {
  return (
    <div class="fr-row" data-field={row.fieldId} data-kind={row.kind} data-pair={pair ? "true" : undefined}>
      <span class="fr-label">{row.label}</span>
      <div class="fr-value" data-mono={row.mono ? "true" : undefined}>
        {children}
      </div>
    </div>
  );
}

/** `rest` (narrow windows): every group EXCEPT State, drawn inline at the head of the content column. */
export function FieldRail(props: FieldRailProps & { rest?: boolean }) {
  const { rest, detail, geometry, layout, writes, offline, openToken, ctx, onTransition, onAssign, onFlag, onRetry, onOpenUrl } = props;
  const [statusOpen, setStatusOpen] = useState(false);
  const assigneeRef = useRef<HTMLDivElement>(null);
  const lastToken = useRef(openToken?.token);

  // The S / A keys: only a CHANGE of the token opens a combobox (a re-render with the same token does not).
  useLayoutEffect(() => {
    if (!openToken || openToken.token === lastToken.current) return;
    lastToken.current = openToken.token;
    if (openToken.field === "status") {
      setStatusOpen(true);
    } else {
      const el = assigneeRef.current?.querySelector<HTMLInputElement>("input[role=combobox]");
      if (el && !el.disabled) {
        el.focus();
        el.click();
      }
    }
  }, [openToken?.token, openToken?.field]);

  const groups: RailGroup[] = rest
    ? detail.rail.filter((g) => g.id !== "state")
    : layout === "strip"
      ? detail.rail.filter((g) => g.id === "state")
      : detail.rail;
  const saving = (f: EditField): boolean => writes[f].phase === "saving";

  const stateRow = (row: RailRow) => {
    if (row.edit === "status") {
      return (
        <Row row={row} key={row.fieldId}>
          <StatusCombo
            id="ticket-status"
            statusName={row.text || detail.row.statusName}
            lane={detail.row.lane}
            open={statusOpen}
            onOpenChange={setStatusOpen}
            disabled={!detail.can.transition || offline}
            offline={offline}
            saving={saving("status")}
            load={props.loadTransitions}
            onSelect={onTransition}
            onOpenInJira={onOpenUrl}
          />
          <WriteNote state={writes.status} onRetry={() => onRetry("status")} />
        </Row>
      );
    }
    if (row.edit === "assignee") {
      const current = row.users?.[0];
      const value: UserOption = current
        ? { accountId: current.accountId, displayName: current.name, initials: current.initials, onBoard: true }
        : UNASSIGNED;
      return (
        <Row row={row} key={row.fieldId}>
          <div class="fr-assignee" ref={assigneeRef} data-unassigned={current ? undefined : "true"}>
            {current ? (
              <span class="fr-assignee-ini">
                <Initials text={current.initials} />
              </span>
            ) : null}
            <Combobox<UserOption>
              id="ticket-assignee"
              label="Assignee"
              value={value}
              getKey={(u) => u.accountId}
              getLabel={(u) => u.displayName}
              load={async (q, signal) => {
                const users = await props.loadAssignees(q, signal);
                const wantsNone = q === "" || "unassigned".includes(q.toLowerCase());
                return [...(wantsNone ? [UNASSIGNED] : []), ...users.filter((u) => u.accountId !== "")];
              }}
              onSelect={(u) => onAssign(u.accountId)}
              disabled={!detail.can.assign || offline || saving("assignee")}
              renderOption={(u) => (
                <span class="fr-opt" data-unassigned={u.accountId === "" ? "true" : undefined}>
                  {u.accountId === "" ? null : <Initials text={u.initials} />}
                  <span>{u.displayName}</span>
                </span>
              )}
            />
          </div>
          <WriteNote state={writes.assignee} onRetry={() => onRetry("assignee")} />
        </Row>
      );
    }
    // Flag
    const blocked = detail.row.blocked;
    return (
      <div class="fr-row fr-flag" data-field={row.fieldId} data-kind={row.kind} key={row.fieldId}>
        <Switch
          id="ticket-flag"
          label="Flag"
          checked={blocked}
          disabled={!detail.can.flag || offline || saving("flag")}
          onChange={onFlag}
        />
        <span class="fr-flag-state">
          {blocked ? (
            <>
              <span class="fr-blocked">BLOCKED</span>
              {row.sub ? <span class="fr-sub">{row.sub}</span> : null}
            </>
          ) : (
            <span class="fr-muted">Not flagged</span>
          )}
        </span>
        <WriteNote state={writes.flag} onRetry={() => onRetry("flag")} />
      </div>
    );
  };

  const renderGroup = (g: RailGroup) => (
    <section class="fr-group" data-group={g.id} key={g.id}>
      {layout === "strip" ? null : (
        <div class="fr-head">
          <h3>{g.label}</h3>
          {g.id === "state" ? <span class="fr-hint">edits go to Jira</span> : null}
        </div>
      )}
      {g.rows.map((row) =>
        row.edit !== "" ? (
          stateRow(row)
        ) : (
          <Row row={row} pair={PAIRABLE.has(row.kind)} key={row.fieldId}>
            <Value row={row} ctx={ctx} onOpenUrl={onOpenUrl} />
          </Row>
        ),
      )}
      {g.notSet.length > 0 ? <p class="fr-notset">{`Not set: ${g.notSet.join(" · ")}`}</p> : null}
    </section>
  );

  return (
    <aside
      class="fr"
      aria-label="Ticket fields"
      data-layout={rest ? "inline" : layout}
      data-stacked={geometry.stackedLabels ? "true" : "false"}
    >
      {groups.map(renderGroup)}
    </aside>
  );
}
