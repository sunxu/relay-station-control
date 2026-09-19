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
import { formatDateTime } from "../foundation/format";
import type { AccountOperationsApi } from "../api/account-operations-api";
import { UploadNewAccountAction } from "../components/AccountOperationsPanel";
import { useOptionalAppLocale } from "../foundation/FrontendFoundationProvider";
import { resources } from "../foundation/resources";
import type { TranslationResource } from "../foundation/resources";

const { Text } = Typography;
function unauthorized(error: unknown) {
  return (error instanceof TopologyApiError || error instanceof AssetApiError) && error.status === 401;
}
function ReadError({ retry, message, retryLabel }: { retry: () => void; message: string; retryLabel: string }) {
  return <Alert type="error" title={message} action={<Button onClick={retry}>{retryLabel}</Button>} />;
}
function accountReadError(error: unknown, copy: TranslationResource["topology"]): string {
  if (error instanceof TopologyApiError) {
    if (error.status === 400) return copy.invalidAccountFilter;
    if (error.status === 403) return copy.forbiddenAccountList;
    if (error.status === 404) return copy.selectedNodeNotFound;
    if (error.status === 409) return copy.unsupportedAccountList;
    if (error.status === 503) return copy.readUnavailable;
  }
  return copy.unavailableAccountList;
}
function Evidence({ api, occurrenceId, onUnauthorized, copy }: { api: TopologyApi; occurrenceId: string; onUnauthorized: () => void; copy: TranslationResource["topology"] }) {
  const locale = useOptionalAppLocale()?.locale ?? "zh-CN";
  const [cursor, setCursor] = useState<string>();
  const query = useTopologyEvidence(api, occurrenceId, cursor);
  useEffect(() => { if (unauthorized(query.error)) onUnauthorized(); }, [query.error, onUnauthorized]);
  if (query.isPending) return <Spin size="small" />;
  if (query.error) return <ReadError message={copy.readUnavailable} retryLabel={copy.retry} retry={() => void query.refetch()} />;
  return <Flex vertical gap={8}>
    <Text>{copy.evidenceNote}</Text>
    <Table className="topology-table" size="small" scroll={{ x: 1050 }} pagination={false} rowKey="observation_id" dataSource={query.data.items} columns={[
      { title: copy.node, dataIndex: "instance_id" },
      { title: copy.type, dataIndex: "observation_kind" },
      { title: copy.provider, dataIndex: "source_provider" },
      { title: copy.sourceTime, dataIndex: "source_scheduled_at", render: (value) => formatDateTime(value, locale) },
      { title: copy.evaluationTime, dataIndex: "evaluation_at", render: (value) => formatDateTime(value, locale) },
      { title: copy.recordedTime, dataIndex: "recorded_at", render: (value) => formatDateTime(value, locale) },
    ]} />
    <Flex gap={8} justify="end">
      <Button data-testid="topology-evidence-first" disabled={!cursor} onClick={() => setCursor(undefined)}>{copy.evidenceFirstPage}</Button>
      <Button data-testid="topology-evidence-next" disabled={!query.data.next_cursor || query.isFetching} onClick={() => setCursor(query.data.next_cursor ?? undefined)}>{copy.evidenceNextPage}</Button>
    </Flex>
  </Flex>;
}

