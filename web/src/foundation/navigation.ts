import type { TranslationKey } from "./resources";

export type NavigationRoute = "dashboard" | "accounts" | "nodes" | "operations" | "monitoring" | "problems" | "settings";
export type LegacyRoute = "assets" | "jobs" | "topology";
export type AuthenticatedRoute = NavigationRoute | LegacyRoute | "management";

export type NavigationEntry = {
  id: string;
  route: AuthenticatedRoute;
  path: string;
  labelKey: TranslationKey;
  testId: string;
};

export const primaryNavigation = [
  { id: "dashboard", route: "dashboard", path: "/", labelKey: "navigation.dashboard", testId: "sidebar-dashboard" },
  { id: "accounts", route: "accounts", path: "/accounts", labelKey: "navigation.accounts", testId: "sidebar-accounts" },
  { id: "nodes", route: "nodes", path: "/nodes", labelKey: "navigation.nodes", testId: "sidebar-nodes" },
  { id: "operations", route: "operations", path: "/operations", labelKey: "navigation.operations", testId: "sidebar-operations" },
  { id: "monitoring", route: "monitoring", path: "/monitoring", labelKey: "navigation.monitoring", testId: "sidebar-monitoring" },
  { id: "problems", route: "problems", path: "/problems", labelKey: "navigation.problems", testId: "sidebar-problems" },
  { id: "settings", route: "settings", path: "/settings", labelKey: "navigation.settings", testId: "sidebar-settings" },
] as const satisfies readonly NavigationEntry[];

export const searchNavigation = [
  ...primaryNavigation,
  { id: "assets", route: "assets", path: "/assets", labelKey: "navigation.assets", testId: "search-result-assets" },
] as const satisfies readonly NavigationEntry[];

function normalizedPath(pathname: string): string {
  if (pathname.length > 1 && pathname.endsWith("/")) return pathname.slice(0, -1);
  return pathname || "/";
}

export function authenticatedRouteFromPath(pathname: string): AuthenticatedRoute {
  const path = normalizedPath(pathname);
  if (path === "/assets") return "assets";
  if (path === "/jobs") return "jobs";
  if (path === "/topology") return "topology";
  const entry = primaryNavigation.find((item) => item.path === path);
  return entry?.route ?? "dashboard";
}

export function navigationPath(route: AuthenticatedRoute): string {
  if (route === "management") return "/settings";
  if (route === "assets" || route === "jobs" || route === "topology") return `/${route}`;
  return primaryNavigation.find((item) => item.route === route)?.path ?? "/";
}

export function isAuthenticatedRoute(route: string): route is AuthenticatedRoute {
  return route === "management" || route === "assets" || route === "jobs" || route === "topology" || primaryNavigation.some((item) => item.route === route);
}
