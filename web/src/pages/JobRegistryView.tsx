import { useEffect, useMemo, useState } from "react";
import type { HTMLAttributes } from "react";
import { Alert, Button, Card, Descriptions, Drawer, Empty, Flex, Input, Select, Space, Spin, Table, Tag, Timeline, Typography } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useJob, useJobs } from "../api/job-hooks";
import { JobApiError, jobStatuses } from "../api/job-types";
import type { JobApi, JobFilters, JobLifecycleEvent, JobStatus, JobSummary } from "../api/job-types";
import { formatDateTime, formatNumber } from "../foundation/format";
import { useOptionalAppLocale } from "../foundation/FrontendFoundationProvider";
import { resources } from "../foundation/resources";
import type { TranslationResource } from "../foundation/resources";

const { Text } = Typography;
const defaultPageSize = 50;
type PageSize = 50 | 100 | 200;

function requestTime(value: string): string | undefined {
  if (!value) return undefined;
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? undefined : parsed.toISOString();
}

function ReadFailure({ error, retry, notFound, copy, retryTestId }: { error: unknown; retry: () => void; notFound?: boolean; copy: TranslationResource["jobs"]; retryTestId: string }) {
  const missing = notFound && error instanceof JobApiError && error.status === 404;
  return (
    <Alert
      type="error"
      showIcon
      message={missing ? copy.notFound : copy.readError}
      action={<Button data-testid={retryTestId} onClick={retry}>{copy.retryRead}</Button>}
    />
  );
}

function EventLine({ event, copy }: { event: JobLifecycleEvent; copy: TranslationResource["jobs"] }) {
  const locale = useOptionalAppLocale()?.locale ?? "zh-CN";
  return (
    <Flex vertical gap={2} data-testid={`job-event-${event.sequence}`}>
      <Space wrap>
        <Text strong>#{formatNumber(event.sequence, locale)} {event.eventType}</Text>
        <Tag>{event.fromStatus ?? copy.initial} → {event.toStatus}</Tag>
        <Text type="secondary">{copy.attempt} {formatNumber(event.attemptCount, locale)}</Text>
      </Space>
      <Text type="secondary">{event.actorType} · {formatDateTime(event.occurredAt, locale)}</Text>
      {(event.reasonCode || event.errorCode) && <Text code>{[event.reasonCode, event.errorCode].filter(Boolean).join(" / ")}</Text>}
    </Flex>
  );
}

