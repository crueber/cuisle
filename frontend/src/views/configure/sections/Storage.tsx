// Configure > Display > Storage section (ux-spec §14.11; architecture AppService MediaCache / ClearMediaCache).
// Exports StorageSection (AppSectionProps). It is its OWN section component (Display.tsx does not render it, so
// Display.test.tsx's "no network calls" stays true); WP-T9 places it under Display.
// Reads local sizes once on mount, clears the image cache on demand, and autosaves the cleanup cap at once.
import { useEffect, useState } from "preact/hooks";
import { api } from "../../../api";
import type { Diagnostics, MediaCacheInfo } from "../../../api/types";
import { Stepper } from "../../../components/Stepper";
import { setAppSetting, settings } from "../../../state/app";
import type { AppSectionProps } from "../props";
import "./Storage.css";

const DEFAULT_CAP_GB = 2;
const MIN_CAP_GB = 1;
const MAX_CAP_GB = 64;

/** Decimal units (1 KB = 1000 B), as in the mockup: "1.2 GB", "640 MB", "412 KB". */
export function formatBytes(n: number): string {
  if (n >= 1e9) return `${(n / 1e9).toFixed(1)} GB`;
  if (n >= 1e6) return `${Math.round(n / 1e6)} MB`;
  if (n >= 1e3) return n >= 1e5 ? `${Math.round(n / 1e3)} KB` : `${(n / 1e3).toFixed(1)} KB`;
  return `${n} B`;
}

/** The cleanup cap is stored in MiB (like every *_mb setting) and shown in GB (1 GB = 1024 MB). */
function capGb(raw: string | undefined): number {
  const n = Number.parseInt(raw ?? "", 10);
  if (Number.isNaN(n)) return DEFAULT_CAP_GB;
  return Math.min(MAX_CAP_GB, Math.max(MIN_CAP_GB, Math.round(n / 1024)));
}

export function StorageSection(props: AppSectionProps) {
  void props;
  const [diag, setDiag] = useState<Diagnostics | null>(null);
  const [media, setMedia] = useState<MediaCacheInfo | null>(null);
  const [error, setError] = useState("");
  const [loadFailed, setLoadFailed] = useState(false);

  useEffect(() => {
    Promise.all([api().app.diagnostics(), api().app.mediaCache()])
      .then(([d, m]) => {
        setDiag(d);
        setMedia(m);
      })
      .catch(() => setLoadFailed(true));
  }, []);

  const clear = () => {
    setError("");
    api()
      .app.clearMediaCache()
      .then((m) => setMedia(m))
      .catch(() => setError("Couldn't clear the image cache. Nothing was removed."));
  };

  const total = diag && media ? diag.dbBytes + media.bytes + media.thumbBytes : null;
  const images = media ? media.bytes + media.thumbBytes : null;

  return (
    <section class="cfg-section sto" aria-labelledby="sto-title">
      <header class="cfg-section-head">
        <h2 id="sto-title" class="cfg-section-title">
          Storage
        </h2>
        <span class="cfg-section-sub">Kept on this computer</span>
      </header>

      <p class="sto-size">
        {total !== null && images !== null ? (
          <>
            <strong>{formatBytes(total)}</strong> · images {formatBytes(images)}
          </>
        ) : loadFailed ? (
          "Couldn't read the local size."
        ) : (
          "Reading…"
        )}
      </p>

      <div class="sto-actions">
        <button type="button" class="sto-btn" disabled={media === null} onClick={clear}>
          Clear image cache
        </button>
      </div>
      {error ? (
        <p class="sto-alert" role="alert">
          {error}
        </p>
      ) : null}

      <div class="cfg-field">
        <span class="cfg-field-label">Cleanup offer above</span>
        <div class="cfg-field-control">
          <Stepper
            id="sto-cap"
            label="Cleanup offer above"
            value={capGb(settings.value["storage.cleanup_offer_mb"])}
            min={MIN_CAP_GB}
            max={MAX_CAP_GB}
            suffix="GB"
            onChange={(v) => setAppSetting("storage.cleanup_offer_mb", String(v * 1024))}
          />
        </div>
      </div>
    </section>
  );
}
