import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { AccountInventoryView } from "./AccountInventoryView";
import { AccountInventoryApiError } from "../api/account-inventory-types";
import type { AccountInventoryApi, AccountInventoryPollCapacity } from "../api/account-inventory-types";
import type { AssetApi } from "../api/asset-types";

const capacity: AccountInventoryPollCapacity = {
  status: "ready", enabled: true, eligibleNodeCount: 9, effectiveCapacity: 8, concurrency: 4,
  requestTimeoutMs: 1000, finalizeTimeoutMs: 2000, lifecycleTimeoutMs: 3000, claimTimeoutMs: 400,
  dispatchMarginMs: 100, pollStartGraceMs: 500, evaluatedSlot: "2026-09-08T01:00:00Z", evaluatedAt: "2026-09-08T01:00:01Z",
};
const exceeded: AccountInventoryPollCapacity = { ...capacity, status: "capacity_exceeded" };

function wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}>{children}</QueryClientProvider>;
}

function assets(): AssetApi {
  return { nodes: vi.fn().mockResolvedValue({ items: [], nextCursor: null }) } as unknown as AssetApi;
}

function renderCapacity(api: AccountInventoryApi, onUnauthorized = vi.fn()) {
  return render(<AccountInventoryView api={api} assetApi={assets()} csrfToken="csrf" onUnauthorized={onUnauthorized} />, { wrapper });
}

describe("account inventory poll capacity", () => {
  it("does not query accounts and shows loading then the 9-of-8 diagnostic", async () => {
    let resolve!: (value: AccountInventoryPollCapacity) => void;
    const pending = new Promise<AccountInventoryPollCapacity>((r) => { resolve = r; });
    const query = vi.fn();
    const api: AccountInventoryApi = { query, capacity: vi.fn().mockReturnValue(pending) };
    renderCapacity(api);
    fireEvent.click(screen.getByRole("button", { name: "刷新容量" }));
    expect(screen.getByTestId("account-inventory-capacity")).toHaveTextContent("刷新容量");
    resolve(exceeded);
    expect(await screen.findByText("容量不足")).toBeInTheDocument();
    expect(screen.getByText("符合采集条件的 Node：9")).toBeInTheDocument();
    expect(screen.getByText("有效容量：8")).toBeInTheDocument();
    expect(screen.getByText("监控规模超过采集容量，整轮新采集暂停")).toBeInTheDocument();
    expect(screen.getByText(/请调整采集并发/)).toBeInTheDocument();
    expect(query).not.toHaveBeenCalled();
  });

  it("renders ready and disabled independently from Node selection", async () => {
    const api: AccountInventoryApi = { query: vi.fn(), capacity: vi.fn().mockResolvedValueOnce(capacity).mockResolvedValueOnce({ ...capacity, status: "disabled", enabled: false }) };
    renderCapacity(api);
    fireEvent.click(screen.getByRole("button", { name: "刷新容量" }));
    expect(await screen.findByText("可用")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "刷新容量" }));
    expect(await screen.findByText("已禁用")).toBeInTheDocument();
  });

  it("hides the old result after a 503 and recovers on explicit refresh", async () => {
    const api: AccountInventoryApi = { query: vi.fn(), capacity: vi.fn()
      .mockRejectedValueOnce(new AccountInventoryApiError(503, { code: "temporarily_unavailable", message: "raw", request_id: "r" }))
      .mockResolvedValueOnce(capacity) };
    renderCapacity(api);
    fireEvent.click(screen.getByRole("button", { name: "刷新容量" }));
    expect(await screen.findByText("容量诊断暂不可用")).toBeInTheDocument();
    expect(screen.queryByText("可用")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "刷新容量" }));
    expect(await screen.findByText("可用")).toBeInTheDocument();
  });

  it("hands 401 to authentication and does not expose raw error details", async () => {
    const onUnauthorized = vi.fn();
    const api: AccountInventoryApi = { query: vi.fn(), capacity: vi.fn().mockRejectedValue(new AccountInventoryApiError(401, { code: "unauthorized", message: "secret-canary", request_id: "r" })) };
    renderCapacity(api, onUnauthorized);
    fireEvent.click(screen.getByRole("button", { name: "刷新容量" }));
    await waitFor(() => expect(onUnauthorized).toHaveBeenCalledTimes(1));
    expect(screen.getByText("容量诊断暂不可用")).toBeInTheDocument();
    expect(document.body.textContent).not.toContain("secret-canary");
  });
});
