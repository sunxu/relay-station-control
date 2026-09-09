import { Alert, Button, Descriptions, Drawer, Empty, Flex, Select, Spin, Table, Tabs, Tag, Typography } from "antd";
import type { AccountRequestHistoryApi } from "../api/account-request-history-types";
import { AccountRequestHistorySection } from "../pages/AccountRequestHistorySection";
import type { AccountListRow } from "./AccountList";
import { formatDateTime } from "../time";
import { useAccountAvailabilityOccurrences } from "../api/account-availability-hooks";
import type { AccountAvailabilityApi, AccountAvailabilityOccurrence, AccountAvailabilityOccurrenceStatus } from "../api/account-availability-types";
import { useEffect, useState } from "react";

const { Text } = Typography;

export function AccountDetailsDrawer({ api, instanceId, row, accountKey, onClose, onUnauthorized }: {
  api: AccountRequestHistoryApi & AccountAvailabilityApi;
  instanceId?: string;
  row?: AccountListRow;
  accountKey?: string;
  onClose: () => void;
  onUnauthorized: () => void;
}) {
  const identity = row?.account_key ?? accountKey;
  return <Drawer title={identity ? `账号详情 · ${row?.email || identity}` : "账号详情"} open={Boolean(identity && instanceId)} onClose={onClose} width={720} destroyOnHidden>
    {identity && instanceId && <>
      <Tabs items={[{ key: "history", label: "请求历史", children: <AccountRequestHistorySection key={`${instanceId}:${identity}`} api={api} instanceId={instanceId} accountKey={identity} onUnauthorized={onUnauthorized} /> }, { key: "availability", label: "可用性事件", children: <AvailabilityOccurrences key={`${instanceId}:${identity}`} api={api} instanceId={instanceId} accountKey={identity} onUnauthorized={onUnauthorized} /> }, { key: "inventory", label: "采集信息", children: row ? <Descriptions column={2} size="small" bordered>
        <Descriptions.Item label="Account key"><Text copyable>{row.account_key}</Text></Descriptions.Item>
        <Descriptions.Item label="Provider"><Tag>{row.provider}</Tag></Descriptions.Item>
        <Descriptions.Item label="生命周期">{row.lifecycle}</Descriptions.Item>
        <Descriptions.Item label="基础状态">{row.basic_status}</Descriptions.Item>
        <Descriptions.Item label="质量"><Tag>{row.quality}</Tag></Descriptions.Item>
        <Descriptions.Item label="连续缺失">{row.consecutive_missing_count ?? 0}</Descriptions.Item>
        <Descriptions.Item label="首次出现">{formatDateTime(row.first_seen_at)}</Descriptions.Item>
        <Descriptions.Item label="最近出现">{formatDateTime(row.last_seen_at)}</Descriptions.Item>
        <Descriptions.Item label="最近刷新">{formatDateTime(row.last_refresh_at)}</Descriptions.Item>
        <Descriptions.Item label="下次重试">{formatDateTime(row.next_retry_at)}</Descriptions.Item>
        <Descriptions.Item label="Provider 快照">{formatDateTime(row.provider_last_complete_at)}</Descriptions.Item>
        <Descriptions.Item label="Snapshot freshness">{row.snapshot_freshness ?? "—"}</Descriptions.Item>
        <Descriptions.Item label="Provider health">{row.provider_degraded ? <Tag color="orange">degraded</Tag> : <Tag color="green">normal</Tag>}</Descriptions.Item>
        <Descriptions.Item label="Availability">{row.availability?.state ?? "—"}</Descriptions.Item>
        <Descriptions.Item label="Availability reason">{row.availability?.reason ?? "—"}</Descriptions.Item>
        <Descriptions.Item label="Availability since">{formatDateTime(row.availability?.since)}</Descriptions.Item>
        {(row.availability?.state === "UNKNOWN" || row.availability?.state === "DISABLED") && <Descriptions.Item span={2}><Text type="secondary">当前可用性为 {row.availability.state}；既有 ACTIVE 可用性事件仍保留在事件历史中。</Text></Descriptions.Item>}
      </Descriptions> : <Typography.Text type="secondary">该账号不在当前列表页，采集信息未加载。</Typography.Text> }]}/>
    </>}
  </Drawer>;
}

function AvailabilityOccurrences({ api, instanceId, accountKey, onUnauthorized }: {
  api: AccountAvailabilityApi;
  instanceId: string;
  accountKey: string;
  onUnauthorized: () => void;
}) {
  const [status, setStatus] = useState<AccountAvailabilityOccurrenceStatus>("ACTIVE");
  const [cursor, setCursor] = useState<string>();
  const query = useAccountAvailabilityOccurrences(api, instanceId, accountKey, status, cursor);
  useEffect(() => {
    if (query.error instanceof Error && "status" in query.error && query.error.status === 401) onUnauthorized();
  }, [query.error, onUnauthorized]);
  const columns = [
    { title: "原因", dataIndex: "reason" },
    { title: "严重度", dataIndex: "severity", render: (value: string) => <Tag color={value === "Critical" ? "red" : "orange"}>{value}</Tag> },
    { title: "状态", dataIndex: "status" },
    { title: "首次发现", dataIndex: "first_seen_at", render: formatDateTime },
    { title: "最近失败", dataIndex: "last_failure_at", render: formatDateTime },
    { title: "确认时间", dataIndex: "confirmed_at", render: formatDateTime },
    { title: "恢复时间", dataIndex: "resolved_at", render: formatDateTime },
  ];
  return <Flex vertical gap={8}>
    <Select aria-label="可用性事件状态" value={status} onChange={(value: AccountAvailabilityOccurrenceStatus) => { setStatus(value); setCursor(undefined); }} options={[{ value: "ACTIVE", label: "Active" }, { value: "RESOLVED", label: "Resolved" }]} />
    {query.isPending && <Flex role="status" aria-label="正在读取可用性事件" justify="center"><Spin /></Flex>}
    {query.error && !query.isPending && <Alert type="error" message="读取不可用（unavailable）" action={<Button onClick={() => void query.refetch()}>重试</Button>} />}
    {query.data && !query.error && (query.data.items.length === 0 ? <Empty description={status === "ACTIVE" ? "当前没有 Active 可用性事件" : "当前没有 Resolved 可用性事件"} /> : <Table<AccountAvailabilityOccurrence> size="small" pagination={false} rowKey="occurrence_id" dataSource={query.data.items} columns={columns} scroll={{ x: 900 }} />)}
    {query.data && !query.error && <Flex justify="end" gap={8}><Button disabled={!cursor} onClick={() => setCursor(undefined)}>可用性首页</Button><Button disabled={!query.data.next_cursor || query.isFetching} onClick={() => setCursor(query.data.next_cursor ?? undefined)}>可用性下一页</Button></Flex>}
  </Flex>;
}
