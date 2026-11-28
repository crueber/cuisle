// Connect card: first run (no connection yet) and the reconnect variant shown inline in Configure (ux-spec §9.7;
// product-spec F1; architecture R4). The token is sent once in connection.connect and is never stored in app state:
// it stays only in the field the user typed it in, and is cleared after a successful connect.
import { useRef, useState } from "preact/hooks";
import { api, toAppError } from "../../api";
import { TextField } from "../../components/TextField";
import { connection } from "../../state/app";
import { createStandup } from "../../state/config";
import { goConfigure } from "../../state/route";
import type { ConnectScreenProps } from "../configure/props";
import "./ConnectScreen.css";

const TOKEN_URL = "https://id.atlassian.com/manage-profile/security/api-tokens";

type FieldName = "site" | "email" | "token";
type FieldErrors = Partial<Record<FieldName, string>>;

/** The host part of a Jira site as typed or stored (no scheme, no trailing slashes). */
function hostOf(baseUrl: string): string {
  return baseUrl.trim().replace(/^https?:\/\//i, "").replace(/\/+$/, "");
}

/** localhost, 127.x.x.x and [::1], with an optional port. */
function isLoopback(host: string): boolean {
  return /^(localhost|127(\.\d{1,3}){3}|\[::1\])(:\d+)?$/i.test(host);
}

/**
 * What the site field shows for a stored base URL: the bare host, except a loopback http:// URL (the fake Jira), which
 * keeps its scheme so it round-trips unchanged.
 */
function displaySite(baseUrl: string): string {
  const host = hostOf(baseUrl);
  return /^http:\/\//i.test(baseUrl.trim()) && isLoopback(host) ? `http://${host}` : host;
}

/**
 * "acme.atlassian.net", "HTTP://Acme.atlassian.net/" → "https://acme.atlassian.net". A loopback host defaults to
 * http:// (plain-http fake Jira) and keeps an explicit http://; any other host is always https.
 */
function normaliseSite(typed: string): string {
  const host = hostOf(typed).toLowerCase();
  const plainHttp = isLoopback(host) && !/^https:\/\//i.test(typed.trim());
  return `${plainHttp ? "http" : "https"}://${host}`;
}

/** Maps the field named by a backend error onto one of the three inputs. */
const FIELD_OF: Record<string, FieldName> = { baseUrl: "site", account: "email", secret: "token" };

export function ConnectScreen({ reconnect = false }: ConnectScreenProps) {
  // Reconnect prefills site and email from the stored connection; the token is never prefilled.
  const prior = reconnect ? connection.value : null;
  const [site, setSite] = useState(() => (prior ? displaySite(prior.baseUrl) : ""));
  const [email, setEmail] = useState(() => prior?.account ?? "");
  const [token, setToken] = useState("");
  const [pending, setPending] = useState(false);
  const [fieldErrors, setFieldErrors] = useState<FieldErrors>({});
  const [formError, setFormError] = useState("");
  const inFlight = useRef(false);

  const onSubmit = async (e: Event) => {
    e.preventDefault();
    if (inFlight.current) return;

    const errs: FieldErrors = {};
    if (!site.trim()) errs.site = "Enter your Jira site, like your-site.atlassian.net.";
    if (!email.trim()) errs.email = "Enter the email for this Jira account.";
    if (!token) errs.token = "Enter an API token.";
    if (Object.keys(errs).length > 0) {
      setFieldErrors(errs);
      setFormError("");
      return;
    }

    inFlight.current = true;
    setPending(true);
    setFieldErrors({});
    setFormError("");
    try {
      const c = await api().connection.connect({
        source: "jira",
        baseUrl: normaliseSite(site),
        account: email.trim(),
        secret: token,
      });
      connection.value = c;
      setToken("");
      if (!reconnect) {
        await createStandup();
        goConfigure(null);
      }
    } catch (err) {
      const e = toAppError(err);
      const field = FIELD_OF[e.field];
      if (field) setFieldErrors({ [field]: e.message });
      else setFormError(e.message);
    } finally {
      inFlight.current = false;
      setPending(false);
    }
  };

  // First run is the page's only landmark (<main>). The inline reconnect variant sits inside Configure, so it stays a div.
  const Root = reconnect ? "div" : "main";
  return (
    <Root class={reconnect ? "connect connect-inline" : "connect"}>
      <div class="connect-card">
        {reconnect ? null : (
          <>
            <h1 class="connect-wordmark">CUISLE</h1>
            <p class="connect-value">Standups from your Jira board, one screen per person.</p>
          </>
        )}
        {reconnect ? <p class="connect-value">Paste a new API token. The site and email stay the same.</p> : null}

        <form class="connect-form" onSubmit={onSubmit} noValidate>
          <TextField
            id="connect-site"
            label="Jira site"
            value={site}
            placeholder="your-site.atlassian.net"
            onInput={setSite}
            error={fieldErrors.site}
          />
          <TextField
            id="connect-email"
            label="Email"
            type="email"
            value={email}
            onInput={setEmail}
            error={fieldErrors.email}
          />
          <TextField
            id="connect-token"
            label="API token"
            type="password"
            value={token}
            onInput={setToken}
            error={fieldErrors.token}
            hint="Classic API tokens only. Stored in your system keychain."
          />
          <p class="connect-help">
            <a
              href={TOKEN_URL}
              target="_blank"
              rel="noopener noreferrer"
              onClick={(e) => {
                e.preventDefault();
                void api().app.openUrl(TOKEN_URL).catch(() => undefined);
              }}
            >
              Create an API token ↗
            </a>
          </p>
          {formError ? (
            <p class="connect-error" role="alert">
              {formError}
            </p>
          ) : null}
          <div class="connect-actions">
            <button type="submit" class="connect-submit" disabled={pending}>
              {pending ? "Connecting…" : "Connect"}
            </button>
          </div>
        </form>
      </div>
    </Root>
  );
}
