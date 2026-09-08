import { Alert, Button, Card, Empty, Flex, Grid, Input, Select, Spin, Table, Tag, Typography } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useNodeAsset, useNodeAssets } from "../api/asset-hooks";
import { AssetApiError } from "../api/asset-types";
import type { AssetApi } from "../api/asset-types";
import { useTopologyBinding, useTopologyCurrentDuplicates, useTopologyEvidence, useTopologyHistory, useTopologyProviders } from "../api/topology-hooks";
import { TopologyApiError } from "../api/topology-types";
import type { TopologyApi, TopologyOccurrence, TopologyProviderState } from "../api/topology-types";
import type { AccountQualityFilter, AccountQualityItem, AccountQualityLifecycle, AccountQualityWindow } from "../api/account-quality-types";
import { AccountQualityIncidentsSection } from "./AccountQualityIncidentsSection";
import { AccountList, type AccountListRow } from "../components/AccountList";
import { AccountDetailsDrawer } from "../components/AccountDetailsDrawer";
import { useAccountListQuery } from "../api/account-list-hooks";
import type { AccountInventoryApi, AccountInventoryBasicStatus } from "../api/account-inventory-types";
import { accountInventoryBasicStatuses, accountInventoryLifecycles } from "../api/account-inventory-types";
import type { AccountListFilters } from "../api/account-quality-types";
import { AccountInventoryCapacity } from "../components/AccountInventoryCapacity";

const { Text } = Typography;
function utc(value?: string | null) {
  return value ? `${new Date(value).toISOString().replace("T", " ").replace(".000Z", "")} UTC` : "—";
}
function unauthorized(error: unknown) {
  return (error instanceof TopologyApiError || error instanceof AssetApiError) && error.status === 401;
}
function ReadError({ retry, message = "读取不可用（unavailable）" }: { retry: () => void; message?: string }) {
  return <Alert type="error" title={message} action={<Button onClick={retry}>重试</Button>} />;
}
function accountReadError(error: unknown): string {
  if (error instanceof TopologyApiError) {
    if (error.status === 400) return "筛选条件或分页凭据无效，请清除筛选并重新查询。";
    if (error.status === 403) return "当前会话无权读取账号清单。";
    if (error.status === 404) return "所选 Node 不存在或已不属于当前环境。";
    if (error.status === 409) return "所选 Node 不支持账号清单查询。";
    if (error.status === 503) return "读取不可用（unavailable）";
  }
  return "Control 暂时无法读取账号清单。";
}
function Evidence({ api, occurrenceId, onUnauthorized }: { api: TopologyApi; occurrenceId: string; onUnauthorized: () => void }) {
  const [cursor, setCursor] = useState<string>();
  const query = useTopologyEvidence(api, occurrenceId, cursor);
  useEffect(() => { if (unauthorized(query.error)) onUnauthorized(); }, [query.error, onUnauthorized]);
  if (query.isPending) return <Spin size="small" />;
  if (query.error) return <ReadError retry={() => void query.refetch()} />;
  return <Flex vertical gap={8}>
    <Text>Evidence 记录 authoritative evaluation 涉及关系，不等于历史 confirmed owner。</Text>
    <Table size="small" scroll={{ x: 1050 }} pagination={false} rowKey="observation_id" dataSource={query.data.items} columns={[
      { title: "Node", dataIndex: "instance_id" },
      { title: "类型", dataIndex: "observation_kind" },
      { title: "Provider", dataIndex: "source_provider" },
      { title: "来源时间", dataIndex: "source_scheduled_at", render: utc },
      { title: "评估时间", dataIndex: "evaluation_at", render: utc },
      { title: "记录时间", dataIndex: "recorded_at", render: utc },
    ]} />
    <Flex gap={8} justify="end">
      <Button disabled={!cursor} onClick={() => setCursor(undefined)}>Evidence 首页</Button>
      <Button disabled={!query.data.next_cursor || query.isFetching} onClick={() => setCursor(query.data.next_cursor ?? undefined)}>Evidence 下一页</Button>
    </Flex>
  </Flex>;
}

