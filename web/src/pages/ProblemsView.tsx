import { useCallback, useEffect, useMemo, useState } from "react";
import { Alert, Button, Card, Empty, Flex, Input, Select, Space, Spin, Table, Tag, Typography } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useProblemAccountsQuery } from "../api/problem-accounts-hooks";
import type { ProblemAccountItem, ProblemAccountQueryRequest, ProblemAccountsApi } from "../api/problem-accounts-types";
import { ProblemAccountsApiError } from "../api/problem-accounts-types";
import { formatDateTime } from "../time";

const { Text } = Typography;
type PageSize = 25 | 50 | 100;
const issueLabels = [
  ["token_invalid", "TOKEN_INVALID"],
  ["account_blocked", "ACCOUNT_BLOCKED"],
  ["forbidden", "FORBIDDEN"],
  ["cross_node_duplicate_ownership", "CROSS_NODE_DUPLICATE_OWNERSHIP"],
] as const;

function issueColor(severity: string) { return severity === "Critical" ? "red" : "gold"; }

function RequestError({ error, retry }: { error: unknown; retry: () => void }) {
  const status = error instanceof ProblemAccountsApiError ? error.status : undefined;
  if (status === 400) return <Alert type="error" showIcon message="筛选条件或分页凭据无效，请清除筛选并重新查询。" />;
  if (status === 403) return <Alert type="error" showIcon message="当前会话无权读取 Problems。" />;
  return <Alert type="error" showIcon message="读取不可用（unavailable）" action={<Button onClick={retry}>Retry</Button>} />;
}

