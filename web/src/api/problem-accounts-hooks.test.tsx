import { renderHook, act, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ProblemAccountResponse } from "./problem-accounts-types";
import { useProblemAccountsQuery } from "./problem-accounts-hooks";
import type { ProblemAccountsApi } from "./problem-accounts-types";

function wrapper({ children }: { children: React.ReactNode }) {
  const client = new QueryClient();
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

const page: ProblemAccountResponse = { items: [], next_cursor: null };

describe("useProblemAccountsQuery", () => {
  it("aborts the active request before starting a new one", async () => {
    const deferred: Array<{ signal?: AbortSignal; resolve: (value: ProblemAccountResponse) => void }> = [];
    const api: ProblemAccountsApi = {
      query: vi.fn((_request, _csrf, signal) => new Promise<ProblemAccountResponse>((resolve) => deferred.push({ signal, resolve }))),
    };
    const { result } = renderHook(() => useProblemAccountsQuery(api, "csrf-proof"), { wrapper });
    const first = { limit: 25 };
    const second = { limit: 50 };

    act(() => result.current.mutate(first));
    await waitFor(() => expect(deferred).toHaveLength(1));
    act(() => result.current.mutate(second));

    await waitFor(() => expect(deferred).toHaveLength(2));
    expect(deferred).toHaveLength(2);
    expect(deferred[0]!.signal?.aborted).toBe(true);
    expect(deferred[1]!.signal?.aborted).toBe(false);
    act(() => deferred[1]!.resolve(page));
    await waitFor(() => expect(result.current.data).toEqual(page));
  });

  it("creates controllers at mutate time, so same-tick reset and replacement are safe", async () => {
    const signals: AbortSignal[] = [];
    const api: ProblemAccountsApi = {
      query: vi.fn((_request, _csrf, signal) => {
        signals.push(signal!);
        return new Promise<ProblemAccountResponse>(() => undefined);
      }),
    };
    const { result } = renderHook(() => useProblemAccountsQuery(api, "csrf-proof"), { wrapper });

    act(() => {
      result.current.mutate({ limit: 25 });
      result.current.mutate({ limit: 50 });
    });
    await waitFor(() => expect(api.query).toHaveBeenCalledTimes(1));

    expect(signals).toHaveLength(1);
    expect(signals[0]!.aborted).toBe(false);
    act(() => result.current.reset());
    expect(signals[0]!.aborted).toBe(true);
  });

  it("does not send a request when same-tick mutate is reset or unmounted", async () => {
    const api: ProblemAccountsApi = { query: vi.fn(() => Promise.resolve(page)) };
    const resetHook = renderHook(() => useProblemAccountsQuery(api, "csrf-proof"), { wrapper });
    act(() => {
      resetHook.result.current.mutate({ limit: 25 });
      resetHook.result.current.reset();
    });
    resetHook.unmount();

    const unmountHook = renderHook(() => useProblemAccountsQuery(api, "csrf-proof"), { wrapper });
    act(() => unmountHook.result.current.mutate({ limit: 25 }));
    unmountHook.unmount();
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(api.query).not.toHaveBeenCalled();
  });

  it("aborts an active request on unmount without reset", async () => {
    let signal: AbortSignal | undefined;
    const api: ProblemAccountsApi = {
      query: vi.fn((_request, _csrf, nextSignal) => {
        signal = nextSignal;
        return new Promise<ProblemAccountResponse>(() => undefined);
      }),
    };
    const { result, unmount } = renderHook(() => useProblemAccountsQuery(api, "csrf-proof"), { wrapper });
    act(() => result.current.mutate({ limit: 25 }));
    await waitFor(() => expect(api.query).toHaveBeenCalledTimes(1));
    unmount();
    expect(signal?.aborted).toBe(true);
  });

  it("does not retry a rejected request", async () => {
    const api: ProblemAccountsApi = { query: vi.fn(() => Promise.reject(new Error("network"))) };
    const { result } = renderHook(() => useProblemAccountsQuery(api, "csrf-proof"), { wrapper });
    act(() => result.current.mutate({ limit: 25 }));
    await waitFor(() => expect(result.current.error).toBeInstanceOf(Error));
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(api.query).toHaveBeenCalledTimes(1);
  });

  it("ignores a late success after reset", async () => {
    let resolveRequest!: (value: ProblemAccountResponse) => void;
    const api: ProblemAccountsApi = {
      query: vi.fn(() => new Promise<ProblemAccountResponse>((resolve) => { resolveRequest = resolve; })),
    };
    const { result } = renderHook(() => useProblemAccountsQuery(api, "csrf-proof"), { wrapper });
    act(() => result.current.mutate({ limit: 25 }));
    await waitFor(() => expect(api.query).toHaveBeenCalledTimes(1));
    act(() => result.current.reset());
    act(() => resolveRequest(page));
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(result.current.data).toBeUndefined();
  });

  it("does not let an older response replace the latest mutation result", async () => {
    const deferred: Array<(value: ProblemAccountResponse) => void> = [];
    const api: ProblemAccountsApi = {
      query: vi.fn(() => new Promise<ProblemAccountResponse>((resolve) => deferred.push(resolve))),
    };
    const { result } = renderHook(() => useProblemAccountsQuery(api, "csrf-proof"), { wrapper });
    const oldPage = { items: [], next_cursor: "old" };
    const newPage = { items: [], next_cursor: "new" };

    act(() => result.current.mutate({ limit: 25 }));
    await waitFor(() => expect(deferred).toHaveLength(1));
    act(() => result.current.mutate({ limit: 50 }));
    await waitFor(() => expect(deferred).toHaveLength(2));
    act(() => deferred[1]!(newPage));
    await waitFor(() => expect(result.current.data).toEqual(newPage));
    act(() => deferred[0]!(oldPage));
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(result.current.data).toEqual(newPage);
  });

  it("aborts on reset and unmount, and exposes no automatic retry", async () => {
    let signal: AbortSignal | undefined;
    const api: ProblemAccountsApi = {
      query: vi.fn((_request, _csrf, nextSignal) => {
        signal = nextSignal;
        return new Promise<ProblemAccountResponse>(() => undefined);
      }),
    };
    const { result, unmount } = renderHook(() => useProblemAccountsQuery(api, "csrf-proof"), { wrapper });
    act(() => result.current.mutate({ limit: 25 }));
    await waitFor(() => expect(api.query).toHaveBeenCalledTimes(1));
    act(() => result.current.reset());
    expect(signal?.aborted).toBe(true);
    expect(api.query).toHaveBeenCalledTimes(1);
    unmount();
    expect(signal?.aborted).toBe(true);
  });
});
