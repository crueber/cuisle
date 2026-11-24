// WP-T7 tests: TicketHeader (ux-spec §14.3 header, §14.12 Refresh states; props frozen in props.ts).
//
// SPEC-NOTE: DOM HOOKS used by this file:
//   - Root: the first element child of the render container; data-short="true|false" mirrors geometry.short.
//   - Type icon: an <svg> with class `type-<kind>` (kind = story|bug|task|spike|other), i.e. the existing TypeIcon of
//     views/standup/TicketRow (reuse it).
//   - Key: the element whose text is exactly the key; clicking it calls onCopyKey (it may be a button).
//   - No parent crumb and no stepper: the parent lives in the field rail, J/K step from the keyboard (TicketModal).
//   - Summary: a heading (any level) whose name is the summary.
//   - Meta text: compact "\u26A1\uFE0E9:41" from detail.syncedAt (local time, H:mm unpadded) with a `title` tooltip
//     "Synced at 9:41 (this ticket's data is as of that sync)". Phase done: "\u26A1\uFE0E9:58" with the tooltip
//     "Refreshed just this ticket at 9:58 · board synced 9:41" REPLACES it. Phase refreshing: the synced text stays and
//     "refreshing…" is added after 250 ms. Phase failed: "Couldn't refresh · <reason>" on an element with
//     data-alert="true". Phase waiting: "waiting for Jira (rate limit) · 6s" (waitMs rounded up to whole seconds).
//   - Back: a button whose text contains backKey (only when backKey !== ""). Close: button named "Close".
//   - Refresh: button named /refresh/i, icon-only below modalW 1216 (the visible text is absent, the accessible name
//     stays). Open in Jira: an icon-only button at every width, accessible name "Open in Jira", title "Open in Jira (O)". Refresh is `disabled` when cooldownMs > 0 or the phase is
//     refreshing / waiting, and has aria-busy="true" while refreshing / waiting (the spinning icon). During a cooldown
//     its text contains the whole-second countdown, e.g. "8s" for 8000 ms (rounded up), even when icon-only.
//   - Pulse row: PulseTrack from ../standup/PulseTrack (replaced by a double here) gets detail.row.pulse, days =
//     detail.pulseDays and a width of 600 / 440 / 440 / 300 for the four table geometries (modalW 1320 / 1240 / 1216 /
//     1000). Days chip: the element with text "<n>d" (n = max(1, row.daysInLane)) with data-alert="true" when row.overLimit
//     or row.blocked, else "false". Blocked lozenge text "BLOCKED 3D" (blockedDays) when row.blocked. Limit text
//     "limit 5d · 21 days" when limitDays > 0. The lane name (row.statusName) appears (CSS makes it caps).
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { act, fireEvent, render, screen, within } from "@testing-library/preact";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import mob1421 from "../../api/fixtures/ticket_mob1421.json";
import mob1429 from "../../api/fixtures/ticket_mob1429.json";
import type { TicketDetail } from "../../api/types";
import { TicketHeader } from "./TicketHeader";
import type { ModalGeometry, RefreshState, TicketHeaderProps } from "./props";

vi.mock("../standup/PulseTrack", async () => {
  const { h } = await import("preact");
  return {
    PulseTrack: (p: { pulse: { label: string }; days: number; width: number }) =>
      h("div", { role: "img", "aria-label": p.pulse.label, "data-testid": "track", "data-width": String(p.width), "data-days": String(p.days) }),
  };
});

const clone = <X,>(x: X): X => JSON.parse(JSON.stringify(x)) as X;
const D1421: TicketDetail = mob1421;
const D1429: TicketDetail = mob1429;

const G1920: ModalGeometry = { modalW: 1320, modalH: 1000, contentW: 880, railW: 432, short: false, narrow: false, stackedLabels: false };
const G1440: ModalGeometry = { modalW: 1240, modalH: 836, contentW: 840, railW: 392, short: false, narrow: false, stackedLabels: false };
const G1280: ModalGeometry = { modalW: 1216, modalH: 672, contentW: 836, railW: 372, short: true, narrow: false, stackedLabels: false };
const G1024: ModalGeometry = { modalW: 1000, modalH: 616, contentW: 700, railW: 292, short: true, narrow: false, stackedLabels: true };

