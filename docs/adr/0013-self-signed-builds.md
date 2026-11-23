# ADR 0013 — Self-signed builds; GitHub Actions for CI and releases, Forgejo for Linux

Status: Accepted (revisit if distribution widens)

## Context
Cuisle is a personal/team tool. There is no Apple Developer ID and no commercial Windows code-signing certificate. The
repository's origin is a self-hosted Forgejo (`github.com`) with Linux runners only.

## Decision
- **CI** (Weave model, architecture §6.5): Forgejo Actions on the origin runs Linux tests, build and Linux releases
  (`.forgejo/workflows/ci.yml`). GitHub is a published mirror (`publish-to-github.sh`, run manually through
  `.forgejo/workflows/publish-github.yml`). The GitHub workflows are templates in `scripts/publish/` (`ci.yml` for 3-OS CI,
  `release.yml` for the tag-triggered desktop release). Workflows stay thin; logic lives in `Taskfile.yml`. Local macOS builds are fine
  for day-to-day use.
- **macOS**: sign with a stable self-signed code-signing certificate ("Cuisle Self-Signed"; CI secrets
  `MACOS_SIGN_P12`, `MACOS_SIGN_P12_PASSWORD`), falling back to ad-hoc (`codesign --sign -`) when absent. No notarisation.
- **Windows**: self-signed Authenticode certificate via `signtool` (CI secrets `WINDOWS_SIGN_PFX`,
  `WINDOWS_SIGN_PFX_PASSWORD`); unsigned when absent.
- **Linux**: unsigned AppImage and `.deb`. Every release carries `SHA256SUMS` for all artifacts.
- Bundle id / single-instance id / keychain service: `crueber.cuisle`. Module path: `github.com/crueber/cuisle`.

## Consequences
- macOS Gatekeeper blocks first launch: right-click → Open, or Privacy & Security → Open Anyway, or
  `xattr -dr com.apple.quarantine /Applications/Cuisle.app`. Documented in the README install section.
- macOS keychain items are bound to the signing identity: with the stable cert the access prompt appears once; with
  ad-hoc signatures, or if the cert is lost/rotated, it reappears after each update. Chris keeps the `.p12` safe.
- Windows SmartScreen warns on every new version ("More info → Run anyway").
- No auto-update (it would need trusted signatures anyway). Integrity for downloaders rests on HTTPS + `SHA256SUMS`.
- Moving to Developer ID + notarisation / a commercial cert later changes only the release workflow and this ADR.
