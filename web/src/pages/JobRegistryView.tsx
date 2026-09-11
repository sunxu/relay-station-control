import { useEffect, useMemo, useState } from "react";
import type { HTMLAttributes } from "react";
import { Alert, Button, Card, Descriptions, Drawer, Empty, Flex, Input, Select, Space, Spin, Table, Tag, Timeline, Typography } from "antd";
import type { ColumnsType } from "antd/es/table";
import { useJob, useJobs } from "../api/job-hooks";
import { JobApiError, jobStatuses } from "../api/job-types";
import type { JobApi, JobFilters, JobLifecycleEvent, JobStatus, JobSummary } from "../api/job-types";
import { formatDateTime } from "../time";

const { Text } = Typography;
const defaultPageSize = 50;
type PageSize = 50 | 100 | 200;

function requestTime(value: string): string | undefined {
  if (!value) return undefined;
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? undefined : parsed.toISOString();
}

function ReadFailure({ error, retry, notFound }: { error: unknown; retry: () => void; notFound?: boolean }) {
  const missing = notFound && error instanceof JobApiError && error.status === 404;
  return (
    <Alert
      type="error"
      showIcon
      message={missing ? "任务不存在" : "读取失败"}
      description={missing ? "该任务不存在或已不在当前环境中。" : "Control 暂时无法读取当前任务状态。"}
      action={<Button onClick={retry}>重试读取</Button>}
    />
  );
}

function EventLine({ event }: { event: JobLifecycleEvent }) {
  return (
    <Flex vertical gap={2} data-testid={`job-event-${event.sequence}`}>
      <Space wrap>
        <Text strong>#{event.sequence} {event.eventType}</Text>
        <Tag>{event.fromStatus ?? "初始"} → {event.toStatus}</Tag>
        <Text type="secondary">attempt {event.attemptCount}</Text>
      </Space>
      <Text type="secondary">{event.actorType} · {formatDateTime(event.occurredAt)}</Text>
      {(event.reasonCode || event.errorCode) && <Text code>{[event.reasonCode, event.errorCode].filter(Boolean).join(" / ")}</Text>}
    </Flex>
  );
}

