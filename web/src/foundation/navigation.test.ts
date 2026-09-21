import { describe, expect, it } from "vitest";
import { authenticatedRouteFromPath, primaryNavigation, searchNavigation, navigationPath } from "./navigation";

describe("foundation navigation model", () => {
  it("defines the seven primary entries with stable routes and test ids", () => {
    expect(primaryNavigation.map(({ path, testId }) => [path, testId])).toEqual([
      ["/", "sidebar-dashboard"],
      ["/accounts", "sidebar-accounts"],
      ["/nodes", "sidebar-nodes"],
      ["/operations", "sidebar-operations"],
      ["/monitoring", "sidebar-monitoring"],
      ["/problems", "sidebar-problems"],
      ["/settings", "sidebar-settings"],
    ]);
    expect(searchNavigation.some((entry) => entry.path === "/assets")).toBe(true);
    expect(searchNavigation.map((entry) => entry.path)).not.toContain("/jobs");
    expect(searchNavigation.map((entry) => entry.path)).not.toContain("/topology");
  });

  it.each([
    ["/", "dashboard"],
    ["/accounts/", "accounts"],
    ["/nodes", "nodes"],
    ["/operations/", "operations"],
    ["/monitoring", "monitoring"],
    ["/problems/", "problems"],
    ["/settings", "settings"],
    ["/assets/", "assets"],
    ["/jobs", "jobs"],
    ["/topology", "topology"],
  ] as const)("normalizes %s to %s", (path, route) => {
    expect(authenticatedRouteFromPath(path)).toBe(route);
  });

  it("maps every authenticated route back to its canonical path", () => {
    expect(navigationPath("dashboard")).toBe("/");
    expect(navigationPath("settings")).toBe("/settings");
    expect(navigationPath("management")).toBe("/settings");
  });
});
