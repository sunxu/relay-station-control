import { useEffect, useMemo, useState } from "react";
import {
  Alert,
  Button,
  Card,
  Descriptions,
  Empty,
  Flex,
  Select,
  Space,
  Spin,
  Table,
  Tag,
  Typography,
} from "antd";
import type { ReactNode } from "react";
import type { DriverAsset, DriverScope, NodeAsset, NodeFilters, AssetApi } from "../api/asset-types";
import { AssetApiError } from "../api/asset-types";
import {
  useCurrentProviderPolicy,
  useDriverAssets,
  useEnvironmentAsset,
  useGatewayAsset,
  useNodeAssets,
} from "../api/asset-hooks";
import { formatDateTime } from "../time";

const { Text } = Typography;
const pageSize = 50;

function ResourceFrame({
  loading,
  error,
  retry,
  children,
}: {
  loading: boolean;
  error: unknown;
  retry(): void;
  children: ReactNode;
}) {
  if (loading) return <Flex justify="center" className="asset-loading"><Spin /></Flex>;
  if (error) {
    return (
      <Alert
        type="error"
        showIcon
        message="读取失败"
        description="当前数据不可用；未显示旧数据或内部错误。"
        action={<Button onClick={retry}>重试</Button>}
      />
    );
  }
  return children;
}

function capabilities(values: string[]) {
  return values.length > 0 ? <Space size={[0, 4]} wrap>{values.map((value) => <Tag key={value}>{value}</Tag>)}</Space> : "—";
}