export function ProblemsView({ api, csrfToken, onUnauthorized }: { api: ProblemAccountsApi; csrfToken: string; onUnauthorized: () => void }) {
  const [provider, setProvider] = useState<string>();
  const [node, setNode] = useState("");
  const [severity, setSeverity] = useState<"Critical" | "Warning">();
  const [reason, setReason] = useState<ProblemAccountQueryRequest["reason"]>();
  const [email, setEmail] = useState("");
  const [pageSize, setPageSize] = useState<PageSize>(25);
  const [cursorHistory, setCursorHistory] = useState<Array<string | undefined>>([undefined]);
  const currentCursor = cursorHistory[cursorHistory.length - 1];
  const queryState = useProblemAccountsQuery(api, csrfToken);
  const { mutate, reset, ...list } = queryState;

  const request = useMemo<ProblemAccountQueryRequest>(() => {
    const normalizedEmail = email.trim().toLowerCase();
    const normalizedNode = node.trim();
    return {
      ...(provider ? { provider } : {}),
      ...(normalizedNode ? { node: normalizedNode } : {}),
      ...(severity ? { severity } : {}),
      ...(reason ? { reason } : {}),
      ...(normalizedEmail ? { email: normalizedEmail } : {}),
      limit: pageSize,
      ...(currentCursor ? { cursor: currentCursor } : {}),
    };
  }, [provider, node, severity, reason, email, pageSize, currentCursor]);

  const query = useCallback((next = request) => {
    const normalized = { ...next };
    if (!normalized.cursor) delete normalized.cursor;
    reset();
    mutate(normalized);
  }, [mutate, reset, request]);
  const resetFilters = useCallback(() => { setCursorHistory([undefined]); reset(); }, [reset]);

  useEffect(() => { reset(); mutate({ limit: 25 }); }, [mutate, reset]); // initial read: no filters, first page
  useEffect(() => {
    if (list.error instanceof ProblemAccountsApiError && list.error.status === 401) {
      reset();
      setProvider(undefined); setNode(""); setSeverity(undefined); setReason(undefined); setEmail(""); setPageSize(25); setCursorHistory([undefined]);
      onUnauthorized();
    }
  }, [list.error, onUnauthorized, reset]);

  const updateFilter = <T,>(setter: (value: T) => void, value: T) => { setter(value); resetFilters(); };
  const nextPage = () => {
    const next = list.data?.next_cursor;
    if (next) { setCursorHistory((history) => [...history, next]); query({ ...request, cursor: next }); }
  };
  const previousPage = () => {
    if (cursorHistory.length < 2) return;
    const history = cursorHistory.slice(0, -1);
    setCursorHistory(history);
    query({ ...request, cursor: history[history.length - 1] });
  };

  const columns = useMemo<ColumnsType<ProblemAccountItem>>(() => [
    { title: "Node", key: "node", width: 220, render: (_, row) => <Flex vertical><Text>{row.node_name || "—"}</Text><Text type="secondary" code>{row.instance_id}</Text></Flex> },
    { title: "Account", key: "account", width: 250, render: (_, row) => <Flex vertical><Text>{row.email}</Text><Text type="secondary" code>{row.account_key}</Text></Flex> },
    { title: "Provider", dataIndex: "provider", key: "provider", width: 130, render: (value: string) => <Tag>{value}</Tag> },
    { title: "Issues", key: "issues", width: 360, render: (_, row) => <Flex vertical gap={4}>{row.issues.map((issue) => <Space key={`${issue.occurrence_id}:${issue.type}`} wrap><Tag color={issueColor(issue.severity)}>{issue.severity}</Tag><Tag>{issue.type}</Tag><Text type="secondary">Since {formatDateTime(issue.since)}</Text><Text type="secondary" code>{issue.occurrence_id}</Text></Space>)}</Flex> },
    { title: "Availability", key: "availability", width: 220, render: (_, row) => row.availability ? <Flex vertical><Text>{row.availability.state}</Text><Text type="secondary">{row.availability.reason}</Text><Text type="secondary">{formatDateTime(row.availability.since)}</Text></Flex> : "—" },
    { title: "Token", key: "token", width: 190, render: (_, row) => <Flex vertical><Tag color={row.token_state === "VALID" ? "green" : row.token_state === "INVALID" ? "red" : undefined}>{row.token_state ?? "—"}</Tag><Text type="secondary">Expected Valid Until</Text><Text type="secondary">{formatDateTime(row.expected_valid_until)}</Text></Flex> },
    { title: "Inventory timing", key: "inventory", width: 200, render: (_, row) => <Flex vertical><Text>last_refresh_at: {formatDateTime(row.last_refresh_at)}</Text><Text>next_retry_at: {formatDateTime(row.next_retry_at)}</Text></Flex> },
    { title: "Request evidence", key: "request-evidence", width: 200, render: (_, row) => <Flex vertical><Text>last_success_at: {formatDateTime(row.last_success_at)}</Text><Text>last_failure_at: {formatDateTime(row.last_failure_at)}</Text></Flex> },
  ], []);

  const hasFilters = Boolean(provider || node.trim() || severity || reason || email.trim());
  const emptyText = hasFilters ? "当前过滤条件下没有 confirmed problems" : "当前没有 confirmed problems";
  return (
    <Flex vertical gap={16} data-testid="problems-view">
      <Card title="Problems 过滤">
        <Space wrap>
          <Select disabled={list.isPending} aria-label="Provider" allowClear placeholder="Provider" value={provider} options={[{ value: "antigravity", label: "antigravity" }]} onChange={(value) => updateFilter(setProvider, value)} style={{ width: 150 }} />
          <Input disabled={list.isPending} aria-label="Node UUID" placeholder="Node UUID" maxLength={36} value={node} onChange={(event) => updateFilter(setNode, event.target.value)} style={{ width: 280 }} />
          <Select disabled={list.isPending} aria-label="Severity" allowClear placeholder="Severity" value={severity} options={["Critical", "Warning"].map((value) => ({ value, label: value }))} onChange={(value) => updateFilter(setSeverity, value)} style={{ width: 150 }} />
          <Select disabled={list.isPending} aria-label="Reason" allowClear placeholder="Reason" value={reason} options={issueLabels.map(([value, label]) => ({ value, label }))} onChange={(value) => updateFilter(setReason, value)} style={{ width: 260 }} />
          <Input disabled={list.isPending} aria-label="Email" placeholder="Email" autoComplete="off" maxLength={320} value={email} onChange={(event) => updateFilter(setEmail, event.target.value)} style={{ width: 260 }} />
          <Select disabled={list.isPending} aria-label="Page size" value={pageSize} options={[25, 50, 100].map((value) => ({ value: value as PageSize, label: `${value} / 页` }))} onChange={(value) => { setPageSize(value); setCursorHistory([undefined]); reset(); mutate({ ...request, limit: value, cursor: undefined }); }} style={{ width: 130 }} />
          <Button type="primary" loading={list.isPending} onClick={() => { setCursorHistory([undefined]); query({ ...request, cursor: undefined }); }} disabled={list.isPending}>Query</Button>
        </Space>
      </Card>
      <Card title="Problems" data-testid="problems-card">
        {list.isPending && <Flex justify="center"><div role="status" aria-label="正在读取 Problems"><Spin /></div></Flex>}
        {list.error && <RequestError error={list.error} retry={() => query()} />}
        {!list.isPending && !list.error && list.data?.items.length === 0 && <Empty description={emptyText} />}
        {!list.error && list.data && list.data.items.length > 0 && <Table<ProblemAccountItem> rowKey={(row) => `${row.instance_id}:${row.account_key}`} size="small" scroll={{ x: 1500 }} pagination={false} dataSource={list.data.items} columns={columns} />}
        {!list.error && <Flex justify="end" gap={8} style={{ marginTop: 16 }}><Button disabled={list.isPending || cursorHistory.length === 1} onClick={previousPage}>上一页</Button><Button disabled={list.isPending || !list.data?.next_cursor} onClick={nextPage}>下一页</Button></Flex>}
      </Card>
    </Flex>
  );
}
