// Configure > Export section (per standup; ux-spec §9.4 Export; product-spec F10; architecture §3.2 SinkService).
// Every change autosaves through state/save (text fields debounced 400 ms). A secret is sent once with the save that
// follows the edit and is never read back: the field always starts empty.
import { useEffect, useRef, useState } from "preact/hooks";
import { api, toAppError } from "../../../api";
import type { OutboxItem, SinkConfig, SinkField, SinkInput, SinkKind } from "../../../api/types";
import { Combobox } from "../../../components/Combobox";
import { Segmented } from "../../../components/Segmented";
import { Switch } from "../../../components/Switch";
import { TextField } from "../../../components/TextField";
import { on } from "../../../api/events";
import { enqueue, fieldErrors, flush, TEXT_DEBOUNCE_MS } from "../../../state/save";
import { showToast } from "../../../state/ui";
import type { SectionProps } from "../props";
import { ageText } from "../../standup/headerText";
import "./Export.css";

type SendOn = "manual" | "after_sync";
const SEND_ON: { value: SendOn; label: string }[] = [
  { value: "manual", label: "Manual" },
  { value: "after_sync", label: "After each sync" },
];

/** One destination row. `key` is a client key (it names the save queue); `id` is the server id (0 until saved). */
interface Row {
  key: number;
  id: number;
  standupId: number; // this standup, or 0 = all standups
  kind: string;
  name: string;
  config: Record<string, string>;
  sendOn: string;
  enabled: boolean;
  hasSecret: boolean;
  lastDelivery: OutboxItem | null;
  secretDraft: string; // what the secret field shows; typed here, never loaded
  secretPending: string | null; // typed secret waiting for the next save (null = unchanged)
}

let nextKey = 1;
// Server ids by client key. It outlives the section, so a save queued before a rail switch still finds the id an
// earlier save of the same new row got (no duplicate sink), and a queued delete knows what to delete.
const sinkIds = new Map<number, number>();

function cleanConfig(c: Record<string, string | undefined> | null): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(c ?? {})) if (typeof v === "string") out[k] = v;
  return out;
}

function toRow(s: SinkConfig): Row {
  return { ...s, key: nextKey++, config: cleanConfig(s.config), secretDraft: "", secretPending: null };
}

function newRow(kind: SinkKind, standupId: number): Row {
  const config: Record<string, string> = {};
  for (const f of kind.schema ?? []) if (f.default !== "") config[f.key] = f.default;
  return toRow({ id: 0, standupId, kind: kind.kind, name: kind.displayName, config, sendOn: "manual", enabled: true, hasSecret: false, lastDelivery: null });
}

function rowInput(r: Row, secret: string | null): SinkInput {
  return {
    id: r.id,
    kind: r.kind,
    name: r.name,
    standupId: r.standupId,
    config: { ...r.config },
    secret,
    sendOn: r.sendOn,
    enabled: r.enabled,
  };
}

