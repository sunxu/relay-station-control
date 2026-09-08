import { afterEach, describe, expect, it, vi } from "vitest";
import { generatedTopologyApi } from "./topology-api";

const instanceId = "00000000-0000-4000-8000-000000000731";

function captureRequests() {
  const fetchMock = vi.fn(async () => new Response(JSON.stringify({
    instance_id: instanceId, window: "15m", items: [], next_cursor: null,
  }), { status: 200, headers: { "Content-Type": "application/json" } }));
  vi.stubGlobal("fetch", fetchMock);
  const request = (index: number) => {
    const [url, init] = fetchMock.mock.calls[index] as unknown as [string, RequestInit];
    return { url, init, body: JSON.parse(String(init.body)) };
  };
  return { fetchMock, request };
}

afterEach(() => vi.unstubAllGlobals());

describe("unified account list HTTP serialization", () => {
  it.each([25, 50])("preserves explicit present then omits cleared lifecycle at page size %i", async (limit) => {
    const { request } = captureRequests();
    await generatedTopologyApi.accountList(instanceId, { lifecycle: "present", limit }, "csrf-proof");
    expect(request(0).body.lifecycle).toBe("present");
    await generatedTopologyApi.accountList(instanceId, { lifecycle: "missing", limit }, "csrf-proof");
    expect(request(1).body.lifecycle).toBe("missing");
    await generatedTopologyApi.accountList(instanceId, { lifecycle: undefined, limit }, "csrf-proof");
    expect(request(2).body).toEqual({ window: "15m", limit });
    for (const key of ["provider", "lifecycle", "basic_status", "quality", "email", "cursor"]) {
      expect(request(2).body).not.toHaveProperty(key);
    }
  });

  it("sends selected filters unchanged only in the protected POST body", async () => {
    const { fetchMock, request } = captureRequests();
    const controller = new AbortController();
    await generatedTopologyApi.accountList(instanceId, {
      window: "1h", provider: "openai", lifecycle: "missing", basicStatus: "disabled",
      quality: "bad", email: "transport@example.invalid", cursor: "opaque-cursor", limit: 25,
    }, "csrf-proof", controller.signal);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const { url, init, body } = request(0);
    expect(url).toBe(`/api/topology/nodes/${instanceId}/account-quality/query`);
    expect(body).toEqual({ window: "1h", provider: "openai", lifecycle: "missing",
      basic_status: "disabled", quality: "bad", email: "transport@example.invalid",
      cursor: "opaque-cursor", limit: 25 });
    expect(init.method).toBe("POST");
    expect(init.cache).toBe("no-store");
    expect(init.credentials).toBe("same-origin");
    expect(init.signal).toBe(controller.signal);
    expect(new Headers(init.headers).get("X-CSRF-Token")).toBe("csrf-proof");
  });
});
