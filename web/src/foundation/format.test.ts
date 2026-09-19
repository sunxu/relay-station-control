import { describe, expect, it } from "vitest";
import { formatDateTime, formatNumber, formatPercent } from "./format";

describe("locale-aware formatting", () => {
  it("returns a placeholder for missing or invalid date values", () => {
    expect(formatDateTime(undefined, "en")).toBe("—");
    expect(formatDateTime(null, "zh-CN")).toBe("—");
    expect(formatDateTime("not-a-date", "en")).toBe("—");
  });

  it("formats the same instant in the system timezone for each locale", () => {
    const timestamp = "2026-09-08T18:40:40.000Z";
    const expected = new Intl.DateTimeFormat("en", {
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
      hour12: false,
    }).format(new Date(timestamp));

    expect(formatDateTime(timestamp, "en")).toBe(expected);
    expect(formatDateTime(timestamp, "zh-CN")).not.toBe("");
  });

  it("formats numbers and percentages with locale-aware Intl formatting", () => {
    expect(formatNumber(1234567.89, "en")).toBe("1,234,567.89");
    expect(formatNumber(1234567.89, "zh-CN")).toBe("1,234,567.89");
    expect(formatPercent(0.125, "en")).toBe("12.5%");
    expect(formatPercent(0.125, "zh-CN")).toBe("12.5%");
    expect(formatNumber(undefined, "en")).toBe("—");
    expect(formatPercent(null, "en")).toBe("—");
  });
});