export function ExportSection(props: SectionProps) {
  const standupId = props.config.id;
  const [kinds, setKinds] = useState<SinkKind[]>([]);
  const [rows, setRows] = useState<Row[]>([]);
  const [loadError, setLoadError] = useState("");
  const latest = useRef<Row[]>([]);

  const commit = (next: Row[]) => {
    latest.current = next;
    setRows(next);
  };
  const patch = (key: number, fn: (r: Row) => Row) => commit(latest.current.map((r) => (r.key === key ? fn(r) : r)));

  // Loads kinds and sinks once on mount; afterwards only sink events re-read the list (no timers).
  useEffect(() => {
    let alive = true;
    const inScope = (s: SinkConfig) => s.standupId === standupId || s.standupId === 0;
    const merge = (list: SinkConfig[]) => {
      const byId = new Map(list.map((s) => [s.id, s] as const));
      const known = new Set(latest.current.map((r) => r.id));
      const next = latest.current.map((r) => {
        const s = r.id > 0 ? byId.get(r.id) : undefined;
        return s ? { ...r, lastDelivery: s.lastDelivery, hasSecret: s.hasSecret } : r;
      });
      for (const s of list) if (inScope(s) && !known.has(s.id)) next.push(toRow(s));
      commit(next);
    };
    const reload = () => {
      api()
        .sink.list()
        .then((list) => {
          if (alive) merge(list);
        })
        .catch(() => undefined);
    };
    commit([]);
    setLoadError("");
    Promise.all([api().sink.kinds(), api().sink.list()])
      .then(([k, list]) => {
        if (!alive) return;
        setKinds(k);
        merge(list);
      })
      .catch((e: unknown) => {
        if (alive) setLoadError(toAppError(e).message);
      });
    const offs = [on("sink:delivered", reload), on("sink:failed", reload)];
    return () => {
      alive = false;
      offs.forEach((off) => off());
    };
  }, [standupId]);

  const kindOf = (name: string): SinkKind | undefined => kinds.find((k) => k.kind === name);

  /** Queues a save of the row as it is now (captured here, so a rail switch cannot lose the edit). */
  const persist = (key: number, debounced: boolean) => {
    const snap = latest.current.find((x) => x.key === key);
    if (!snap) return;
    enqueue({
      key: `sink:${key}`,
      ...(debounced ? { debounceMs: TEXT_DEBOUNCE_MS } : {}),
      run: async () => {
        const secret = snap.secretPending;
        const saved = await api().sink.save(rowInput({ ...snap, id: sinkIds.get(key) ?? snap.id }, secret));
        sinkIds.set(key, saved.id);
        return { saved, secret };
      },
      onSaved: ({ saved, secret }) => {
        patch(key, (r) => {
          const sent = secret !== null && r.secretPending === secret; // nothing newer was typed meanwhile
          return { ...r, id: saved.id, hasSecret: saved.hasSecret, secretPending: sent ? null : r.secretPending, secretDraft: sent ? "" : r.secretDraft };
        });
      },
    });
  };

  const setName = (key: number, name: string) => {
    patch(key, (r) => ({ ...r, name }));
    persist(key, true);
  };
  const setConfig = (key: number, field: string, value: string, debounced: boolean) => {
    patch(key, (r) => ({ ...r, config: { ...r.config, [field]: value } }));
    persist(key, debounced);
  };
  const setSendOn = (key: number, sendOn: SendOn) => {
    patch(key, (r) => ({ ...r, sendOn }));
    persist(key, false);
  };
  const setSecret = (key: number, value: string) => {
    patch(key, (r) => ({ ...r, secretDraft: value, secretPending: value }));
    persist(key, true);
  };

  const addDraft = (kind: SinkKind) => commit([...latest.current, newRow(kind, standupId)]);

  const remove = (r: Row) => {
    commit(latest.current.filter((x) => x.key !== r.key));
    // Through the save queue, so it runs after any save of this row that is still waiting or in flight.
    enqueue({
      key: `sink:${r.key}`,
      run: async () => {
        const id = sinkIds.get(r.key) ?? r.id;
        sinkIds.delete(r.key);
        if (id > 0) await api().sink.delete(id);
      },
    });
    showToast({
      message: `Deleted ${r.name}`,
      action: {
        label: "Undo",
        run: () => {
          // A new client key: its save is independent of the delete above and creates the sink again.
          const back: Row = { ...r, key: nextKey++, id: 0, hasSecret: false, lastDelivery: null, secretDraft: "", secretPending: null };
          commit([...latest.current, back]);
          persist(back.key, false);
        },
      },
      timeoutMs: 10_000,
    });
  };

  const sendNow = (r: Row) => {
    api()
      .sink.send(r.id, standupId, { kind: "current", from: "", to: "" })
      .catch((e: unknown) => showToast({ message: `Couldn't send to ${r.name}: ${toAppError(e).message}`, timeoutMs: 10_000 }));
  };

  const retry = (d: OutboxItem) => {
    api()
      .sink.retry(d.id)
      .catch((e: unknown) => showToast({ message: `Retry failed: ${toAppError(e).message}`, timeoutMs: 10_000 }));
  };

  const renderField = (r: Row, f: SinkField) => {
    const id = `exp-${r.key}-${f.key}`;
    if (f.type === "secret") {
      return (
        <TextField
          key={f.key}
          id={id}
          label={f.label}
          type="password"
          value={r.secretDraft}
          placeholder={r.hasSecret ? "Saved in keychain. Type to replace." : ""}
          hint="Stored in your system keychain."
          onInput={(v) => setSecret(r.key, v)}
          onCommit={() => void flush()}
        />
      );
    }
    if (f.type === "select") {
      const current = r.config[f.key] ?? f.default;
      return (
        <Combobox<string>
          key={f.key}
          id={id}
          label={f.label}
          value={current ? current : null}
          getKey={(o) => o}
          getLabel={(o) => o}
          options={f.options ?? []}
          onSelect={(o) => setConfig(r.key, f.key, o, false)}
        />
      );
    }
    if (f.type === "bool") {
      const checked = (r.config[f.key] ?? f.default) === "true";
      return (
        <Switch
          key={f.key}
          id={id}
          label={f.label}
          checked={checked}
          onChange={(c) => setConfig(r.key, f.key, c ? "true" : "false", false)}
        />
      );
    }
    return (
      <TextField
        key={f.key}
        id={id}
        label={f.label}
        type={f.type === "url" ? "url" : "text"}
        mono={f.type === "path"}
        value={r.config[f.key] ?? ""}
        hint={f.help || undefined}
        onInput={(v) => setConfig(r.key, f.key, v, true)}
        onCommit={() => void flush()}
      />
    );
  };

  const renderStatus = (r: Row) => {
    const d = r.lastDelivery;
    if (!d) return null;
    if (d.status === "delivered") {
      return <p class="exp-status">{`Delivered ${ageText(Date.now(), d.deliveredAt)} ago${d.receipt ? ` · ${d.receipt}` : ""}`}</p>;
    }
    if (d.status === "failed" || d.status === "dead") {
      return (
        <p class="exp-status exp-alert" role="alert">
          <span>Failed</span>
          {d.error ? ` · ${d.error}` : ""}{" "}
          <button type="button" class="exp-btn" onClick={() => retry(d)}>
            Retry
          </button>
        </p>
      );
    }
    return null;
  };

  const errorFor = (r: Row) => fieldErrors.value.get(`sink:${r.key}`);

  return (
    <section class="cfg-section exp" aria-labelledby="exp-title">
      <header class="cfg-section-head">
        <h2 id="exp-title" class="cfg-section-title">
          Export
        </h2>
        <span class="cfg-section-sub">Per standup · cuisle.export.v1 · autosaves</span>
      </header>

      {loadError ? (
        <p class="exp-alert" role="alert">
          {loadError}
        </p>
      ) : null}
      {!loadError && rows.length === 0 ? (
        <p class="exp-empty">No destinations yet. Add one below to export this standup.</p>
      ) : null}

      <ul class="exp-list">
        {rows.map((r) => {
          const kind = kindOf(r.kind);
          const sendOn: SendOn = r.sendOn === "after_sync" ? "after_sync" : "manual";
          const err = errorFor(r);
          return (
            <li class="exp-row" key={r.key}>
              <div class="exp-top">
                <div class="exp-name">
                  <TextField id={`exp-${r.key}-name`} label="Name" value={r.name} onInput={(v) => setName(r.key, v)} onCommit={() => void flush()} />
                </div>
                <button type="button" class="exp-btn" onClick={() => remove(r)}>
                  Delete
                </button>
              </div>
              <p class="exp-kind">
                <span>{kind ? kind.displayName : r.kind}</span>
                {r.standupId === 0 ? <span class="exp-tag">All standups</span> : null}
              </p>
              {(kind?.schema ?? []).map((f) => renderField(r, f))}
              <div class="exp-actions">
                <Segmented<SendOn>
                  id={`exp-${r.key}-sendon`}
                  label="Send on"
                  value={sendOn}
                  options={SEND_ON}
                  onChange={(v) => setSendOn(r.key, v)}
                />
                <button type="button" class="exp-btn exp-primary" disabled={r.id === 0} onClick={() => sendNow(r)}>
                  Send now
                </button>
              </div>
              {renderStatus(r)}
              {err ? (
                <p class="exp-alert" role="alert">
                  {err.message}
                </p>
              ) : null}
            </li>
          );
        })}
      </ul>

      <div class="exp-add">
        <Combobox<SinkKind>
          id="exp-add-destination"
          label="Add destination"
          value={null}
          getKey={(k) => k.kind}
          getLabel={(k) => k.displayName}
          options={kinds}
          placeholder="+ Add destination"
          clearOnSelect
          onSelect={addDraft}
        />
      </div>
    </section>
  );
}
