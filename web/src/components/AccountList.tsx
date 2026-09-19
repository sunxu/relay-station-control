import { Button, Empty, Flex, Spin, Table, Tag, Tooltip, Typography } from "antd";
import type { ColumnsType } from "antd/es/table";
import { formatDateTime } from "../foundation/format";
import { useOptionalAppLocale } from "../foundation/FrontendFoundationProvider";
import { resources } from "../foundation/resources";
import type { TranslationResource } from "../foundation/resources";
import type { AccountAvailability } from "../api/account-availability-types";
import type { NodeAccountQualityItemTokenState } from "../api/generated/control";

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
  availability?: AccountAvailability | null;
  token_state?: NodeAccountQualityItemTokenState;
  expected_valid_until?: string | null;
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

function qualityLabel(value: AccountListRow["quality"], copy: TranslationResource["accounts"]): string {
  return value === "good" ? copy.good : value === "degraded" ? copy.degraded : value === "bad" ? copy.bad : copy.unknown;
}

function RecentRequestStrip({ requests, locale, copy }: { requests: AccountListRecentRequest[]; locale: "zh-CN" | "en"; copy: TranslationResource["accounts"] }) {
  const ordered = requests.slice(0, 10).reverse();
  if (ordered.length === 0) return <Text type="secondary">{copy.noRequests}</Text>;
  return <Flex gap={3} align="center" aria-label={copy.recentRequestsAria} data-testid="recent-request-strip">
    {ordered.map((request, index) => <Tooltip key={`${request.occurred_at}:${request.request_id}:${index}`} title={`${formatDateTime(request.occurred_at, locale)} · ${request.model || "—"} · ${request.success ? copy.success : `${copy.failed}${request.failure_class ? ` · ${request.failure_class}` : ""}`} · ${request.duration_ms == null ? "—" : `${request.duration_ms} ms`}`}><Tag
      color={request.success ? "green" : "red"}
      title={`${formatDateTime(request.occurred_at, locale)} · ${request.model || "—"} · ${request.success ? copy.success : `${copy.failed}${request.failure_class ? ` · ${request.failure_class}` : ""}`} · ${request.duration_ms == null ? "—" : `${request.duration_ms} ms`}`}
      aria-label={`${request.success ? copy.success : copy.failed}${request.failure_class ? ` ${request.failure_class}` : ""}`}
      style={{ width: 9, height: 18, padding: 0, margin: 0, borderRadius: 2 }}
      tabIndex={0}
    /></Tooltip>)}
  </Flex>;
}

export function AccountList({ rows, loading = false, unavailable = false, onSelectAccount, onRetry, emptyDescription }: AccountListProps) {
  const locale = useOptionalAppLocale()?.locale ?? "zh-CN";
  const copy = resources[locale].translation.accounts;
  const columns: ColumnsType<AccountListRow> = [
    { title: copy.account, key: "account", render: (_, row) => <Flex vertical><Text strong>{row.email || row.account_key}</Text><Text type="secondary" style={{ overflowWrap: "anywhere" }}>{row.account_key}</Text></Flex> },
    { title: copy.provider, dataIndex: "provider", render: (value: string) => <Tag>{value}</Tag> },
    { title: copy.status, key: "status", render: (_, row) => <Flex vertical gap={2}><Tag>{row.lifecycle}</Tag><Text type="secondary">{row.basic_status}</Text></Flex> },
    { title: copy.quality, dataIndex: "quality", render: (value: AccountListRow["quality"]) => <Tag color={qualityColor(value)}>{qualityLabel(value, copy)}</Tag> },
    { title: copy.availability, key: "availability", render: (_, row) => row.availability ? <Tag color={row.availability.state === "AVAILABLE" ? "green" : row.availability.state === "UNKNOWN" || row.availability.state === "DISABLED" ? "default" : "red"}>{row.availability.state}</Tag> : "—" },
    { title: copy.reason, key: "availability-reason", render: (_, row) => row.availability?.reason || "—" },
    { title: copy.since, key: "availability-since", render: (_, row) => formatDateTime(row.availability?.since, locale) },
    { title: copy.recentRequests, key: "recent", render: (_, row) => <Flex vertical gap={4}><RecentRequestStrip requests={row.recent_requests} locale={locale} copy={copy} />{row.recent_requests[0] && <Text type="secondary">{formatDateTime(row.recent_requests[0].occurred_at, locale)}</Text>}</Flex> },
    { title: copy.successRate, dataIndex: "success_rate", render: (value: number | null) => value == null ? "—" : `${(value * 100).toFixed(1)}%` },
    { title: copy.requests, dataIndex: "request_count" },
    { title: copy.p95, dataIndex: "p95_latency_ms", render: (value: number | null) => value == null ? "—" : `${value} ms` },
    { title: copy.recentFailure, key: "failure", render: (_, row) => <Flex vertical><Text>{row.last_failure_class ?? "—"}</Text><Text type="secondary">{formatDateTime(row.last_failure_at, locale)}</Text></Flex> },
    ...(onSelectAccount ? [{ title: copy.details, key: "details", render: (_: unknown, row: AccountListRow) => <Button data-testid={`account-details-${encodeURIComponent(row.account_key)}`} type="link" onClick={() => onSelectAccount(row)}>{copy.viewDetails}</Button> }] : []),
  ];

  if (loading) return <Flex justify="center" role="status" aria-label={copy.readingAccounts}><Spin /></Flex>;
  if (unavailable) return <Flex vertical align="center" gap={8}><Text type="danger">{copy.unavailable}</Text>{onRetry && <Button onClick={onRetry}>{copy.retry}</Button>}</Flex>;
  if (rows.length === 0) return <Empty description={emptyDescription ?? copy.noAccounts} />;
  return <Flex vertical gap={8}><Text type="secondary">{copy.recentRequestsDescription}</Text><Table<AccountListRow> rowKey="account_key" size="small" scroll={{ x: 1350 }} pagination={false} dataSource={rows} columns={columns} /></Flex>;
}
