import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { FrontendFoundationProvider } from "../foundation/FrontendFoundationProvider";

const auth = { clearSession: vi.fn(), session: { csrf_token: "csrf" } };
const captured = { onUnauthorized: undefined as (() => void) | undefined };

vi.mock("../auth/AuthContext", () => ({ useAuth: () => auth }));
vi.mock("../api/job-api", () => ({ generatedJobApi: {} }));
vi.mock("./JobRegistryView", () => ({
  JobRegistryView: ({ onUnauthorized }: { onUnauthorized: () => void }) => {
    captured.onUnauthorized = onUnauthorized;
    return <div data-testid="durable-jobs-view" />;
  },
}));

import OperationsPage from "./OperationsPage";

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const cancelQueries = vi.spyOn(client, "cancelQueries").mockResolvedValue();
  const clear = vi.spyOn(client, "clear");
  render(<FrontendFoundationProvider initialLocale="zh-CN"><QueryClientProvider client={client}><OperationsPage /></QueryClientProvider></FrontendFoundationProvider>);
  return { cancelQueries, clear };
}

describe("OperationsPage", () => {
  it("uses the canonical Operations shell and expires the authenticated cache on 401", async () => {
    const { cancelQueries, clear } = renderPage();
    expect(await screen.findByTestId("operations-page")).toBeInTheDocument();
    expect(screen.getByTestId("durable-jobs-view")).toBeInTheDocument();
    captured.onUnauthorized?.();
    await waitFor(() => expect(auth.clearSession).toHaveBeenCalledTimes(1));
    expect(cancelQueries).toHaveBeenCalledTimes(1);
    expect(clear).toHaveBeenCalledTimes(1);
  });
});
