// Configure > Connection section (app-wide; ux-spec §9.4 Connection, product-spec F1).
// Shows the site and account, offers Reconnect (the Connect form inline, reconnect mode) and Disconnect behind an
// inline two-step confirm (no window.confirm: webviews block native dialogs).
import { useState } from "preact/hooks";
import { api, toAppError } from "../../../api";
import { connection } from "../../../state/app";
import { ConnectScreen } from "../../connect/ConnectScreen";
import type { AppSectionProps } from "../props";
import "./Connection.css";

function hostOf(baseUrl: string): string {
  return baseUrl.replace(/^https?:\/\//i, "").replace(/\/+$/, "");
}

export function ConnectionSection(props: AppSectionProps) {
  void props;
  const conn = connection.value;
  const [reconnecting, setReconnecting] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const disconnect = () => {
    setBusy(true);
    setError("");
    api()
      .connection.disconnect()
      .then(() => api().connection.get())
      .then((c) => {
        connection.value = c;
      })
      .catch((e: unknown) => setError(`Couldn't disconnect: ${toAppError(e).message}`))
      .finally(() => {
        setBusy(false);
        setConfirming(false);
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
            <button type="button" class="conn-btn" onClick={() => setConfirming(false)}>
              Cancel
            </button>
          </>
        ) : (
          <button type="button" class="conn-btn" onClick={() => setConfirming(true)}>
            Disconnect
          </button>
        )}
      </div>

      {error ? (
        <p class="conn-alert" role="alert">
          {error}
        </p>
      ) : null}

      {reconnecting ? <ConnectScreen reconnect /> : null}
    </section>
  );
}
