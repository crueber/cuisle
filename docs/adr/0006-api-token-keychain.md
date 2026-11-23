# ADR 0006 — Jira auth: email + API token in the OS keychain

Status: Accepted

## Context
Cuisle is a personal desktop tool; OAuth 2.0 (3LO) would require a registered app, a client secret in a distributed
binary, and a redirect flow, and does not cover the agile/dev-status endpoints uniformly.

## Decision
Authenticate with Jira Cloud **Basic auth (email + classic API token)** against `https://<site>.atlassian.net`.
The token is stored only in the OS credential store via `github.com/zalando/go-keyring` (macOS Keychain, Windows
Credential Manager, Linux Secret Service): service `crueber.cuisle`, account `<source>:<host>:<account>` (e.g. `jira:acme.atlassian.net:me@acme.com`); sink secrets use `sink:<id>`.
Site URL and email live in `connection`. The token is never written to SQLite, logs, diagnostics, or the frontend after
submission (`ConnectionService.Connect` is the only method that receives it; nothing returns it).
If no keychain is available (`keychain_unavailable`), connecting fails with instructions; the only bypass is the
`CUISLE_JIRA_TOKEN` environment variable, used in-memory for that process. No plaintext fallback file.

## Consequences
- Simple setup (paste a token), works for agile and dev-status endpoints with the user's own permissions.
- Tokens expire (Atlassian enforces expiry on new tokens): a 401 flips the app to a "Reconnect" banner while cached data
  stays usable.
- Scoped tokens / the `api.atlassian.com` gateway are not supported in milestone 1 (risk R4).
- macOS binds keychain items to the app's signing identity: builds are self-signed (ADR 0013), so a stable self-signed
  certificate is used for distributed builds; ad-hoc builds re-prompt after every update. Bundle id `crueber.cuisle` is final.
