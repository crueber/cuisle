# ADR 0016 — Auto-update from the download channel, once a day, verified, no telemetry

Status: Accepted (2026-11-24). Supersedes the "no auto-update" consequence of ADR 0013 and risk R16.

## Context
Cuisle ships as self-signed builds (ADR 0013) from two places: GitHub releases (macOS, Windows, Linux; built by
`scripts/publish/release.yml` in the published mirror) and Forgejo releases (Linux only; built by
`.forgejo/workflows/ci.yml` on `v*` tags). Users had to notice and download new versions themselves. The maintainer
wants an in-app update that respects the app's rules: no background polling, no network without a trigger, no
telemetry, and nothing installed that wasn't verified.

## Decision
**Channels.** A build knows where it was published, compiled in with `-ldflags -X` (`internal/buildinfo`):
`Channel` (`github` | `forgejo`), `ReleaseAPI` (that channel's releases-list endpoint) and, optionally,
`ReleasePubKey` (base64 ed25519, `internal/relsign`). The Taskfiles read `CUISLE_CHANNEL`, `CUISLE_RELEASE_API` and
`CUISLE_RELEASE_PUBKEY` from the environment:
- `release.yml` (GitHub) sets `github` and `https://api.github.com/repos/<owner>/<repo>/releases`, and, when the
  `RELEASE_SIGNING_KEY` secret exists, the public key (`relsign pubkey`).
- Forgejo `ci.yml` sets `forgejo` and the repository's `/api/v1/repos/<owner>/<repo>/releases` URL on tags only. The
  URL is written only in `.forgejo/workflows/ci.yml`, which is never published; no other file names the Forgejo host.
- Local `task build`/`task package`, `task dev`, branch CI builds (`0.0.0-<sha>`, a pre-release) and `dev` builds have
  no usable channel: they never check and never show the Update button. `--fake-jira` and `--bench-startup` build
  no updater at all.

Forgejo gets releases from the same publish: after the GitHub push, `publish-github.yml` creates the same `vX.Y.Z`
tag on Forgejo at the published source commit through the Forgejo API (a personal token, `FORGEJO_TAG_TOKEN`, else
`REGISTRY_TOKEN`: events made with the job's automatic token start no workflows). That tag runs `ci.yml`'s release
job, which attaches the AppImage, the .deb and `SHA256SUMS.txt`.

**When it checks.** At most one releases-list request per local calendar day, from two triggers only (no new
timers). A start check at 23:59 followed by the 00:00 rollover check is allowed: they are on different days.
- app start, when the last check is at least 24 h old (or the clock went backwards);
- the existing `day:rolled` local-midnight event, when no check ran that local day. (Gating rollover on "≥ 24 h"
  would make an always-open app check only every other day.)
- **Check now** (Configure → Display) always sends one request.

The setting **Check for updates daily** (`app_setting` `update.check_daily`, `on`|`off`, default on) turns the first two
off. A failed attempt counts as the day's check: no retries. The last check time, its error and the newest suitable
release are kept in `update.json` in the data folder.

**What it sends.** `GET <ReleaseAPI>?per_page=20` (GitHub) or `?limit=20` (Forgejo), anonymous, User-Agent
`Cuisle/<version>`, no cookies, no identifiers, no OS details. Downloads use the asset URLs the API returns, over
HTTPS only, with the same User-Agent.

**What counts as an update.** The highest `vX.Y.Z` tag that is not a draft or pre-release, newer than the running
version, and that already has this platform's asset **and** `SHA256SUMS.txt` (the release jobs create the release
first and upload after). Asset selection is a table (`internal/update/assets.go`): macOS `*.dmg` (universal, or this
arch), Windows `*-setup.exe`/`*-installer.exe` for this arch, Linux `*.AppImage` when running from one (`$APPIMAGE`),
otherwise `.deb` (release page only). Names are matched by pattern, not exactly.

**Verification before anything runs.** `SHA256SUMS.txt` is fetched first; when the build carries a public key, the
release's `SHA256SUMS.txt.sig` is required and must verify (a key in the binary with an unsigned release is a
failure, so stripping the signature cannot downgrade verification). Without a key, the signature is not checked and
SHA-256 alone is required. The asset must be listed, is hashed while streaming, and must match. A failure deletes the
download and shows "Couldn't verify the download, so nothing was installed."

