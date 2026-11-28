// Update confirm dialog (ux-spec §2.1 Update, ADR 0016): current → new version, What's new (the release page), and
// Install and restart. While installing it shows the phase and download percent; failures say what went wrong and
// nothing was installed. A click on the scrim or Escape closes it (not while installing).
import { useRef } from "preact/hooks";
import type { UpdateStatus } from "../../api/types";
import { trapTab, useDialogFocus } from "../../components/modal";
import { installUpdate, installing, openReleasePage, updateDialogOpen, updateStatus } from "../../state/update";
import "./UpdateDialog.css";

/** The progress line for a phase ("" when idle). */
export function phaseText(s: UpdateStatus): string {
  switch (s.phase) {
    case "downloading":
      return s.progress >= 0 ? `Downloading… ${s.progress}%` : "Downloading…";
    case "verifying":
      return "Verifying the download…";
    case "installing":
      return "Installing…";
    case "restarting":
      return "Restarting…";
  }
  return "";
}

export function UpdateDialog() {
  const s = updateStatus.value;
  const avail = s.available;
  const busy = installing(s);
  const ref = useRef<HTMLDivElement>(null);
  const scrimDown = useRef(false);

  useDialogFocus(ref);

  const close = () => {
    if (!busy) updateDialogOpen.value = false;
  };
  if (!avail) return null;
  const progress = phaseText(s);
  const failed = s.phase === "failed" && s.error !== "";
  // Installed, but the automatic restart failed: there is nothing to retry, only to reopen Cuisle.
  const installedNoRestart = failed && s.errorCode === "restart";

  return (
    <div
      class="ud-scrim"
      onMouseDown={(e) => {
        scrimDown.current = e.target === e.currentTarget;
      }}
      onClick={(e) => {
        const down = scrimDown.current;
        scrimDown.current = false;
        if (down && e.target === e.currentTarget) close();
      }}
    >
      <div
        ref={ref}
        class="ud"
        role="dialog"
        aria-modal="true"
        aria-labelledby="ud-title"
        tabIndex={-1}
        onKeyDown={(e) => {
          if (e.key === "Escape") {
            e.preventDefault();
            e.stopPropagation();
            close();
            return;
          }
          if (e.key === "Tab") trapTab(e, ref.current);
        }}
      >
        <h2 id="ud-title" class="ud-title">
          Update Cuisle
        </h2>
        <p class="ud-versions">
          <span class="ud-version">{s.currentVersion}</span>
          <span class="ud-arrow" aria-hidden="true">
            →
          </span>
          <span class="ud-version ud-version--new">{avail.version}</span>
        </p>
        <button type="button" class="ud-link" onClick={openReleasePage}>
          What's new
        </button>
        {!avail.canInstall ? (
          <p class="ud-note">This copy can't replace itself. Download the new version from the release page.</p>
        ) : null}
        {progress ? (
          <div class="ud-progress" role="status">
            <span>{progress}</span>
            <div
              class="ud-bar"
              role="progressbar"
              aria-label="Update progress"
              aria-valuemin={0}
              aria-valuemax={100}
              aria-valuenow={s.phase === "downloading" && s.progress >= 0 ? s.progress : undefined}
            >
              <span style={`width:${s.phase === "downloading" ? Math.max(0, s.progress) : 100}%`} />
            </div>
          </div>
        ) : null}
        {failed ? (
          <p class="ud-error" role="alert">
            {s.error}
          </p>
        ) : null}
        <div class="ud-actions">
          <button type="button" class="ud-btn" onClick={close} disabled={busy}>
            {failed ? "Close" : "Not now"}
          </button>
          {installedNoRestart ? null : avail.canInstall ? (
            <button
              type="button"
              class="ud-btn ud-btn--primary"
              disabled={busy}
              onClick={() => {
                void installUpdate();
              }}
            >
              {failed ? "Try again" : "Install and restart"}
            </button>
          ) : (
            <button type="button" class="ud-btn ud-btn--primary" onClick={openReleasePage}>
              Open the release page
            </button>
          )}
        </div>
      </div>
    </div>
  );
}
