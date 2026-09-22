import { useCallback, useEffect, useMemo, useState } from "react";
import type { HTMLAttributes } from "react";
import { Alert, Button, Card, Empty, Flex, Input, Select, Space, Spin, Table, Tag, Typography } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useProblemAccountsQuery } from "../api/problem-accounts-hooks";
import type { ProblemAccountItem, ProblemAccountQueryRequest, ProblemAccountsApi } from "../api/problem-accounts-types";
import { ProblemAccountsApiError } from "../api/problem-accounts-types";
import { formatDateTime } from "../foundation/format";
import { useOptionalAppLocale } from "../foundation/FrontendFoundationProvider";
import { resources } from "../foundation/resources";
import type { TranslationResource } from "../foundation/resources";

const { Text } = Typography;
type PageSize = 25 | 50 | 100;
const issueLabels = [
  ["token_invalid", "TOKEN_INVALID"],
  ["account_blocked", "ACCOUNT_BLOCKED"],
  ["forbidden", "FORBIDDEN"],
  ["cross_node_duplicate_ownership", "CROSS_NODE_DUPLICATE_OWNERSHIP"],
] as const;

function issueColor(severity: string) { return severity === "Critical" ? "red" : "gold"; }

function RequestError({ error, retry, copy }: { error: unknown; retry: () => void; copy: TranslationResource["problems"] }) {
  const status = error instanceof ProblemAccountsApiError ? error.status : undefined;
  if (status === 400) return <Alert type="error" showIcon message={copy.invalidFilter} />;
  if (status === 403) return <Alert type="error" showIcon message={copy.forbidden} />;
  return <Alert type="error" showIcon message={copy.unavailable} action={<Button data-testid="problems-retry" onClick={retry}>{copy.retry}</Button>} />;
}