**Hardening.**
- Every client the updater uses (releases list and downloads) has a redirect policy: each hop must be https, at most
  5 hops (GitHub's asset → `objects.githubusercontent.com` hop works). The releases URL itself must be https.
- Download URLs must be on the channel's host (the releases API host; for GitHub also `github.com` and
  `*.githubusercontent.com`). `update.json` is not trusted blindly: a stored release that fails this (or whose dmg or
  installer name carries a version other than its tag) is looked up again on Install and dropped if it still fails.
- A dmg or installer whose name carries a version must carry the release's own version.
- `SHA256SUMS.txt` listing a file twice with different sums fails closed.
- The download is read up to the asset's size + 1 (when known, and never past 512 MiB); compression is off on the
  download transport so the hashed bytes are the asset's.
- Windows: the helper is the absolute `%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe`; it re-checks
  the installer's SHA-256 with `Get-FileHash` immediately before `Start-Process -Verb RunAs` and skips it on a
  mismatch. The installer waits in the user's own temp folder (per-user ACL). Quoting doubles `'` and the typographic
  single quotes U+2018–U+201B.
- Relaunch helpers wait at most 60 s for this process to exit (sh loop 300 × 0.2 s; PowerShell `Wait-Process
  -Timeout 60`).
- A symlinked `$APPIMAGE` is resolved and the real file is replaced.

**Installing.** Behind an `Installer` interface (`update.System`, system commands behind a `Runner` so tests use fakes):
- **macOS:** `hdiutil attach -nobrowse -readonly -noautoopen -mountpoint <tmp>`, `ditto` the one `.app` into a staging
  folder next to the running bundle, detach (always, best-effort), swap by two renames (old aside, new in; the first is
  undone if the second fails), remove the old one (best-effort; if the old app could not be put back after a failed swap it is kept and the
  error says where it is), then a detached `sh` waits for this process to exit
  and runs `open -n` on the bundle. Once the second rename succeeded nothing reports "unchanged": if the relaunch
  helper can't start, the app stays open and says "Cuisle vX.Y.Z is installed. Quit and open Cuisle again to finish." A bundle that is not writable, not inside a `.app`, or translocated (`/AppTranslocation/`) can't update in
  place: the dialog offers the release page.
- **Windows:** a hidden, detached PowerShell waits for Cuisle to exit, runs the installer with `/S` through
  `Start-Process -Verb RunAs` (the NSIS installer is machine-scope and asks for elevation), starts Cuisle again and
  deletes the installer's folder.
- **Linux AppImage:** the download is written next to `$APPIMAGE`, made executable and renamed over it (atomic, same
  folder); a detached `sh` starts it once this process exits. Any other Linux install (`.deb` needs root) opens the
  release page instead.
- After a successful install the app quits through Wails (`OnShutdown` closes the database), and the helper relaunches it.

**UI.** When a newer release is known, the standup header shows **Update** between Resync and Configure (same
button family; icon + label when labels show, icon only otherwise; tooltip "Cuisle vX.Y.Z is available"). It opens a
small dialog: current → new version, **What's new** (the release page), **Install and restart**, with progress
(downloading %, verifying, installing, restarting) and plain failures. Configure → Display has the setting, the
version, the last check time and **Check now**.

**Code.** `internal/update` (releases clients for both JSON shapes, semver, asset table, verify, gate, state,
installers; imports only `relsign` and `apperr`), a thin `UpdateService` in `internal/app` (`Status` local,
`CheckNow`, `Install`; event `update:status`), wiring in `cmd/cuisle` (start trigger after the sync-on-open timer, the
day timer's `onRoll` hook, a separate download client without an overall timeout).

## Consequences
- Each install still meets Gatekeeper/SmartScreen rules of a self-signed build: macOS keeps the keychain grant only
  with the stable "Cuisle Self-Signed" certificate; Windows shows a UAC prompt for the machine-wide installer.
- The update check is a small, visible network request; the README and the project page say so, and the setting
  turns it off.
- A Forgejo publish needs the secret `FORGEJO_TAG_TOKEN` (a Forgejo access token with `write:repository`; or a
  `REGISTRY_TOKEN` with that scope); without it the
  GitHub publish succeeds and the Forgejo tag step fails loudly.
- The install paths that touch the system (hdiutil/ditto, NSIS, AppImage rename) are unit-tested with fakes and temp
  folders; each OS needs a manual check on a real machine before relying on it.
