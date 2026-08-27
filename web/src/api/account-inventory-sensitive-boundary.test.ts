import { beforeEach, describe, expect, it, vi } from "vitest";
import { generatedAccountInventoryApi } from "./account-inventory-api";

const instanceId = "00000000-0000-4000-8000-000000000731";

describe("account inventory browser sensitive transport boundary", () => {
  beforeEach(() => {
    window.history.replaceState({ fixed: true }, "", "/account-inventory");
    window.localStorage.clear();
    window.sessionStorage.clear();
    vi.unstubAllGlobals();
  });

  it("keeps email and cursor only in a no-store POST body with no redirect or browser persistence", async () => {
    const emailCanary = ["browser", "identity-canary-913f", "example.invalid"].join("@");
    const cursorCanary = "opaque-ciphertext-canary-913f";
    const response = new Response(JSON.stringify({ items: [], next_cursor: null }), {
      status: 200,
      headers: { "Content-Type": "application/json", "Cache-Control": "no-store" },
    });
    const fetchMock = vi.fn().mockResolvedValue(response);
    vi.stubGlobal("fetch", fetchMock);

    const page = await generatedAccountInventoryApi.query("csrf-proof", {
      instanceId,
      provider: "openai",
      email: emailCanary,
      cursor: cursorCanary,
      limit: 25,
    });
    expect(page).toEqual({ items: [], nextCursor: null });
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [input, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    const url = new URL(input, window.location.origin);
    expect(url.pathname).toBe("/api/account-inventory/query");
    expect(url.search).toBe("");
    expect(url.href).not.toContain(emailCanary);
    expect(url.href).not.toContain(cursorCanary);
    expect(init.method).toBe("POST");
    expect(init.cache).toBe("no-store");
    expect(init.credentials).toBe("same-origin");
    expect(init.redirect).toBeUndefined();
    expect(init.body).toEqual(expect.stringContaining(emailCanary));
    expect(init.body).toEqual(expect.stringContaining(cursorCanary));
    expect(response.redirected).toBe(false);
    expect(response.headers.get("Location")).toBeNull();

    const persistedBrowserState = JSON.stringify({
      href: window.location.href,
      history: window.history.state,
      localStorage: { ...window.localStorage },
      sessionStorage: { ...window.sessionStorage },
    });
    expect(persistedBrowserState).not.toContain(emailCanary);
    expect(persistedBrowserState).not.toContain(cursorCanary);
  });
});
