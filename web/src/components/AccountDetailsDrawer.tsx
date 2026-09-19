import { Alert, Button, Descriptions, Drawer, Empty, Flex, Select, Spin, Table, Tabs, Tag, Typography } from "antd";
import type { AccountRequestHistoryApi } from "../api/account-request-history-types";
import { AccountRequestHistorySection } from "../pages/AccountRequestHistorySection";
import type { AccountListRow } from "./AccountList";
import { formatDateTime } from "../foundation/format";
import { useOptionalAppLocale } from "../foundation/FrontendFoundationProvider";
import { resources } from "../foundation/resources";
import { useAccountAvailabilityOccurrences } from "../api/account-availability-hooks";
import type { AccountAvailabilityApi, AccountAvailabilityOccurrence, AccountAvailabilityOccurrenceStatus } from "../api/account-availability-types";
import { useEffect, useState } from "react";
import type { AccountOperationsApi } from "../api/account-operations-api";
import { AccountOperationsPanel } from "./AccountOperationsPanel";

const { Text } = Typography;

export function AccountDetailsDrawer({ api, accountOperationsApi, csrfToken, instanceId, row, accountKey, onClose, onUnauthorized }: {
  api: AccountRequestHistoryApi & AccountAvailabilityApi;
  accountOperationsApi?: AccountOperationsApi;
  csrfToken?: string;
  instanceId?: string;
  row?: AccountListRow;
  accountKey?: string;
  onClose: () => void;
  onUnauthorized: () => void;
}) {
  const identity = row?.account_key ?? accountKey;
  const locale = useOptionalAppLocale()?.locale ?? "zh-CN";
  const copy = resources[locale].translation.accounts;
  return <Drawer title={identity ? `${copy.accountDetails} · ${row?.email || identity}` : copy.accountDetails} open={Boolean(identity && instanceId)} onClose={onClose} width={720} destroyOnHidden>
    {identity && instanceId && <>
      <Tabs items={[{ key: "history", label: <span data-testid="account-request-history-tab">{copy.requestHistory}</span>, children: <AccountRequestHistorySection key={`${instanceId}:${identity}`} api={api} instanceId={instanceId} accountKey={identity} onUnauthorized={onUnauthorized} /> }, { key: "availability", label: <span data-testid="account-availability-tab">{copy.availabilityEvents}</span>, children: <AvailabilityOccurrences key={`${instanceId}:${identity}`} api={api} instanceId={instanceId} accountKey={identity} onUnauthorized={onUnauthorized} /> }, ...(accountOperationsApi && csrfToken ? [{ key: "operations", label: <span data-testid="account-operations-tab">{copy.accountOperations}</span>, children: <AccountOperationsPanel api={accountOperationsApi} csrf={csrfToken} nodeInstanceId={instanceId} accountKey={identity} basicStatus={row?.basic_status} onUnauthorized={onUnauthorized} /> }] : []), { key: "inventory", label: <span data-testid="account-inventory-tab">{copy.inventory}</span>, children: row ? <Descriptions column={2} size="small" bordered>
        <Descriptions.Item label={copy.accountKey}><Text copyable>{row.account_key}</Text></Descriptions.Item>
        <Descriptions.Item label={copy.provider}><Tag>{row.provider}</Tag></Descriptions.Item>
        <Descriptions.Item label={copy.lifecycle}>{row.lifecycle}</Descriptions.Item>
        <Descriptions.Item label={copy.basicStatus}>{row.basic_status}</Descriptions.Item>
        <Descriptions.Item label={copy.quality}><Tag>{row.quality}</Tag></Descriptions.Item>
        <Descriptions.Item label={copy.consecutiveMissing}>{row.consecutive_missing_count ?? 0}</Descriptions.Item>
        <Descriptions.Item label={copy.firstSeen}>{formatDateTime(row.first_seen_at, locale)}</Descriptions.Item>
        <Descriptions.Item label={copy.lastSeen}>{formatDateTime(row.last_seen_at, locale)}</Descriptions.Item>
        <Descriptions.Item label={copy.lastRefresh}>{formatDateTime(row.last_refresh_at, locale)}</Descriptions.Item>
        <Descriptions.Item label={copy.nextRetry}>{formatDateTime(row.next_retry_at, locale)}</Descriptions.Item>
        <Descriptions.Item label={copy.providerSnapshot}>{formatDateTime(row.provider_last_complete_at, locale)}</Descriptions.Item>
        <Descriptions.Item label={copy.snapshotFreshness}>{row.snapshot_freshness ?? "—"}</Descriptions.Item>
        <Descriptions.Item label={copy.providerHealth}>{row.provider_degraded ? <Tag color="orange">{copy.degraded}</Tag> : <Tag color="green">{copy.good}</Tag>}</Descriptions.Item>
        <Descriptions.Item label={copy.tokenHealth}>{row.token_state ? <Tag color={row.token_state === "VALID" ? "green" : row.token_state === "INVALID" ? "red" : undefined}>{row.token_state}</Tag> : "—"}</Descriptions.Item>
        <Descriptions.Item label={copy.expectedValidUntil}>{formatDateTime(row.expected_valid_until, locale)}</Descriptions.Item>
        <Descriptions.Item label={copy.availabilityState}>{row.availability?.state ?? "—"}</Descriptions.Item>
        <Descriptions.Item label={copy.availabilityReason}>{row.availability?.reason ?? "—"}</Descriptions.Item>
        <Descriptions.Item label={copy.availabilitySince}>{formatDateTime(row.availability?.since, locale)}</Descriptions.Item>
        {(row.availability?.state === "UNKNOWN" || row.availability?.state === "DISABLED") && <Descriptions.Item span={2}><Text type="secondary">{copy.unknownAvailabilityNote.replace("{{state}}", row.availability.state)}</Text></Descriptions.Item>}
      </Descriptions> : <Typography.Text type="secondary">{copy.notLoaded}</Typography.Text> }]}/>
    </>}
  </Drawer>;
}