export function TopologyView({ api, assetApi, inventoryApi, accountOperationsApi, initialInstanceId, csrfToken = "", onUnauthorized }: {
  api: TopologyApi; assetApi: AssetApi; inventoryApi?: AccountInventoryApi; accountOperationsApi?: AccountOperationsApi; initialInstanceId?: string; csrfToken?: string; onUnauthorized: () => void;
}) {
  const cache = useQueryClient();
  const locale = useOptionalAppLocale()?.locale ?? "zh-CN";
  const copy = resources[locale].translation.topology;
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
    { title: copy.provider, dataIndex: "provider", width: 120 },
    { title: copy.monitoringScope, dataIndex: "monitoring_status", width: 130, render: (v) => <Tag>{v}</Tag> },
    { title: copy.freshness, dataIndex: "snapshot_freshness", width: 180, render: (v, row) => <Tag color={v === "fresh" ? "green" : v === "stale" ? "orange" : "default"}>{v === "unknown" && row.state === null ? copy.notYetObserved : v}</Tag> },
    { title: copy.latestSnapshot, dataIndex: "last_complete_at", width: 225, render: (value) => formatDateTime(value, locale) },
    { title: copy.health, width: 220, render: (_, row) => <Flex vertical gap={4}><Tag color={row.health_degraded === true ? "orange" : row.health_degraded === false ? "green" : "default"}>{row.health_degraded === true ? "degraded" : row.health_degraded === false ? copy.normal : copy.unknown}</Tag><Text type="secondary">{row.health_reason ?? "—"}</Text></Flex> },
    { title: copy.healthObservation, dataIndex: "health_scheduled_at", width: 225, render: (value) => formatDateTime(value, locale) },
  ];
  const occurrenceColumns: ColumnsType<TopologyOccurrence> = [
    { title: copy.account, dataIndex: "account_key", width: 260, render: (v) => <Text style={{ overflowWrap: "anywhere" }}>{v}</Text> },
    { title: copy.statusSeverity, width: 150, render: (_, row) => <Flex vertical><Tag color={row.status === "ACTIVE" ? "red" : "default"}>{row.status}</Tag><Text>{row.severity}</Text></Flex> },
    { title: copy.evidenceHealth, dataIndex: "evidence_state", width: 140, render: (v) => <Tag>{v}</Tag> },
    { title: copy.latestVerified, dataIndex: "last_fully_verified_at", width: 225, render: (value) => formatDateTime(value, locale) },
    { title: copy.recentObservation, width: 225, render: (_, row) => <Flex vertical><Text>{formatDateTime(row.last_seen_at, locale)}</Text><Text>{formatDateTime(row.resolved_at, locale)}</Text></Flex> },
    { title: copy.affectedNodes, dataIndex: "affected_nodes", width: 320, render: (ids: string[]) => <Flex vertical><Tag color="blue">{copy.current}</Tag>{ids.map((id) => <Text key={id} code style={{ overflowWrap: "anywhere" }}>{id}</Text>)}{ids.length === 0 && <Text>{copy.emptySet}</Text>}</Flex> },
  ];
  const nodeOptions = (nodes.error ? [] : nodes.data?.items ?? []).map((item) => ({ value: item.instanceId, label: `${item.displayName} · ${item.instanceId}` }));
  if (node && !nodeOptions.some((option) => option.value === node.instanceId)) nodeOptions.unshift({ value: node.instanceId, label: `${node.displayName} · ${node.instanceId}` });
  const occurrenceTable = (items: TopologyOccurrence[]) => <Table className="topology-table"
    key={instanceId} rowKey="occurrence_id" size="small" scroll={{ x: 1320 }} pagination={false}
    dataSource={items} columns={occurrenceColumns}
    expandable={{
      expandedRowRender: (row) => <Evidence api={api} occurrenceId={row.occurrence_id} onUnauthorized={expireSession} copy={copy} />,
      expandIcon: ({ expanded, onExpand, record }) => <Button type="text" data-testid={`topology-evidence-expand-${record.occurrence_id}`} onClick={(event) => onExpand(record, event)}>{expanded ? "−" : "+"}</Button>,
    }}
    locale={{ emptyText: copy.emptyOccurrence }}
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
      availability: (item as AccountQualityItem & { availability?: AccountListRow["availability"] }).availability,
      token_state: item.token_state,
      expected_valid_until: item.expected_valid_until,
    };
  });
  return <Flex vertical gap={16} data-testid="topology-view" className="topology-view" style={{ minWidth: 0 }}>
    <Card className="topology-card" title={copy.node}>
      {nodes.isPending && <Spin />}
      {nodes.error && <ReadError message={copy.nodeListUnavailable} retryLabel={copy.retry} retry={() => void nodes.refetch()} />}
      <Select data-testid="relay-node-selector" aria-label={copy.relayNode} placeholder={copy.selectNode} value={instanceId} onChange={select} options={nodeOptions} style={{ width: "100%", maxWidth: 560 }} />
      {!nodes.error && nodes.data?.items.length === 0 && <Empty description={copy.empty} />}
      <Flex justify="end" gap={8} style={{ marginTop: 8 }}>
        <Button disabled={!nodeCursor} onClick={() => setNodeCursor(undefined)}>{copy.nodeFirstPage}</Button>
        <Button data-testid="node-pagination-next" disabled={!nodes.data?.nextCursor || nodes.isFetching || nodes.isError} onClick={() => setNodeCursor(nodes.data?.nextCursor ?? undefined)}>{copy.nodeNextPage}</Button>
      </Flex>
    </Card>
    {inventoryApi && <AccountInventoryCapacity api={inventoryApi} csrfToken={csrfToken} onUnauthorized={expireSession} />}
    {!instanceId && <Empty description={copy.noInstance} />}
    {instanceId && <>
      <Card className="topology-card" title={copy.inventoryEvidence}>
        {selected.isPending && <Spin />}
        {selected.error && <ReadError message={selected.error instanceof AssetApiError && selected.error.status === 404 ? copy.nodeNotFound : copy.nodeReadUnavailable} retryLabel={copy.retry} retry={() => void selected.refetch()} />}
        <Flex vertical>
          <Text>{copy.node}：{node?.displayName ?? instanceId}</Text>
          <Text style={{ overflowWrap: "anywhere" }}>{copy.instanceId}：{instanceId}</Text>
          <Text>{copy.monitoring}：{node ? node.monitoringActive ? "active" : "inactive" : copy.unknown}</Text>
        </Flex>
      </Card>
      <Card className="topology-card" title={copy.accountQuality} role="region" aria-label={copy.accountQuality}>
        {accountOperationsApi && csrfToken && <Flex justify="end" style={{ marginBottom: 12 }}><UploadNewAccountAction api={accountOperationsApi} csrf={csrfToken} nodeInstanceId={instanceId} onUnauthorized={expireSession} /></Flex>}
        <Flex gap={8} wrap>
          <Input aria-label={copy.providerExact} placeholder={copy.providerPlaceholder} value={qualityProvider ?? ""} disabled={accountQualityView.isPending} maxLength={64} onChange={(event) => { setQualityProvider(event.target.value || undefined); resetAccountResult(); }} style={{ width: 190 }} />
          <Select allowClear aria-label={copy.lifecycle} placeholder={copy.allLifecycle} value={qualityLifecycle} disabled={accountQualityView.isPending} options={accountInventoryLifecycles.map((value) => ({ value, label: value }))} onChange={(value) => { setQualityLifecycle(value); resetAccountResult(); }} style={{ width: 180 }} />
          <Select allowClear aria-label={copy.basicStatus} placeholder={copy.allBasicStatus} value={qualityBasicStatus} disabled={accountQualityView.isPending} options={accountInventoryBasicStatuses.map((value) => ({ value, label: value }))} onChange={(value) => { setQualityBasicStatus(value); resetAccountResult(); }} style={{ width: 200 }} />
          <Input aria-label={copy.emailExact} placeholder={copy.emailPlaceholder} value={qualityEmail} disabled={accountQualityView.isPending} maxLength={320} autoComplete="off" onChange={(event) => { setQualityEmail(event.target.value); resetAccountResult(); }} style={{ width: 240 }} />
          <Select aria-label={copy.qualityWindow} value={qualityWindow} disabled={accountQualityView.isPending} options={[{ value: "15m", label: copy.recent15m }, { value: "1h", label: copy.recent1h }]} onChange={(value) => { setQualityWindow(value); resetAccountResult(); }} />
          <Select allowClear aria-label={copy.quality} placeholder={copy.allQuality} value={qualityFilter} disabled={accountQualityView.isPending} options={[{ value: "good", label: copy.good }, { value: "degraded", label: copy.degraded }, { value: "bad", label: copy.bad }, { value: "unknown", label: copy.unknown }]} onChange={(value) => { setQualityFilter(value); resetAccountResult(); }} />
          <Select aria-label={copy.perPage} value={qualityPageSize} disabled={accountQualityView.isPending} options={[25, 50, 100].map((value) => ({ value, label: `${value}${copy.pageSuffix}` }))} onChange={(value) => { setQualityPageSize(value); resetAccountResult(); }} />
          <Button data-testid="account-query" type="primary" disabled={!instanceId} loading={accountQualityView.isPending} onClick={() => { resetAccountResult(); executeAccountQuery(); }}>{copy.query}</Button>
        </Flex>

        {accountQualityView.isPending && <Flex role="status" aria-label={copy.readingQuality} justify="center" style={{ marginTop: 12 }}><Spin /></Flex>}
        {accountQualityView.error && !accountQualityView.isPending && <ReadError message={accountReadError(accountQualityView.error, copy)} retryLabel={copy.retry} retry={() => executeAccountQuery(qualityCursor)} />}
        {accountQualityView.data && !accountQualityView.error && <>
          {accountQualityView.data.items.length === 0 && <Empty description={copy.noAccounts} />}
          {accountQualityView.data.items.length > 0 && <AccountList rows={accountRows} onSelectAccount={(row) => { setDetailsAccountKey(row.account_key); setDetailsRow(row); }} />}
          <Flex justify="end" gap={8} style={{ marginTop: 8 }}><Button disabled={qualityCursorHistory.length === 1 || accountQualityView.isPending} onClick={() => { const previous = qualityCursorHistory.slice(0, -1); const cursor = previous.at(-1); setQualityCursorHistory(previous); setQualityCursor(cursor); executeAccountQuery(cursor); }}>{copy.previousAccounts}</Button><Button disabled={!accountQualityView.data.next_cursor || accountQualityView.isPending} onClick={() => { const next = accountQualityView.data.next_cursor ?? undefined; setQualityCursorHistory((items) => [...items, next]); setQualityCursor(next); executeAccountQuery(next); }}>{copy.nextAccounts}</Button></Flex>
        </>}
      </Card>
      <AccountDetailsDrawer api={api} accountOperationsApi={accountOperationsApi} csrfToken={csrfToken} instanceId={instanceId} row={detailsRow} accountKey={detailsAccountKey} onClose={() => { setDetailsRow(undefined); setDetailsAccountKey(undefined); }} onUnauthorized={expireSession} />
      <AccountQualityIncidentsSection key={instanceId} api={api} instanceId={instanceId} providers={providers.data?.providers ?? []} providerError={Boolean(providers.error)} onSelectAccount={(accountKey) => { setDetailsAccountKey(accountKey); setDetailsRow(accountRows.find((item) => item.account_key === accountKey)); }} onUnauthorized={expireSession} />
      <Card className="topology-card" title={copy.providerSnapshot} extra={<Button data-testid="topology-refresh-provider" onClick={() => void providers.refetch()} loading={providers.isFetching}>{copy.refreshProvider}</Button>}>
        {providers.isPending && <Spin />}
        {providers.error && <ReadError message={copy.readUnavailable} retryLabel={copy.retry} retry={() => void providers.refetch()} />}
        {providers.data && !providers.error && <>
          <Text type="secondary">{copy.observedAt}：{formatDateTime(providers.data.observed_at, locale)}</Text>
          {screens.xs ? <Flex vertical gap={12} style={{ marginTop: 12 }}>
            {providers.data.providers.length === 0 && <Empty description={copy.noProviders} />}
            {providers.data.providers.map((row) => <Card className="topology-card" key={row.provider} size="small" title={row.provider}>
              <Flex vertical gap={8}>
                <Text>{copy.monitoringScope}：{row.monitoring_status}</Text>
                <Flex wrap gap={8}>
                  <Text>{copy.snapshot} <Tag color={row.snapshot_freshness === "fresh" ? "green" : row.snapshot_freshness === "stale" ? "orange" : "default"}>{row.state === null ? copy.notYetObserved : row.snapshot_freshness}</Tag></Text>
                  <Text>{copy.health} <Tag color={row.health_degraded === true ? "orange" : row.health_degraded === false ? "green" : "default"}>{row.health_degraded === true ? "degraded" : row.health_degraded === false ? copy.normal : copy.unknown}</Tag></Text>
                </Flex>
                <Text>{copy.latestSnapshot}：{formatDateTime(row.last_complete_at, locale)}</Text>
                <Text>{copy.healthObservation}：{formatDateTime(row.health_scheduled_at, locale)}</Text>
                <Text style={{ overflowWrap: "anywhere" }}>{copy.reason}：{row.health_reason ?? "—"}</Text>
              </Flex>
            </Card>)}
          </Flex> : <Table className="topology-table" rowKey="provider" size="small" scroll={{ x: 1100 }} pagination={false} dataSource={providers.data.providers} columns={providerColumns} locale={{ emptyText: copy.noProviders }} />}
        </>}
      </Card>
      <Card className="topology-card" title={copy.gatewayContext} extra={<Button onClick={() => void binding.refetch()} loading={binding.isFetching}>{copy.refreshBinding}</Button>}>
        {binding.isPending && <Spin />}
        {binding.error && <ReadError message={copy.readUnavailable} retryLabel={copy.retry} retry={() => void binding.refetch()} />}
        {binding.data && !binding.error && <Flex vertical gap={6} style={{ overflowWrap: "anywhere" }}>
          <Text>{copy.bindingTruth}：<Tag>{binding.data.current_binding ? "BOUND" : "UNBOUND"}</Tag></Text>
          <Text>{copy.bindingResolution}：<Tag>{binding.data.resolution}</Tag></Text>
          <Text>{copy.directoryFreshness}：<Tag>{binding.data.directory_freshness}</Tag></Text>
          <Text>{copy.contextSource}：{binding.data.context_source}</Text>
          <Text>{copy.gateway}：{binding.data.gateway_instance_id ?? binding.data.current_binding?.gateway_instance_id ?? "—"}</Text>
          <Text>{copy.accountId}：{binding.data.gateway_account_id ?? binding.data.current_binding?.gateway_account_id ?? "—"}</Text>
          {binding.data.account_context && <Text>{copy.accountContext}（{binding.data.context_source}）：{binding.data.account_context.name} · {binding.data.account_context.platform} · {binding.data.account_context.status}</Text>}
          <Text>{copy.boundAt}：{formatDateTime(binding.data.current_binding?.bound_at, locale)}</Text>
          <Text>{copy.lastSuccess}：{formatDateTime(binding.data.last_success_observation_at, locale)}</Text>
          <Text>{copy.observedTime}：{formatDateTime(binding.data.observed_at, locale)}</Text>
        </Flex>}
      </Card>
      <Card className="topology-card" data-testid="topology-current-ownership" title={copy.ownershipCurrent} extra={<Button onClick={() => void current.refetch()} loading={current.isFetching}>{copy.refreshCurrent}</Button>}>
        {current.isPending && <Spin />}
        {current.error && <ReadError message={copy.readUnavailable} retryLabel={copy.retry} retry={() => void current.refetch()} />}
        {current.data && !current.error && <>
          {occurrenceTable(current.data.items)}
          <Flex justify="end" gap={8}><Button data-testid="topology-current-first" disabled={!currentCursor} onClick={() => setCurrentCursor(undefined)}>{copy.firstPage}</Button><Button data-testid="topology-current-next" disabled={!current.data.next_cursor || current.isFetching} onClick={() => setCurrentCursor(current.data?.next_cursor ?? undefined)}>{copy.nextPage}</Button></Flex>
        </>}
      </Card>
      <Card className="topology-card" data-testid="topology-history-ownership" title={copy.ownershipHistory}>
        <Flex gap={8} wrap>
          <Select data-testid="topology-history-status" allowClear aria-label={copy.historyStatus} placeholder={copy.historyPlaceholder} value={historyStatus} onChange={(value) => { setHistoryStatus(value); setHistoryCursor(undefined); }} options={[{ value: "ACTIVE", label: <span data-testid="topology-history-status-active">ACTIVE</span> }, { value: "RESOLVED", label: <span data-testid="topology-history-status-resolved">Resolved</span> }]} style={{ minWidth: 170 }} />
          <Button onClick={() => void history.refetch()} loading={history.isFetching}>{copy.refreshHistory}</Button>
        </Flex>
        {history.isPending && <Spin />}
        {history.error && <ReadError message={copy.readUnavailable} retryLabel={copy.retry} retry={() => void history.refetch()} />}
        {history.data && !history.error && <>
          <Text type="secondary">{copy.historicalInvolvement} · {formatDateTime(history.data.observed_at, locale)}</Text>
          {occurrenceTable(history.data.items)}
          <Flex justify="end" gap={8}><Button data-testid="topology-history-first" disabled={!historyCursor} onClick={() => setHistoryCursor(undefined)}>{copy.firstPage}</Button><Button data-testid="topology-history-next" disabled={!history.data.next_cursor || history.isFetching} onClick={() => setHistoryCursor(history.data?.next_cursor ?? undefined)}>{copy.nextPage}</Button></Flex>
        </>}
      </Card>
    </>}
  </Flex>;
}
