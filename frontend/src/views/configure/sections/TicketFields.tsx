// Configure > Ticket fields section (per connection; ux-spec §14.8; architecture ConnectionService ticket-field methods).
// Exports TicketFieldsSection (AppSectionProps, like Connection: it has no standup config).
// Every action saves at once (ADR 0005). Pin, unpin and hide show the server's result; a reorder shows at once and
// returns to the last saved order if the save fails. Errors never show raw text.
import { useEffect, useRef, useState } from "preact/hooks";
import { api } from "../../../api";
import type { FieldOption, FieldPref, TicketFieldPrefs } from "../../../api/types";
import { Combobox } from "../../../components/Combobox";
import { Switch } from "../../../components/Switch";
import type { AppSectionProps } from "../props";
import "./TicketFields.css";

const SAVE_FAILED = "Couldn't save that change. The list shows the last saved order.";

/** Moves the field with fieldId to the 0-based position `to`, renumbering the list. */
function moveItem(list: FieldPref[], fieldId: string, to: number): FieldPref[] {
  const from = list.findIndex((f) => f.fieldId === fieldId);
  if (from < 0) return list;
  const next = list.slice();
  const [item] = next.splice(from, 1);
  if (!item) return list;
  next.splice(to, 0, item);
  return next.map((f, i) => ({ ...f, position: i }));
}

export function TicketFieldsSection(props: AppSectionProps) {
  void props;
  const [prefs, setPrefs] = useState<TicketFieldPrefs | null>(null);
  const [pinned, setPinned] = useState<FieldPref[]>([]);
  const [hiddenOpen, setHiddenOpen] = useState(false);
  const [error, setError] = useState("");
  const saved = useRef<FieldPref[]>([]); // last order the server confirmed

  const apply = (p: TicketFieldPrefs) => {
    setPrefs(p);
    setPinned(p.pinned ?? []);
    saved.current = p.pinned ?? [];
  };
  const fail = () => setError(SAVE_FAILED);

  useEffect(() => {
    api()
      .connection.ticketFields()
      .then(apply)
      .catch(() => setError("Couldn't read the ticket fields."));
  }, []);

  const pin = (f: FieldOption) => {
    setError("");
    api().connection.pinTicketField(f.fieldId).then(apply).catch(fail);
  };

  const unpin = (fieldId: string) => {
    setError("");
    api().connection.unpinTicketField(fieldId).then(apply).catch(fail);
  };

  const move = (fieldId: string, to: number) => {
    setError("");
    const next = moveItem(pinned, fieldId, to);
    setPinned(next);
    api()
      .connection.moveTicketField(fieldId, to)
      .then(() => {
        saved.current = next;
      })
      .catch(() => {
        setPinned(saved.current);
        fail();
      });
  };

  const setHidden = (fieldId: string, hidden: boolean) => {
    setError("");
    api().connection.setTicketFieldHidden(fieldId, hidden).then(apply).catch(fail);
  };

  const hidden = prefs?.hidden ?? [];

  return (
    <section class="cfg-section tf" aria-labelledby="tf-title">
      <header class="cfg-section-head">
        <h2 id="tf-title" class="cfg-section-title">
          Ticket fields
        </h2>
        <span class="cfg-section-sub">Per connection · saved as you change it</span>
      </header>

      {prefs ? (
        <>
          <Combobox<FieldOption>
            id="tf-pin"
            label="Pin a field"
            value={null}
            getKey={(f) => f.fieldId}
            getLabel={(f) => f.name}
            options={prefs.available ?? []}
            onSelect={pin}
            placeholder="Pin a field…"
            clearOnSelect
          />

          {pinned.length === 0 ? (
            <p class="tf-empty">Nothing pinned. All non-empty custom fields show A–Z.</p>
          ) : (
            <ul class="tf-list" aria-label="Pinned fields">
              {pinned.map((f, i) => (
                <li class="tf-row" key={f.fieldId}>
                  <span class="tf-name">{f.name}</span>
                  <button
                    type="button"
                    class="tf-btn"
                    aria-label={`Move ${f.name} up`}
                    disabled={i === 0}
                    onClick={() => move(f.fieldId, i - 1)}
                  >
                    ↑
                  </button>
                  <button
                    type="button"
                    class="tf-btn"
                    aria-label={`Move ${f.name} down`}
                    disabled={i === pinned.length - 1}
                    onClick={() => move(f.fieldId, i + 1)}
                  >
                    ↓
                  </button>
                  <button type="button" class="tf-btn" aria-label={`Unpin ${f.name}`} onClick={() => unpin(f.fieldId)}>
                    Unpin
                  </button>
                  <span class="tf-hide">
                    Hide
                    <Switch
                      id={`tf-hide-${f.fieldId}`}
                      label={`Hide ${f.name}`}
                      checked={false}
                      onChange={(checked) => setHidden(f.fieldId, checked)}
                    />
                  </span>
                </li>
              ))}
            </ul>
          )}

          {hidden.length > 0 ? (
            <div class="tf-hidden">
              <button
                type="button"
                class="tf-toggle"
                aria-expanded={hiddenOpen ? "true" : "false"}
                onClick={() => setHiddenOpen(!hiddenOpen)}
              >
                Hidden ({hidden.length})
              </button>
              {hiddenOpen ? (
                <ul class="tf-list" aria-label="Hidden fields">
                  {hidden.map((f) => (
                    <li class="tf-row" key={f.fieldId}>
                      <span class="tf-name">{f.name}</span>
                      <span class="tf-hide">
                        Hidden
                        <Switch
                          id={`tf-hide-${f.fieldId}`}
                          label={`Hide ${f.name}`}
                          checked
                          onChange={(checked) => setHidden(f.fieldId, checked)}
                        />
                      </span>
                    </li>
                  ))}
                </ul>
              ) : null}
            </div>
          ) : null}
        </>
      ) : null}

      {error ? (
        <p class="tf-alert" role="alert">
          {error}
        </p>
      ) : null}
    </section>
  );
}
