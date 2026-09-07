import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { bindRelayNode, getGatewayAccountRelayBindings, rebindRelayNode } from "./generated/control";
import type { GatewayAccountRelayBindingsResponse } from "./generated/control";

const node = "11111111-1111-4111-8111-111111111111";
const gateway = "22222222-2222-4222-8222-222222222222";
const ids = ["9007199254740991", "9007199254740992", "9007199254740993", "9223372036854775807"];

afterEach(() => vi.unstubAllGlobals());

it("round trips four decimal int64 IDs through generated reads, React string state, bind and rebind JSON", async () => {
  const requests: Array<{ method: string; body?: unknown }> = [];
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    requests.push({ method: init?.method ?? "GET", body: init?.body ? JSON.parse(String(init.body)) : undefined });
    if (url.includes("/gateways/")) return new Response(JSON.stringify({ gateway_instance_id: gateway, directory_freshness: "fresh", observed_at: "2026-09-07T00:00:00Z", accounts: ids.map((account_id) => ({ gateway_account_id: account_id, account_context: { account_id, name: "fixture", platform: "openai", type: "oauth", status: "active" }, resolution: "unbound", context_source: "current_directory" })) }), { status: 200, headers: { "content-type": "application/json" } });
    return new Response(JSON.stringify({ outcome: "success", operation_at: "2026-09-07T00:00:00Z", binding: null, previous_binding: null }), { status: 200, headers: { "content-type": "application/json" } });
  }));

  function Harness() {
    const [selected, setSelected] = useState("");
    const [available, setAvailable] = useState<string[]>([]);
    return <>
      <button onClick={async () => { const result = await getGatewayAccountRelayBindings(gateway); const data = result.data as GatewayAccountRelayBindingsResponse; setAvailable(data.accounts.map((item) => item.gateway_account_id)); }}>load</button>
      <select aria-label="account" value={selected} onChange={(event) => setSelected(event.target.value)}>{available.map((id) => <option key={id} value={id}>{id}</option>)}</select>
      <button onClick={() => void bindRelayNode({ relay_node_id: node, gateway_instance_id: gateway, gateway_account_id: selected })}>bind</button>
      <button onClick={() => void rebindRelayNode({ relay_node_id: node, new_gateway_instance_id: gateway, new_gateway_account_id: selected })}>rebind</button>
    </>;
  }
  render(<Harness />);
  fireEvent.click(screen.getByText("load"));
  await waitFor(() => expect(screen.getByRole("option", { name: ids[1] })).toBeInTheDocument());
  for (const [index, id] of ids.entries()) {
    fireEvent.change(screen.getByLabelText("account"), { target: { value: id } });
    fireEvent.click(screen.getByText("bind"));
    fireEvent.click(screen.getByText("rebind"));
    await waitFor(() => expect(requests.filter((request) => request.method === "POST").length).toBe((index + 1) * 2));
    const posts = requests.filter((request) => request.method === "POST").slice(-2);
    expect((posts[0]!.body as { gateway_account_id: string }).gateway_account_id).toBe(id);
    expect((posts[1]!.body as { new_gateway_account_id: string }).new_gateway_account_id).toBe(id);
  }
  expect(requests.filter((request) => request.method === "GET")).toHaveLength(1);
});
