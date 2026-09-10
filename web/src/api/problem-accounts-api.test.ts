import { beforeEach, afterEach, describe, expect, it, vi } from "vitest";
import { generatedProblemAccountsApi } from "./problem-accounts-api";

const response = {
  items: [],
  next_cursor: null,
};

beforeEach(() => vi.stubGlobal("fetch", vi.fn()));
afterEach(() => vi.unstubAllGlobals());

function fetchMock() {
  return vi.mocked(fetch);
}

describe("problem accounts API adapter", () => {
  it("normalizes optional filters and sends POST security options", async () => {
    fetchMock().mockResolvedValue(new Response(JSON.stringify(response), { status: 200 }));
    const signal = new AbortController().signal;

    await generatedProblemAccountsApi.query({
      provider: "  antigravity  ",
      node: " 00000000-0000-4000-8000-000000000001 ",
      severity: "Critical",
      reason: "token_invalid",
      email: "  Operator@Example.INVALID ",
      limit: 25,
      cursor: "opaque-cursor",
    }, "csrf-proof", signal);

    expect(fetchMock()).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock().mock.calls[0]!;
    expect(url).toBe("/api/problem-accounts/query");
    expect(init).toMatchObject({ method: "POST", cache: "no-store", credentials: "same-origin", signal });
    expect(init?.headers).toMatchObject({ "Content-Type": "application/json", "X-CSRF-Token": "csrf-proof" });
    expect(JSON.parse(String(init?.body))).toEqual({
      provider: "antigravity", node: "00000000-0000-4000-8000-000000000001", severity: "Critical",
      reason: "token_invalid", email: "operator@example.invalid", limit: 25, cursor: "opaque-cursor",
    });
  });

  it("omits empty optional filters", async () => {
    fetchMock().mockResolvedValue(new Response(JSON.stringify(response), { status: 200 }));

    await generatedProblemAccountsApi.query({ provider: " ", node: "", email: "  ", limit: 25 }, "csrf-proof");

    expect(JSON.parse(String(fetchMock().mock.calls[0]?.[1]?.body))).toEqual({ limit: 25 });
  });

  it("turns every non-2xx response into an error carrying status", async () => {
    fetchMock().mockResolvedValue(new Response(JSON.stringify({ code: "unauthorized", message: "fixed", request_id: "request-fixed" }), { status: 401 }));

    await expect(generatedProblemAccountsApi.query({}, "csrf-proof")).rejects.toMatchObject({
      name: "ProblemAccountsApiError",
      status: 401,
      detail: { code: "unauthorized", request_id: "request-fixed" },
    });
  });

  it("keeps status when an error body is malformed", async () => {
    fetchMock().mockResolvedValue(new Response(JSON.stringify({ unexpected: true }), { status: 503 }));

    await expect(generatedProblemAccountsApi.query({}, "csrf-proof")).rejects.toMatchObject({ status: 503 });
  });

  it.each([400, 401, 403, 503])("preserves HTTP status %s", async (status) => {
    fetchMock().mockResolvedValue(new Response(JSON.stringify({ code: "fixed", message: "fixed", request_id: "request-fixed" }), { status }));
    await expect(generatedProblemAccountsApi.query({}, "csrf-proof")).rejects.toMatchObject({ status });
  });

  it("surfaces network rejection without making another request", async () => {
    fetchMock().mockRejectedValue(new TypeError("network unavailable"));
    await expect(generatedProblemAccountsApi.query({}, "csrf-proof")).rejects.toThrow("network unavailable");
    expect(fetchMock()).toHaveBeenCalledTimes(1);
  });
});
