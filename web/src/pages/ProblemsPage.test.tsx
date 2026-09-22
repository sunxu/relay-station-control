import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { FrontendFoundationProvider } from "../foundation/FrontendFoundationProvider";

const auth = { clearSession: vi.fn(), session: { csrf_token: "csrf" } };
const captured = { onUnauthorized: undefined as (() => void) | undefined };

vi.mock("../auth/AuthContext", () => ({ useAuth: () => auth }));
vi.mock("../api/problem-accounts-api", () => ({ generatedProblemAccountsApi: {} }));
vi.mock("./ProblemsView", () => ({
  ProblemsView: ({ onUnauthorized }: { onUnauthorized: () => void }) => {
    captured.onUnauthorized = onUnauthorized;
    return <div data-testid="problems-view" />;
  },
}));

import ProblemsPage from "./ProblemsPage";

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const cancelQueries = vi.spyOn(client, "cancelQueries").mockResolvedValue();
  const clear = vi.spyOn(client, "clear");
  render(<FrontendFoundationProvider initialLocale="zh-CN"><QueryClientProvider client={client}><ProblemsPage /></QueryClientProvider></FrontendFoundationProvider>);
  return { cancelQueries, clear };
}

describe("ProblemsPage session boundary", () => {
  beforeEach(() => {
    auth.clearSession.mockClear();
    captured.onUnauthorized = undefined;
  });

  it("cancels reads, clears the authenticated cache, and clears the session on 401", async () => {
    const { cancelQueries, clear } = renderPage();
    expect(await screen.findByTestId("problems-page")).toBeInTheDocument();
    captured.onUnauthorized?.();
    await waitFor(() => expect(auth.clearSession).toHaveBeenCalledTimes(1));
    expect(cancelQueries).toHaveBeenCalledTimes(1);
    expect(clear).toHaveBeenCalledTimes(1);
  });
});
