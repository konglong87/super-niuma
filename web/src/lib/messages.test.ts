import { describe, expect, it } from "vitest";
import { formatDuration, isReasonableReportedDurationMs, MAX_REASONABLE_REPORTED_DURATION_MS } from "./messages";

const SECOND = 1000;
const MINUTE = 60 * SECOND;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

describe("message duration formatting", () => {
  it("uses compact seconds, minutes, hours, and days", () => {
    expect(formatDuration(15 * SECOND)).toBe("15s");
    expect(formatDuration(75 * SECOND)).toBe("1m 15s");
    expect(formatDuration(HOUR + 2 * MINUTE)).toBe("1h 02m");
    expect(formatDuration(2 * DAY + 3 * HOUR)).toBe("2d 03h");
  });

  it("does not expose malformed or unbounded durations as long minute strings", () => {
    expect(isReasonableReportedDurationMs(MAX_REASONABLE_REPORTED_DURATION_MS)).toBe(true);
    expect(isReasonableReportedDurationMs(MAX_REASONABLE_REPORTED_DURATION_MS + 1)).toBe(false);
    expect(formatDuration(MAX_REASONABLE_REPORTED_DURATION_MS + SECOND)).toBe(">7d");
    expect(formatDuration(Number.NaN)).toBe("—");
    expect(formatDuration(-1)).toBe("—");
  });
});
