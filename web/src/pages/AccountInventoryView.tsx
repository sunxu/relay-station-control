import { useEffect, useState } from "react";
import { Alert, Button, Card, Empty, Flex, Input, Select, Space, Spin, Tag, Typography } from "antd";
import { useAccountInventoryPollCapacity } from "../api/account-inventory-hooks";
import {
  AccountInventoryApiError,
  accountInventoryBasicStatuses,
  accountInventoryLifecycles,
} from "../api/account-inventory-types";
import type {
  AccountInventoryApi,
  AccountInventoryBasicStatus,
  AccountInventoryLifecycle,
  AccountInventoryPollCapacity,
} from "../api/account-inventory-types";
import { useNodeAssets } from "../api/asset-hooks";
import type { AssetApi } from "../api/asset-types";
import { AccountDetailsDrawer } from "../components/AccountDetailsDrawer";
import { TopologyApiError } from "../api/topology-types";
import type { AccountRequestHistoryApi } from "../api/account-request-history-types";
import { AccountList, type AccountListRow } from "../components/AccountList";
import { useAccountListQuery } from "../api/account-list-hooks";
import type { AccountListApi, AccountListFilters } from "../api/account-quality-types";
import type { AccountQualityFilter, AccountQualityWindow } from "../api/account-quality-types";

const { Text } = Typography;
type PageSize = 25 | 50 | 100;
const nodeFilters = { capability: "management_account_inventory_read", limit: 200 } as const;

function utc(value: string | null): string {
  if (!value) return "—";
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return "—";
  return `${new Intl.DateTimeFormat("zh-CN", {
    timeZone: "UTC", year: "numeric", month: "2-digit", day: "2-digit",
    hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false,
  }).format(parsed)} UTC`;
}

function errorDescription(error: AccountInventoryApiError | TopologyApiError): string {
  if (error.status === 403) return "安全凭据已变化。请刷新会话后重新提交，本次查询不会自动重放。";
  if (error.status === 404) return "所选 Node 不存在或已不属于当前环境。";
  if (error.status === 409) return "所选 Node 不支持账号清单查询。";
  if (error.status === 400) return "筛选条件或分页凭据无效，请清除筛选并重新查询。";
  return error instanceof AccountInventoryApiError ? `Control 暂时无法读取账号清单（请求 ID：${error.detail.request_id}）。` : "Control 暂时无法读取账号清单。";
}

function capacityStatusLabel(value: AccountInventoryPollCapacity["status"]): string {
  if (value === "ready") return "可用";
  if (value === "capacity_exceeded") return "容量不足";
  return "已禁用";
}

