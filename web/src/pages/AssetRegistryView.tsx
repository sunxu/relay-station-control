import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Alert,
  Button,
  Card,
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
import type { DriverAsset, DriverScope, NodeAsset, NodeDetail, NodeFilters, AssetApi, NodeMonitoringResult, NodeProbeResult } from "../api/asset-types";
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
import { formatDateTime } from "../foundation/format";
import { useOptionalAppLocale } from "../foundation/FrontendFoundationProvider";
import { validateInternalHttpEndpoint } from "../validation/internal-http-endpoint";
import { useTranslation } from "react-i18next";

const { Text } = Typography;
const pageSize = 50;

type GatewayFormValues = {
  display_name: string;
  management_endpoint: string;
  credential?: string;
  new_instance_id?: string;
  credential_action?: "keep" | "set" | "clear";
};

type NodeFormValues = {
  new_instance_id?: string;
  display_name: string;
  management_endpoint: string;
  node_type: string;
  driver_contract_version: string;
  capabilities: NodeCapability[];
  credential?: string;
  credential_action?: "keep" | "set" | "clear";
};

type CredentialAction = "keep" | "set" | "clear" | undefined;

export function buildCredentialPatch(field: "management_credential" | "directory_credential", action: CredentialAction, credential?: string): Record<string, string | null> {
  if (action === "clear") return { [field]: null };
  if (action === "set") return { [field]: credential ?? "" };
  return {};
}

