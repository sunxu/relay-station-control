import { useEffect, useMemo, useState } from "react";
import { Alert, Button, Card, Empty, Flex, Input, Select, Space, Spin, Table, Tag, Typography } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useAccountInventoryQuery } from "../api/account-inventory-hooks";
import {
  AccountInventoryApiError,
  accountInventoryBasicStatuses,
  accountInventoryLifecycles,
} from "../api/account-inventory-types";
import type {
  AccountInventoryApi,
  AccountInventoryBasicStatus,
  AccountInventoryFilters,
  AccountInventoryItem,
  AccountInventoryLifecycle,
} from "../api/account-inventory-types";
import { useNodeAssets } from "../api/asset-hooks";
import type { AssetApi } from "../api/asset-types";

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

function lifecycleColor(value: AccountInventoryLifecycle): string {
  if (value === "present") return "green";
  if (value === "suspected_missing") return "gold";
  if (value === "missing") return "red";
  return "default";
}

function errorDescription(error: AccountInventoryApiError): string {
  if (error.status === 403) return "安全凭据已变化。请刷新会话后重新提交，本次查询不会自动重放。";
  if (error.status === 404) return "所选 Node 不存在或已不属于当前环境。";
  if (error.status === 409) return "所选 Node 不支持账号清单查询。";
  if (error.status === 400) return "筛选条件或分页凭据无效，请清除筛选并重新查询。";
  return `Control 暂时无法读取账号清单（请求 ID：${error.detail.request_id}）。`;
}