export function AccountInventoryView({
  api,
  assetApi,
  csrfToken,
  onUnauthorized,
  accountListApi,
}: {
  api: AccountInventoryApi;
  assetApi: AssetApi;
  csrfToken: string;
  onUnauthorized: () => void;
  accountListApi: AccountListApi & AccountRequestHistoryApi;
}) {
  const nodes = useNodeAssets(assetApi, nodeFilters);
  const unifiedQuery = useAccountListQuery(accountListApi, csrfToken);
  const capacity = useAccountInventoryPollCapacity(api, csrfToken);
  const [instanceId, setInstanceId] = useState<string | undefined>(() =>
    new URLSearchParams(window.location.search).get("instance_id") || undefined,
  );
  const [detailsRow, setDetailsRow] = useState<AccountListRow>();
  const [provider, setProvider] = useState("");
  const [lifecycle, setLifecycle] = useState<AccountInventoryLifecycle>("present");
  const [basicStatus, setBasicStatus] = useState<AccountInventoryBasicStatus>();
  const [email, setEmail] = useState("");
  const [qualityWindow, setQualityWindow] = useState<AccountQualityWindow>("15m");
  const [qualityFilter, setQualityFilter] = useState<AccountQualityFilter>();
  const [pageSize, setPageSize] = useState<PageSize>(50);
  const [cursorHistory, setCursorHistory] = useState<Array<string | undefined>>([undefined]);

  useEffect(() => {
    if ((unifiedQuery.error instanceof AccountInventoryApiError || unifiedQuery.error instanceof TopologyApiError) && unifiedQuery.error.status === 401) onUnauthorized();
    if (capacity.error instanceof AccountInventoryApiError && capacity.error.status === 401) onUnauthorized();
  }, [unifiedQuery.error, capacity.error, onUnauthorized]);

  useEffect(() => () => unifiedQuery.reset(), []); // Component state is memory-only and is discarded on unmount.

  const execute = (cursor?: string) => {
    if (!instanceId) return;
    const filters: AccountListFilters = {
      instanceId,
      provider: provider.trim().toLowerCase() || undefined,
      window: qualityWindow,
      quality: qualityFilter,
      lifecycle,
      basicStatus,
      email: email.trim().toLowerCase() || undefined,
      cursor,
      limit: pageSize,
    };
    unifiedQuery.mutate(filters);
  };

  // Defer until mount settles so StrictMode's discarded mount does not issue a read/audit.
  useEffect(() => {
    let active = true;
    queueMicrotask(() => { if (active) execute(); });
    return () => { active = false; };
  }, []); // Only the initial deep link auto-loads; filter edits remain explicit.

  const resetResult = () => {
    setCursorHistory([undefined]);
    setDetailsRow(undefined);
    unifiedQuery.reset();
  };

  const applyFilters = () => {
    setCursorHistory([undefined]);
    execute(undefined);
  };

  const unifiedItems = unifiedQuery.data?.items ?? [];
  const displayData = unifiedQuery.data ? { items: unifiedItems, nextCursor: unifiedQuery.data.next_cursor } : undefined;
  const displayPending = unifiedQuery.isPending;
  const displayError = unifiedQuery.error;
  const accountRows: AccountListRow[] = unifiedItems.map((item) => ({
    account_key: item.account_key, email: item.email, provider: item.provider,
    basic_status: item.inventory.basic_status, lifecycle: item.inventory.lifecycle, quality: item.quality,
    request_count: item.request_count, success_count: item.success_count, failure_count: item.failure_count, success_rate: item.success_rate, p95_latency_ms: item.p95_latency_ms,
    last_success_at: item.last_success_at, last_failure_at: item.last_failure_at, last_failure_class: item.last_failure_class, recent_requests: item.recent_requests,
    consecutive_missing_count: item.inventory.consecutive_missing_count, first_seen_at: item.inventory.first_seen_at, last_seen_at: item.inventory.last_seen_at,
    last_refresh_at: item.inventory.last_refresh_at, next_retry_at: item.inventory.next_retry_at, provider_last_complete_at: item.inventory.provider_last_complete_at,
    provider_degraded: item.inventory.provider_degraded, snapshot_freshness: item.inventory.snapshot_freshness,
  }));

  const currentNode = nodes.data?.items.find((node) => node.instanceId === instanceId);
  const openTopology = () => { if (!instanceId) return; window.history.pushState(null, "", `/topology?instance_id=${encodeURIComponent(instanceId)}`); window.dispatchEvent(new PopStateEvent("popstate")); };
  const failure = displayError instanceof AccountInventoryApiError || displayError instanceof TopologyApiError ? displayError : null;

  return (
    <Flex vertical gap={16} data-testid="account-inventory-view">
      {api.capacity && <Card title="采集容量诊断" data-testid="account-inventory-capacity">
        <Flex justify="space-between" align="center" gap={12} wrap>
          <Text type="secondary">当前环境采集条件评估，不代表最近一次采集成功</Text>
          <Button onClick={() => capacity.mutate()} loading={capacity.isPending}>刷新容量</Button>
        </Flex>
        {capacity.isPending && <Flex justify="center" style={{ marginTop: 12 }}><Spin /></Flex>}
        {capacity.error && <Alert style={{ marginTop: 12 }} type="error" showIcon message="容量诊断暂不可用" />}
        {!capacity.error && capacity.data && <CapacitySummary value={capacity.data} />}
      </Card>}
      <Card title="账号筛选">
        {nodes.isPending && <Flex justify="center"><Spin /></Flex>}
        {nodes.error && <Alert type="error" showIcon message="Node 清单读取失败" description="Control 暂时无法读取资产注册表。" action={<Button onClick={() => void nodes.refetch()}>重试</Button>} />}
        {!nodes.isPending && !nodes.error && nodes.data?.items.length === 0 && <Empty description="当前没有支持账号清单能力的 Node" />}
        {!nodes.error && nodes.data && nodes.data.items.length > 0 && (
          <Flex vertical gap={12}>
            <Space wrap aria-label="账号清单过滤器">
              <Select
                aria-label="Relay Node"
                placeholder="选择 Relay Node"
                value={instanceId}
                disabled={displayPending}
                options={nodes.data.items.map((node) => ({ value: node.instanceId, label: `${node.displayName} · ${node.instanceId}` }))}
                onChange={(value) => { setInstanceId(value); resetResult(); }}
                style={{ minWidth: 300 }}
              />
              <Input aria-label="Provider 精确筛选" placeholder="Provider（精确）" value={provider} disabled={displayPending} maxLength={64} onChange={(event) => { setProvider(event.target.value); resetResult(); }} style={{ width: 190 }} />
              <Select aria-label="生命周期" allowClear placeholder="全部生命周期" value={lifecycle} disabled={displayPending} options={accountInventoryLifecycles.map((value) => ({ value, label: value }))} onChange={(value) => { setLifecycle(value); resetResult(); }} style={{ width: 200 }} />
              <Select aria-label="最后报告基础状态" allowClear placeholder="全部基础状态" value={basicStatus} disabled={displayPending} options={accountInventoryBasicStatuses.map((value) => ({ value, label: value }))} onChange={(value) => { setBasicStatus(value); resetResult(); }} style={{ width: 210 }} />
              <Input aria-label="邮箱精确筛选" placeholder="邮箱（精确匹配）" value={email} disabled={displayPending} maxLength={320} autoComplete="off" onChange={(event) => { setEmail(event.target.value); resetResult(); }} style={{ width: 260 }} />
              <Select aria-label="质量窗口" value={qualityWindow} disabled={displayPending} options={[{ value: "15m", label: "最近 15 分钟" }, { value: "1h", label: "最近 1 小时" }]} onChange={(value) => { setQualityWindow(value); resetResult(); }} style={{ width: 150 }} />
              <Select allowClear aria-label="质量分类" placeholder="全部质量" value={qualityFilter} disabled={displayPending} options={["good", "degraded", "bad", "unknown"].map((value) => ({ value, label: value }))} onChange={(value) => { setQualityFilter(value); resetResult(); }} style={{ width: 150 }} />
              <Select<PageSize> aria-label="每页账号数" value={pageSize} disabled={displayPending} options={[25, 50, 100].map((value) => ({ value: value as PageSize, label: `${value} / 页` }))} onChange={(value) => { setPageSize(value); resetResult(); }} style={{ width: 120 }} />
            <Button type="primary" disabled={!instanceId} loading={displayPending} onClick={applyFilters}>查询</Button>
            <Button disabled={!instanceId} onClick={openTopology}>查看 Node Topology</Button>
            </Space>
            <Text type="secondary">完整邮箱仅供实名管理员定位；每次查询（包括空结果和翻页）都会写入查看审计。</Text>
          </Flex>
        )}
      </Card>

      <Card title={currentNode ? `${currentNode.displayName} 当前账号` : "当前账号"} data-testid="account-inventory-card">
        {!instanceId && <Empty description="请先选择一个支持账号清单能力的 Node" />}
        {instanceId && !displayData && !displayPending && !displayError && <Empty description="设置筛选后点击查询" />}
        {displayPending && <Flex justify="center"><Spin /></Flex>}
        {failure && <Alert type="error" showIcon message="账号清单读取失败" description={errorDescription(failure)} action={<Button onClick={() => execute(cursorHistory.at(-1))}>重试</Button>} />}
        {displayError && !failure && <Alert type="error" showIcon message="账号清单读取失败" description="Control 暂时无法完成查询。" action={<Button onClick={() => execute(cursorHistory.at(-1))}>重试</Button>} />}
        {!displayError && displayData?.items.length === 0 && <Empty description="当前过滤条件下没有账号" />}
        {!displayError && displayData && displayData.items.length > 0 && (
          <AccountList rows={accountRows} onSelectAccount={setDetailsRow} />
        )}
        {instanceId && displayData && !displayError && (
          <Flex justify="end" gap={8} style={{ marginTop: 16 }}>
            <Button disabled={cursorHistory.length === 1 || displayPending} onClick={() => {
              const previous = cursorHistory.slice(0, -1);
              setCursorHistory(previous);
              execute(previous.at(-1));
            }}>上一页</Button>
            <Button disabled={!displayData.nextCursor || displayPending} onClick={() => {
              if (!displayData?.nextCursor) return;
              const next = displayData.nextCursor;
              setCursorHistory((history) => [...history, next]);
              execute(next);
            }}>下一页</Button>
          </Flex>
        )}
      </Card>
      <AccountDetailsDrawer key={instanceId} api={accountListApi} instanceId={instanceId} row={detailsRow} onClose={() => setDetailsRow(undefined)} onUnauthorized={onUnauthorized} />
    </Flex>
  );
}

