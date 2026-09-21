import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { FrontendFoundationProvider } from "../foundation/FrontendFoundationProvider";
import type { TopologyApi } from "../api/topology-types";
import { TopologyApiError } from "../api/topology-types";
import { AccountWorkspace } from "./AccountWorkspace";

const A = "11111111-1111-4111-8111-111111111111";
const B = "22222222-2222-4222-8222-222222222222";

function apiFixture() {
  const api = {
    accountList: vi.fn().mockResolvedValue({ instance_id: A, window: "15m", items: [], next_cursor: null }),
    incidents: vi.fn().mockResolvedValue({ items: [], next_cursor: null }),
    requestHistory: vi.fn().mockResolvedValue({ items: [], next_cursor: null }),
    accountAvailabilityOccurrences: vi.fn().mockResolvedValue({ items: [], next_cursor: null }),
  } as unknown as TopologyApi;
  return api;
}

function renderWorkspace(api: TopologyApi, instanceId?: string) {
  return render(
    <FrontendFoundationProvider>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <AccountWorkspace api={api} instanceId={instanceId} csrfToken="csrf" providers={[]} providerError={false} onUnauthorized={vi.fn()} />
      </QueryClientProvider>
    </FrontendFoundationProvider>,
  );
}

describe("AccountWorkspace", () => {
  it("does not query without a selected Node", async () => {
    const api = apiFixture();
    renderWorkspace(api);
    await act(async () => { await Promise.resolve(); });
    expect(api.accountList).not.toHaveBeenCalled();
    expect(screen.getByTestId("accounts-no-node")).toBeInTheDocument();
  });

  it("queries the selected Node and resets stale account state when the context changes", async () => {
    const api = apiFixture();
    let resolveA!: (value: { instance_id: string; window: "15m"; items: never[]; next_cursor: null }) => void;
    api.accountList = vi.fn((instanceId: string) => instanceId === A
      ? new Promise((resolve) => { resolveA = resolve; })
      : Promise.resolve({ instance_id: B, window: "15m" as const, items: [], next_cursor: null })) as never;
    const view = renderWorkspace(api, A);
    await waitFor(() => expect(api.accountList).toHaveBeenCalledWith(A, expect.objectContaining({ cursor: undefined }), "csrf", expect.any(AbortSignal)));
    view.rerender(
      <FrontendFoundationProvider>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <AccountWorkspace api={api} instanceId={B} csrfToken="csrf" providers={[]} providerError={false} onUnauthorized={vi.fn()} />
        </QueryClientProvider>
      </FrontendFoundationProvider>,
    );
    await waitFor(() => expect(api.accountList).toHaveBeenCalledWith(B, expect.objectContaining({ cursor: undefined }), "csrf", expect.any(AbortSignal)));
    await act(async () => { resolveA({ instance_id: A, window: "15m", items: [], next_cursor: null }); });
    expect(screen.getByTestId("accounts-no-results")).toBeInTheDocument();
  });

  it("hands an account query 401 to the shared session boundary", async () => {
    const api = apiFixture();
    api.accountList = vi.fn().mockRejectedValue(new TopologyApiError(401)) as never;
    const onUnauthorized = vi.fn();
    render(<FrontendFoundationProvider><QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><AccountWorkspace api={api} instanceId={A} csrfToken="csrf" providers={[]} providerError={false} onUnauthorized={onUnauthorized} /></QueryClientProvider></FrontendFoundationProvider>);
    await waitFor(() => expect(onUnauthorized).toHaveBeenCalledTimes(1));
  });
});
