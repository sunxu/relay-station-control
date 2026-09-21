import { Alert, Button, Card, Empty, Flex, Spin, Table, Tag, Typography } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useEffect, useState } from "react";
import { useAccountRequestHistory } from "../api/account-request-history-hooks";
import type { AccountRequestHistoryApi, AccountRequestHistoryItem } from "../api/account-request-history-types";
import { TopologyApiError } from "../api/topology-types";
import { formatDateTime, formatNumber } from "../foundation/format";
import { useOptionalAppLocale } from "../foundation/FrontendFoundationProvider";
import { resources } from "../foundation/resources";

const { Text } = Typography;

export function AccountRequestHistorySection({ api, instanceId, accountKey, onUnauthorized }: {
  api: AccountRequestHistoryApi;
  instanceId: string;
  accountKey?: string;
  onUnauthorized: () => void;
}) {
  const locale = useOptionalAppLocale()?.locale ?? "zh-CN";
  const copy = resources[locale].translation.accounts;
  const [cursor, setCursor] = useState<string>();
  const query = useAccountRequestHistory(api, instanceId, accountKey, cursor);
  useEffect(() => { setCursor(undefined); }, [accountKey, instanceId]);
  useEffect(() => { if (query.error instanceof TopologyApiError && query.error.status === 401) onUnauthorized(); }, [query.error, onUnauthorized]);
  const columns: ColumnsType<AccountRequestHistoryItem> = [
    { title: copy.time, dataIndex: "occurred_at", render: (value: string | null) => formatDateTime(value, locale) },
    { title: copy.model, dataIndex: "model", render: (value: string) => value || "—" },
    { title: copy.result, dataIndex: "success", render: (value: boolean) => <Tag color={value ? "green" : "red"}>{value ? copy.success : copy.failed}</Tag> },
    { title: copy.failure, dataIndex: "failure_class", render: (value: string | null, row) => row.success ? "—" : value ?? "—" },
    { title: copy.latency, dataIndex: "duration_ms", render: (value: number | null) => value == null ? "—" : `${formatNumber(value, locale)} ms` },
    { title: copy.requestId, dataIndex: "request_id", render: (value: string | null) => value || "—" },
  ];
  return <Card title={copy.requestHistoryCard} role="region" aria-label={copy.requestHistoryCard}>
    {!accountKey && <Empty description={copy.unselectedHistory} />}
    {accountKey && query.isPending && <Flex role="status" aria-label={copy.readingEvents} justify="center"><Spin /></Flex>}
    {accountKey && query.error && !query.isPending && <Alert type="error" title={query.error instanceof TopologyApiError && query.error.status === 404 ? copy.nodeNotFound : copy.unavailable} action={<Button onClick={() => void query.refetch()}>{copy.retry}</Button>} />}
    {accountKey && query.data && !query.error && <>
      <Text type="secondary" style={{ overflowWrap: "anywhere" }}>{copy.node}：{instanceId} · {copy.account}：{accountKey}</Text>
      {query.data.items.length === 0 ? <Empty description={copy.noHistory} /> : <Table<AccountRequestHistoryItem> size="small" pagination={false} rowKey={(_, index) => `${instanceId}:${accountKey}:${cursor ?? "first"}:${index ?? 0}`} dataSource={query.data.items} columns={columns} scroll={{ x: 900 }} />}
      <Flex justify="end" gap={8} style={{ marginTop: 8 }}>
        <Button disabled={!cursor} onClick={() => setCursor(undefined)}>{copy.requestFirstPage}</Button>
        <Button disabled={!query.data.next_cursor || query.isFetching} onClick={() => setCursor(query.data.next_cursor ?? undefined)}>{copy.requestNextPage}</Button>
      </Flex>
    </>}
  </Card>;
}
