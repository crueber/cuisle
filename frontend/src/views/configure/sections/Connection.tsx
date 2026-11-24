// Configure > Connection section (app-wide; ux-spec §9.4 Connection, product-spec F1).
// Shows the site and account, offers Reconnect (the Connect form inline, reconnect mode) and Disconnect behind an
// inline two-step confirm (no window.confirm: webviews block native dialogs).
import { useRef, useState } from "preact/hooks";
import { api, toAppError } from "../../../api";
import type { Diagnostics, MediaCacheInfo } from "../../../api/types";
import { Checkbox } from "../../../components/Checkbox";
import { connection, settings } from "../../../state/app";
import { ConnectScreen } from "../../connect/ConnectScreen";
import type { AppSectionProps } from "../props";
import { formatBytes } from "./Storage";
import "./Connection.css";

const MIB = 1048576;
const DEFAULT_CAP_MB = 2048;

function hostOf(baseUrl: string): string {
  return baseUrl.replace(/^https?:\/\//i, "").replace(/\/+$/, "");
}

/** The cleanup cap in bytes (storage.cleanup_offer_mb is MiB). */
function capBytes(): number {
  const n = Number.parseInt(settings.value["storage.cleanup_offer_mb"] ?? "", 10);
  return (Number.isNaN(n) ? DEFAULT_CAP_MB : n) * MIB;
}

export function ConnectionSection(props: AppSectionProps) {
  void props;
  const conn = connection.value;
  const [reconnecting, setReconnecting] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [offer, setOffer] = useState<string | null>(null); // formatted content size, only when over the cap
  const [deleteContent, setDeleteContent] = useState(false);
  const lookup = useRef(0); // invalidates a size lookup that answers after Cancel or a re-open

  /** Reads the local content size when the confirm opens. Confirm never waits for it; failure means no offer. */
  const readSize = () => {
    const id = ++lookup.current;
    const cap = capBytes();
    let sizes: Promise<[Diagnostics, MediaCacheInfo]>;
    try {
      sizes = Promise.all([api().app.diagnostics(), api().app.mediaCache()]);
    } catch {
      return; // no offer: the safe default (keep synced content)
    }
    sizes
      .then(([d, m]) => {
        if (id !== lookup.current) return;
        const total = d.dbBytes + m.bytes + m.thumbBytes;
        if (total > cap) setOffer(formatBytes(total));
      })
      .catch(() => {
        // No offer: the safe default (keep synced content).
      });
  };

  const openConfirm = () => {
    setOffer(null);
    setDeleteContent(false);
    setConfirming(true);
    readSize();
  };

  const cancel = () => {
    lookup.current++;
    setConfirming(false);
    setOffer(null);
    setDeleteContent(false);
  };

  const disconnect = () => {
    lookup.current++;
    setBusy(true);
    setError("");
    api()
      .connection.disconnect(deleteContent)
      .then(() => api().connection.get())
      .then((c) => {
        connection.value = c;
      })
      .catch((e: unknown) => setError(`Couldn't disconnect: ${toAppError(e).message}`))
      .finally(() => {
        setBusy(false);
        setConfirming(false);
        setOffer(null);
        setDeleteContent(false);
      });
  };

  return (
    <section class="cfg-section conn" aria-labelledby="conn-title">
      <header class="cfg-section-head">
        <h2 id="conn-title" class="cfg-section-title">
          Connection
        </h2>
        <span class="cfg-section-sub">Jira Cloud · app-wide</span>
      </header>

      <div class="conn-status">
        <p class="conn-site">{conn?.baseUrl ? hostOf(conn.baseUrl) : "Not connected"}</p>
        {conn?.account ? <p class="conn-account">{conn.account}</p> : null}
        {conn?.state === "ok" ? <span class="conn-tag">Connected</span> : null}
        {conn && conn.state !== "ok" && conn.state !== "missing" ? (
          <p class="conn-alert" role="status">
            Jira rejected the token. Reconnect with a new one.
          </p>
        ) : null}
      </div>

      <div class="conn-actions">
        <button type="button" class="conn-btn" onClick={() => setReconnecting(true)}>
          Reconnect
        </button>
        {confirming ? (
          <>
            <button type="button" class="conn-btn conn-danger" disabled={busy} onClick={disconnect}>
              Confirm disconnect
            </button>
            <button type="button" class="conn-btn" onClick={cancel}>
              Cancel
            </button>
          </>
        ) : (
          <button type="button" class="conn-btn" onClick={openConfirm}>
            Disconnect
          </button>
        )}
      </div>

      {confirming && offer ? (
        <div class="conn-offer">
          <Checkbox
            id="conn-delete-content"
            label={`Also delete synced content (${offer}): descriptions, comments and images. History of status, assignee and flags is kept.`}
            checked={deleteContent}
            onChange={setDeleteContent}
          />
        </div>
      ) : null}

      {error ? (
        <p class="conn-alert" role="alert">
          {error}
        </p>
      ) : null}

      {reconnecting ? <ConnectScreen reconnect /> : null}
    </section>
  );
}
