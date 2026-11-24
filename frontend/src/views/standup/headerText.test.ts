// headerText: pure strings and width helpers for the standup header (ux-spec §2.1, jira-sync §4.7).
import { describe, expect, it } from "vitest";
import { headerCollapse } from "../../layout/responsive";
import { ageText, dateWidth, dayLabel, ordinal, rightClusterWidth, syncLabel } from "./headerText";

const MIN = 60_000;
const HOUR = 60 * MIN;

describe("dayLabel", () => {
  it.each([
    ["2026-11-24", "Tues, 24th of Nov"],
    ["2026-11-23", "Mon, 23rd of Nov"],
    ["2026-03-01", "Sun, 1st of Mar"],
    ["2026-11-26", "Thurs, 26th of Nov"],
    ["2026-11-25", "Wed, 25th of Nov"],
    ["2026-11-27", "Fri, 27th of Nov"],
    ["2026-11-28", "Sat, 28th of Nov"],
    ["2026-12-31", "Thurs, 31st of Dec"],
    ["2026-01-02", "Fri, 2nd of Jan"],
  ])("%s -> %s", (iso, text) => {
    expect(dayLabel(iso)).toBe(text);
  });

  it("is empty for a malformed date", () => {
    expect(dayLabel("")).toBe("");
    expect(dayLabel("24 Nov")).toBe("");
  });
});

describe("ordinal", () => {
  it("gets st, nd, rd and th right for 1..31", () => {
    const got = Array.from({ length: 31 }, (_, i) => ordinal(i + 1));
    expect(got).toEqual([
      "1st", "2nd", "3rd", "4th", "5th", "6th", "7th", "8th", "9th", "10th",
      "11th", "12th", "13th", "14th", "15th", "16th", "17th", "18th", "19th", "20th",
      "21st", "22nd", "23rd", "24th", "25th", "26th", "27th", "28th", "29th", "30th", "31st",
    ]);
  });
});

describe("ageText", () => {
  it("keeps minutes and days spaced and tightens hours", () => {
    expect(ageText(2 * MIN, 0)).toBe("2 min");
    expect(ageText(30_000, 0)).toBe("1 min");
    expect(ageText(HOUR, 0)).toBe("1h");
    expect(ageText(23 * HOUR, 0)).toBe("23h");
    expect(ageText(47 * HOUR, 0)).toBe("47h");
    expect(ageText(48 * HOUR, 0)).toBe("2 days");
  });
});

describe("syncLabel", () => {
  it("uses the tight hour form in the synced and offline strings", () => {
    expect(syncLabel(undefined, 1, 1 + 3 * HOUR).text).toBe("synced 3h ago");
    expect(syncLabel({ state: "failed", errorCode: "offline" } as never, 0, HOUR).text).toBe("offline · no data synced yet");
    expect(syncLabel({ state: "failed", errorCode: "offline" } as never, 1, HOUR + 1).text).toBe("offline · data from 1h ago");
  });
});

describe("header widths", () => {
  it("counts the date separately from the right cluster", () => {
    expect(dateWidth(headerCollapse(1920))).toBeGreaterThan(0);
    expect(dateWidth(headerCollapse(1359))).toBe(0);
    expect(rightClusterWidth(headerCollapse(1920))).toBeGreaterThan(rightClusterWidth(headerCollapse(1280)));
    expect(rightClusterWidth(headerCollapse(1280))).toBeGreaterThan(rightClusterWidth(headerCollapse(1279)));
  });
});