export function AssetRegistryView({ api, onUnauthorized }: { api: AssetApi; onUnauthorized(): void }) {
  const [nodeType, setNodeType] = useState<string>();
  const [capability, setCapability] = useState<string>();
  const [monitoringActive, setMonitoringActive] = useState<boolean>();
  const [policyScope, setPolicyScope] = useState<DriverScope>();
  const [cursorHistory, setCursorHistory] = useState<Array<string | undefined>>([undefined]);
  const cursor = cursorHistory.at(-1);
  const filters = useMemo<NodeFilters>(() => ({
    nodeType,
    capability,
    monitoringActive,
    cursor,
    limit: pageSize,
  }), [nodeType, capability, monitoringActive, cursor]);

  const environment = useEnvironmentAsset(api);
  const gateway = useGatewayAsset(api);
  const drivers = useDriverAssets(api);
  const policy = useCurrentProviderPolicy(api, policyScope);
  const nodes = useNodeAssets(api, filters);
  const errors = [environment.error, gateway.error, drivers.error, policy.error, nodes.error];

  useEffect(() => {
    if (errors.some((error) => error instanceof AssetApiError && error.status === 401)) onUnauthorized();
  }, [environment.error, gateway.error, drivers.error, policy.error, nodes.error, onUnauthorized]);

  useEffect(() => {
    const firstDriver = drivers.data?.[0];
    if (policyScope || !firstDriver) return;
    setPolicyScope({
      nodeType: firstDriver.nodeType,
      driverContractVersion: firstDriver.driverContractVersion,
    });
  }, [drivers.data, policyScope]);

  const resetCursor = () => setCursorHistory([undefined]);
  const driverTypes = useMemo(() => {
    const values = new Set((drivers.data ?? []).map((driver) => driver.nodeType));
    return [...values].sort().map((value) => ({ value, label: value }));
  }, [drivers.data]);
  const policyScopes = useMemo(() => (drivers.data ?? []).map((driver) => ({
    value: `${driver.nodeType}\u0000${driver.driverContractVersion}`,
    label: `${driver.nodeType} / ${driver.driverContractVersion}`,
  })), [drivers.data]);

  const columns = useMemo(() => [
    { title: "Node", dataIndex: "displayName", key: "displayName" },
    { title: "Instance ID", dataIndex: "instanceId", key: "instanceId", render: (value: string) => <Text code>{value}</Text> },
    { title: "类型", dataIndex: "nodeType", key: "nodeType" },
    { title: "Driver 合约", dataIndex: "driverContractVersion", key: "driverContractVersion" },
    { title: "能力", dataIndex: "capabilities", key: "capabilities", render: capabilities },
    {
      title: "账号监控",
      dataIndex: "monitoringActive",
      key: "monitoringActive",
      render: (active: boolean, node: NodeAsset) => (
        <Flex vertical gap={4}>
          <Tag color={active ? "green" : "default"}>{active ? "已激活" : "未激活"}</Tag>
          {active && <Text type="secondary">{formatDateTime(node.monitoringEffectiveFrom)} – {formatDateTime(node.monitoringEffectiveTo)}</Text>}
        </Flex>
      ),
    },
    {
      title: "Management endpoint",
      dataIndex: "managementEndpoint",
      key: "managementEndpoint",
      render: (value: string) => <Text code className="asset-endpoint">{value}</Text>,
    },
    { title: "Secret", dataIndex: "secretConfigured", key: "secretConfigured", render: (value: boolean) => value ? "已配置" : "未配置" },
  ], []);

  return (
    <Flex vertical gap={20} data-testid="asset-registry-view">
      <div className="asset-card-grid">
        <Card title="环境" data-testid="environment-card">
          <ResourceFrame loading={environment.isPending} error={environment.error} retry={() => void environment.refetch()}>
            {environment.data && (
              <Descriptions column={1} size="small">
                <Descriptions.Item label="环境 ID"><Text code>{environment.data.environmentId}</Text></Descriptions.Item>
                <Descriptions.Item label="类型">{environment.data.environmentType}</Descriptions.Item>
                <Descriptions.Item label="名称">{environment.data.displayName}</Descriptions.Item>
              </Descriptions>
            )}
          </ResourceFrame>
        </Card>

        <Card title="Gateway" data-testid="gateway-card">
          <ResourceFrame loading={gateway.isPending} error={gateway.error} retry={() => void gateway.refetch()}>
            {gateway.data?.status === "configured" && gateway.data.gateway ? (
              <Descriptions column={1} size="small">
                <Descriptions.Item label="名称">{gateway.data.gateway.displayName}</Descriptions.Item>
                <Descriptions.Item label="Instance ID"><Text code>{gateway.data.gateway.instanceId}</Text></Descriptions.Item>
                <Descriptions.Item label="Endpoint"><Text code className="asset-endpoint">{gateway.data.gateway.managementEndpoint}</Text></Descriptions.Item>
                <Descriptions.Item label="Reader Secret">{gateway.data.gateway.secretConfigured ? "已配置" : "未配置"}</Descriptions.Item>
                <Descriptions.Item label="更新时间">{formatDateTime(gateway.data.gateway.updatedAt)}</Descriptions.Item>
              </Descriptions>
            ) : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="尚未登记 Gateway" />}
          </ResourceFrame>
        </Card>

        <Card title="当前 Provider 策略" data-testid="policy-card">
          <Flex vertical gap={12}>
            {policyScopes.length > 0 && (
              <Select
                aria-label="Provider 策略作用域"
                value={policyScope ? `${policyScope.nodeType}\u0000${policyScope.driverContractVersion}` : undefined}
                options={policyScopes}
                onChange={(value) => {
                  const next = drivers.data?.find((driver) => `${driver.nodeType}\u0000${driver.driverContractVersion}` === value);
                  if (next) setPolicyScope({ nodeType: next.nodeType, driverContractVersion: next.driverContractVersion });
                }}
              />
            )}
            <ResourceFrame loading={Boolean(policyScope) && policy.isPending} error={policy.error} retry={() => void policy.refetch()}>
              {!policyScope ? <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="请先登记 Driver" /> : policy.data?.status === "configured" && policy.data.policy ? (
                <Descriptions column={1} size="small">
                  <Descriptions.Item label="版本"><Text code>{policy.data.policy.policyVersionId}</Text></Descriptions.Item>
                  <Descriptions.Item label="作用域">{policy.data.policy.nodeType} / {policy.data.policy.driverContractVersion}</Descriptions.Item>
                  <Descriptions.Item label="Active">{capabilities(policy.data.policy.activeProviders)}</Descriptions.Item>
                  <Descriptions.Item label="Out of scope">{capabilities(policy.data.policy.outOfScopeProviders)}</Descriptions.Item>
                  <Descriptions.Item label="激活区间">{formatDateTime(policy.data.policy.effectiveFrom)} – {formatDateTime(policy.data.policy.effectiveTo)}</Descriptions.Item>
                </Descriptions>
              ) : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="尚未配置当前 Provider 策略" />}
            </ResourceFrame>
          </Flex>
        </Card>
      </div>

      <Card title="Driver 与 capability" data-testid="drivers-card">
        <ResourceFrame loading={drivers.isPending} error={drivers.error} retry={() => void drivers.refetch()}>
          {drivers.data && drivers.data.length > 0 ? (
            <Table<DriverAsset>
              rowKey={(driver) => `${driver.nodeType}:${driver.driverContractVersion}`}
              size="small"
              pagination={false}
              dataSource={drivers.data}
              columns={[
                { title: "Node 类型", dataIndex: "nodeType", key: "nodeType" },
                { title: "合约版本", dataIndex: "driverContractVersion", key: "driverContractVersion" },
                { title: "名称", dataIndex: "displayName", key: "displayName" },
                { title: "状态", dataIndex: "status", key: "status", render: (value) => <Tag>{value}</Tag> },
                { title: "能力", dataIndex: "capabilities", key: "capabilities", render: capabilities },
              ]}
            />
          ) : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="尚未登记 Driver" />}
        </ResourceFrame>
      </Card>

      <Card title="Relay Node" data-testid="nodes-card">
        <Flex vertical gap={16}>
          <Space wrap aria-label="Node 过滤器">
            <Select
              aria-label="Node 类型"
              allowClear
              placeholder="全部 Node 类型"
              value={nodeType}
              options={driverTypes}
              onChange={(value) => { setNodeType(value); resetCursor(); }}
              style={{ minWidth: 180 }}
            />
            <Select
              aria-label="Capability"
              allowClear
              placeholder="全部 capability"
              value={capability}
              options={[
                { value: "management_health_read", label: "management_health_read" },
                { value: "management_account_inventory_read", label: "management_account_inventory_read" },
              ]}
              onChange={(value) => { setCapability(value); resetCursor(); }}
              style={{ minWidth: 280 }}
            />
            <Select
              aria-label="监控状态"
              allowClear
              placeholder="全部监控状态"
              value={monitoringActive}
              options={[{ value: true, label: "监控已激活" }, { value: false, label: "监控未激活" }]}
              onChange={(value) => { setMonitoringActive(value); resetCursor(); }}
              style={{ minWidth: 180 }}
            />
          </Space>
          <ResourceFrame loading={nodes.isPending} error={nodes.error} retry={() => void nodes.refetch()}>
            {nodes.data && nodes.data.items.length > 0 ? (
              <Table<NodeAsset> rowKey="instanceId" size="small" scroll={{ x: 1260 }} pagination={false} dataSource={nodes.data.items} columns={columns} />
            ) : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="当前过滤条件下没有 Relay Node" />}
          </ResourceFrame>
          {!nodes.error && (
            <Flex justify="end" gap={8}>
              <Button disabled={cursorHistory.length === 1} onClick={() => setCursorHistory((history) => history.slice(0, -1))}>上一页</Button>
              <Button disabled={!nodes.data?.nextCursor} onClick={() => nodes.data?.nextCursor && setCursorHistory((history) => [...history, nodes.data!.nextCursor!])}>下一页</Button>
            </Flex>
          )}
        </Flex>
      </Card>
    </Flex>
  );
}