function CapacitySummary({ value }: { value: AccountInventoryPollCapacity }) {
  return <Flex vertical gap={8} style={{ marginTop: 12 }} data-testid="account-inventory-capacity-summary">
    {value.status === "capacity_exceeded" && <Alert type="warning" showIcon message="监控规模超过采集容量，整轮新采集暂停" description="请调整采集并发，或通过监控管理流程减少监控 Node；完成后刷新容量诊断。已有账号证据保留。" />}

    <Space wrap>
      <Tag color={value.status === "ready" ? "green" : value.status === "capacity_exceeded" ? "orange" : "default"}>{capacityStatusLabel(value.status)}</Tag>
      <Text>启用：{value.enabled ? "是" : "否"}</Text>
      <Text>符合采集条件的 Node：{value.eligibleNodeCount}</Text>
      <Text>有效容量：{value.effectiveCapacity}</Text>
      <Text>并发：{value.concurrency}</Text>
    </Space>
    <Space wrap>
      <Text type="secondary">请求 {value.requestTimeoutMs}ms</Text>
      <Text type="secondary">落库预算 {value.finalizeTimeoutMs}ms</Text>
      <Text type="secondary">生命周期 {value.lifecycleTimeoutMs}ms</Text>
      <Text type="secondary">任务认领预算 {value.claimTimeoutMs}ms</Text>
      <Text type="secondary">调度余量 {value.dispatchMarginMs}ms</Text>
      <Text type="secondary">启动宽限 {value.pollStartGraceMs}ms</Text>
    </Space>
    <Text type="secondary">评估槽位：{value.evaluatedSlot}；时间：{utc(value.evaluatedAt)}</Text>
  </Flex>;
}
