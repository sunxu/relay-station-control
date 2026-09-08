import { useEffect } from "react";
import { Alert, Button, Card, Flex, Space, Spin, Tag, Typography } from "antd";
import { useAccountInventoryPollCapacity } from "../api/account-inventory-hooks";
import { AccountInventoryApiError } from "../api/account-inventory-types";
import type { AccountInventoryApi, AccountInventoryPollCapacity } from "../api/account-inventory-types";
import { formatDateTime } from "../time";

const { Text } = Typography;

function capacityStatusLabel(value: AccountInventoryPollCapacity["status"]): string {
  if (value === "ready") return "可用";
  if (value === "capacity_exceeded") return "容量不足";
  return "已禁用";
}

export function AccountInventoryCapacity({ api, csrfToken, onUnauthorized }: {
  api: AccountInventoryApi; csrfToken: string; onUnauthorized: () => void;
}) {
  const capacity = useAccountInventoryPollCapacity(api, csrfToken);
  useEffect(() => {
    if (capacity.error instanceof AccountInventoryApiError && capacity.error.status === 401) onUnauthorized();
  }, [capacity.error, onUnauthorized]);
  return <Card title="采集容量诊断" data-testid="account-inventory-capacity">
    <Flex justify="space-between" align="center" gap={12} wrap>
      <Text type="secondary">当前环境采集条件评估，不代表最近一次采集成功</Text>
      <Button onClick={() => capacity.mutate()} loading={capacity.isPending}>刷新容量</Button>
    </Flex>
    {capacity.isPending && <Flex role="status" aria-label="正在读取采集容量" justify="center" style={{ marginTop: 12 }}><Spin /></Flex>}
    {capacity.error && <Alert style={{ marginTop: 12 }} type="error" showIcon message="容量诊断暂不可用" />}
    {!capacity.error && capacity.data && <CapacitySummary value={capacity.data} />}
  </Card>;
}

function CapacitySummary({ value }: { value: AccountInventoryPollCapacity }) {
  return <Flex vertical gap={8} style={{ marginTop: 12 }} data-testid="account-inventory-capacity-summary">
    {value.status === "capacity_exceeded" && <Alert type="warning" showIcon message="监控规模超过采集容量，整轮新采集暂停" description="请调整采集并发，或通过监控管理流程减少监控 Node；完成后刷新容量诊断。已有账号证据保留。" />}

    <Space wrap>
      <Tag color={value.status === "ready" ? "green" : value.status === "capacity_exceeded" ? "orange" : "default"}>{capacityStatusLabel(value.status)}</Tag>
      <Text>启用：{value.enabled ? "是" : "否"}</Text>
      <Text>符合采集条件的 Node：{value.eligibleNodeCount}</Text>
      <Text>有效容量：{value.effectiveCapacity}</Text>
      <Text>并发：{value.concurrency}</Text>
    </Space>
    <Space wrap>
      <Text type="secondary">请求 {value.requestTimeoutMs}ms</Text>
      <Text type="secondary">落库预算 {value.finalizeTimeoutMs}ms</Text>
      <Text type="secondary">生命周期 {value.lifecycleTimeoutMs}ms</Text>
      <Text type="secondary">任务认领预算 {value.claimTimeoutMs}ms</Text>
      <Text type="secondary">调度余量 {value.dispatchMarginMs}ms</Text>
      <Text type="secondary">启动宽限 {value.pollStartGraceMs}ms</Text>
    </Space>
    <Text type="secondary">评估槽位：{formatDateTime(value.evaluatedSlot)}；时间：{formatDateTime(value.evaluatedAt)}</Text>
  </Flex>;
}
