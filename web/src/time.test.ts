import { describe, expect, it } from "vitest";
import { formatDateTime } from "./time";

describe("formatDateTime", () => {
  it("formats in the system timezone with seconds", () => {
    const zone = new Intl.DateTimeFormat().resolvedOptions().timeZone;
    const local = new Date(2026, 8, 8, 18, 40, 40, 987);
    expect(formatDateTime(local.toISOString())).toBe("2026-09-08 18:40:40");
    expect(formatDateTime("2026-09-08T18:40:40.987Z")).toMatch(/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/);
    expect(zone).toEqual(expect.any(String));
  });

  it("follows local calendar boundaries and seasonal offsets", () => {
    const zone = new Intl.DateTimeFormat().resolvedOptions().timeZone;
    const summer = formatDateTime("2026-07-01T00:00:00Z");
    const winter = formatDateTime("2026-01-01T00:00:00Z");
    expect(summer).toMatch(/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/);
    expect(winter).toMatch(/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/);
    if (zone === "Asia/Shanghai") {
      expect(summer).toBe("2026-07-01 08:00:00");
      expect(winter).toBe("2026-01-01 08:00:00");
    }
    if (zone === "America/New_York") {
      expect(summer).toBe("2026-06-30 20:00:00");
      expect(winter).toBe("2025-12-31 19:00:00");
    }
  });

  it("returns a placeholder for missing or invalid values", () => {
    expect(formatDateTime()).toBe("—");
    expect(formatDateTime(null)).toBe("—");
    expect(formatDateTime("not-a-date")).toBe("—");
  });
});