export function JobRegistryView({ api, onUnauthorized }: { api: JobApi; onUnauthorized: () => void }) {
  const locale = useOptionalAppLocale()?.locale ?? "zh-CN";
  const copy = resources[locale].translation.jobs;
  const [jobKind, setJobKind] = useState("");
  const [status, setStatus] = useState<JobStatus>();
  const [createdFrom, setCreatedFrom] = useState("");
  const [createdTo, setCreatedTo] = useState("");
  const [pageSize, setPageSize] = useState<PageSize>(defaultPageSize);
  const [cursorHistory, setCursorHistory] = useState<Array<string | undefined>>([undefined]);
  const [selectedJobID, setSelectedJobID] = useState<string>();
  const cursor = cursorHistory.at(-1);
  const filters = useMemo<JobFilters>(() => ({
    jobKind: jobKind.trim() || undefined,
    status,
    createdFrom: requestTime(createdFrom),
    createdTo: requestTime(createdTo),
    cursor,
    limit: pageSize,
  }), [jobKind, status, createdFrom, createdTo, cursor, pageSize]);
  const list = useJobs(api, filters);
  const detail = useJob(api, selectedJobID);

  useEffect(() => {
    if ((list.error instanceof JobApiError && list.error.status === 401) ||
      (detail.error instanceof JobApiError && detail.error.status === 401)) onUnauthorized();
  }, [list.error, detail.error, onUnauthorized]);

  const resetCursor = () => setCursorHistory([undefined]);
  const columns = useMemo<ColumnsType<JobSummary>>(() => [
    { title: copy.jobId, dataIndex: "jobId", key: "jobId", render: (value: string) => <Text code>{value}</Text> },
    { title: copy.type, dataIndex: "jobKind", key: "jobKind", render: (value: string) => <Tag>{value}</Tag> },
    { title: copy.status, dataIndex: "status", key: "status", render: (value: JobStatus) => <Tag>{value}</Tag> },
    { title: copy.attempt, key: "attempts", render: (_, item) => `${formatNumber(item.attemptCount, locale)} / ${formatNumber(item.maxAttempts, locale)}` },
    { title: copy.outbox, dataIndex: "outboxStatus", key: "outboxStatus" },
    { title: copy.createdAt, dataIndex: "createdAt", key: "createdAt", render: (value: string | null) => formatDateTime(value, locale) },
    { title: copy.action, key: "view", render: (_, item) => <Button data-testid={`job-details-${item.jobId}`} onClick={() => setSelectedJobID(item.jobId)}>{copy.viewDetails}</Button> },
  ], [copy, locale]);

  return (
    <Flex vertical gap={16} data-testid="durable-jobs-view">
      <Card title={copy.filters}>
        <Space wrap>
          <Input data-testid="job-kind-filter"
            aria-label={copy.type}
            placeholder={copy.allTypes}
            value={jobKind}
            maxLength={64}
            onChange={(event) => { setJobKind(event.target.value); resetCursor(); }}
            style={{ width: 220 }}
          />
          <Select data-testid="job-status-filter"
            aria-label={copy.status}
            allowClear
            placeholder={copy.allStatuses}
            value={status}
            options={jobStatuses.map((value) => ({ value, label: value }))}
            optionRender={(option) => <span data-testid={`job-status-option-${option.value}`}>{option.label}</span>}
            onChange={(value) => { setStatus(value); resetCursor(); }}
            style={{ width: 190 }}
          />
          <Input data-testid="job-created-from" aria-label={copy.createdFrom} type="datetime-local" value={createdFrom} onChange={(event) => { setCreatedFrom(event.target.value); resetCursor(); }} />
          <Input data-testid="job-created-to" aria-label={copy.createdTo} type="datetime-local" value={createdTo} onChange={(event) => { setCreatedTo(event.target.value); resetCursor(); }} />
          <Select<PageSize>
            data-testid="job-page-size" aria-label={copy.perPage}
            value={pageSize}
            options={[50, 100, 200].map((value) => ({ value: value as PageSize, label: `${value}${copy.pageSuffix}` }))}
            optionRender={(option) => <span data-testid={`job-page-size-option-${option.value}`}>{option.label}</span>}
            onChange={(value) => { setPageSize(value); resetCursor(); }}
            style={{ width: 130 }}
          />
        </Space>
      </Card>

      <Card title={copy.title} data-testid="jobs-card">
        {list.isPending && <Flex justify="center"><Spin /></Flex>}
        {list.error && <ReadFailure error={list.error} retry={() => void list.refetch()} copy={copy} retryTestId="job-retry-list" />}
        {!list.isPending && !list.error && list.data?.items.length === 0 && <Empty description={copy.empty} />}
        {!list.error && list.data && list.data.items.length > 0 && (
          <Table<JobSummary> rowKey={(job) => job.jobId} onRow={(job): HTMLAttributes<HTMLTableRowElement> => ({ "data-testid": `job-row-${job.jobId}`, "data-job-id": job.jobId } as unknown as HTMLAttributes<HTMLTableRowElement>)} size="small" scroll={{ x: 1100 }} pagination={false} dataSource={list.data.items} columns={columns} />
        )}
        {!list.error && (
          <Flex justify="end" gap={8} style={{ marginTop: 16 }}>
            <Button data-testid="job-previous-page" disabled={cursorHistory.length === 1} onClick={() => setCursorHistory((history) => history.slice(0, -1))}>{copy.previous}</Button>
            <Button data-testid="job-next-page" disabled={!list.data?.nextCursor} onClick={() => list.data?.nextCursor && setCursorHistory((history) => [...history, list.data!.nextCursor!])}>{copy.next}</Button>
          </Flex>
        )}
      </Card>

      <Drawer data-testid="job-detail-drawer" closeIcon={<span data-testid="job-detail-close">×</span>} title={copy.detail} width={720} open={Boolean(selectedJobID)} onClose={() => setSelectedJobID(undefined)} destroyOnHidden>
        {detail.isPending && <Flex justify="center"><Spin /></Flex>}
        {detail.error && <ReadFailure error={detail.error} retry={() => void detail.refetch()} notFound copy={copy} retryTestId="job-retry-detail" />}
        {!detail.error && detail.data && (
          <Flex vertical gap={24} data-testid="job-detail">
            <Descriptions column={1} size="small" bordered>
              <Descriptions.Item label={copy.jobId}><Text code>{detail.data.jobId}</Text></Descriptions.Item>
              <Descriptions.Item label={copy.operationId}><Text code>{detail.data.operationId}</Text></Descriptions.Item>
              <Descriptions.Item label={copy.type}>{detail.data.jobKind}</Descriptions.Item>
              <Descriptions.Item label={copy.status}>{detail.data.status}</Descriptions.Item>
              <Descriptions.Item label={copy.attempt}>{formatNumber(detail.data.attemptCount, locale)} / {formatNumber(detail.data.maxAttempts, locale)}</Descriptions.Item>
              <Descriptions.Item label={copy.cancelRequested}>{detail.data.cancelRequested ? copy.requested : copy.notRequested}</Descriptions.Item>
              <Descriptions.Item label={copy.fixedError}>{detail.data.errorCode ?? "—"}</Descriptions.Item>
              <Descriptions.Item label={copy.outbox}>{detail.data.outboxStatus}</Descriptions.Item>
              <Descriptions.Item label={copy.availableAt}>{formatDateTime(detail.data.availableAt, locale)}</Descriptions.Item>
              <Descriptions.Item label={copy.startedAt}>{formatDateTime(detail.data.startedAt, locale)}</Descriptions.Item>
              <Descriptions.Item label={copy.completedAt}>{formatDateTime(detail.data.completedAt, locale)}</Descriptions.Item>
              <Descriptions.Item label={copy.createdAt}>{formatDateTime(detail.data.createdAt, locale)}</Descriptions.Item>
              <Descriptions.Item label={copy.updatedAt}>{formatDateTime(detail.data.updatedAt, locale)}</Descriptions.Item>
            </Descriptions>
            <div>
              <Typography.Title level={4}>{copy.lifecycleEvents}</Typography.Title>
              {detail.data.events.length === 0 ? <Empty description={copy.noEvents} /> : (
                <Timeline items={detail.data.events.map((event) => ({ key: event.sequence, children: <EventLine event={event} copy={copy} /> }))} />
              )}
            </div>
          </Flex>
        )}
      </Drawer>
    </Flex>
  );
}