const IDLE: RefreshState = { phase: "idle", at: 0, cooldownMs: 0, waitMs: 0, reason: "" };
const local = (h: number, m: number) => new Date(2026, 10, 23, h, m, 0).getTime();

function props(over: Partial<TicketHeaderProps> = {}): TicketHeaderProps {
  const detail = clone(D1421);
  detail.syncedAt = local(9, 41);
  return {
    detail,
    geometry: G1920,
    backKey: "",
    refresh: IDLE,
    onBack: vi.fn(),
    onRefresh: vi.fn(),
    onOpenInJira: vi.fn(),
    onCopyKey: vi.fn(),
    onClose: vi.fn(),
    ...over,
  };
}
const renderHeader = (over: Partial<TicketHeaderProps> = {}) => {
  const p = props(over);
  const view = render(<TicketHeader {...p} />);
  return { ...view, p };
};
const BOLT = "\u26A1\uFE0E";
const refreshBtn = () => screen.getByRole("button", { name: /refresh/i });

describe("identity: type icon, key, summary", () => {
  it("rendersTheKeyTheTypeIconAndTheSummary_WithNoParentCrumb", () => {
    const { container } = renderHeader();
    expect(screen.getByText("MOB-1421")).toBeTruthy();
    expect(container.querySelector("svg.type-story")).not.toBeNull();
    expect(screen.getByRole("heading", { name: "Unit toggle between metric and US cups" })).toBeTruthy();
    expect(screen.queryByText(/MOB-1200/)).toBeNull();
    expect(screen.queryByText(/Recipe units v2/)).toBeNull();
    expect(screen.queryByText("in")).toBeNull();
  });

  it("usesTheTypeOfTheTicketForTheIcon", () => {
    const detail = clone(D1429);
    const { container } = renderHeader({ detail });
    expect(screen.getByText("MOB-1429")).toBeTruthy();
    expect(container.querySelector("svg.type-bug")).not.toBeNull();
    expect(container.querySelector("svg.type-story")).toBeNull();
  });

  it("clickingTheKeyCopiesIt", () => {
    const { p } = renderHeader();
    fireEvent.click(screen.getByText("MOB-1421"));
    expect(p.onCopyKey).toHaveBeenCalledTimes(1);
  });

  it("neverWrapsTheKey_AtAnyGeometry", () => {
    // jsdom has no layout; the CSS contract is pinned here and the real layout by the Playwright header spec.
    const css = readFileSync(resolve(__dirname, "TicketHeader.css"), "utf8");
    const rule = (sel: string) =>
      [...css.matchAll(new RegExp(`^\\${sel} \\{([^}]*)\\}`, "gm"))].map((m) => m[1]).join("\n");
    expect(rule(".th-key")).toMatch(/white-space:\s*nowrap/);
    expect(rule(".th-back")).toMatch(/white-space:\s*nowrap/);
  });
});

