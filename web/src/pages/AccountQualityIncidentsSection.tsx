import { Alert, Button, Card, Empty, Flex, Select, Spin, Table, Tag, Typography } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useEffect, useState } from "react";
import { useAccountQualityIncidents } from "../api/account-quality-incidents-hooks";
import type { AccountQualityIncidentItem, AccountQualityIncidentsApi, IncidentFailureClass } from "../api/account-quality-incidents-types";
import type { TopologyProviderState } from "../api/topology-types";
import { TopologyApiError } from "../api/topology-types";
import { formatDateTime } from "../time";

const { Text } = Typography;
const failureOptions = [{ value: "auth", label: "auth" }, { value: "quota", label: "quota" }, { value: "rate_limit", label: "rate_limit" }, { value: "upstream", label: "upstream" }];

export function AccountQualityIncidentsSection({ api, instanceId, providers, providerError, onSelectAccount, onUnauthorized }: {
  api: AccountQualityIncidentsApi; instanceId: string; providers: TopologyProviderState[]; providerError: boolean;
  onSelectAccount: (accountKey: string) => void; onUnauthorized: () => void;
}) {
  const [provider, setProvider] = useState<string>();
  const [failureClass, setFailureClass] = useState<IncidentFailureClass>();
  const [cursor, setCursor] = useState<string>();
  const query = useAccountQualityIncidents(api, instanceId, provider, failureClass, cursor);
  const columns: ColumnsType<AccountQualityIncidentItem> = [
    { title: "Account", dataIndex: "account_key", render: (value: string) => <Button type="link" size="small" onClick={() => onSelectAccount(value)}>{value}</Button> },
    { title: "Provider", dataIndex: "provider" },
    { title: "Reason", dataIndex: "failure_class" },
    { title: "Status", dataIndex: "status", render: () => <Tag color="red">Active</Tag> },
    { title: "Hits", dataIndex: "hit_count" },
    { title: "First Seen", dataIndex: "first_seen", render: formatDateTime },
    { title: "Last Seen", dataIndex: "last_seen", render: formatDateTime },
  ];
  useEffect(() => { if (query.error instanceof TopologyApiError && query.error.status === 401) onUnauthorized(); }, [query.error, onUnauthorized]);
  return <Card title="Account Quality Incidents" role="region" aria-label="Account Quality Incidents">
    <Flex gap={8} wrap>
      <Select allowClear aria-label="Incident Provider" placeholder="全部 Provider" value={provider} onChange={(v) => { setProvider(v); setCursor(undefined); }} options={providers.map((row) => ({ value: row.provider, label: row.provider }))} disabled={providerError} />
      <Select allowClear aria-label="Incident Reason" placeholder="全部 Reason" value={failureClass} onChange={(v) => { setFailureClass(v); setCursor(undefined); }} options={failureOptions} />
      <Button onClick={() => void query.refetch()} loading={query.isFetching}>刷新 Incidents</Button>
    </Flex>
    {providerError && <Alert style={{ marginTop: 12 }} type="warning" message="Provider 筛选来源不可用" description="无法安全加载 Provider 过滤项。" />}
    {query.isPending && <Flex role="status" aria-label="正在读取 Incidents" justify="center"><Spin /></Flex>}
    {query.error && !query.isPending && <Alert type="error" title={query.error instanceof TopologyApiError && query.error.status === 404 ? "Node 不存在（not found）" : "读取不可用（unavailable）"} />}
    {query.data && !query.error && <>
      {query.data.items.length === 0 ? <Empty description="当前没有 Active incidents" /> : <Table<AccountQualityIncidentItem> size="small" pagination={false} rowKey={(row) => `${row.account_key}:${row.failure_class}`} dataSource={query.data.items} columns={columns} scroll={{ x: 900 }} />}
      <Flex justify="end" gap={8} style={{ marginTop: 8 }}><Button disabled={!cursor} onClick={() => setCursor(undefined)}>Incidents 首页</Button><Button disabled={!query.data.next_cursor || query.isFetching} onClick={() => setCursor(query.data.next_cursor ?? undefined)}>Incidents 下一页</Button></Flex>
    </>}
  </Card>;
}