export function TopologyView({ api, assetApi, inventoryApi, initialInstanceId, csrfToken = "", onUnauthorized }: {
  api: TopologyApi; assetApi: AssetApi; inventoryApi?: AccountInventoryApi; initialInstanceId?: string; csrfToken?: string; onUnauthorized: () => void;
}) {
  const cache = useQueryClient();
  const screens = Grid.useBreakpoint();
  const [instanceId, setInstanceId] = useState(initialInstanceId);
  const [nodeCursor, setNodeCursor] = useState<string>();
  const [historyStatus, setHistoryStatus] = useState<"ACTIVE" | "RESOLVED">();
  const [historyCursor, setHistoryCursor] = useState<string>();
  const [currentCursor, setCurrentCursor] = useState<string>();
  const [qualityWindow, setQualityWindow] = useState<AccountQualityWindow>("15m");
  const [qualityProvider, setQualityProvider] = useState<string>();
  const [qualityFilter, setQualityFilter] = useState<AccountQualityFilter>();
  const [qualityLifecycle, setQualityLifecycle] = useState<AccountQualityLifecycle | undefined>("present");
  const [qualityBasicStatus, setQualityBasicStatus] = useState<AccountInventoryBasicStatus>();
  const [qualityEmail, setQualityEmail] = useState("");
  const [qualityPageSize, setQualityPageSize] = useState<25 | 50 | 100>(25);
  const [qualityCursor, setQualityCursor] = useState<string>();
  const [qualityCursorHistory, setQualityCursorHistory] = useState<Array<string | undefined>>([undefined]);
  const [detailsRow, setDetailsRow] = useState<AccountListRow>();
  const [detailsAccountKey, setDetailsAccountKey] = useState<string>();
  const nodes = useNodeAssets(assetApi, { limit: 200, cursor: nodeCursor });
  const selected = useNodeAsset(assetApi, instanceId);
  const providers = useTopologyProviders(api, instanceId);
  const binding = useTopologyBinding(api, instanceId);
  const current = useTopologyCurrentDuplicates(api, instanceId, currentCursor);
  const history = useTopologyHistory(api, instanceId, historyStatus, historyCursor);
  const accountQualityView = useAccountListQuery(api, csrfToken);
  const node = selected.error ? undefined : selected.data;

  const expireSession = () => {
    // Cancel in-flight reads before clearing so a late response cannot repopulate
    // administrator evidence after a 401. The authenticated page then unmounts.
    void cache.cancelQueries();
    cache.clear();
    onUnauthorized();
  };
  useEffect(() => {
    if ([providers.error, binding.error, current.error, history.error, accountQualityView.error, nodes.error, selected.error].some(unauthorized)) expireSession();
  }, [providers.error, binding.error, current.error, history.error, accountQualityView.error, nodes.error, selected.error]);
  useEffect(() => {
    const onPop = () => {
      setInstanceId(new URLSearchParams(window.location.search).get("instance_id") ?? undefined);
      setHistoryCursor(undefined);
      setCurrentCursor(undefined);
      setQualityCursor(undefined); setQualityCursorHistory([undefined]);
      setQualityWindow("15m"); setQualityProvider(undefined); setQualityFilter(undefined); setQualityLifecycle("present");
      setQualityBasicStatus(undefined); setQualityEmail(""); setQualityPageSize(25);
      setDetailsRow(undefined);
      setDetailsAccountKey(undefined);
    };
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);
  const select = (id: string) => {
    setInstanceId(id);
    setHistoryCursor(undefined);
    setCurrentCursor(undefined);
    setQualityCursor(undefined); setQualityCursorHistory([undefined]);
    setQualityWindow("15m"); setQualityProvider(undefined); setQualityFilter(undefined); setQualityLifecycle("present");
    setQualityBasicStatus(undefined); setQualityEmail(""); setQualityPageSize(25);
    setDetailsRow(undefined);
    setDetailsAccountKey(undefined);
    window.history.pushState(null, "", `/topology?instance_id=${encodeURIComponent(id)}`);
  };
  const executeAccountQuery = (cursor?: string) => {
    if (!instanceId) return;
    const filters: AccountListFilters = {
      instanceId, window: qualityWindow, provider: qualityProvider?.trim().toLowerCase() || undefined, quality: qualityFilter,
      lifecycle: qualityLifecycle, basicStatus: qualityBasicStatus,
      email: qualityEmail.trim().toLowerCase() || undefined, cursor, limit: qualityPageSize,
    };
    accountQualityView.mutate(filters);
  };
  const resetAccountResult = () => { setQualityCursor(undefined); setQualityCursorHistory([undefined]); setDetailsRow(undefined); setDetailsAccountKey(undefined); accountQualityView.reset(); };
  useEffect(() => {
    let active = true;
    accountQualityView.reset();
    queueMicrotask(() => { if (active) executeAccountQuery(); });
    return () => { active = false; accountQualityView.reset(); };
  }, [instanceId]);
  const providerColumns: ColumnsType<TopologyProviderState> = [
    { title: "Provider", dataIndex: "provider", width: 120 },
    { title: "监控范围", dataIndex: "monitoring_status", width: 130, render: (v) => <Tag>{v}</Tag> },
    { title: "Snapshot freshness", dataIndex: "snapshot_freshness", width: 180, render: (v, row) => <Tag color={v === "fresh" ? "green" : v === "stale" ? "orange" : "default"}>{v === "unknown" && row.state === null ? "not-yet-observed" : v}</Tag> },
    { title: "最近完整快照", dataIndex: "last_complete_at", width: 225, render: utc },
    { title: "Latest health", width: 220, render: (_, row) => <Flex vertical gap={4}><Tag color={row.health_degraded === true ? "orange" : row.health_degraded === false ? "green" : "default"}>{row.health_degraded === true ? "degraded" : row.health_degraded === false ? "normal" : "unknown"}</Tag><Text type="secondary">{row.health_reason ?? "—"}</Text></Flex> },
    { title: "健康观测", dataIndex: "health_scheduled_at", width: 225, render: utc },
  ];
  const occurrenceColumns: ColumnsType<TopologyOccurrence> = [
    { title: "账号", dataIndex: "account_key", width: 260, render: (v) => <Text style={{ overflowWrap: "anywhere" }}>{v}</Text> },
    { title: "状态 / 严重度", width: 150, render: (_, row) => <Flex vertical><Tag color={row.status === "ACTIVE" ? "red" : "default"}>{row.status}</Tag><Text>{row.severity}</Text></Flex> },
    { title: "Evidence health", dataIndex: "evidence_state", width: 140, render: (v) => <Tag>{v}</Tag> },
    { title: "最近完整验证", dataIndex: "last_fully_verified_at", width: 225, render: utc },
    { title: "最近观察 / 恢复", width: 225, render: (_, row) => <Flex vertical><Text>{utc(row.last_seen_at)}</Text><Text>{utc(row.resolved_at)}</Text></Flex> },
    { title: "当前 affected Nodes", dataIndex: "affected_nodes", width: 320, render: (ids: string[]) => <Flex vertical><Tag color="blue">current</Tag>{ids.map((id) => <Text key={id} code style={{ overflowWrap: "anywhere" }}>{id}</Text>)}{ids.length === 0 && <Text>当前集合为空</Text>}</Flex> },
  ];
  const nodeOptions = (nodes.error ? [] : nodes.data?.items ?? []).map((item) => ({ value: item.instanceId, label: `${item.displayName} · ${item.instanceId}` }));
  if (node && !nodeOptions.some((option) => option.value === node.instanceId)) nodeOptions.unshift({ value: node.instanceId, label: `${node.displayName} · ${node.instanceId}` });
  const occurrenceTable = (items: TopologyOccurrence[]) => <Table
    key={instanceId} rowKey="occurrence_id" size="small" scroll={{ x: 1320 }} pagination={false}
    dataSource={items} columns={occurrenceColumns}
    expandable={{ expandedRowRender: (row) => <Evidence api={api} occurrenceId={row.occurrence_id} onUnauthorized={expireSession} /> }}
    locale={{ emptyText: "没有符合条件的 occurrence" }}
  />;
  const accountRows: AccountListRow[] = (accountQualityView.data?.items ?? []).map((item) => {
    const extended = item as AccountQualityItem & { inventory?: Record<string, unknown>; recent_requests?: AccountListRow["recent_requests"] };
    const inventory = extended.inventory ?? {};
    return {
      account_key: item.account_key, email: item.email, provider: item.provider,
      basic_status: String(inventory.basic_status ?? "unknown"),
      lifecycle: String(inventory.lifecycle ?? qualityLifecycle), quality: item.quality,
      request_count: item.request_count, success_count: item.success_count, failure_count: item.failure_count,
      success_rate: item.success_rate, p95_latency_ms: item.p95_latency_ms,
      last_success_at: item.last_success_at, last_failure_at: item.last_failure_at,
      last_failure_class: item.last_failure_class, recent_requests: extended.recent_requests ?? [],
      consecutive_missing_count: Number(inventory.consecutive_missing_count ?? 0),
      first_seen_at: typeof inventory.first_seen_at === "string" ? inventory.first_seen_at : undefined,
      last_seen_at: typeof inventory.last_seen_at === "string" ? inventory.last_seen_at : undefined,
      last_refresh_at: typeof inventory.last_refresh_at === "string" ? inventory.last_refresh_at : null,
      next_retry_at: typeof inventory.next_retry_at === "string" ? inventory.next_retry_at : null,
      provider_last_complete_at: typeof inventory.provider_last_complete_at === "string" ? inventory.provider_last_complete_at : null,
      provider_degraded: inventory.provider_degraded === true,
      snapshot_freshness: typeof inventory.snapshot_freshness === "string" ? inventory.snapshot_freshness : undefined,
    };
  });
  return <Flex vertical gap={16} data-testid="topology-view" className="topology-view" style={{ minWidth: 0 }}>
    <Card title="Node">
      {nodes.isPending && <Spin />}
      {nodes.error && <ReadError message="Node 清单读取不可用" retry={() => void nodes.refetch()} />}
      <Select aria-label="Relay Node" placeholder="选择 Relay Node" value={instanceId} onChange={select} options={nodeOptions} style={{ width: "100%", maxWidth: 560 }} />
      {!nodes.error && nodes.data?.items.length === 0 && <Empty description="当前没有登记 Node" />}
      <Flex justify="end" gap={8} style={{ marginTop: 8 }}>
        <Button disabled={!nodeCursor} onClick={() => setNodeCursor(undefined)}>Node 首页</Button>
        <Button disabled={!nodes.data?.nextCursor || nodes.isFetching || nodes.isError} onClick={() => setNodeCursor(nodes.data?.nextCursor ?? undefined)}>下一页 Node</Button>
      </Flex>
    </Card>
    {inventoryApi && <AccountInventoryCapacity api={inventoryApi} csrfToken={csrfToken} onUnauthorized={expireSession} />}
    {!instanceId && <Empty description="请选择 Node 查看只读拓扑" />}
    {instanceId && <>
      <Card title="Inventory evidence">
        {selected.isPending && <Spin />}
        {selected.error && <ReadError message={selected.error instanceof AssetApiError && selected.error.status === 404 ? "Node 不存在" : "Node 读取不可用"} retry={() => void selected.refetch()} />}
        <Flex vertical>
          <Text>Node：{node?.displayName ?? instanceId}</Text>
          <Text style={{ overflowWrap: "anywhere" }}>Instance ID：{instanceId}</Text>
          <Text>监控：{node ? node.monitoringActive ? "active" : "inactive" : "unknown"}</Text>
        </Flex>
      </Card>
      <Card title="账号清单与质量" role="region" aria-label="Account Quality">
        <Flex gap={8} wrap>
          <Input aria-label="Provider 精确筛选" placeholder="Provider（精确）" value={qualityProvider ?? ""} disabled={accountQualityView.isPending} maxLength={64} onChange={(event) => { setQualityProvider(event.target.value || undefined); resetAccountResult(); }} style={{ width: 190 }} />
          <Select allowClear aria-label="质量生命周期" placeholder="全部生命周期" value={qualityLifecycle} disabled={accountQualityView.isPending} options={accountInventoryLifecycles.map((value) => ({ value, label: value }))} onChange={(value) => { setQualityLifecycle(value); resetAccountResult(); }} style={{ width: 180 }} />
          <Select allowClear aria-label="最后报告基础状态" placeholder="全部基础状态" value={qualityBasicStatus} disabled={accountQualityView.isPending} options={accountInventoryBasicStatuses.map((value) => ({ value, label: value }))} onChange={(value) => { setQualityBasicStatus(value); resetAccountResult(); }} style={{ width: 200 }} />
          <Input aria-label="邮箱精确筛选" placeholder="邮箱（精确匹配）" value={qualityEmail} disabled={accountQualityView.isPending} maxLength={320} autoComplete="off" onChange={(event) => { setQualityEmail(event.target.value); resetAccountResult(); }} style={{ width: 240 }} />
          <Select aria-label="质量窗口" value={qualityWindow} disabled={accountQualityView.isPending} options={[{ value: "15m", label: "最近 15 分钟" }, { value: "1h", label: "最近 1 小时" }]} onChange={(value) => { setQualityWindow(value); resetAccountResult(); }} />
          <Select allowClear aria-label="质量分类" placeholder="全部质量" value={qualityFilter} disabled={accountQualityView.isPending} options={[{ value: "good", label: "Good" }, { value: "degraded", label: "Degraded" }, { value: "bad", label: "Bad" }, { value: "unknown", label: "Unknown" }]} onChange={(value) => { setQualityFilter(value); resetAccountResult(); }} />
          <Select aria-label="每页账号数" value={qualityPageSize} disabled={accountQualityView.isPending} options={[25, 50, 100].map((value) => ({ value, label: `${value} / 页` }))} onChange={(value) => { setQualityPageSize(value); resetAccountResult(); }} />
          <Button type="primary" disabled={!instanceId} loading={accountQualityView.isPending} onClick={() => { resetAccountResult(); executeAccountQuery(); }}>查询</Button>
        </Flex>

        {accountQualityView.isPending && <Flex role="status" aria-label="正在读取账号质量" justify="center" style={{ marginTop: 12 }}><Spin /></Flex>}
        {accountQualityView.error && !accountQualityView.isPending && <ReadError message={accountReadError(accountQualityView.error)} retry={() => executeAccountQuery(qualityCursor)} />}
        {accountQualityView.data && !accountQualityView.error && <>
          {accountQualityView.data.items.length === 0 && <Empty description="没有 Inventory 账号或匹配账号" />}
          {accountQualityView.data.items.length > 0 && <AccountList rows={accountRows} onSelectAccount={(row) => { setDetailsAccountKey(row.account_key); setDetailsRow(row); }} />}
          <Flex justify="end" gap={8} style={{ marginTop: 8 }}><Button disabled={qualityCursorHistory.length === 1 || accountQualityView.isPending} onClick={() => { const previous = qualityCursorHistory.slice(0, -1); const cursor = previous.at(-1); setQualityCursorHistory(previous); setQualityCursor(cursor); executeAccountQuery(cursor); }}>账号上一页</Button><Button disabled={!accountQualityView.data.next_cursor || accountQualityView.isPending} onClick={() => { const next = accountQualityView.data.next_cursor ?? undefined; setQualityCursorHistory((items) => [...items, next]); setQualityCursor(next); executeAccountQuery(next); }}>账号下一页</Button></Flex>
        </>}
      </Card>
      <AccountDetailsDrawer api={api} instanceId={instanceId} row={detailsRow} accountKey={detailsAccountKey} onClose={() => { setDetailsRow(undefined); setDetailsAccountKey(undefined); }} onUnauthorized={expireSession} />
      <AccountQualityIncidentsSection key={instanceId} api={api} instanceId={instanceId} providers={providers.data?.providers ?? []} providerError={Boolean(providers.error)} onSelectAccount={(accountKey) => { setDetailsAccountKey(accountKey); setDetailsRow(accountRows.find((item) => item.account_key === accountKey)); }} onUnauthorized={expireSession} />
      <Card title="Provider snapshot 与 latest health" extra={<Button onClick={() => void providers.refetch()} loading={providers.isFetching}>刷新 Provider</Button>}>
        {providers.isPending && <Spin />}
        {providers.error && <ReadError retry={() => void providers.refetch()} />}
        {providers.data && !providers.error && <>
          <Text type="secondary">来源时间：{utc(providers.data.observed_at)}</Text>
          {screens.xs ? <Flex vertical gap={12} style={{ marginTop: 12 }}>
            {providers.data.providers.length === 0 && <Empty description="没有应监控或已持有 state 的 Provider" />}
            {providers.data.providers.map((row) => <Card key={row.provider} size="small" title={row.provider}>
              <Flex vertical gap={8}>
                <Text>监控范围：{row.monitoring_status}</Text>
                <Flex wrap gap={8}>
                  <Text>Snapshot <Tag color={row.snapshot_freshness === "fresh" ? "green" : row.snapshot_freshness === "stale" ? "orange" : "default"}>{row.state === null ? "not-yet-observed" : row.snapshot_freshness}</Tag></Text>
                  <Text>Health <Tag color={row.health_degraded === true ? "orange" : row.health_degraded === false ? "green" : "default"}>{row.health_degraded === true ? "degraded" : row.health_degraded === false ? "normal" : "unknown"}</Tag></Text>
                </Flex>
                <Text>最近完整快照：{utc(row.last_complete_at)}</Text>
                <Text>健康观测：{utc(row.health_scheduled_at)}</Text>
                <Text style={{ overflowWrap: "anywhere" }}>原因：{row.health_reason ?? "—"}</Text>
              </Flex>
            </Card>)}
          </Flex> : <Table rowKey="provider" size="small" scroll={{ x: 1100 }} pagination={false} dataSource={providers.data.providers} columns={providerColumns} locale={{ emptyText: "没有应监控或已持有 state 的 Provider" }} />}
        </>}
      </Card>
      <Card title="Gateway Usage Context" extra={<Button onClick={() => void binding.refetch()} loading={binding.isFetching}>刷新 Binding</Button>}>
        {binding.isPending && <Spin />}
        {binding.error && <ReadError retry={() => void binding.refetch()} />}
        {binding.data && !binding.error && <Flex vertical gap={6} style={{ overflowWrap: "anywhere" }}>
          <Text>Binding truth：<Tag>{binding.data.current_binding ? "BOUND" : "UNBOUND"}</Tag></Text>
          <Text>Binding resolution：<Tag>{binding.data.resolution}</Tag></Text>
          <Text>Directory freshness：<Tag>{binding.data.directory_freshness}</Tag></Text>
          <Text>Context source：{binding.data.context_source}</Text>
          <Text>Gateway：{binding.data.gateway_instance_id ?? binding.data.current_binding?.gateway_instance_id ?? "—"}</Text>
          <Text>Account ID：{binding.data.gateway_account_id ?? binding.data.current_binding?.gateway_account_id ?? "—"}</Text>
          {binding.data.account_context && <Text>Account context（{binding.data.context_source}）：{binding.data.account_context.name} · {binding.data.account_context.platform} · {binding.data.account_context.status}</Text>}
          <Text>绑定时间：{utc(binding.data.current_binding?.bound_at)}</Text>
          <Text>最近成功观测：{utc(binding.data.last_success_observation_at)}</Text>
          <Text>观察时间：{utc(binding.data.observed_at)}</Text>
        </Flex>}
      </Card>
      <Card title="Ownership Fact · 当前 duplicate" extra={<Button onClick={() => void current.refetch()} loading={current.isFetching}>刷新 Current</Button>}>
        {current.isPending && <Spin />}
        {current.error && <ReadError retry={() => void current.refetch()} />}
        {current.data && !current.error && <>
          {occurrenceTable(current.data.items)}
          <Flex justify="end" gap={8}><Button disabled={!currentCursor} onClick={() => setCurrentCursor(undefined)}>Current 首页</Button><Button disabled={!current.data.next_cursor || current.isFetching} onClick={() => setCurrentCursor(current.data?.next_cursor ?? undefined)}>Current 下一页</Button></Flex>
        </>}
      </Card>
      <Card title="Ownership Fact · 历史评估涉及">
        <Flex gap={8} wrap>
          <Select allowClear aria-label="历史状态" placeholder="History 全部状态" value={historyStatus} onChange={(value) => { setHistoryStatus(value); setHistoryCursor(undefined); }} options={[{ value: "ACTIVE", label: "ACTIVE" }, { value: "RESOLVED", label: "Resolved" }]} style={{ minWidth: 170 }} />
          <Button onClick={() => void history.refetch()} loading={history.isFetching}>刷新 History</Button>
        </Flex>
        {history.isPending && <Spin />}
        {history.error && <ReadError retry={() => void history.refetch()} />}
        {history.data && !history.error && <>
          <Text type="secondary">historical involvement · {utc(history.data.observed_at)}</Text>
          {occurrenceTable(history.data.items)}
          <Flex justify="end" gap={8}><Button disabled={!historyCursor} onClick={() => setHistoryCursor(undefined)}>History 首页</Button><Button disabled={!history.data.next_cursor || history.isFetching} onClick={() => setHistoryCursor(history.data?.next_cursor ?? undefined)}>History 下一页</Button></Flex>
        </>}
      </Card>
    </>}
  </Flex>;
}