export function ProblemsView({ api, csrfToken, onUnauthorized }: { api: ProblemAccountsApi; csrfToken: string; onUnauthorized: () => void }) {
  const locale = useOptionalAppLocale()?.locale ?? "zh-CN";
  const copy = resources[locale].translation.problems;
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
    { title: copy.node, key: "node", width: 220, render: (_, row) => <Flex vertical><Text>{row.node_name || "—"}</Text><Text type="secondary" code>{row.instance_id}</Text></Flex> },
    { title: copy.account, key: "account", width: 250, render: (_, row) => <Flex vertical><Text>{row.email}</Text><Text type="secondary" code>{row.account_key}</Text></Flex> },
    { title: copy.provider, dataIndex: "provider", key: "provider", width: 130, render: (value: string) => <Tag>{value}</Tag> },
    { title: copy.issues, key: "issues", width: 360, render: (_, row) => <Flex vertical gap={4}>{row.issues.map((issue) => <Space key={`${issue.occurrence_id}:${issue.type}`} wrap><Tag color={issueColor(issue.severity)}>{issue.severity}</Tag><Tag>{issue.type}</Tag><Text type="secondary">{copy.since} {formatDateTime(issue.since, locale)}</Text><Text type="secondary" code>{issue.occurrence_id}</Text></Space>)}</Flex> },
    { title: copy.availability, key: "availability", width: 220, render: (_, row) => row.availability ? <Flex vertical><Text>{row.availability.state}</Text><Text type="secondary">{row.availability.reason}</Text><Text type="secondary">{formatDateTime(row.availability.since, locale)}</Text></Flex> : "—" },
    { title: copy.token, key: "token", width: 190, render: (_, row) => <Flex vertical><Tag color={row.token_state === "VALID" ? "green" : row.token_state === "INVALID" ? "red" : undefined}>{row.token_state ?? "—"}</Tag><Text type="secondary">{copy.expectedValidUntil}</Text><Text type="secondary">{formatDateTime(row.expected_valid_until, locale)}</Text></Flex> },
    { title: copy.inventoryTiming, key: "inventory", width: 200, render: (_, row) => <Flex vertical><Text>{copy.lastRefresh}: {formatDateTime(row.last_refresh_at, locale)}</Text><Text>{copy.nextRetry}: {formatDateTime(row.next_retry_at, locale)}</Text></Flex> },
    { title: copy.requestEvidence, key: "request-evidence", width: 200, render: (_, row) => <Flex vertical><Text>{copy.lastSuccess}: {formatDateTime(row.last_success_at, locale)}</Text><Text>{copy.lastFailure}: {formatDateTime(row.last_failure_at, locale)}</Text></Flex> },
  ], [copy, locale]);

  const hasFilters = Boolean(provider || node.trim() || severity || reason || email.trim());
  const emptyText = hasFilters ? copy.noFiltered : copy.noProblems;
  return (
    <Flex vertical gap={16} data-testid="problems-view">
      <Card title={copy.filters}>
        <Space wrap>
          <Select data-testid="problems-provider-filter" disabled={list.isPending} aria-label={copy.provider} allowClear placeholder={copy.provider} value={provider} options={[{ value: "antigravity", label: <span data-testid="problems-provider-antigravity">antigravity</span> }]} onChange={(value) => updateFilter(setProvider, value)} style={{ width: 150 }} />
          <Input data-testid="problems-node-filter" disabled={list.isPending} aria-label={copy.nodeUuid} placeholder={copy.nodeUuid} maxLength={64} value={node} onChange={(event) => updateFilter(setNode, event.target.value)} style={{ width: 280 }} />
          <Select data-testid="problems-severity-filter" disabled={list.isPending} aria-label={copy.severity} allowClear placeholder={copy.severity} value={severity} options={["Critical", "Warning"].map((value) => ({ value, label: <span data-testid={`problems-severity-${value.toLowerCase()}`}>{value}</span> }))} onChange={(value) => updateFilter(setSeverity, value)} style={{ width: 150 }} />
          <Select data-testid="problems-reason-filter" disabled={list.isPending} aria-label={copy.reason} allowClear placeholder={copy.reason} value={reason} options={issueLabels.map(([value, label]) => ({ value, label: <span data-testid={`problems-reason-${value.replaceAll("_", "-")}`}>{label}</span> }))} onChange={(value) => updateFilter(setReason, value)} style={{ width: 260 }} />
          <Input data-testid="problems-email-filter" disabled={list.isPending} aria-label={copy.email} placeholder={copy.email} autoComplete="off" maxLength={320} value={email} onChange={(event) => updateFilter(setEmail, event.target.value)} style={{ width: 260 }} />
          <Select data-testid="problems-page-size" disabled={list.isPending} aria-label={copy.pageSize} value={pageSize} options={[25, 50, 100].map((value) => ({ value: value as PageSize, label: <span data-testid={`problems-page-size-${value}`}>{value}{copy.pageSuffix}</span> }))} onChange={(value) => { setPageSize(value); setCursorHistory([undefined]); reset(); mutate({ ...request, limit: value, cursor: undefined }); }} style={{ width: 130 }} />
          <Button data-testid="problems-query" type="primary" loading={list.isPending} onClick={() => { setCursorHistory([undefined]); query({ ...request, cursor: undefined }); }} disabled={list.isPending}>{copy.query}</Button>
        </Space>
      </Card>
      <Card title={copy.title} data-testid="problems-card">
        {list.isPending && <Flex justify="center"><div role="status" aria-label={copy.reading}><Spin /></div></Flex>}
        {list.error && <RequestError error={list.error} retry={() => query()} copy={copy} />}
        {!list.isPending && !list.error && list.data?.items.length === 0 && <Empty description={emptyText} />}
        {!list.error && list.data && list.data.items.length > 0 && <Table<ProblemAccountItem> rowKey={(row) => `${row.instance_id}:${row.account_key}`} onRow={(row): HTMLAttributes<HTMLTableRowElement> => ({ "data-testid": "problem-row", "data-instance-id": row.instance_id, "data-account-key": row.account_key } as unknown as HTMLAttributes<HTMLTableRowElement>)} size="small" scroll={{ x: 1500 }} pagination={false} dataSource={list.data.items} columns={columns} />}
        {!list.error && <Flex justify="end" gap={8} style={{ marginTop: 16 }}><Button data-testid="problems-previous" disabled={list.isPending || cursorHistory.length === 1} onClick={previousPage}>{copy.previous}</Button><Button data-testid="problems-next" disabled={list.isPending || !list.data?.next_cursor} onClick={nextPage}>{copy.next}</Button></Flex>}
      </Card>
    </Flex>
  );
}
