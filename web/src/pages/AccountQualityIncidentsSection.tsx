import { Alert, Button, Card, Empty, Flex, Select, Spin, Table, Tag, Typography } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useEffect, useState } from "react";
import { useAccountQualityIncidents } from "../api/account-quality-incidents-hooks";
import type { AccountQualityIncidentItem, AccountQualityIncidentsApi, IncidentFailureClass } from "../api/account-quality-incidents-types";
import type { TopologyProviderState } from "../api/topology-types";
import { TopologyApiError } from "../api/topology-types";
import { formatDateTime } from "../foundation/format";
import { useOptionalAppLocale } from "../foundation/FrontendFoundationProvider";
import { resources } from "../foundation/resources";

const { Text } = Typography;
const failureOptions = [{ value: "auth", label: "auth" }, { value: "quota", label: "quota" }, { value: "rate_limit", label: "rate_limit" }, { value: "upstream", label: "upstream" }];

export function AccountQualityIncidentsSection({ api, instanceId, providers, providerError, onSelectAccount, onUnauthorized }: {
  api: AccountQualityIncidentsApi; instanceId: string; providers: TopologyProviderState[]; providerError: boolean;
  onSelectAccount: (accountKey: string) => void; onUnauthorized: () => void;
}) {
  const locale = useOptionalAppLocale()?.locale ?? "zh-CN";
  const copy = resources[locale].translation.accounts;
  const [provider, setProvider] = useState<string>();
  const [failureClass, setFailureClass] = useState<IncidentFailureClass>();
  const [cursor, setCursor] = useState<string>();
  const query = useAccountQualityIncidents(api, instanceId, provider, failureClass, cursor);
  const columns: ColumnsType<AccountQualityIncidentItem> = [
    { title: copy.incidentAccount, dataIndex: "account_key", render: (value: string) => <Button type="link" size="small" onClick={() => onSelectAccount(value)}>{value}</Button> },
    { title: copy.provider, dataIndex: "provider" },
    { title: copy.incidentReason, dataIndex: "failure_class" },
    { title: copy.incidentStatus, dataIndex: "status", render: () => <Tag color="red">{copy.active}</Tag> },
    { title: copy.hits, dataIndex: "hit_count" },
    { title: copy.firstSeenLabel, dataIndex: "first_seen", render: (value: string | null) => formatDateTime(value, locale) },
    { title: copy.lastSeenLabel, dataIndex: "last_seen", render: (value: string | null) => formatDateTime(value, locale) },
  ];
  useEffect(() => { if (query.error instanceof TopologyApiError && query.error.status === 401) onUnauthorized(); }, [query.error, onUnauthorized]);
  return <Card title={copy.incidents} role="region" aria-label={copy.incidents}>
    <Flex gap={8} wrap>
      <Select allowClear aria-label={copy.incidentProvider} placeholder={copy.allProviders} value={provider} onChange={(v) => { setProvider(v); setCursor(undefined); }} options={providers.map((row) => ({ value: row.provider, label: row.provider }))} disabled={providerError} />
      <Select allowClear aria-label={copy.incidentReasonFilter} placeholder={copy.allReasons} value={failureClass} onChange={(v) => { setFailureClass(v); setCursor(undefined); }} options={failureOptions} />
      <Button onClick={() => void query.refetch()} loading={query.isFetching}>{copy.refreshIncidents}</Button>
    </Flex>
    {providerError && <Alert style={{ marginTop: 12 }} type="warning" message={copy.providerUnavailable} description={copy.providerUnavailableDescription} />}
    {query.isPending && <Flex role="status" aria-label={copy.readingIncidents} justify="center"><Spin /></Flex>}
    {query.error && !query.isPending && <Alert type="error" title={query.error instanceof TopologyApiError && query.error.status === 404 ? copy.nodeNotFound : copy.unavailable} />}
    {query.data && !query.error && <>
      {query.data.items.length === 0 ? <Empty description={copy.activeIncidentsEmpty} /> : <Table<AccountQualityIncidentItem> size="small" pagination={false} rowKey={(row) => `${row.account_key}:${row.failure_class}`} dataSource={query.data.items} columns={columns} scroll={{ x: 900 }} />}
      <Flex justify="end" gap={8} style={{ marginTop: 8 }}><Button disabled={!cursor} onClick={() => setCursor(undefined)}>{copy.incidentsFirst}</Button><Button disabled={!query.data.next_cursor || query.isFetching} onClick={() => setCursor(query.data.next_cursor ?? undefined)}>{copy.incidentsNext}</Button></Flex>
    </>}
  </Card>;
}
