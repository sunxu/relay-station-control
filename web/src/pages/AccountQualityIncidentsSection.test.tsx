import { formatDateTime } from "../time";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { AccountQualityIncidentsSection } from "./AccountQualityIncidentsSection";
import type { AccountQualityIncidentsApi } from "../api/account-quality-incidents-types";
import type { TopologyProviderState } from "../api/topology-types";
import { TopologyApiError } from "../api/topology-types";
const NODE="11111111-1111-4111-8111-111111111111", ACCOUNT="openai:a@example.invalid";
const providers: TopologyProviderState[]=[{provider:"openai",monitoring_status:"active",state:"current",current_scheduled_at:null,last_complete_at:null,snapshot_freshness:"fresh",health_scheduled_at:null,health_degraded:false,health_reason:null}];
const row={node_id:NODE,account_key:ACCOUNT,provider:"openai",failure_class:"auth" as const,status:"active" as const,first_seen:"2026-09-08T01:00:00Z",last_seen:"2026-09-08T01:05:00Z",hit_count:3,last_success_at:null};
function renderSection(a: AccountQualityIncidentsApi, onSelect=vi.fn()) { return {onSelect,...render(<QueryClientProvider client={new QueryClient({defaultOptions:{queries:{retry:false}}})}><AccountQualityIncidentsSection api={a} instanceId={NODE} providers={providers} providerError={false} onSelectAccount={onSelect} onUnauthorized={vi.fn()}/></QueryClientProvider>)}; }
it("shows loading independently", () => {
  const a = { incidents: vi.fn().mockReturnValue(new Promise(() => {})) };
  renderSection(a);
  expect(screen.getByRole("status", { name: "正在读取 Incidents" })).toBeInTheDocument();
});
it("renders active incidents and opens account history", async () => {
  const a = { incidents: vi.fn().mockResolvedValue({ instance_id: NODE, items: [row], next_cursor: null }) };
  const x = renderSection(a);
  expect(await screen.findByText("Active")).toBeInTheDocument();
  expect(screen.getByText(formatDateTime(row.first_seen))).toBeInTheDocument();
  expect(screen.getByText(formatDateTime(row.last_seen))).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: ACCOUNT }));
  expect(x.onSelect).toHaveBeenCalledWith(ACCOUNT);
});
it("shows empty state", async () => {
  const a = { incidents: vi.fn().mockResolvedValue({ instance_id: NODE, items: [], next_cursor: null }) };
  renderSection(a);
  expect(await screen.findByText("当前没有 Active incidents")).toBeInTheDocument();
});
it("clears the session on 401", async () => {
  const onUnauthorized = vi.fn();
  const a = { incidents: vi.fn().mockRejectedValue(new TopologyApiError(401)) };
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><AccountQualityIncidentsSection api={a} instanceId={NODE} providers={providers} providerError={false} onSelectAccount={vi.fn()} onUnauthorized={onUnauthorized} /></QueryClientProvider>);
  await waitFor(() => expect(onUnauthorized).toHaveBeenCalled());
});
it("shows unavailable for a 503", async () => {
  const a = { incidents: vi.fn().mockRejectedValue(new TopologyApiError(503)) };
  renderSection(a);
  expect(await screen.findByText("读取不可用（unavailable）")).toBeInTheDocument();
  expect(screen.queryByText("当前没有 Active incidents")).not.toBeInTheDocument();
});
it("passes provider and reason filters and resets cursor",async()=>{const a={incidents:vi.fn().mockResolvedValue({instance_id:NODE,items:[row],next_cursor:"next"})};renderSection(a);await screen.findByText("Active");fireEvent.click(screen.getByRole("button",{name:"Incidents 下一页"}));await waitFor(()=>expect(a.incidents).toHaveBeenLastCalledWith(NODE,undefined,undefined,"next",expect.any(AbortSignal)));fireEvent.mouseDown(screen.getByRole("combobox",{name:"Incident Provider"}));fireEvent.click((await screen.findAllByText("openai")).at(-1)!);fireEvent.mouseDown(screen.getByRole("combobox",{name:"Incident Reason"}));fireEvent.click((await screen.findAllByText("auth")).at(-1)!);await waitFor(()=>expect(a.incidents).toHaveBeenLastCalledWith(NODE,"openai","auth",undefined,expect.any(AbortSignal)));});

it("cancels old Node incidents and ignores a late 401", async()=>{
  const oldNode = NODE, newNode = "22222222-2222-4222-8222-222222222222";
  let rejectOld!: (reason: unknown)=>void, oldSignal!: AbortSignal;
  const old = new Promise<never>((_, reject)=>{ rejectOld=reject; });
  const api: AccountQualityIncidentsApi = { incidents: vi.fn((node: string, _provider, _failure, _cursor, signal?: AbortSignal)=>{ if(node===oldNode){oldSignal=signal!;return old;} return Promise.resolve({instance_id:newNode,items:[{...row,node_id:newNode}],next_cursor:null}); }) };
  const onUnauthorized=vi.fn(); const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
  const view=render(<QueryClientProvider client={client}><AccountQualityIncidentsSection api={api} instanceId={oldNode} providers={providers} providerError={false} onSelectAccount={vi.fn()} onUnauthorized={onUnauthorized}/></QueryClientProvider>);
  await waitFor(()=>expect(api.incidents).toHaveBeenCalledWith(oldNode,undefined,undefined,undefined,expect.any(AbortSignal)));
  view.rerender(<QueryClientProvider client={client}><AccountQualityIncidentsSection api={api} instanceId={newNode} providers={providers} providerError={false} onSelectAccount={vi.fn()} onUnauthorized={onUnauthorized}/></QueryClientProvider>);
  expect(oldSignal.aborted).toBe(true); expect(await screen.findByText("Active")).toBeInTheDocument(); rejectOld(new TopologyApiError(401)); await new Promise(resolve=>setTimeout(resolve,0)); expect(onUnauthorized).not.toHaveBeenCalled();
});
it("paginates with cursor",async()=>{const a={incidents:vi.fn().mockResolvedValueOnce({instance_id:NODE,items:[row],next_cursor:"next"}).mockResolvedValue({instance_id:NODE,items:[],next_cursor:null})};renderSection(a);await screen.findByText("Active");fireEvent.click(screen.getByRole("button",{name:"Incidents 下一页"}));await waitFor(()=>expect(a.incidents).toHaveBeenLastCalledWith(NODE,undefined,undefined,"next",expect.any(AbortSignal)));});
it("keeps provider source failure explicit",async()=>{const a={incidents:vi.fn().mockResolvedValue({instance_id:NODE,items:[],next_cursor:null})};render(<QueryClientProvider client={new QueryClient()}><AccountQualityIncidentsSection api={a} instanceId={NODE} providers={[]} providerError onSelectAccount={vi.fn()} onUnauthorized={vi.fn()}/></QueryClientProvider>);expect(await screen.findByText("Provider 筛选来源不可用")).toBeInTheDocument();});
it("has no mutation actions",async()=>{const a={incidents:vi.fn().mockResolvedValue({instance_id:NODE,items:[row],next_cursor:null})};renderSection(a);await screen.findByText("Active");expect(screen.queryByRole("button",{name:/bind|rebind|unbind|删除|修改/i})).not.toBeInTheDocument();});