export function AccountInventoryView({
  api,
  assetApi,
  csrfToken,
  onUnauthorized,
}: {
  api: AccountInventoryApi;
  assetApi: AssetApi;
  csrfToken: string;
  onUnauthorized: () => void;
}) {
  const nodes = useNodeAssets(assetApi, nodeFilters);
  const query = useAccountInventoryQuery(api, csrfToken);
  const [instanceId, setInstanceId] = useState<string>();
  const [provider, setProvider] = useState("");
  const [lifecycle, setLifecycle] = useState<AccountInventoryLifecycle>();
  const [basicStatus, setBasicStatus] = useState<AccountInventoryBasicStatus>();
  const [email, setEmail] = useState("");
  const [pageSize, setPageSize] = useState<PageSize>(50);
  const [cursorHistory, setCursorHistory] = useState<Array<string | undefined>>([undefined]);

  useEffect(() => {
    if (query.error instanceof AccountInventoryApiError && query.error.status === 401) onUnauthorized();
  }, [query.error, onUnauthorized]);

  useEffect(() => () => query.reset(), []); // Component state is memory-only and is discarded on unmount.

  const execute = (cursor?: string) => {
    if (!instanceId) return;
    const filters: AccountInventoryFilters = {
      instanceId,
      provider: provider.trim().toLowerCase() || undefined,
      lifecycle,
      basicStatus,
      email: email.trim().toLowerCase() || undefined,
      cursor,
      limit: pageSize,
    };
    query.mutate(filters);
  };

  const resetResult = () => {
    setCursorHistory([undefined]);
    query.reset();
  };

  const applyFilters = () => {
    setCursorHistory([undefined]);
    execute(undefined);
  };

  const columns = useMemo<ColumnsType<AccountInventoryItem>>(() => [
    { title: "邮箱", dataIndex: "email", key: "email", render: (value: string) => <Text>{value}</Text> },
    { title: "Provider", dataIndex: "provider", key: "provider", render: (value: string) => <Tag>{value}</Tag> },
    { title: "最后报告基础状态", dataIndex: "basicStatus", key: "basicStatus", render: (value: string) => <Tag>{value}</Tag> },
    { title: "生命周期", dataIndex: "lifecycle", key: "lifecycle", render: (value: AccountInventoryLifecycle, item) => <Space orientation="vertical" size={2}><Tag color={lifecycleColor(value)}>{value}</Tag>{item.consecutiveMissingCount > 0 && <Text type="secondary">连续缺失 {item.consecutiveMissingCount}</Text>}</Space> },
    { title: "最近出现", dataIndex: "lastSeenAt", key: "lastSeenAt", render: utc },
    { title: "最近刷新", dataIndex: "lastRefreshAt", key: "lastRefreshAt", render: utc },
    { title: "下次重试", dataIndex: "nextRetryAt", key: "nextRetryAt", render: utc },
    { title: "Provider 快照", key: "providerSnapshot", render: (_, item) => <Space orientation="vertical" size={2}><Text>{utc(item.providerLastCompleteAt)}</Text><Space wrap><Tag color={item.snapshotFreshness === "fresh" ? "green" : item.snapshotFreshness === "stale" ? "red" : "default"}>{item.snapshotFreshness}</Tag>{item.providerDegraded && <Tag color="orange">degraded</Tag>}</Space></Space> },
  ], []);

  const currentNode = nodes.data?.items.find((node) => node.instanceId === instanceId);
  const openTopology = () => { if (!instanceId) return; window.history.pushState(null, "", `/topology?instance_id=${encodeURIComponent(instanceId)}`); window.dispatchEvent(new PopStateEvent("popstate")); };
  const failure = query.error instanceof AccountInventoryApiError ? query.error : null;

  return (
    <Flex vertical gap={16} data-testid="account-inventory-view">
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
                disabled={query.isPending}
                options={nodes.data.items.map((node) => ({ value: node.instanceId, label: `${node.displayName} · ${node.instanceId}` }))}
                onChange={(value) => { setInstanceId(value); resetResult(); }}
                style={{ minWidth: 300 }}
              />
              <Input aria-label="Provider 精确筛选" placeholder="Provider（精确）" value={provider} disabled={query.isPending} maxLength={64} onChange={(event) => { setProvider(event.target.value); resetResult(); }} style={{ width: 190 }} />
              <Select aria-label="生命周期" allowClear placeholder="全部生命周期" value={lifecycle} disabled={query.isPending} options={accountInventoryLifecycles.map((value) => ({ value, label: value }))} onChange={(value) => { setLifecycle(value); resetResult(); }} style={{ width: 200 }} />
              <Select aria-label="最后报告基础状态" allowClear placeholder="全部基础状态" value={basicStatus} disabled={query.isPending} options={accountInventoryBasicStatuses.map((value) => ({ value, label: value }))} onChange={(value) => { setBasicStatus(value); resetResult(); }} style={{ width: 210 }} />
              <Input aria-label="邮箱精确筛选" placeholder="邮箱（精确匹配）" value={email} disabled={query.isPending} maxLength={320} autoComplete="off" onChange={(event) => { setEmail(event.target.value); resetResult(); }} style={{ width: 260 }} />
              <Select<PageSize> aria-label="每页账号数" value={pageSize} disabled={query.isPending} options={[25, 50, 100].map((value) => ({ value: value as PageSize, label: `${value} / 页` }))} onChange={(value) => { setPageSize(value); resetResult(); }} style={{ width: 120 }} />
            <Button type="primary" disabled={!instanceId} loading={query.isPending} onClick={applyFilters}>查询</Button>
            <Button disabled={!instanceId} onClick={openTopology}>查看 Node Topology</Button>
            </Space>
            <Text type="secondary">完整邮箱仅供实名管理员定位；每次查询（包括空结果和翻页）都会写入查看审计。</Text>
          </Flex>
        )}
      </Card>

      <Card title={currentNode ? `${currentNode.displayName} 当前账号` : "当前账号"} data-testid="account-inventory-card">
        {!instanceId && <Empty description="请先选择一个支持账号清单能力的 Node" />}
        {instanceId && !query.data && !query.isPending && !query.error && <Empty description="设置筛选后点击查询" />}
        {query.isPending && <Flex justify="center"><Spin /></Flex>}
        {failure && <Alert type="error" showIcon message="账号清单读取失败" description={errorDescription(failure)} action={<Button onClick={() => execute(cursorHistory.at(-1))}>重试</Button>} />}
        {query.error && !failure && <Alert type="error" showIcon message="账号清单读取失败" description="Control 暂时无法完成查询。" />}
        {!query.error && query.data?.items.length === 0 && <Empty description="当前过滤条件下没有账号" />}
        {!query.error && query.data && query.data.items.length > 0 && (
          <Table<AccountInventoryItem>
            rowKey={(item) => `${item.instanceId}:${item.provider}:${item.email}`}
            size="small"
            scroll={{ x: 1450 }}
            pagination={false}
            dataSource={query.data.items}
            columns={columns}
          />
        )}
        {instanceId && query.data && !query.error && (
          <Flex justify="end" gap={8} style={{ marginTop: 16 }}>
            <Button disabled={cursorHistory.length === 1 || query.isPending} onClick={() => {
              const previous = cursorHistory.slice(0, -1);
              setCursorHistory(previous);
              execute(previous.at(-1));
            }}>上一页</Button>
            <Button disabled={!query.data.nextCursor || query.isPending} onClick={() => {
              if (!query.data?.nextCursor) return;
              const next = query.data.nextCursor;
              setCursorHistory((history) => [...history, next]);
              execute(next);
            }}>下一页</Button>
          </Flex>
        )}
      </Card>
    </Flex>
  );
}
