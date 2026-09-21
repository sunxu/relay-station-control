import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { FrontendFoundationProvider } from "../foundation/FrontendFoundationProvider";
import { DashboardApiError } from "../api/dashboard-api";
import DashboardPage from "./DashboardPage";

const navigate = vi.fn();
const clearSession = vi.fn();

vi.mock("../auth/AuthContext", () => ({
  useAuth: () => ({ navigate, clearSession }),
}));

function makeApi(overrides: Partial<Parameters<typeof DashboardPage>[0]["api"]> = {}) {
  return {
    health: vi.fn().mockResolvedValue({ status: "ok" as const, version: "0.9.3" }),
    gatewayCounts: vi.fn().mockResolvedValue({ active: 30, retired: 7, total: 37 }),
    nodeCounts: vi.fn().mockResolvedValue({ active: 15, retired: 4, total: 19 }),
    pollCapacity: vi.fn().mockResolvedValue({ status: "disabled" as const, enabled: false, eligibleNodeCount: 0, effectiveCapacity: 0, evaluatedSlot: "slot-1", evaluatedAt: "2026-09-21T00:00:00Z" }),
    ...overrides,
  };
}

function renderDashboard(api: Parameters<typeof DashboardPage>[0]["api"], locale: "zh-CN" | "en" = "zh-CN") {
  return render(<FrontendFoundationProvider initialLocale={locale}><DashboardPage api={api} /></FrontendFoundationProvider>);
}

describe("DashboardPage", () => {
  it("renders authoritative counts, disabled capacity, and navigation-only entries", async () => {
    const api = makeApi();
    renderDashboard(api);
    await waitFor(() => expect(screen.getByTestId("dashboard-gateway-summary")).toHaveTextContent("37"));
    expect(screen.getByTestId("dashboard-node-summary")).toHaveTextContent("19");
    expect(screen.getByTestId("dashboard-poll-capacity-summary")).toHaveTextContent("已禁用");
    fireEvent.click(screen.getByTestId("dashboard-nav-accounts"));
    fireEvent.click(screen.getByTestId("dashboard-nav-operations"));
    fireEvent.click(screen.getByTestId("dashboard-nav-problems"));
    expect(navigate).toHaveBeenNthCalledWith(1, "accounts");
    expect(navigate).toHaveBeenNthCalledWith(2, "operations");
    expect(navigate).toHaveBeenNthCalledWith(3, "problems");
  });

  it("isolates a failed source and retries only that source", async () => {
    const gateway = vi.fn().mockRejectedValueOnce(new Error("unavailable")).mockResolvedValue({ active: 0, retired: 0, total: 0 });
    const api = makeApi({ gatewayCounts: gateway });
    renderDashboard(api, "en");
    await waitFor(() => expect(screen.getByTestId("dashboard-gateway-summary")).toHaveTextContent("Unavailable"));
    expect(screen.getByTestId("dashboard-control-summary")).toHaveTextContent("Available");
    expect(screen.getByTestId("dashboard-node-summary")).toHaveTextContent("19");
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    await waitFor(() => expect(screen.getByTestId("dashboard-gateway-summary")).toHaveTextContent("0"));
    expect(gateway).toHaveBeenCalledTimes(2);
  });

  it("clears the session on an unauthorized source response", async () => {
    const api = makeApi({ health: vi.fn().mockRejectedValue(new DashboardApiError(401)) });
    renderDashboard(api);
    await waitFor(() => expect(clearSession).toHaveBeenCalledOnce());
  });
});