function AvailabilityOccurrences({ api, instanceId, accountKey, onUnauthorized }: {
  api: AccountAvailabilityApi;
  instanceId: string;
  accountKey: string;
  onUnauthorized: () => void;
}) {
  const locale = useOptionalAppLocale()?.locale ?? "zh-CN";
  const copy = resources[locale].translation.accounts;
  const [status, setStatus] = useState<AccountAvailabilityOccurrenceStatus>("ACTIVE");
  const [cursor, setCursor] = useState<string>();
  const query = useAccountAvailabilityOccurrences(api, instanceId, accountKey, status, cursor);
  useEffect(() => {
    if (query.error instanceof Error && "status" in query.error && query.error.status === 401) onUnauthorized();
  }, [query.error, onUnauthorized]);
  const columns = [
    { title: copy.reason, dataIndex: "reason" },
    { title: "Severity", dataIndex: "severity", render: (value: string) => <Tag color={value === "Critical" ? "red" : "orange"}>{value}</Tag> },
    { title: copy.status, dataIndex: "status" },
    { title: copy.firstSeen, dataIndex: "first_seen_at", render: (value: string | null) => formatDateTime(value, locale) },
    { title: copy.recentFailure, dataIndex: "last_failure_at", render: (value: string | null) => formatDateTime(value, locale) },
    { title: "Confirmed", dataIndex: "confirmed_at", render: (value: string | null) => formatDateTime(value, locale) },
    { title: "Resolved", dataIndex: "resolved_at", render: (value: string | null) => formatDateTime(value, locale) },
  ];
  return <Flex vertical gap={8}>
    <Select aria-label={copy.eventStatus} value={status} onChange={(value: AccountAvailabilityOccurrenceStatus) => { setStatus(value); setCursor(undefined); }} options={[{ value: "ACTIVE", label: copy.active }, { value: "RESOLVED", label: copy.resolved }]} />
    {query.isPending && <Flex role="status" aria-label={copy.readingEvents} justify="center"><Spin /></Flex>}
    {query.error && !query.isPending && <Alert type="error" message={copy.unavailable} action={<Button onClick={() => void query.refetch()}>{copy.retry}</Button>} />}
    {query.data && !query.error && (query.data.items.length === 0 ? <Empty description={status === "ACTIVE" ? copy.activeEmpty : copy.resolvedEmpty} /> : <Table<AccountAvailabilityOccurrence> size="small" pagination={false} rowKey="occurrence_id" dataSource={query.data.items} columns={columns} scroll={{ x: 900 }} />)}
    {query.data && !query.error && <Flex justify="end" gap={8}><Button disabled={!cursor} onClick={() => setCursor(undefined)}>{copy.firstPage}</Button><Button disabled={!query.data.next_cursor || query.isFetching} onClick={() => setCursor(query.data.next_cursor ?? undefined)}>{copy.nextPage}</Button></Flex>}
  </Flex>;
}