describe("back, close, open in Jira, no stepper", () => {
  it("hasNoStepperButtonsAndNoPositionCounter", () => {
    renderHeader();
    expect(screen.queryByRole("button", { name: "Previous ticket" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Next ticket" })).toBeNull();
    expect(screen.queryByText(/\d+ \/ \d+/)).toBeNull();
  });

  it("showsTheBackButtonOnlyWhenThereIsABackKey", () => {
    const detail = clone(D1429);
    const withBack = renderHeader({ detail, backKey: "MOB-1421" });
    const back = screen.getByRole("button", { name: /MOB-1421/ });
    fireEvent.click(back);
    expect(withBack.p.onBack).toHaveBeenCalledTimes(1);
    withBack.unmount();

    renderHeader({ detail, backKey: "" });
    expect(screen.getByText("MOB-1429")).toBeTruthy();
    expect(screen.queryByRole("button", { name: /MOB-1421/ })).toBeNull();
  });

  it("closeAndOpenInJiraCallTheirHandlers", () => {
    const { p } = renderHeader();
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(p.onClose).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole("button", { name: /open in jira/i }));
    expect(p.onOpenInJira).toHaveBeenCalledTimes(1);
  });

  it("openInJiraIsAnIconOnlyButton_WithAnAccessibleNameAndATooltip", () => {
    for (const geometry of [G1920, G1280, G1024]) {
      const view = renderHeader({ geometry });
      const btn = screen.getByRole("button", { name: "Open in Jira" });
      expect(btn.textContent).toBe("");
      expect(btn.getAttribute("title")).toBe("Open in Jira (O)");
      expect(btn.querySelector("svg")).not.toBeNull();
      view.unmount();
    }
  });

  it("refreshIsIconOnlyBelow1280_WithTheNameKept", () => {
    const wide = renderHeader({ geometry: G1280 });
    expect(refreshBtn().textContent).toContain("Refresh");
    wide.unmount();

    renderHeader({ geometry: G1024 });
    expect(refreshBtn().textContent).not.toContain("Refresh");
  });
});

describe("meta text and the Refresh button (ux-spec §14.12)", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("idle_ShowsTheCompactSyncTimeWithATooltip_AndRefreshCallsTheHandlerOnce", () => {
    const { p } = renderHeader();
    const meta = screen.getByText(`${BOLT}9:41`);
    expect(meta.getAttribute("title")).toBe("Synced at 9:41 (this ticket's data is as of that sync)");
    expect(screen.queryByText(/as of 9:41 · synced/)).toBeNull();
    expect((refreshBtn() as HTMLButtonElement).disabled).toBe(false);
    expect(refreshBtn().getAttribute("aria-busy")).not.toBe("true");
    fireEvent.click(refreshBtn());
    expect(p.onRefresh).toHaveBeenCalledTimes(1);
  });

  it("refreshing_DisablesTheButton_SpinsIt_AndSaysRefreshingOnlyAfter250ms", () => {
    const { p } = renderHeader({ refresh: { ...IDLE, phase: "refreshing" } });
    const btn = refreshBtn() as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    expect(btn.getAttribute("aria-busy")).toBe("true");
    expect(screen.queryByText("refreshing…")).toBeNull();
    act(() => void vi.advanceTimersByTime(249));
    expect(screen.queryByText("refreshing…")).toBeNull();
    act(() => void vi.advanceTimersByTime(1));
    expect(screen.getByText("refreshing…")).toBeTruthy();
    expect(p.onRefresh).not.toHaveBeenCalled();
  });

  it("done_ReplacesTheCompactTimeAndTheTooltipWithTheRefreshTime", () => {
    renderHeader({ refresh: { ...IDLE, phase: "done", at: local(9, 58) } });
    const meta = screen.getByText(`${BOLT}9:58`);
    expect(meta.getAttribute("title")).toBe("Refreshed just this ticket at 9:58 · board synced 9:41");
    expect(screen.queryByText(`${BOLT}9:41`)).toBeNull();
  });

  it("notSyncedYet_ShowsPlainTextWithoutTheBolt", () => {
    const detail = clone(D1421);
    detail.syncedAt = 0;
    renderHeader({ detail });
    expect(screen.getByText("not synced yet")).toBeTruthy();
    expect(screen.queryByText(new RegExp(BOLT))).toBeNull();
  });

  it("failed_ShowsCouldntRefreshWithTheReason_InAlertText", () => {
    renderHeader({ refresh: { ...IDLE, phase: "failed", reason: "offline" } });
    const meta = screen.getByText("Couldn't refresh · offline");
    expect(meta.getAttribute("data-alert")).toBe("true");
    expect((refreshBtn() as HTMLButtonElement).disabled).toBe(false); // another explicit press is allowed
  });

  it("failedWithJirasReason_PrintsItAfterTheDot", () => {
    renderHeader({ refresh: { ...IDLE, phase: "failed", reason: "Jira returned 500." } });
    expect(screen.getByText("Couldn't refresh · Jira returned 500.")).toBeTruthy();
  });

  it("waiting_ShowsTheRateLimitWaitInWholeSeconds_AndDisablesTheButton", () => {
    renderHeader({ refresh: { ...IDLE, phase: "waiting", waitMs: 6000 } });
    expect(screen.getByText("waiting for Jira (rate limit) · 6s")).toBeTruthy();
    expect((refreshBtn() as HTMLButtonElement).disabled).toBe(true);
    expect(refreshBtn().getAttribute("aria-busy")).toBe("true");
  });

  it("cooldown_DisablesTheButtonAndShowsTheCountdown", () => {
    const { p } = renderHeader({ refresh: { ...IDLE, phase: "done", at: local(9, 58), cooldownMs: 8000 } });
    const btn = refreshBtn() as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    expect(btn.textContent).toContain("8s");
    expect(p.onRefresh).not.toHaveBeenCalled();
  });

  it("cooldownCountdownShowsEvenWhenTheButtonIsIconOnly", () => {
    renderHeader({ geometry: G1024, refresh: { ...IDLE, phase: "done", at: local(9, 58), cooldownMs: 3000 } });
    expect(refreshBtn().textContent).toContain("3s");
    expect((refreshBtn() as HTMLButtonElement).disabled).toBe(true);
  });

  it("noCountdownWhenTheCooldownIsOver", () => {
    renderHeader({ refresh: { ...IDLE, phase: "done", at: local(9, 58), cooldownMs: 0 } });
    expect(screen.getByText(`${BOLT}9:58`)).toBeTruthy();
    expect(refreshBtn().textContent).not.toMatch(/\d+s/);
    expect((refreshBtn() as HTMLButtonElement).disabled).toBe(false);
  });
});

describe("pulse row (row 3)", () => {
  it.each([
    ["1920x1080", G1920, 600],
    ["1440x900", G1440, 440],
    ["1280x720", G1280, 440],
    ["1024x640", G1024, 300],
  ])("drawsTheTrackAtTheSpecWidth_%s", (_name, geometry, width) => {
    renderHeader({ geometry });
    const track = screen.getByTestId("track");
    expect(track.getAttribute("data-width")).toBe(String(width));
    expect(track.getAttribute("data-days")).toBe("21");
  });

  it("showsTheLaneNameTheDaysChipAndTheLimit", () => {
    renderHeader();
    expect(screen.getByText(/in progress/i)).toBeTruthy();
    const chip = screen.getByText("3d");
    expect(chip.getAttribute("data-alert")).toBe("false");
    expect(screen.getByText("limit 5d · 21 days")).toBeTruthy();
    expect(screen.queryByText(/BLOCKED/)).toBeNull();
  });

  it("marksTheChipAlert_AndShowsTheBlockedLozenge_ForAFlaggedTicketOverItsLimit", () => {
    renderHeader({ detail: clone(D1429) });
    const chip = screen.getByText("6d");
    expect(chip.getAttribute("data-alert")).toBe("true");
    expect(screen.getByText("BLOCKED 3D")).toBeTruthy();
  });

  it("marksTheChipAlert_ForAnUnflaggedTicketOverItsLimit", () => {
    const detail = clone(D1421);
    detail.row.daysInLane = 6;
    detail.row.overLimit = true;
    renderHeader({ detail });
    expect(screen.getByText("6d").getAttribute("data-alert")).toBe("true");
    expect(screen.queryByText(/BLOCKED/)).toBeNull();
  });
});

describe("short mode", () => {
  it("mirrorsGeometryShortOnTheRoot", () => {
    const tall = renderHeader({ geometry: G1920 });
    expect((tall.container.firstElementChild as HTMLElement).getAttribute("data-short")).toBe("false");
    tall.unmount();
    const short = renderHeader({ geometry: G1280 });
    expect((short.container.firstElementChild as HTMLElement).getAttribute("data-short")).toBe("true");
    expect(within(short.container as HTMLElement).getByText("MOB-1421")).toBeTruthy();
  });
});