export function JobRegistryView({ api, onUnauthorized }: { api: JobApi; onUnauthorized: () => void }) {
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
    { title: "任务", dataIndex: "jobId", key: "jobId", render: (value: string) => <Text code>{value}</Text> },
    { title: "类型", dataIndex: "jobKind", key: "jobKind", render: (value: string) => <Tag>{value}</Tag> },
    { title: "状态", dataIndex: "status", key: "status", render: (value: JobStatus) => <Tag>{value}</Tag> },
    { title: "尝试", key: "attempts", render: (_, item) => `${item.attemptCount} / ${item.maxAttempts}` },
    { title: "Outbox", dataIndex: "outboxStatus", key: "outboxStatus" },
    { title: "创建时间", dataIndex: "createdAt", key: "createdAt", render: formatDateTime },
    { title: "操作", key: "view", render: (_, item) => <Button onClick={() => setSelectedJobID(item.jobId)}>查看详情</Button> },
  ], []);

  return (
    <Flex vertical gap={16} data-testid="job-registry-view">
      <Card title="任务过滤">
        <Space wrap>
          <Input
            aria-label="任务类型"
            placeholder="全部任务类型"
            value={jobKind}
            maxLength={64}
            onChange={(event) => { setJobKind(event.target.value); resetCursor(); }}
            style={{ width: 220 }}
          />
          <Select
            aria-label="任务状态"
            allowClear
            placeholder="全部状态"
            value={status}
            options={jobStatuses.map((value) => ({ value, label: value }))}
            onChange={(value) => { setStatus(value); resetCursor(); }}
            style={{ width: 190 }}
          />
          <Input aria-label="创建时间起点" type="datetime-local" value={createdFrom} onChange={(event) => { setCreatedFrom(event.target.value); resetCursor(); }} />
          <Input aria-label="创建时间终点" type="datetime-local" value={createdTo} onChange={(event) => { setCreatedTo(event.target.value); resetCursor(); }} />
          <Select<PageSize>
            aria-label="每页任务数"
            value={pageSize}
            options={[50, 100, 200].map((value) => ({ value: value as PageSize, label: `${value} / 页` }))}
            onChange={(value) => { setPageSize(value); resetCursor(); }}
            style={{ width: 130 }}
          />
        </Space>
      </Card>

      <Card title="持久任务" data-testid="jobs-card">
        {list.isPending && <Flex justify="center"><Spin /></Flex>}
        {list.error && <ReadFailure error={list.error} retry={() => void list.refetch()} />}
        {!list.isPending && !list.error && list.data?.items.length === 0 && <Empty description="当前过滤条件下没有持久任务" />}
        {!list.error && list.data && list.data.items.length > 0 && (
          <Table<JobSummary> rowKey={(job) => job.jobId} onRow={(job): HTMLAttributes<HTMLTableRowElement> => ({ "data-testid": "job-row", "data-job-id": job.jobId } as unknown as HTMLAttributes<HTMLTableRowElement>)} size="small" scroll={{ x: 1100 }} pagination={false} dataSource={list.data.items} columns={columns} />
        )}
        {!list.error && (
          <Flex justify="end" gap={8} style={{ marginTop: 16 }}>
            <Button disabled={cursorHistory.length === 1} onClick={() => setCursorHistory((history) => history.slice(0, -1))}>上一页</Button>
            <Button disabled={!list.data?.nextCursor} onClick={() => list.data?.nextCursor && setCursorHistory((history) => [...history, list.data!.nextCursor!])}>下一页</Button>
          </Flex>
        )}
      </Card>

      <Drawer title="任务详情" width={720} open={Boolean(selectedJobID)} onClose={() => setSelectedJobID(undefined)} destroyOnHidden>
        {detail.isPending && <Flex justify="center"><Spin /></Flex>}
        {detail.error && <ReadFailure error={detail.error} retry={() => void detail.refetch()} notFound />}
        {!detail.error && detail.data && (
          <Flex vertical gap={24} data-testid="job-detail">
            <Descriptions column={1} size="small" bordered>
              <Descriptions.Item label="Job ID"><Text code>{detail.data.jobId}</Text></Descriptions.Item>
              <Descriptions.Item label="Operation ID"><Text code>{detail.data.operationId}</Text></Descriptions.Item>
              <Descriptions.Item label="类型">{detail.data.jobKind}</Descriptions.Item>
              <Descriptions.Item label="状态">{detail.data.status}</Descriptions.Item>
              <Descriptions.Item label="尝试">{detail.data.attemptCount} / {detail.data.maxAttempts}</Descriptions.Item>
              <Descriptions.Item label="取消请求">{detail.data.cancelRequested ? "已请求" : "未请求"}</Descriptions.Item>
              <Descriptions.Item label="固定错误码">{detail.data.errorCode ?? "—"}</Descriptions.Item>
              <Descriptions.Item label="Outbox">{detail.data.outboxStatus}</Descriptions.Item>
              <Descriptions.Item label="可执行时间">{formatDateTime(detail.data.availableAt)}</Descriptions.Item>
              <Descriptions.Item label="开始时间">{formatDateTime(detail.data.startedAt)}</Descriptions.Item>
              <Descriptions.Item label="完成时间">{formatDateTime(detail.data.completedAt)}</Descriptions.Item>
              <Descriptions.Item label="创建时间">{formatDateTime(detail.data.createdAt)}</Descriptions.Item>
              <Descriptions.Item label="更新时间">{formatDateTime(detail.data.updatedAt)}</Descriptions.Item>
            </Descriptions>
            <div>
              <Typography.Title level={4}>生命周期事件</Typography.Title>
              {detail.data.events.length === 0 ? <Empty description="尚无生命周期事件" /> : (
                <Timeline items={detail.data.events.map((event) => ({ key: event.sequence, children: <EventLine event={event} /> }))} />
              )}
            </div>
          </Flex>
        )}
      </Drawer>
    </Flex>
  );
}
