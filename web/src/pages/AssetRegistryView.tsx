import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Alert,
  Button,
  Card,
  Checkbox,
  Form,
  Descriptions,
  Input,
  Empty,
  Flex,
  Modal,
  Popconfirm,
  Select,
  Space,
  Spin,
  Table,
  Tag,
  Typography,
} from "antd";
import type { ReactNode } from "react";
import type { DriverAsset, DriverScope, NodeAsset, NodeDetail, NodeFilters, AssetApi } from "../api/asset-types";
import { AssetApiError } from "../api/asset-types";
import type { GatewayAdminApi } from "../api/gateway-api";
import { GatewayApiError } from "../api/gateway-api";
import type { GatewayAsset, GatewayAssetDetailResponse, NodeCapability } from "../api/generated/control";
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

type GatewayFormValues = {
  display_name: string;
  management_endpoint: string;
  reader_secret_ref?: string;
  new_instance_id?: string;
  clear_secret?: boolean;
};

type NodeFormValues = {
  new_instance_id?: string;
  display_name: string;
  management_endpoint: string;
  node_type: string;
  driver_contract_version: string;
  capabilities: string;
  reader_secret_ref?: string;
  clear_secret?: boolean;
};

function commandId() {
  return globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

function gatewayError(error: unknown) {
  if (error instanceof GatewayApiError) {
    if (error.code === "stale_revision") return "资产已被其他管理员修改，请刷新后重试。";
    if (error.code === "command_conflict") return "该操作标识已用于其他请求。";
    if (error.code === "invalid_endpoint") return "Gateway 管理地址必须使用 http://。";
    if (error.code === "current_gateway_exists") return "当前已经存在 Gateway，请使用 Replace。";
    if (error.code === "asset_retired") return "已退役的 Gateway 不能继续操作。";
    if (error.status === 401) return "认证已过期，请重新登录。";
    if (error.status === 403) return "当前账号没有执行该操作的权限。";
  }
  return "操作未完成，请刷新后重试。";
}

function nodeErrorMessage(error: unknown) {
  if (error instanceof AssetApiError) {
    if (error.status === 409) return "Node 已被其他管理员修改或当前状态不允许该操作，请刷新后重试。";
    if (error.status === 400) return "Node 配置无效，请检查字段后重试。";
    if (error.status === 401) return "认证已过期，请重新登录。";
    if (error.status === 403) return "当前账号没有执行该操作的权限。";
  }
  return "Node 操作未完成，请刷新后重试。";
}

function GatewayManagement({ api, csrfToken, onUnauthorized }: { api: GatewayAdminApi; csrfToken: string; onUnauthorized(): void }) {
  const [lifecycle, setLifecycle] = useState<"active" | "retired" | "all">("active");
  const [cursorHistory, setCursorHistory] = useState<Array<string | undefined>>([undefined]);
  const [selected, setSelected] = useState<GatewayAsset>();
  const [detail, setDetail] = useState<GatewayAssetDetailResponse>();
  const [modal, setModal] = useState<"register" | "edit" | "replace">();
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string>();
  const [form] = Form.useForm<GatewayFormValues>();
  const cursor = cursorHistory.at(-1);
  const list = useQuery({ queryKey: ["asset-registry", "gateways", lifecycle, cursor], queryFn: () => api.list(lifecycle, cursor), retry: false, staleTime: 0 });

  const refresh = async () => { await list.refetch(); };
  const changeLifecycle = (next: "active" | "retired" | "all") => {
    setCursorHistory([undefined]);
    setLifecycle(next);
  };
  const previousPage = () => setCursorHistory((history) => history.length > 1 ? history.slice(0, -1) : history);
  const nextPage = () => {
    const next = list.data?.next_cursor;
    if (next && !list.isFetching) setCursorHistory((history) => [...history, next]);
  };
  const openForm = (mode: "register" | "edit" | "replace", asset?: GatewayAsset) => {
    setMessage(undefined);
    setSelected(asset);
    setModal(mode);
    form.setFieldsValue(asset ? {
      display_name: asset.display_name,
      management_endpoint: asset.management_endpoint,
      new_instance_id: undefined,
      reader_secret_ref: undefined,
      clear_secret: false,
    } : { display_name: "", management_endpoint: "http://", new_instance_id: "", reader_secret_ref: undefined, clear_secret: false });
  };

  const submit = async (values: GatewayFormValues) => {
    if (!modal) return;
    setBusy(true);
    try {
      if (modal === "register") {
        await api.register({ command_id: commandId(), new_instance_id: values.new_instance_id!, display_name: values.display_name, management_endpoint: values.management_endpoint, reader_secret_ref: values.reader_secret_ref || null }, csrfToken);
      } else if (modal === "edit" && selected) {
        await api.edit(selected.instance_id, { command_id: commandId(), expected_revision: selected.revision, display_name: values.display_name, management_endpoint: values.management_endpoint, ...(values.clear_secret ? { reader_secret_ref: null } : values.reader_secret_ref ? { reader_secret_ref: values.reader_secret_ref } : {}) }, csrfToken);
      } else if (modal === "replace" && selected) {
        await api.replace(selected.instance_id, { command_id: commandId(), expected_revision: selected.revision, new_instance_id: values.new_instance_id!, display_name: values.display_name, management_endpoint: values.management_endpoint, reader_secret_ref: values.reader_secret_ref || null }, csrfToken);
      }
      setModal(undefined);
      await refresh();
    } catch (error) {
      if (error instanceof GatewayApiError && error.status === 401) onUnauthorized();
      else setMessage(gatewayError(error));
    } finally { setBusy(false); }
  };

  const retire = async (asset: GatewayAsset) => {
    setBusy(true);
    try {
      await api.retire(asset.instance_id, { command_id: commandId(), expected_revision: asset.revision }, csrfToken);
      await refresh();
    } catch (error) {
      if (error instanceof GatewayApiError && error.status === 401) onUnauthorized();
      else setMessage(gatewayError(error));
    } finally { setBusy(false); }
  };

  const showDetail = async (asset: GatewayAsset) => {
    try { setDetail(await api.detail(asset.instance_id)); }
    catch (error) { if (error instanceof GatewayApiError && error.status === 401) onUnauthorized(); else setMessage(gatewayError(error)); }
  };

  const probe = async (asset: GatewayAsset, connectionTest: boolean) => {
    setBusy(true);
    try { await (connectionTest ? api.connectionTest(asset.instance_id, csrfToken) : api.health(asset.instance_id)); setMessage(connectionTest ? "Connection Test 已完成。" : "Health 检查已完成。"); }
    catch (error) { if (error instanceof GatewayApiError && error.status === 401) onUnauthorized(); else setMessage(gatewayError(error)); }
    finally { setBusy(false); }
  };

  const items = list.data?.items ?? [];
  return <Card title="Gateway 管理" data-testid="gateway-management-card">
    <Flex vertical gap={12}>
      {message && <Alert type="warning" showIcon message={message} closable onClose={() => setMessage(undefined)} />}
      <Flex justify="space-between" align="center" wrap gap={8}>
        <Select aria-label="Gateway 生命周期过滤" value={lifecycle} onChange={changeLifecycle} options={[{ value: "active", label: "当前 Gateway" }, { value: "retired", label: "历史 Gateway" }, { value: "all", label: "全部" }]} />
        {lifecycle === "active" && items.length === 0 && <Button type="primary" onClick={() => openForm("register")}>登记 Gateway</Button>}
      </Flex>
      {list.isPending ? <Spin /> : list.error ? <Alert type="error" message="Gateway 列表读取失败" action={<Button onClick={() => void refresh()}>重试</Button>} /> : <>
        <Table<GatewayAsset> rowKey="instance_id" size="small" pagination={false} dataSource={items} scroll={{ x: 1100 }} columns={[
        { title: "名称", dataIndex: "display_name" },
        { title: "Instance ID", dataIndex: "instance_id", render: (value: string) => <Text code>{value}</Text> },
        { title: "状态", dataIndex: "lifecycle_status", render: (value: string) => <Tag color={value === "active" ? "green" : "default"}>{value === "active" ? "当前" : "已退役"}</Tag> },
        { title: "Revision", dataIndex: "revision" },
        { title: "Secret", dataIndex: "secret_configured", render: (value: boolean) => value ? "已配置" : "未配置" },
        { title: "操作", key: "actions", render: (_: unknown, asset: GatewayAsset) => <Space wrap>
          <Button size="small" onClick={() => void showDetail(asset)}>详情</Button>
          <Button size="small" onClick={() => void probe(asset, false)} disabled={asset.lifecycle_status !== "active" || busy}>Health</Button>
          <Button size="small" onClick={() => void probe(asset, true)} disabled={asset.lifecycle_status !== "active" || busy}>Connection Test</Button>
          {asset.lifecycle_status === "active" && <><Button size="small" onClick={() => openForm("edit", asset)}>编辑</Button><Button size="small" onClick={() => openForm("replace", asset)}>Replace</Button><Popconfirm title="确认退役此 Gateway？" description="退役后将清理当前绑定，历史记录仍会保留。" onConfirm={() => void retire(asset)} okText="退役" cancelText="取消"><Button size="small" danger loading={busy}>Retire</Button></Popconfirm></>}
        </Space> },
        ]} />
        <Flex justify="end" gap={8} style={{ marginTop: 12 }}>
          <Button disabled={cursorHistory.length === 1 || list.isFetching} onClick={previousPage}>上一页</Button>
          <Button disabled={!list.data?.next_cursor || list.isFetching} onClick={nextPage}>下一页</Button>
        </Flex>
      </>}
    </Flex>
    <Modal open={Boolean(modal)} title={modal === "register" ? "登记 Gateway" : modal === "edit" ? "编辑 Gateway" : "Replace Gateway"} okText="保存" cancelText="取消" confirmLoading={busy} onCancel={() => setModal(undefined)} onOk={() => void form.submit()} destroyOnHidden>
      <Form form={form} layout="vertical" onFinish={(values) => void submit(values)}>
        {modal !== "edit" && <Form.Item name="new_instance_id" label="新 Instance ID" rules={[{ required: true, message: "请输入 UUID" }]}><Input placeholder="UUID" /></Form.Item>}
        <Form.Item name="display_name" label="显示名称" rules={[{ required: true, message: "请输入显示名称" }]}><Input maxLength={100} /></Form.Item>
        <Form.Item name="management_endpoint" label="Management endpoint" rules={[{ required: true, type: "url", message: "请输入有效的 http:// 地址" }, { validator: (_, value) => value && !value.startsWith("http://") ? Promise.reject(new Error("仅支持 http://")) : Promise.resolve() }]}><Input placeholder="http://gateway:8317" /></Form.Item>
        <Form.Item name="reader_secret_ref" label="Reader Secret reference（可选）" extra="仅提交引用；页面不会回显已保存的 Secret reference。"><Input.Password autoComplete="new-password" placeholder={modal === "edit" ? "留空表示不修改" : "可选"} /></Form.Item>
        {modal === "edit" && <Form.Item name="clear_secret" valuePropName="checked"><Checkbox>清除已保存的 Secret reference</Checkbox></Form.Item>}
      </Form>
    </Modal>
    <Modal open={Boolean(detail)} title="Gateway 详情" footer={null} onCancel={() => setDetail(undefined)}>
      {detail && <Descriptions column={1} size="small" bordered>
        <Descriptions.Item label="Instance ID"><Text code>{detail.asset.instance_id}</Text></Descriptions.Item>
        <Descriptions.Item label="状态">{detail.asset.lifecycle_status}</Descriptions.Item>
        <Descriptions.Item label="Revision">{detail.asset.revision}</Descriptions.Item>
        <Descriptions.Item label="名称">{detail.asset.display_name}</Descriptions.Item>
        <Descriptions.Item label="Endpoint"><Text code>{detail.asset.management_endpoint}</Text></Descriptions.Item>
        <Descriptions.Item label="Secret">{detail.asset.secret_configured ? "已配置" : "未配置"}</Descriptions.Item>
        <Descriptions.Item label="前驱">{detail.predecessor?.old_instance_id ?? "—"}</Descriptions.Item>
        <Descriptions.Item label="后继">{detail.successor?.new_instance_id ?? "—"}</Descriptions.Item>
      </Descriptions>}
    </Modal>
  </Card>;
}

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

export function AssetRegistryView({ api, gatewayApi, csrfToken = "", onUnauthorized }: { api: AssetApi; gatewayApi?: GatewayAdminApi; csrfToken?: string; onUnauthorized(): void }) {
	const [lifecycle, setLifecycle] = useState<"active" | "retired" | "all">("active");
  const [nodeType, setNodeType] = useState<string>();
  const [capability, setCapability] = useState<string>();
  const [monitoringActive, setMonitoringActive] = useState<boolean>();
  const [policyScope, setPolicyScope] = useState<DriverScope>();
  const [cursorHistory, setCursorHistory] = useState<Array<string | undefined>>([undefined]);
	const [nodeModal, setNodeModal] = useState<"register" | "edit" | "replace">();
	const [selectedNode, setSelectedNode] = useState<NodeAsset>();
	const [nodeDetail, setNodeDetail] = useState<NodeDetail>();
	const [nodeBusy, setNodeBusy] = useState(false);
	const [nodeMessage, setNodeMessage] = useState<string>();
	const [nodeForm] = Form.useForm<NodeFormValues>();
  const cursor = cursorHistory.at(-1);
  const filters = useMemo<NodeFilters>(() => ({
		lifecycle,
    nodeType,
    capability,
    monitoringActive,
    cursor,
    limit: pageSize,
  }), [lifecycle, nodeType, capability, monitoringActive, cursor]);

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
	const openNodeForm = (mode: "register" | "edit" | "replace", node?: NodeAsset) => {
		setSelectedNode(node);
		setNodeMessage(undefined);
		setNodeModal(mode);
		nodeForm.setFieldsValue(node ? {
			display_name: node.displayName,
			management_endpoint: node.managementEndpoint,
			node_type: node.nodeType,
			driver_contract_version: node.driverContractVersion,
			capabilities: node.capabilities.join(","),
			new_instance_id: undefined,
			reader_secret_ref: undefined,
			clear_secret: false,
		} : { display_name: "", management_endpoint: "https://", node_type: "", driver_contract_version: "", capabilities: "", new_instance_id: "" });
	};
	const submitNode = async (values: NodeFormValues) => {
		if (!nodeModal) return;
		setNodeBusy(true);
		try {
			const capabilities = (values.capabilities ?? "").split(",").map((value) => value.trim()).filter(Boolean) as NodeCapability[];
			if (nodeModal === "register" && api.registerNode) {
				await api.registerNode({ command_id: commandId(), new_instance_id: values.new_instance_id!, display_name: values.display_name, management_endpoint: values.management_endpoint, node_type: values.node_type, driver_contract_version: values.driver_contract_version, capabilities, ...(values.reader_secret_ref ? { reader_secret_ref: values.reader_secret_ref } : {}) }, csrfToken);
			} else if (nodeModal === "edit" && selectedNode && api.editNode) {
				await api.editNode(selectedNode.instanceId, { command_id: commandId(), expected_revision: selectedNode.revision, display_name: values.display_name, management_endpoint: values.management_endpoint, ...(values.clear_secret ? { reader_secret_ref: null } : values.reader_secret_ref ? { reader_secret_ref: values.reader_secret_ref } : {}) }, csrfToken);
			} else if (nodeModal === "replace" && selectedNode && api.replaceNode) {
				await api.replaceNode(selectedNode.instanceId, { command_id: commandId(), expected_revision: selectedNode.revision, new_instance_id: values.new_instance_id!, display_name: values.display_name, management_endpoint: values.management_endpoint, node_type: values.node_type, driver_contract_version: values.driver_contract_version, capabilities, ...(values.reader_secret_ref ? { reader_secret_ref: values.reader_secret_ref } : {}) }, csrfToken);
			}
			setNodeModal(undefined);
			await nodes.refetch();
		} catch (error) {
			if (error instanceof AssetApiError && error.status === 401) onUnauthorized();
			else setNodeMessage(nodeErrorMessage(error));
		} finally { setNodeBusy(false); }
	};
	const retireNode = async (node: NodeAsset) => {
		if (!api.retireNode) return;
		setNodeBusy(true);
		try { await api.retireNode(node.instanceId, { command_id: commandId(), expected_revision: node.revision }, csrfToken); await nodes.refetch(); }
		catch (error) { if (error instanceof AssetApiError && error.status === 401) onUnauthorized(); else setNodeMessage(nodeErrorMessage(error)); }
		finally { setNodeBusy(false); }
	};
	const showNodeDetail = async (node: NodeAsset) => {
		try {
			setNodeDetail(api.nodeDetail ? await api.nodeDetail(node.instanceId) : { asset: await api.node(node.instanceId), predecessor: null, successor: null });
		} catch (error) { if (error instanceof AssetApiError && error.status === 401) onUnauthorized(); else setNodeMessage(nodeErrorMessage(error)); }
	};
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
		{ title: "生命周期", dataIndex: "lifecycleStatus", key: "lifecycleStatus", render: (value: string) => <Tag color={value === "active" ? "green" : "default"}>{value === "active" ? "当前" : "已退役"}</Tag> },
		{ title: "Revision", dataIndex: "revision", key: "revision" },
		{ title: "操作", key: "actions", render: (_: unknown, node: NodeAsset) => <Space wrap>
			<Button size="small" onClick={() => void showNodeDetail(node)}>详情</Button>
			{node.lifecycleStatus === "active" && <>
				<Button size="small" onClick={() => openNodeForm("edit", node)}>编辑</Button>
				<Button size="small" onClick={() => openNodeForm("replace", node)}>Replace</Button>
				<Popconfirm title="确认退役此 Node？" onConfirm={() => void retireNode(node)} okText="退役" cancelText="取消"><Button size="small" danger disabled={nodeBusy}>Retire</Button></Popconfirm>
			</>}
		</Space> },
  ], [api, csrfToken, nodeBusy]);

  return (
    <Flex vertical gap={20} data-testid="asset-registry-view">
      {gatewayApi && <GatewayManagement api={gatewayApi} csrfToken={csrfToken} onUnauthorized={onUnauthorized} />}
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
			{nodeMessage && <Alert type="warning" showIcon message={nodeMessage} closable onClose={() => setNodeMessage(undefined)} />}
			<Flex justify="space-between" align="center" wrap gap={8}>
          <Space wrap aria-label="Node 过滤器">
			<Select aria-label="Node 生命周期" value={lifecycle} options={[{ value: "active", label: "当前 Node" }, { value: "retired", label: "历史 Node" }, { value: "all", label: "全部 Node" }]} onChange={(value) => { setLifecycle(value); resetCursor(); }} style={{ minWidth: 150 }} />
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
			{lifecycle === "active" && api.registerNode && <Button type="primary" onClick={() => openNodeForm("register")}>登记 Node</Button>}
			</Flex>
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
		<Modal open={Boolean(nodeModal)} title={nodeModal === "register" ? "登记 Node" : nodeModal === "edit" ? "编辑 Node" : "Replace Node"} okText="保存" cancelText="取消" confirmLoading={nodeBusy} onCancel={() => setNodeModal(undefined)} onOk={() => void nodeForm.submit()} destroyOnHidden>
			<Form form={nodeForm} layout="vertical" onFinish={(values) => void submitNode(values)}>
				{nodeModal !== "edit" && <Form.Item name="new_instance_id" label="新 Instance ID" rules={[{ required: true }]}><Input placeholder="UUID" /></Form.Item>}
				<Form.Item name="display_name" label="显示名称" rules={[{ required: true }]}><Input maxLength={100} /></Form.Item>
				<Form.Item name="management_endpoint" label="Management endpoint" rules={[{ required: true, type: "url" }]}><Input /></Form.Item>
				{nodeModal !== "edit" && <>
					<Form.Item name="node_type" label="Node 类型" rules={[{ required: true }]}><Input /></Form.Item>
					<Form.Item name="driver_contract_version" label="Driver 合约" rules={[{ required: true }]}><Input /></Form.Item>
					<Form.Item name="capabilities" label="Capabilities（逗号分隔）" rules={[{ required: true }]}><Input /></Form.Item>
				</>}
				<Form.Item name="reader_secret_ref" label="Reader Secret reference（可选）"><Input.Password autoComplete="new-password" /></Form.Item>
				{nodeModal === "edit" && <Form.Item name="clear_secret" valuePropName="checked"><Checkbox>清除已保存的 Secret reference</Checkbox></Form.Item>}
			</Form>
		</Modal>
		<Modal open={Boolean(nodeDetail)} title="Node 详情" footer={null} onCancel={() => setNodeDetail(undefined)}>
			{nodeDetail && <Descriptions column={1} size="small" bordered>
				<Descriptions.Item label="Instance ID"><Text code>{nodeDetail.asset.instanceId}</Text></Descriptions.Item>
				<Descriptions.Item label="状态">{nodeDetail.asset.lifecycleStatus}</Descriptions.Item>
				<Descriptions.Item label="Revision">{nodeDetail.asset.revision}</Descriptions.Item>
				<Descriptions.Item label="Secret">{nodeDetail.asset.secretConfigured ? "已配置" : "未配置"}</Descriptions.Item>
				<Descriptions.Item label="前驱">{nodeDetail.predecessor?.oldInstanceId ?? "—"}</Descriptions.Item>
				<Descriptions.Item label="后继">{nodeDetail.successor?.newInstanceId ?? "—"}</Descriptions.Item>
			</Descriptions>}
		</Modal>
      </Card>
    </Flex>
  );
}
