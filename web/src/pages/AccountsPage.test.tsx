import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, waitFor } from "@testing-library/react";
import { describe, expect, it, beforeEach, vi } from "vitest";
import { FrontendFoundationProvider } from "../foundation/FrontendFoundationProvider";
import { AssetApiError } from "../api/asset-types";
import { TopologyApiError } from "../api/topology-types";

const hookState = {
  nodes: { data: undefined as unknown, error: undefined as unknown },
  selectedNode: { data: undefined as unknown, error: undefined as unknown },
  providers: { data: undefined as unknown, error: undefined as unknown },
};
const auth = { session: { csrf_token: "csrf" }, clearSession: vi.fn() };

vi.mock("../auth/AuthContext", () => ({ useAuth: () => auth }));
vi.mock("../api/asset-hooks", () => ({
  useNodeAssets: () => hookState.nodes,
  useNodeAsset: () => hookState.selectedNode,
}));
vi.mock("../api/topology-hooks", () => ({ useTopologyProviders: () => hookState.providers }));
vi.mock("../components/AccountInventoryCapacity", () => ({ AccountInventoryCapacity: () => <div data-testid="capacity" /> }));
vi.mock("../components/AccountWorkspace", () => ({ AccountWorkspace: () => <div data-testid="workspace" /> }));

import AccountsPage from "./AccountsPage";

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const cancelQueries = vi.spyOn(client, "cancelQueries").mockResolvedValue();
  const clear = vi.spyOn(client, "clear");
  render(<FrontendFoundationProvider><QueryClientProvider client={client}><AccountsPage /></QueryClientProvider></FrontendFoundationProvider>);
  return { cancelQueries, clear };
}

describe("AccountsPage session boundary", () => {
  beforeEach(() => {
    hookState.nodes = { data: undefined, error: undefined };
    hookState.selectedNode = { data: undefined, error: undefined };
    hookState.providers = { data: undefined, error: undefined };
    auth.clearSession.mockClear();
    window.history.replaceState(null, "", "/accounts");
  });

  it.each([
    ["node list", () => { hookState.nodes.error = new AssetApiError(401); }],
    ["selected node", () => { hookState.selectedNode.error = new AssetApiError(401); }],
    ["provider state", () => { hookState.providers.error = new TopologyApiError(401); }],
  ])("expires the session for a %s 401", async (_name, setError) => {
    setError();
    const { cancelQueries, clear } = renderPage();
    await waitFor(() => expect(auth.clearSession).toHaveBeenCalledTimes(1));
    expect(cancelQueries).toHaveBeenCalledTimes(1);
    expect(clear).toHaveBeenCalledTimes(1);
  });
});
