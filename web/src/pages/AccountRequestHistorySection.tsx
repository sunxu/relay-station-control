import { Alert, Button, Card, Empty, Flex, Spin, Table, Tag, Typography } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useEffect, useState } from "react";
import { useAccountRequestHistory } from "../api/account-request-history-hooks";
import type { AccountRequestHistoryApi, AccountRequestHistoryItem } from "../api/account-request-history-types";
import { TopologyApiError } from "../api/topology-types";
import { formatDateTime } from "../time";

const { Text } = Typography;

export function AccountRequestHistorySection({ api, instanceId, accountKey, onUnauthorized }: {
  api: AccountRequestHistoryApi;
  instanceId: string;
  accountKey?: string;
  onUnauthorized: () => void;
}) {
  const [cursor, setCursor] = useState<string>();
  const query = useAccountRequestHistory(api, instanceId, accountKey, cursor);
  useEffect(() => { setCursor(undefined); }, [accountKey, instanceId]);
  useEffect(() => { if (query.error instanceof TopologyApiError && query.error.status === 401) onUnauthorized(); }, [query.error, onUnauthorized]);
  const columns: ColumnsType<AccountRequestHistoryItem> = [
    { title: "Time", dataIndex: "occurred_at", render: formatDateTime },
    { title: "Model", dataIndex: "model", render: (value: string) => value || "—" },
    { title: "Result", dataIndex: "success", render: (value: boolean) => <Tag color={value ? "green" : "red"}>{value ? "Success" : "Failed"}</Tag> },
    { title: "Failure", dataIndex: "failure_class", render: (value: string | null, row) => row.success ? "—" : value ?? "—" },
    { title: "Latency", dataIndex: "duration_ms", render: (value: number | null) => value == null ? "—" : `${value} ms` },
    { title: "Request ID", dataIndex: "request_id", render: (value: string | null) => value || "—" },
  ];
  return <Card title="Account Request History" role="region" aria-label="Account Request History">
    {!accountKey && <Empty description="请选择账号查看请求历史" />}
    {accountKey && query.isPending && <Flex role="status" aria-label="正在读取请求历史" justify="center"><Spin /></Flex>}
    {accountKey && query.error && !query.isPending && <Alert type="error" title={query.error instanceof TopologyApiError && query.error.status === 404 ? "账号不存在（not found）" : "读取不可用（unavailable）"} action={<Button onClick={() => void query.refetch()}>重试</Button>} />}
    {accountKey && query.data && !query.error && <>
      <Text type="secondary" style={{ overflowWrap: "anywhere" }}>Node：{instanceId} · Account：{accountKey}</Text>
      {query.data.items.length === 0 ? <Empty description="最近 7 天暂无请求历史" /> : <Table<AccountRequestHistoryItem> size="small" pagination={false} rowKey={(_, index) => `${instanceId}:${accountKey}:${cursor ?? "first"}:${index ?? 0}`} dataSource={query.data.items} columns={columns} scroll={{ x: 900 }} />}
      <Flex justify="end" gap={8} style={{ marginTop: 8 }}>
        <Button disabled={!cursor} onClick={() => setCursor(undefined)}>History 首页</Button>
        <Button disabled={!query.data.next_cursor || query.isFetching} onClick={() => setCursor(query.data.next_cursor ?? undefined)}>History 下一页</Button>
      </Flex>
    </>}
  </Card>;
}
