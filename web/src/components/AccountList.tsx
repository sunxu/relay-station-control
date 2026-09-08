import { Button, Empty, Flex, Spin, Table, Tag, Tooltip, Typography } from "antd";
import type { ColumnsType } from "antd/es/table";
import { formatDateTime } from "../time";

const { Text } = Typography;

export interface AccountListRecentRequest {
  occurred_at: string;
  model: string;
  success: boolean;
  failure_class: string | null;
  duration_ms: number | null;
  request_id: string;
}

export interface AccountListRow {
  account_key: string;
  email: string;
  provider: string;
  basic_status: string;
  lifecycle: string;
  quality: "good" | "degraded" | "bad" | "unknown";
  request_count: number;
  success_count: number;
  failure_count: number;
  success_rate: number | null;
  p95_latency_ms: number | null;
  last_success_at: string | null;
  last_failure_at: string | null;
  last_failure_class: string | null;
  recent_requests: AccountListRecentRequest[];
  consecutive_missing_count?: number;
  first_seen_at?: string;
  last_seen_at?: string;
  last_refresh_at?: string | null;
  next_retry_at?: string | null;
  provider_last_complete_at?: string | null;
  provider_degraded?: boolean;
  snapshot_freshness?: string;
}

export interface AccountListProps {
  rows: AccountListRow[];
  loading?: boolean;
  unavailable?: boolean;
  onSelectAccount?: (row: AccountListRow) => void;
  onRetry?: () => void;
  emptyDescription?: string;
}

function qualityColor(value: AccountListRow["quality"]): string {
  return value === "good" ? "green" : value === "degraded" ? "orange" : value === "bad" ? "red" : "default";
}

function qualityLabel(value: AccountListRow["quality"]): string {
  return value === "good" ? "Good" : value === "degraded" ? "Degraded" : value === "bad" ? "Bad" : "Unknown";
}

function RecentRequestStrip({ requests }: { requests: AccountListRecentRequest[] }) {
  const ordered = requests.slice(0, 10).reverse();
  if (ordered.length === 0) return <Text type="secondary">无请求</Text>;
  return <Flex gap={3} align="center" aria-label="最近 7 天最近 10 次请求" data-testid="recent-request-strip">
    {ordered.map((request, index) => <Tooltip key={`${request.occurred_at}:${request.request_id}:${index}`} title={`${formatDateTime(request.occurred_at)} · ${request.model || "—"} · ${request.success ? "Success" : `Failed${request.failure_class ? ` · ${request.failure_class}` : ""}`} · ${request.duration_ms == null ? "—" : `${request.duration_ms} ms`}`}><Tag
      color={request.success ? "green" : "red"}
      title={`${formatDateTime(request.occurred_at)} · ${request.model || "—"} · ${request.success ? "Success" : `Failed${request.failure_class ? ` · ${request.failure_class}` : ""}`} · ${request.duration_ms == null ? "—" : `${request.duration_ms} ms`}`}
      aria-label={`${request.success ? "Success" : "Failed"}${request.failure_class ? ` ${request.failure_class}` : ""}`}
      style={{ width: 9, height: 18, padding: 0, margin: 0, borderRadius: 2 }}
      tabIndex={0}
    /></Tooltip>)}
  </Flex>;
}

export function AccountList({ rows, loading = false, unavailable = false, onSelectAccount, onRetry, emptyDescription = "当前过滤条件下没有账号" }: AccountListProps) {
  const columns: ColumnsType<AccountListRow> = [
    { title: "账号", key: "account", render: (_, row) => <Flex vertical><Text strong>{row.email || row.account_key}</Text><Text type="secondary" style={{ overflowWrap: "anywhere" }}>{row.account_key}</Text></Flex> },
    { title: "Provider", dataIndex: "provider", render: (value: string) => <Tag>{value}</Tag> },
    { title: "状态", key: "status", render: (_, row) => <Flex vertical gap={2}><Tag>{row.lifecycle}</Tag><Text type="secondary">{row.basic_status}</Text></Flex> },
    { title: "质量", dataIndex: "quality", render: (value: AccountListRow["quality"]) => <Tag color={qualityColor(value)}>{qualityLabel(value)}</Tag> },
    { title: "最近请求", key: "recent", render: (_, row) => <Flex vertical gap={4}><RecentRequestStrip requests={row.recent_requests} />{row.recent_requests[0] && <Text type="secondary">{formatDateTime(row.recent_requests[0].occurred_at)}</Text>}</Flex> },
    { title: "成功率", dataIndex: "success_rate", render: (value: number | null) => value == null ? "—" : `${(value * 100).toFixed(1)}%` },
    { title: "Requests", dataIndex: "request_count" },
    { title: "P95", dataIndex: "p95_latency_ms", render: (value: number | null) => value == null ? "—" : `${value} ms` },
    { title: "最近失败", key: "failure", render: (_, row) => <Flex vertical><Text>{row.last_failure_class ?? "—"}</Text><Text type="secondary">{formatDateTime(row.last_failure_at)}</Text></Flex> },
    ...(onSelectAccount ? [{ title: "详情", key: "details", render: (_: unknown, row: AccountListRow) => <Button type="link" onClick={() => onSelectAccount(row)}>查看详情</Button> }] : []),
  ];

  if (loading) return <Flex justify="center" role="status" aria-label="正在读取账号"><Spin /></Flex>;
  if (unavailable) return <Flex vertical align="center" gap={8}><Text type="danger">读取不可用（unavailable）</Text>{onRetry && <Button onClick={onRetry}>重试</Button>}</Flex>;
  if (rows.length === 0) return <Empty description={emptyDescription} />;
  return <Flex vertical gap={8}><Text type="secondary">最近请求：7 天内最新 10 次，左旧右新；成功绿色，失败红色。请求质量按所选窗口独立计算。</Text><Table<AccountListRow> rowKey="account_key" size="small" scroll={{ x: 1350 }} pagination={false} dataSource={rows} columns={columns} /></Flex>;
}
