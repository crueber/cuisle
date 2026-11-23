// The auth banner's "Connect" when standups already exist: the reconnect form full-screen with a Cancel and Esc back to
// where the user was. A successful reconnect only replaces the token; it never creates a standup (product-spec F1).
import { useEffect, useRef } from "preact/hooks";
import { connection } from "../../state/app";
import { leaveConnect } from "../../state/route";
import { ConnectScreen } from "./ConnectScreen";
import "./ConnectScreen.css";

export function ReconnectScreen() {
  const opened = useRef(connection.value);
  const conn = connection.value;

  // A fresh connection object in state "ok" (reconnect succeeded, or the auth event said so) ends the detour.
  useEffect(() => {
    if (conn !== opened.current && conn?.state === "ok") leaveConnect();
  }, [conn]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !e.defaultPrevented) leaveConnect();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  return (
    <main class="connect">
      <div class="connect-reconnect">
        <h1 class="connect-reconnect-title">Reconnect to Jira</h1>
        <ConnectScreen reconnect />
        <button type="button" class="connect-cancel" onClick={() => leaveConnect()}>
          Cancel
        </button>
      </div>
    </main>
  );
}
