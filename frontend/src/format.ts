// Shared formatting helpers (decimal byte sizes, short month names, local "18 Nov" dates).
export const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"] as const;

/** Decimal units (1 KB = 1000 B), matching the mockups: "1.2 GB", "640 MB", "412 KB", "18.4 KB". */
export function formatBytes(n: number): string {
  if (n >= 1e9) return `${(n / 1e9).toFixed(1)} GB`;
  if (n >= 1e6) return n >= 1e7 ? `${Math.round(n / 1e6)} MB` : `${(n / 1e6).toFixed(1)} MB`;
  if (n >= 1e3) return n >= 1e5 ? `${Math.round(n / 1e3)} KB` : `${(n / 1e3).toFixed(1)} KB`;
  return `${n} B`;
}

/** Local calendar day without year, e.g. "18 Nov". */
export function shortDay(ms: number): string {
  const d = new Date(ms);
  return `${d.getDate()} ${MONTHS[d.getMonth()] ?? ""}`;
}
