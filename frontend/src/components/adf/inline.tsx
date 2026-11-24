// Inline chips: status lozenge, mention, date and emoji. Go computes Past and Self; nothing here reads the clock.

const ISO_DAY = /^(\d{4})-(\d{2})-(\d{2})$/;

/** "2026-12-01" -> "1 Dec 2026" (UTC calendar date, so no timezone shift). Unparseable input is shown as is. */
export function formatDay(iso: string): string {
  const m = ISO_DAY.exec(iso);
  if (!m) return iso;
  const date = new Date(Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3])));
  return date.toLocaleDateString("en-GB", { day: "numeric", month: "short", year: "numeric", timeZone: "UTC" });
}

export function Status({ label, lozenge }: { label: string; lozenge: string }) {
  return (
    <span class="adf-status" data-adf="status" data-lozenge={lozenge}>
      {label}
    </span>
  );
}

export function Mention({ label, self }: { label: string; self: boolean }) {
  return (
    <span class="adf-mention" data-adf="mention" data-self={self ? "true" : undefined}>
      @{label}
    </span>
  );
}

export function DateChip({ date, past }: { date: string; past: boolean }) {
  return (
    <span class="adf-date" data-adf="date" data-past={past ? "true" : undefined}>
      <time dateTime={date}>{formatDay(date)}</time>
    </span>
  );
}

export function Emoji({ glyph, shortName }: { glyph: string; shortName: string }) {
  return (
    <span class="adf-emoji" data-adf="emoji">
      {glyph || shortName}
    </span>
  );
}