function commandId() {
  return globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

function instanceId() {
  if (globalThis.crypto?.randomUUID) return globalThis.crypto.randomUUID();
  return "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx".replace(/[xy]/g, (character) => {
    const random = Math.floor(Math.random() * 16);
    const value = character === "x" ? random : (random & 0x3) | 0x8;
    return value.toString(16);
  });
}

function selectableDrivers(drivers: DriverAsset[] | undefined) {
  return [...(drivers ?? [])]
    .filter((driver) => driver.status === "active")
    .sort((left, right) => left.nodeType.localeCompare(right.nodeType) || left.driverContractVersion.localeCompare(right.driverContractVersion));
}

function gatewayError(error: unknown, t: unknown) {
  const translate = t as (key: string) => string;
  if (error instanceof GatewayApiError) {
    if (error.code === "stale_revision") return translate("assets.errorStale");
    if (error.code === "command_conflict") return translate("assets.errorConflict");
    if (error.code === "invalid_endpoint") return translate("assets.errorEndpoint");
    if (error.code === "current_gateway_exists") return translate("assets.errorGatewayExists");
    if (error.code === "asset_retired") return translate("assets.errorRetired");
    if (error.status === 401) return translate("assets.errorExpired");
    if (error.status === 403) return translate("assets.errorForbidden");
  }
  return translate("assets.errorOperation");
}

function nodeErrorMessage(error: unknown, t: unknown) {
  const translate = t as (key: string) => string;
  if (error instanceof AssetApiError) {
    if (error.status === 409) return translate("assets.errorNodeConflict");
    if (error.status === 400) return translate("assets.errorNodeConfig");
    if (error.status === 401) return translate("assets.errorExpired");
    if (error.status === 403) return translate("assets.errorForbidden");
  }
  return translate("assets.errorNodeOperation");
}

function GatewayManagement({ api, csrfToken, onUnauthorized }: { api: GatewayAdminApi; csrfToken: string; onUnauthorized(): void }) {
  const { t } = useTranslation();
  const locale = useOptionalAppLocale()?.locale ?? "zh-CN";
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
      credential: undefined,
      credential_action: "keep",
    } : { display_name: "", management_endpoint: "http://", new_instance_id: "", credential: undefined, credential_action: "set" });
  };

  const submit = async (values: GatewayFormValues) => {
    if (!modal) return;
    setBusy(true);
    try {
      if (modal === "register") {
        await api.register({ command_id: commandId(), new_instance_id: values.new_instance_id!, display_name: values.display_name, management_endpoint: values.management_endpoint, ...(values.credential ? { directory_credential: values.credential } : {}) }, csrfToken);
      } else if (modal === "edit" && selected) {
        await api.edit(selected.instance_id, { command_id: commandId(), expected_revision: selected.revision, display_name: values.display_name, management_endpoint: values.management_endpoint, ...buildCredentialPatch("directory_credential", values.credential_action, values.credential) }, csrfToken);
      } else if (modal === "replace" && selected) {
        await api.replace(selected.instance_id, { command_id: commandId(), expected_revision: selected.revision, new_instance_id: values.new_instance_id!, display_name: values.display_name, management_endpoint: values.management_endpoint, ...(values.credential ? { directory_credential: values.credential } : {}) }, csrfToken);
      }
      setModal(undefined);
      await refresh();
    } catch (error) {
      if (error instanceof GatewayApiError && error.status === 401) onUnauthorized();
      else setMessage(gatewayError(error, t));
    } finally { setBusy(false); }
  };

  const retire = async (asset: GatewayAsset) => {
    setBusy(true);
    try {
      await api.retire(asset.instance_id, { command_id: commandId(), expected_revision: asset.revision }, csrfToken);
      await refresh();
    } catch (error) {
      if (error instanceof GatewayApiError && error.status === 401) onUnauthorized();
      else setMessage(gatewayError(error, t));
    } finally { setBusy(false); }
  };

  const showDetail = async (asset: GatewayAsset) => {
    try { setDetail(await api.detail(asset.instance_id)); }
    catch (error) { if (error instanceof GatewayApiError && error.status === 401) onUnauthorized(); else setMessage(gatewayError(error, t)); }
  };

  const probe = async (asset: GatewayAsset, connectionTest: boolean) => {
    setBusy(true);
    try { await (connectionTest ? api.connectionTest(asset.instance_id, csrfToken) : api.health(asset.instance_id)); setMessage(connectionTest ? "Connection Test 已完成。" : "Health 检查已完成。"); }
    catch (error) { if (error instanceof GatewayApiError && error.status === 401) onUnauthorized(); else setMessage(gatewayError(error, t)); }
    finally { setBusy(false); }
  };

  const items = list.data?.items ?? [];
  return <Card title={t("assets.managementTitle")} data-testid="gateway-management-card">
    <Flex vertical gap={12}>
      {message && <Alert type="warning" showIcon message={message} closable onClose={() => setMessage(undefined)} />}
      <Flex justify="space-between" align="center" wrap gap={8}>
        <Select data-testid="gateway-lifecycle-filter" aria-label={t("assets.lifecycleFilter")} value={lifecycle} onChange={changeLifecycle} options={[{ value: "active", label: t("assets.currentGateway") }, { value: "retired", label: <span data-testid="gateway-filter-retired-option">{t("assets.retiredGateway")}</span> }, { value: "all", label: t("assets.all") }]} />
        {lifecycle === "active" && items.length === 0 && <Button data-testid="gateway-register" type="primary" onClick={() => openForm("register")}>{t("assets.registerGateway")}</Button>}
      </Flex>
      {list.isPending ? <Spin /> : list.error ? <Alert type="error" message={t("assets.listReadFailed")} action={<Button onClick={() => void refresh()}>{t("assets.retry")}</Button>} /> : <>
        <Table<GatewayAsset> rowKey="instance_id" size="small" pagination={false} dataSource={items} scroll={{ x: 1100 }} columns={[
        { title: "名称", dataIndex: "display_name" },
        { title: "Instance ID", dataIndex: "instance_id", render: (value: string) => <Text code>{value}</Text> },
        { title: "状态", dataIndex: "lifecycle_status", render: (value: string) => <Tag color={value === "active" ? "green" : "default"}>{value === "active" ? "当前" : "已退役"}</Tag> },
        { title: "Revision", dataIndex: "revision" },
        { title: "Secret", dataIndex: "secret_configured", render: (value: boolean) => value ? "已配置" : "未配置" },
        { title: "操作", key: "actions", render: (_: unknown, asset: GatewayAsset) => <Space wrap>
          <Button data-testid={`gateway-details-${asset.instance_id}`} size="small" onClick={() => void showDetail(asset)}>{t("assets.details")}</Button>
          <Button data-testid={`gateway-health-${asset.instance_id}`} size="small" onClick={() => void probe(asset, false)} disabled={asset.lifecycle_status !== "active" || busy}>{t("assets.health")}</Button>
          <Button data-testid={`gateway-connection-${asset.instance_id}`} size="small" onClick={() => void probe(asset, true)} disabled={asset.lifecycle_status !== "active" || busy}>{t("assets.connectionTest")}</Button>
          {asset.lifecycle_status === "active" && <><Button data-testid={`gateway-edit-${asset.instance_id}`} size="small" onClick={() => openForm("edit", asset)}>{t("assets.edit")}</Button><Button data-testid={`gateway-replace-${asset.instance_id}`} size="small" onClick={() => openForm("replace", asset)}>{t("assets.replace")}</Button><Popconfirm title={t("assets.retireGatewayTitle")} description={t("assets.retireGatewayDescription")} onConfirm={() => void retire(asset)} okText={t("assets.confirmRetire")} cancelText={t("assets.cancel")} okButtonProps={{ "data-testid": `gateway-retire-confirm-${asset.instance_id}` }} cancelButtonProps={{ "data-testid": `gateway-retire-cancel-${asset.instance_id}` }}><Button data-testid={`gateway-retire-${asset.instance_id}`} size="small" danger loading={busy}>{t("assets.retire")}</Button></Popconfirm></>}
        </Space> },
        ]} />
        <Flex justify="end" gap={8} style={{ marginTop: 12 }}>
          <Button disabled={cursorHistory.length === 1 || list.isFetching} onClick={previousPage}>{t("topology.firstPage")}</Button>
          <Button disabled={!list.data?.next_cursor || list.isFetching} onClick={nextPage}>{t("topology.nextPage")}</Button>
        </Flex>
      </>}
    </Flex>
    <Modal open={Boolean(modal)} title={modal === "register" ? t("assets.gatewayFormRegister") : modal === "edit" ? t("assets.gatewayFormEdit") : t("assets.gatewayFormReplace")} okText={t("assets.save")} cancelText={t("assets.cancel")} confirmLoading={busy} okButtonProps={{ "data-testid": "gateway-form-submit" }} cancelButtonProps={{ "data-testid": "gateway-form-cancel" }} onCancel={() => setModal(undefined)} onOk={() => void form.submit()} destroyOnHidden>
      <Form form={form} layout="vertical" onFinish={(values) => void submit(values)}>
        {modal !== "edit" && <Form.Item name="new_instance_id" label={t("assets.newInstanceId")} rules={[{ required: true, message: t("assets.uuid") }]}><Input data-testid="gateway-form-instance-id" placeholder={t("assets.uuid")} /></Form.Item>}
        <Form.Item name="display_name" label={t("assets.displayName")} rules={[{ required: true, message: t("assets.displayName") }]}><Input data-testid="gateway-form-display-name" maxLength={100} /></Form.Item>
        <Form.Item name="management_endpoint" label={t("assets.endpoint")} rules={[{ required: true, message: t("assets.validHttp") }, { validator: (_, value) => { const message = validateInternalHttpEndpoint(value); return message ? Promise.reject(new Error(message)) : Promise.resolve(); } }]}><Input data-testid="gateway-form-endpoint" placeholder="http://gateway:8317" /></Form.Item>
        <Form.Item name="credential" label={t("assets.directoryCredential")} dependencies={["credential_action"]} rules={[({ getFieldValue }) => ({ validator: async (_, value) => { if (modal === "edit" && getFieldValue("credential_action") === "set" && !value) throw new Error(t("assets.requiredCredential")); } })]} extra={t("assets.noSavedCredential")}><Input.Password autoComplete="new-password" placeholder={modal === "edit" ? t("assets.credentialActionPlaceholder") : t("assets.optional")} /></Form.Item>
        {modal === "edit" && <Form.Item name="credential_action" label={t("assets.credentialAction")}><Select options={[{ value: "keep", label: t("assets.credentialKeep") }, { value: "set", label: t("assets.credentialSet") }, { value: "clear", label: t("assets.credentialClear") }]} /></Form.Item>}
      </Form>
    </Modal>
    <Modal data-testid="gateway-detail-dialog" open={Boolean(detail)} title={t("assets.gatewayDetails")} footer={null} closeIcon={<span data-testid="gateway-detail-close">×</span>} onCancel={() => setDetail(undefined)}>
      {detail && <Descriptions column={1} size="small" bordered>
        <Descriptions.Item label={t("assets.instanceId")}><Text code>{detail.asset.instance_id}</Text></Descriptions.Item>
        <Descriptions.Item label={t("assets.status")}>{detail.asset.lifecycle_status}</Descriptions.Item>
        <Descriptions.Item label={t("assets.revision")}>{detail.asset.revision}</Descriptions.Item>
        <Descriptions.Item label={t("assets.name")}>{detail.asset.display_name}</Descriptions.Item>
        <Descriptions.Item label={t("assets.endpoint")}><Text code>{detail.asset.management_endpoint}</Text></Descriptions.Item>
        <Descriptions.Item label={t("assets.credential")}>{detail.asset.secret_configured ? t("assets.configured") : t("assets.notConfigured")}</Descriptions.Item>
        <Descriptions.Item label={t("assets.predecessorLabel")}>{detail.predecessor?.old_instance_id ?? "—"}</Descriptions.Item>
        <Descriptions.Item label={t("assets.successorLabel")}>{detail.successor?.new_instance_id ?? "—"}</Descriptions.Item>
      </Descriptions>}
    </Modal>
  </Card>;
}

function ResourceFrame({
  loading,
  error,
  retry,
  children,
  labels,
}: {
  loading: boolean;
  error: unknown;
  retry(): void;
  children: ReactNode;
  labels: { readFailed: string; unavailable: string; retry: string };
}) {
  if (loading) return <Flex justify="center" className="asset-loading"><Spin /></Flex>;
  if (error) {
    return (
      <Alert
        type="error"
        showIcon
        message={labels.readFailed}
        description={labels.unavailable}
        action={<Button onClick={retry}>{labels.retry}</Button>}
      />
    );
  }
  return children;
}

function capabilities(values: string[]) {
  return values.length > 0 ? <Space size={[0, 4]} wrap>{values.map((value) => <Tag key={value}>{value}</Tag>)}</Space> : "—";
}

export function AssetRegistryView({ api, gatewayApi, csrfToken = "", onUnauthorized }: { api: AssetApi; gatewayApi?: GatewayAdminApi; csrfToken?: string; onUnauthorized(): void }) {
	const { t } = useTranslation();
	const locale = useOptionalAppLocale()?.locale ?? "zh-CN";
	const labels = { readFailed: t("assets.readFailed"), unavailable: t("assets.unavailable"), retry: t("assets.retry") };
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
	const [nodeOperationBusy, setNodeOperationBusy] = useState<"health" | "connection-test" | "monitoring-enable" | "monitoring-disable">();
	const [probeObservation, setProbeObservation] = useState<{ kind: "health" | "connection-test"; result: NodeProbeResult }>();
	const [monitoringResult, setMonitoringResult] = useState<NodeMonitoringResult>();
	const nodeDetailRequestRef = useRef(0);
	const commandIdsRef = useRef(new Map<string, string>());
	const [nodeForm] = Form.useForm<NodeFormValues>();
  const nodeTypeValue = Form.useWatch("node_type", nodeForm);
  const driverContractValue = Form.useWatch("driver_contract_version", nodeForm);
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
  const activeDrivers = useMemo(() => selectableDrivers(drivers.data), [drivers.data]);
  const nodeTypeOptions = useMemo(() => [...new Set(activeDrivers.map((driver) => driver.nodeType))].map((value) => ({ value, label: value })), [activeDrivers]);
  const contractOptions = useMemo(() => activeDrivers.filter((driver) => driver.nodeType === nodeTypeValue).map((driver) => ({ value: driver.driverContractVersion, label: driver.driverContractVersion })), [activeDrivers, nodeTypeValue]);
  const selectedDriver = activeDrivers.find((driver) => driver.nodeType === nodeTypeValue && driver.driverContractVersion === driverContractValue);
  const nodeRegistrationDisabled = drivers.isPending || Boolean(drivers.error) || activeDrivers.length === 0;
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
		const inheritedDriver = node && activeDrivers.find((driver) => driver.nodeType === node.nodeType && driver.driverContractVersion === node.driverContractVersion);
		const initialDriver = inheritedDriver ?? activeDrivers[0];
		setSelectedNode(node);
		setNodeMessage(undefined);
		setNodeModal(mode);
		nodeForm.setFieldsValue(mode === "edit" && node ? {
			display_name: node.displayName,
			management_endpoint: node.managementEndpoint,
			node_type: node.nodeType,
			driver_contract_version: node.driverContractVersion,
			capabilities: node.capabilities as NodeCapability[],
			new_instance_id: undefined,
      credential: undefined,
      credential_action: "keep",
		} : {
			display_name: node?.displayName ?? "",
			management_endpoint: node?.managementEndpoint ?? "http://",
			node_type: initialDriver?.nodeType ?? "",
			driver_contract_version: initialDriver?.driverContractVersion ?? "",
			capabilities: (initialDriver?.capabilities ?? []) as NodeCapability[],
			new_instance_id: instanceId(),
      credential: undefined,
      credential_action: "set",
		});
	};
	const submitNode = async (values: NodeFormValues) => {
		if (!nodeModal) return;
		setNodeBusy(true);
		try {
			const capabilities = values.capabilities;
			if (nodeModal === "register" && api.registerNode) {
        await api.registerNode({ command_id: commandId(), new_instance_id: values.new_instance_id!, display_name: values.display_name, management_endpoint: values.management_endpoint, node_type: values.node_type, driver_contract_version: values.driver_contract_version, capabilities, ...(values.credential ? { management_credential: values.credential } : {}) }, csrfToken);
			} else if (nodeModal === "edit" && selectedNode && api.editNode) {
        await api.editNode(selectedNode.instanceId, { command_id: commandId(), expected_revision: selectedNode.revision, display_name: values.display_name, management_endpoint: values.management_endpoint, ...buildCredentialPatch("management_credential", values.credential_action, values.credential) }, csrfToken);
			} else if (nodeModal === "replace" && selectedNode && api.replaceNode) {
        await api.replaceNode(selectedNode.instanceId, { command_id: commandId(), expected_revision: selectedNode.revision, new_instance_id: values.new_instance_id!, display_name: values.display_name, management_endpoint: values.management_endpoint, node_type: values.node_type, driver_contract_version: values.driver_contract_version, capabilities, ...(values.credential ? { management_credential: values.credential } : {}) }, csrfToken);
			}
			setNodeModal(undefined);
			await nodes.refetch();
		} catch (error) {
			if (error instanceof AssetApiError && error.status === 401) onUnauthorized();
			else setNodeMessage(nodeErrorMessage(error, t));
		} finally { setNodeBusy(false); }
	};
	const retireNode = async (node: NodeAsset) => {
		if (!api.retireNode) return;
		setNodeBusy(true);
		try { await api.retireNode(node.instanceId, { command_id: commandId(), expected_revision: node.revision }, csrfToken); await nodes.refetch(); }
		catch (error) { if (error instanceof AssetApiError && error.status === 401) onUnauthorized(); else setNodeMessage(nodeErrorMessage(error, t)); }
		finally { setNodeBusy(false); }
	};
	const showNodeDetail = async (node: NodeAsset) => {
		const requestId = ++nodeDetailRequestRef.current;
		setNodeDetail(undefined);
		setProbeObservation(undefined);
		setMonitoringResult(undefined);
		setNodeOperationBusy(undefined);
		setNodeMessage(undefined);
		try {
			const detail = api.nodeDetail ? await api.nodeDetail(node.instanceId) : { asset: await api.node(node.instanceId), predecessor: null, successor: null };
			if (requestId === nodeDetailRequestRef.current) setNodeDetail(detail);
		} catch (error) {
			if (requestId !== nodeDetailRequestRef.current) return;
			if (error instanceof AssetApiError && error.status === 401) onUnauthorized(); else setNodeMessage(nodeErrorMessage(error, t));
		}
	};
	const refreshNodeAfterOperation = async (instanceId: string, requestId: number) => {
		await nodes.refetch();
		if (requestId !== nodeDetailRequestRef.current) return;
		const detail = api.nodeDetail ? await api.nodeDetail(instanceId) : { asset: await api.node(instanceId), predecessor: null, successor: null };
		if (requestId === nodeDetailRequestRef.current) setNodeDetail(detail);
	};
	const runProbe = async (kind: "health" | "connection-test") => {
		if (!nodeDetail || nodeDetail.asset.lifecycleStatus !== "active") return;
		const method = kind === "health" ? api.health : api.connectionTest;
		if (!method) return;
		const requestId = nodeDetailRequestRef.current;
		setNodeOperationBusy(kind);
		setProbeObservation(undefined);
		setNodeMessage(undefined);
		try {
			const result = kind === "health" ? await api.health!(nodeDetail.asset.instanceId) : await api.connectionTest!(nodeDetail.asset.instanceId, csrfToken);
			if (requestId === nodeDetailRequestRef.current) {
				setProbeObservation({ kind, result });
				setNodeMessage(kind === "health" ? t("assets.healthCompleted") : t("assets.connectionCompleted"));
			}
		} catch (error) {
			if (requestId !== nodeDetailRequestRef.current) return;
			if (error instanceof AssetApiError && error.status === 401) onUnauthorized(); else setNodeMessage(nodeErrorMessage(error, t));
		} finally {
			if (requestId === nodeDetailRequestRef.current) setNodeOperationBusy(undefined);
		}
	};
	const runMonitoringCommand = async (kind: "monitoring-enable" | "monitoring-disable") => {
		if (!nodeDetail || nodeDetail.asset.lifecycleStatus !== "active") return;
		const method = kind === "monitoring-enable" ? api.monitoringEnable : api.monitoringDisable;
		if (!method) return;
		const instanceId = nodeDetail.asset.instanceId;
		const key = `${instanceId}:${kind}`;
		const stableCommandId = commandIdsRef.current.get(key) ?? commandId();
		commandIdsRef.current.set(key, stableCommandId);
		const requestId = nodeDetailRequestRef.current;
		setNodeOperationBusy(kind);
		setMonitoringResult(undefined);
		setNodeMessage(undefined);
		try {
			const result = kind === "monitoring-enable"
				? await api.monitoringEnable!(instanceId, stableCommandId, csrfToken)
				: await api.monitoringDisable!(instanceId, stableCommandId, csrfToken);
			commandIdsRef.current.delete(key);
			if (requestId === nodeDetailRequestRef.current) {
				setMonitoringResult(result);
				setNodeMessage(t("assets.operationCompleted", { result: result.result }));
				await refreshNodeAfterOperation(instanceId, requestId);
			}
		} catch (error) {
			if (requestId !== nodeDetailRequestRef.current) return;
		if (error instanceof AssetApiError && error.status === 401) onUnauthorized(); else setNodeMessage(nodeErrorMessage(error, t));
		} finally {
			if (requestId === nodeDetailRequestRef.current) setNodeOperationBusy(undefined);
		}
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
    { title: t("assets.node"), dataIndex: "displayName", key: "displayName" },
    { title: t("assets.instanceId"), dataIndex: "instanceId", key: "instanceId", render: (value: string) => <Text code>{value}</Text> },
    { title: t("assets.nodeTypeLabel"), dataIndex: "nodeType", key: "nodeType" },
    { title: t("assets.contract"), dataIndex: "driverContractVersion", key: "driverContractVersion" },
    { title: t("assets.capabilities"), dataIndex: "capabilities", key: "capabilities", render: capabilities },
    {
      title: t("assets.monitoring"),
      dataIndex: "monitoringActive",
      key: "monitoringActive",
      render: (active: boolean, node: NodeAsset) => (
        <Flex vertical gap={4}>
          <Tag color={active ? "green" : "default"}>{active ? t("assets.activated") : t("assets.inactive")}</Tag>
          {active && <Text type="secondary">{formatDateTime(node.monitoringEffectiveFrom, locale)} – {formatDateTime(node.monitoringEffectiveTo, locale)}</Text>}
        </Flex>
      ),
    },
    {
      title: t("assets.endpoint"),
      dataIndex: "managementEndpoint",
      key: "managementEndpoint",
      render: (value: string) => <Text code className="asset-endpoint">{value}</Text>,
    },
		{ title: t("assets.credential"), dataIndex: "secretConfigured", key: "secretConfigured", render: (value: boolean) => value ? t("assets.configured") : t("assets.notConfigured") },
		{ title: t("assets.lifecycle"), dataIndex: "lifecycleStatus", key: "lifecycleStatus", render: (value: string) => <Tag color={value === "active" ? "green" : "default"}>{value === "active" ? t("assets.currentNode") : t("assets.retiredNode")}</Tag> },
		{ title: t("assets.revision"), dataIndex: "revision", key: "revision" },
		{ title: t("assets.operationLabel"), key: "actions", render: (_: unknown, node: NodeAsset) => <Space wrap>
			<Button data-testid={`node-details-${node.instanceId}`} size="small" onClick={() => void showNodeDetail(node)}>{t("assets.details")}</Button>
			{node.lifecycleStatus === "active" && <>
				<Button data-testid={`node-edit-${node.instanceId}`} size="small" onClick={() => openNodeForm("edit", node)}>{t("assets.edit")}</Button>
				<Button data-testid={`node-replace-${node.instanceId}`} size="small" disabled={nodeRegistrationDisabled} onClick={() => openNodeForm("replace", node)}>{t("assets.replace")}</Button>
				<Popconfirm title={t("assets.retireNodeTitle")} onConfirm={() => void retireNode(node)} okText={t("assets.confirmRetire")} cancelText={t("assets.cancel")}><Button size="small" danger disabled={nodeBusy}>{t("assets.retire")}</Button></Popconfirm>
			</>}
		</Space> },
  ], [api, csrfToken, locale, nodeBusy, t]);

  return (
    <Flex vertical gap={20} data-testid="asset-registry-view">
      {gatewayApi && <GatewayManagement api={gatewayApi} csrfToken={csrfToken} onUnauthorized={onUnauthorized} />}
      <div className="asset-card-grid">
        <Card title={t("assets.environment")} data-testid="environment-card">
          <ResourceFrame loading={environment.isPending} error={environment.error} retry={() => void environment.refetch()} labels={labels}>
            {environment.data && (
              <Descriptions column={1} size="small">
                <Descriptions.Item label={t("assets.environmentId")}><Text code>{environment.data.environmentId}</Text></Descriptions.Item>
                <Descriptions.Item label={t("assets.type")}>{environment.data.environmentType}</Descriptions.Item>
                <Descriptions.Item label={t("assets.name")}>{environment.data.displayName}</Descriptions.Item>
              </Descriptions>
            )}
          </ResourceFrame>
        </Card>

        <Card title={t("assets.gateway")} data-testid="gateway-card">
          <ResourceFrame loading={gateway.isPending} error={gateway.error} retry={() => void gateway.refetch()} labels={labels}>
            {gateway.data?.status === "configured" && gateway.data.gateway ? (
              <Descriptions column={1} size="small">
                <Descriptions.Item label={t("assets.name")}>{gateway.data.gateway.displayName}</Descriptions.Item>
                <Descriptions.Item label={t("assets.instanceId")}><Text code>{gateway.data.gateway.instanceId}</Text></Descriptions.Item>
                <Descriptions.Item label={t("assets.endpoint")}><Text code className="asset-endpoint">{gateway.data.gateway.managementEndpoint}</Text></Descriptions.Item>
                <Descriptions.Item label={t("assets.credential")}>{gateway.data.gateway.secretConfigured ? t("assets.configured") : t("assets.notConfigured")}</Descriptions.Item>
                <Descriptions.Item label={t("assets.updatedAt")}>{formatDateTime(gateway.data.gateway.updatedAt, locale)}</Descriptions.Item>
              </Descriptions>
            ) : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t("assets.emptyGateway")} />}
          </ResourceFrame>
        </Card>

        <Card title={t("assets.policy")} data-testid="policy-card">
          <Flex vertical gap={12}>
            {policyScopes.length > 0 && (
              <Select
                aria-label={t("assets.providerPolicyScope")}
                value={policyScope ? `${policyScope.nodeType}\u0000${policyScope.driverContractVersion}` : undefined}
                options={policyScopes}
                onChange={(value) => {
                  const next = drivers.data?.find((driver) => `${driver.nodeType}\u0000${driver.driverContractVersion}` === value);
                  if (next) setPolicyScope({ nodeType: next.nodeType, driverContractVersion: next.driverContractVersion });
                }}
              />
            )}
            <ResourceFrame loading={Boolean(policyScope) && policy.isPending} error={policy.error} retry={() => void policy.refetch()} labels={labels}>
              {!policyScope ? <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t("assets.selectDriver")} /> : policy.data?.status === "configured" && policy.data.policy ? (
                <Descriptions column={1} size="small">
                  <Descriptions.Item label={t("assets.version")}><Text code>{policy.data.policy.policyVersionId}</Text></Descriptions.Item>
                  <Descriptions.Item label={t("assets.scope")}>{policy.data.policy.nodeType} / {policy.data.policy.driverContractVersion}</Descriptions.Item>
                  <Descriptions.Item label={t("assets.policyActive")}>{capabilities(policy.data.policy.activeProviders)}</Descriptions.Item>
                  <Descriptions.Item label={t("assets.policyOutOfScope")}>{capabilities(policy.data.policy.outOfScopeProviders)}</Descriptions.Item>
                  <Descriptions.Item label={t("assets.activeWindow")}>{formatDateTime(policy.data.policy.effectiveFrom, locale)} – {formatDateTime(policy.data.policy.effectiveTo, locale)}</Descriptions.Item>
                </Descriptions>
              ) : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t("assets.emptyPolicy")} />}
            </ResourceFrame>
          </Flex>
        </Card>
      </div>

      <Card title={t("assets.drivers")} data-testid="drivers-card">
        <ResourceFrame loading={drivers.isPending} error={drivers.error} retry={() => void drivers.refetch()} labels={labels}>
          {drivers.data && drivers.data.length > 0 ? (
            <Table<DriverAsset>
              rowKey={(driver) => `${driver.nodeType}:${driver.driverContractVersion}`}
              size="small"
              pagination={false}
              dataSource={drivers.data}
              columns={[
                { title: t("assets.nodeTypeLabel"), dataIndex: "nodeType", key: "nodeType" },
                { title: t("assets.contractVersion"), dataIndex: "driverContractVersion", key: "driverContractVersion" },
                { title: t("assets.name"), dataIndex: "displayName", key: "displayName" },
                { title: t("assets.status"), dataIndex: "status", key: "status", render: (value) => <Tag>{value}</Tag> },
                { title: t("assets.capabilities"), dataIndex: "capabilities", key: "capabilities", render: capabilities },
              ]}
            />
          ) : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t("assets.emptyDriver")} />}
        </ResourceFrame>
      </Card>

      <Card title={t("assets.nodes")} data-testid="nodes-card">
        <Flex vertical gap={16}>
			{nodeMessage && <Alert type="warning" showIcon message={nodeMessage} closable onClose={() => setNodeMessage(undefined)} />}
			<Flex justify="space-between" align="center" wrap gap={8}>
          <Space wrap aria-label={t("assets.nodeFilter")}>
			<Select data-testid="node-lifecycle-filter" aria-label={t("assets.nodeLifecycle")} value={lifecycle} options={[{ value: "active", label: t("assets.currentNode") }, { value: "retired", label: t("assets.retiredNode") }, { value: "all", label: t("assets.allNodes") }]} onChange={(value) => { setLifecycle(value); resetCursor(); }} style={{ minWidth: 150 }} />
            <Select
              aria-label={t("assets.nodeType")}
              allowClear
              placeholder={t("assets.allNodeTypes")}
              value={nodeType}
              options={driverTypes}
              onChange={(value) => { setNodeType(value); resetCursor(); }}
              style={{ minWidth: 180 }}
            />
            <Select
              aria-label={t("assets.capability")}
              allowClear
              placeholder={t("assets.allCapabilities")}
              value={capability}
              options={[
                { value: "management_health_read", label: "management_health_read" },
                { value: "management_account_inventory_read", label: "management_account_inventory_read" },
              ]}
              onChange={(value) => { setCapability(value); resetCursor(); }}
              style={{ minWidth: 280 }}
            />
            <Select
              aria-label={t("assets.monitoringStatus")}
              allowClear
              placeholder={t("assets.monitoringStatus")}
              value={monitoringActive}
              options={[{ value: true, label: t("assets.monitoringActive") }, { value: false, label: t("assets.monitoringInactive") }]}
              onChange={(value) => { setMonitoringActive(value); resetCursor(); }}
              style={{ minWidth: 180 }}
            />
          </Space>
			{lifecycle === "active" && api.registerNode && <Button type="primary" disabled={nodeRegistrationDisabled} onClick={() => openNodeForm("register")}>{t("assets.registerNode")}</Button>}
			</Flex>
			{drivers.error && <Alert type="warning" showIcon message={t("assets.driverUnavailable")} />}
          <ResourceFrame loading={nodes.isPending} error={nodes.error} retry={() => void nodes.refetch()} labels={labels}>
            {nodes.data && nodes.data.items.length > 0 ? (
              <Table<NodeAsset> rowKey="instanceId" size="small" scroll={{ x: 1260 }} pagination={false} dataSource={nodes.data.items} columns={columns} />
            ) : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t("assets.emptyNodes")} />}
          </ResourceFrame>
          {!nodes.error && (
            <Flex justify="end" gap={8}>
              <Button disabled={cursorHistory.length === 1} onClick={() => setCursorHistory((history) => history.slice(0, -1))}>{t("assets.previousPage")}</Button>
              <Button disabled={!nodes.data?.nextCursor} onClick={() => nodes.data?.nextCursor && setCursorHistory((history) => [...history, nodes.data!.nextCursor!])}>{t("assets.nextPage")}</Button>
            </Flex>
          )}
        </Flex>
		<Modal open={Boolean(nodeModal)} title={nodeModal === "register" ? t("assets.formTitleRegister") : nodeModal === "edit" ? t("assets.formTitleEdit") : t("assets.formTitleReplace")} okText={t("assets.save")} cancelText={t("assets.cancel")} okButtonProps={{ "data-testid": "node-form-submit" }} cancelButtonProps={{ "data-testid": "node-form-cancel" }} confirmLoading={nodeBusy} onCancel={() => setNodeModal(undefined)} onOk={() => void nodeForm.submit()} destroyOnHidden>
			<Form form={nodeForm} layout="vertical" onFinish={(values) => void submitNode(values)}>
				{nodeModal !== "edit" && <Form.Item name="new_instance_id" label={t("assets.newInstanceId")} extra={t("assets.generated")}><Input data-testid={nodeModal === "register" ? "node-register-instance-id" : "node-replace-instance-id"} readOnly /></Form.Item>}
				<Form.Item name="display_name" label={t("assets.displayName")} rules={[{ required: true }]}><Input data-testid="node-form-display-name" maxLength={100} /></Form.Item>
				<Form.Item name="management_endpoint" label={t("assets.endpoint")} rules={[{ required: true, message: t("assets.validHttp") }, { validator: (_, value) => { const message = validateInternalHttpEndpoint(value); return message ? Promise.reject(new Error(message)) : Promise.resolve(); } }]}><Input data-testid="node-form-management-endpoint" placeholder="http://node:8317" /></Form.Item>
				{nodeModal !== "edit" && <>
					<Form.Item name="node_type" label={t("assets.nodeTypeLabel")} rules={[{ required: true }]}><Select data-testid="node-register-node-type" options={nodeTypeOptions} onChange={(value) => { const next = activeDrivers.find((driver) => driver.nodeType === value); nodeForm.setFieldsValue({ driver_contract_version: next?.driverContractVersion ?? "", capabilities: (next?.capabilities ?? []) as NodeCapability[] }); }} /></Form.Item>
					<Form.Item name="driver_contract_version" label={t("assets.contract")} rules={[{ required: true }]}><Select data-testid="node-register-driver-contract" options={contractOptions} onChange={(value) => { const next = activeDrivers.find((driver) => driver.nodeType === nodeTypeValue && driver.driverContractVersion === value); nodeForm.setFieldsValue({ capabilities: (next?.capabilities ?? []) as NodeCapability[] }); }} /></Form.Item>
					<Form.Item name="capabilities" label={t("assets.capabilities")} rules={[{ required: true }]}><Select data-testid="node-register-capabilities" mode="multiple" options={(selectedDriver?.capabilities ?? []).map((value) => ({ value, label: value }))} /></Form.Item>
				</>}
				<Form.Item name="credential" label={t("assets.managementCredential")} dependencies={["credential_action"]} rules={[({ getFieldValue }) => ({ validator: async (_, value) => { if (nodeModal === "edit" && getFieldValue("credential_action") === "set" && !value) throw new Error(t("assets.requiredCredential")); } })]}><Input.Password autoComplete="new-password" /></Form.Item>
				{nodeModal === "edit" && <Form.Item name="credential_action" label={t("assets.credentialAction")}><Select options={[{ value: "keep", label: t("assets.credentialKeep") }, { value: "set", label: t("assets.credentialSet") }, { value: "clear", label: t("assets.credentialClear") }]} /></Form.Item>}
			</Form>
		</Modal>
		<Modal open={Boolean(nodeDetail)} title={t("assets.nodeDetails")} closeIcon={<span data-testid="node-detail-close">×</span>} footer={null} onCancel={() => { nodeDetailRequestRef.current += 1; setNodeDetail(undefined); setProbeObservation(undefined); setMonitoringResult(undefined); setNodeOperationBusy(undefined); setNodeMessage(undefined); }}>
			{nodeDetail && <>
			<Descriptions column={1} size="small" bordered>
				<Descriptions.Item label={t("assets.instanceId")}><Text code>{nodeDetail.asset.instanceId}</Text></Descriptions.Item>
				<Descriptions.Item label={t("assets.status")}>{nodeDetail.asset.lifecycleStatus}</Descriptions.Item>
				<Descriptions.Item label={t("assets.revision")}>{nodeDetail.asset.revision}</Descriptions.Item>
				<Descriptions.Item label={t("assets.credential")}>{nodeDetail.asset.secretConfigured ? t("assets.configured") : t("assets.notConfigured")}</Descriptions.Item>
				<Descriptions.Item label={t("assets.monitoring")}>{nodeDetail.asset.monitoringActive ? t("assets.activated") : t("assets.inactive")}</Descriptions.Item>
				<Descriptions.Item label={t("assets.predecessorLabel")}>{nodeDetail.predecessor?.oldInstanceId ?? "—"}</Descriptions.Item>
				<Descriptions.Item label={t("assets.successorLabel")}>{nodeDetail.successor?.newInstanceId ?? "—"}</Descriptions.Item>
			</Descriptions>
			{nodeDetail.asset.lifecycleStatus === "active" && <Flex vertical gap={12} data-testid="node-management-operations" style={{ marginTop: 16 }}>
				<Space wrap>
					<Button data-testid="node-health-button" loading={nodeOperationBusy === "health"} disabled={Boolean(nodeOperationBusy)} onClick={() => void runProbe("health")}>{t("assets.health")}</Button>
					<Button data-testid="node-connection-test-button" loading={nodeOperationBusy === "connection-test"} disabled={Boolean(nodeOperationBusy)} onClick={() => void runProbe("connection-test")}>{t("assets.connectionTest")}</Button>
					<Button data-testid="node-monitoring-enable-button" loading={nodeOperationBusy === "monitoring-enable"} disabled={Boolean(nodeOperationBusy)} onClick={() => void runMonitoringCommand("monitoring-enable")}>{t("assets.monitoringEnable")}</Button>
					<Popconfirm title={t("assets.disableMonitoringTitle")} description={t("assets.disableMonitoringDescription")} okText={t("assets.disableMonitoring")} cancelText={t("assets.cancel")} okButtonProps={{ "data-testid": "node-monitoring-disable-confirm" }} onConfirm={() => void runMonitoringCommand("monitoring-disable")}>
						<Button data-testid="node-monitoring-disable-button" loading={nodeOperationBusy === "monitoring-disable"} disabled={Boolean(nodeOperationBusy)}>{t("assets.disableMonitoring")}</Button>
					</Popconfirm>
				</Space>
				{probeObservation?.kind === "health" && <Alert data-testid="node-health-result" type={probeObservation.result.result === "success" ? "success" : "warning"} message={t("assets.healthResult")} description={t("assets.healthDescription", { reachability: probeObservation.result.reachable ? t("assets.reachable") : t("assets.unreachable"), reason: probeObservation.result.reason, latency: probeObservation.result.latencyMs })} showIcon />}
				{probeObservation?.kind === "connection-test" && <Alert data-testid="node-connection-test-result" type={probeObservation.result.result === "success" ? "success" : "warning"} message={t("assets.connectionResult")} description={t("assets.healthDescription", { reachability: probeObservation.result.reachable ? t("assets.reachable") : t("assets.unreachable"), reason: probeObservation.result.reason, latency: probeObservation.result.latencyMs })} showIcon />}
				{monitoringResult && <Alert data-testid="node-monitoring-result" type="success" message={t("assets.monitoringResult", { result: monitoringResult.result })} description={t("assets.monitoringDescription", { state: monitoringResult.monitoringActive ? "active" : "inactive", closed: monitoringResult.closedMonitoringCount, cancelled: monitoringResult.cancelledFutureMonitoringCount })} showIcon />}
			</Flex>}
			</>}
		</Modal>
      </Card>
    </Flex>
  );
}
