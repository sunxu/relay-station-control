import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Alert, Button, Card, Descriptions, Empty, Flex, Form, Input, Modal, Popconfirm, Select, Space, Spin, Table, Tag, Typography } from "antd";
import type { AssetApi, DriverAsset, NodeAsset, NodeDetail, NodeFilters, NodeMonitoringResult, NodeProbeResult } from "../api/asset-types";
import { AssetApiError } from "../api/asset-types";
import type { NodeCapability } from "../api/generated/control";
import { useDriverAssets, useNodeAssets } from "../api/asset-hooks";
import { formatDateTime } from "../foundation/format";
import { useOptionalAppLocale } from "../foundation/FrontendFoundationProvider";
import { validateInternalHttpEndpoint } from "../validation/internal-http-endpoint";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { buildCredentialPatch } from "./asset-credential";

const { Text } = Typography;
const pageSize = 50;

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

function nodeErrorMessage(error: unknown, t: TFunction) {
  if (error instanceof AssetApiError) {
    if (error.status === 409) return t("nodes.errorConflict");
    if (error.status === 400) return t("nodes.errorConfig");
    if (error.status === 401) return t("nodes.errorExpired");
    if (error.status === 403) return t("nodes.errorForbidden");
  }
  return t("nodes.errorOperation");
}

function ResourceFrame({ loading, error, retry, children, t }: { loading: boolean; error: unknown; retry(): void; children: React.ReactNode; t: TFunction }) {
  if (loading) return <Flex justify="center" className="asset-loading"><Spin /></Flex>;
  if (error) return <Alert type="error" showIcon message={t("nodes.readFailed")} description={t("nodes.unavailable")} action={<Button data-testid="dashboard-retry-node" onClick={retry}>{t("nodes.retry")}</Button>} />;
  return children;
}

function capabilities(values: string[]) {
  return values.length > 0 ? <Space size={[0, 4]} wrap>{values.map((value) => <Tag key={value}>{value}</Tag>)}</Space> : "—";
}

export function NodeManagementView({ api, csrfToken = "", onUnauthorized }: { api: AssetApi; csrfToken?: string; onUnauthorized(): void }) {
  const { t } = useTranslation();
  const locale = useOptionalAppLocale()?.locale ?? "zh-CN";
  const [lifecycle, setLifecycle] = useState<"active" | "retired" | "all">("active");
  const [nodeType, setNodeType] = useState<string>();
  const [capability, setCapability] = useState<string>();
  const [monitoringActive, setMonitoringActive] = useState<boolean>();
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
  const filters = useMemo<NodeFilters>(() => ({ lifecycle, nodeType, capability, monitoringActive, cursor, limit: pageSize }), [lifecycle, nodeType, capability, monitoringActive, cursor]);
  const drivers = useDriverAssets(api);
  const nodes = useNodeAssets(api, filters);
  const activeDrivers = useMemo(() => selectableDrivers(drivers.data), [drivers.data]);
  const nodeTypeOptions = useMemo(() => [...new Set(activeDrivers.map((driver) => driver.nodeType))].map((value) => ({ value, label: value })), [activeDrivers]);
  const contractOptions = useMemo(() => activeDrivers.filter((driver) => driver.nodeType === nodeTypeValue).map((driver) => ({ value: driver.driverContractVersion, label: driver.driverContractVersion })), [activeDrivers, nodeTypeValue]);
  const selectedDriver = activeDrivers.find((driver) => driver.nodeType === nodeTypeValue && driver.driverContractVersion === driverContractValue);
  const nodeRegistrationDisabled = drivers.isPending || Boolean(drivers.error) || activeDrivers.length === 0;

  useEffect(() => {
    if ([nodes.error, drivers.error].some((error) => error instanceof AssetApiError && error.status === 401)) onUnauthorized();
  }, [drivers.error, nodes.error, onUnauthorized]);

  const handleError = (error: unknown) => {
    if (error instanceof AssetApiError && error.status === 401) onUnauthorized();
    else setNodeMessage(nodeErrorMessage(error, t));
  };

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
      if (nodeModal === "register" && api.registerNode) await api.registerNode({ command_id: commandId(), new_instance_id: values.new_instance_id!, display_name: values.display_name, management_endpoint: values.management_endpoint, node_type: values.node_type, driver_contract_version: values.driver_contract_version, capabilities: values.capabilities, ...(values.credential ? { management_credential: values.credential } : {}) }, csrfToken);
      else if (nodeModal === "edit" && selectedNode && api.editNode) await api.editNode(selectedNode.instanceId, { command_id: commandId(), expected_revision: selectedNode.revision, display_name: values.display_name, management_endpoint: values.management_endpoint, ...buildCredentialPatch("management_credential", values.credential_action, values.credential) }, csrfToken);
      else if (nodeModal === "replace" && selectedNode && api.replaceNode) await api.replaceNode(selectedNode.instanceId, { command_id: commandId(), expected_revision: selectedNode.revision, new_instance_id: values.new_instance_id!, display_name: values.display_name, management_endpoint: values.management_endpoint, node_type: values.node_type, driver_contract_version: values.driver_contract_version, capabilities: values.capabilities, ...(values.credential ? { management_credential: values.credential } : {}) }, csrfToken);
      setNodeModal(undefined);
      await nodes.refetch();
    } catch (error) { handleError(error); } finally { setNodeBusy(false); }
  };
  const retireNode = async (node: NodeAsset) => {
    if (!api.retireNode) return;
    setNodeBusy(true);
    try { await api.retireNode(node.instanceId, { command_id: commandId(), expected_revision: node.revision }, csrfToken); await nodes.refetch(); }
    catch (error) { handleError(error); } finally { setNodeBusy(false); }
  };
  const showNodeDetail = async (node: NodeAsset) => {
    const requestId = ++nodeDetailRequestRef.current;
    setNodeDetail(undefined); setProbeObservation(undefined); setMonitoringResult(undefined); setNodeOperationBusy(undefined); setNodeMessage(undefined);
    try {
      const detail = api.nodeDetail ? await api.nodeDetail(node.instanceId) : { asset: await api.node(node.instanceId), predecessor: null, successor: null };
      if (requestId === nodeDetailRequestRef.current) setNodeDetail(detail);
    } catch (error) { if (requestId === nodeDetailRequestRef.current) handleError(error); }
  };
  const refreshNodeAfterOperation = async (id: string, requestId: number) => {
    await nodes.refetch();
    if (requestId !== nodeDetailRequestRef.current) return;
    try {
      const detail = api.nodeDetail ? await api.nodeDetail(id) : { asset: await api.node(id), predecessor: null, successor: null };
      if (requestId === nodeDetailRequestRef.current) setNodeDetail(detail);
    } catch (error) { if (requestId === nodeDetailRequestRef.current) handleError(error); }
  };
  const runProbe = async (kind: "health" | "connection-test") => {
    if (!nodeDetail || nodeDetail.asset.lifecycleStatus !== "active") return;
    if (kind === "health" && !api.health) return;
    if (kind === "connection-test" && !api.connectionTest) return;
    const requestId = nodeDetailRequestRef.current;
    setNodeOperationBusy(kind); setProbeObservation(undefined); setNodeMessage(undefined);
    try {
      const result = kind === "health" ? await api.health!(nodeDetail.asset.instanceId) : await api.connectionTest!(nodeDetail.asset.instanceId, csrfToken);
      if (requestId === nodeDetailRequestRef.current) { setProbeObservation({ kind, result }); setNodeMessage(kind === "health" ? t("nodes.healthCompleted") : t("nodes.connectionCompleted")); }
    } catch (error) { if (requestId === nodeDetailRequestRef.current) handleError(error); }
    finally { if (requestId === nodeDetailRequestRef.current) setNodeOperationBusy(undefined); }
  };
  const runMonitoringCommand = async (kind: "monitoring-enable" | "monitoring-disable") => {
    if (!nodeDetail || nodeDetail.asset.lifecycleStatus !== "active") return;
    if (kind === "monitoring-enable" && !api.monitoringEnable) return;
    if (kind === "monitoring-disable" && !api.monitoringDisable) return;
    const id = nodeDetail.asset.instanceId;
    const key = `${id}:${kind}`;
    const stableCommandId = commandIdsRef.current.get(key) ?? commandId();
    commandIdsRef.current.set(key, stableCommandId);
    const requestId = nodeDetailRequestRef.current;
    setNodeOperationBusy(kind); setMonitoringResult(undefined); setNodeMessage(undefined);
    try {
      const result = kind === "monitoring-enable" ? await api.monitoringEnable!(id, stableCommandId, csrfToken) : await api.monitoringDisable!(id, stableCommandId, csrfToken);
      commandIdsRef.current.delete(key);
      if (requestId === nodeDetailRequestRef.current) { setMonitoringResult(result); setNodeMessage(t("nodes.operationCompleted", { result: result.result })); await refreshNodeAfterOperation(id, requestId); }
    } catch (error) { if (requestId === nodeDetailRequestRef.current) handleError(error); }
    finally { if (requestId === nodeDetailRequestRef.current) setNodeOperationBusy(undefined); }
  };
  const driverTypes = useMemo(() => [...new Set((drivers.data ?? []).map((driver) => driver.nodeType))].sort().map((value) => ({ value, label: value })), [drivers.data]);
  const columns = useMemo(() => [
    { title: t("nodes.node"), dataIndex: "displayName", key: "displayName" },
    { title: t("nodes.instanceId"), dataIndex: "instanceId", key: "instanceId", render: (value: string) => <Text code>{value}</Text> },
    { title: t("nodes.nodeTypeLabel"), dataIndex: "nodeType", key: "nodeType" },
    { title: t("nodes.contract"), dataIndex: "driverContractVersion", key: "driverContractVersion" },
    { title: t("nodes.capabilities"), dataIndex: "capabilities", key: "capabilities", render: capabilities },
    { title: t("nodes.monitoring"), dataIndex: "monitoringActive", key: "monitoringActive", render: (active: boolean, node: NodeAsset) => <Flex vertical gap={4}><Tag color={active ? "green" : "default"}>{active ? t("nodes.activated") : t("nodes.inactive")}</Tag>{active && <Text type="secondary">{formatDateTime(node.monitoringEffectiveFrom, locale)} – {formatDateTime(node.monitoringEffectiveTo, locale)}</Text>}</Flex> },
    { title: t("nodes.endpoint"), dataIndex: "managementEndpoint", key: "managementEndpoint", render: (value: string) => <Text code className="asset-endpoint">{value}</Text> },
    { title: t("nodes.credential"), dataIndex: "secretConfigured", key: "secretConfigured", render: (value: boolean) => value ? t("nodes.configured") : t("nodes.notConfigured") },
    { title: t("nodes.lifecycle"), dataIndex: "lifecycleStatus", key: "lifecycleStatus", render: (value: string) => <Tag color={value === "active" ? "green" : "default"}>{value === "active" ? t("nodes.currentNode") : t("nodes.retiredNode")}</Tag> },
    { title: t("nodes.revision"), dataIndex: "revision", key: "revision" },
    { title: t("nodes.operationLabel"), key: "actions", render: (_: unknown, node: NodeAsset) => <Space wrap><Button data-testid={`node-details-${node.instanceId}`} size="small" onClick={() => void showNodeDetail(node)}>{t("nodes.details")}</Button>{node.lifecycleStatus === "active" && <><Button data-testid={`node-edit-${node.instanceId}`} size="small" onClick={() => openNodeForm("edit", node)}>{t("nodes.edit")}</Button><Button data-testid={`node-replace-${node.instanceId}`} size="small" disabled={nodeRegistrationDisabled} onClick={() => openNodeForm("replace", node)}>{t("nodes.replace")}</Button><Popconfirm title={t("nodes.retireNodeTitle")} onConfirm={() => void retireNode(node)} okText={t("nodes.confirmRetire")} cancelText={t("nodes.cancel")}><Button data-testid={`node-retire-${node.instanceId}`} size="small" danger disabled={nodeBusy}>{t("nodes.retire")}</Button></Popconfirm></>}</Space> },
  ], [locale, nodeBusy, nodeRegistrationDisabled, t]);

  return <Flex vertical gap={20} data-testid="node-management-view"><div data-testid="nodes-registry"><Card title={t("nodes.nodes")} data-testid="nodes-card"><Flex vertical gap={16}>
    {nodeMessage && <Alert type="warning" showIcon message={nodeMessage} closable onClose={() => setNodeMessage(undefined)} />}
    <Flex justify="space-between" align="center" wrap gap={8}><Space wrap aria-label={t("nodes.nodeFilter")}>
      <Select data-testid="node-lifecycle-filter" aria-label={t("nodes.nodeLifecycle")} value={lifecycle} options={[{ value: "active", label: t("nodes.currentNode") }, { value: "retired", label: t("nodes.retiredNode") }, { value: "all", label: t("nodes.allNodes") }]} onChange={(value) => { setLifecycle(value); resetCursor(); }} />
      <Select data-testid="node-type-filter" aria-label={t("nodes.nodeType")} allowClear placeholder={t("nodes.allNodeTypes")} value={nodeType} options={driverTypes} onChange={(value) => { setNodeType(value); resetCursor(); }} />
      <Select data-testid="node-capability-filter" aria-label={t("nodes.capability")} allowClear placeholder={t("nodes.allCapabilities")} value={capability} options={[{ value: "management_health_read", label: "management_health_read" }, { value: "management_account_inventory_read", label: "management_account_inventory_read" }]} onChange={(value) => { setCapability(value); resetCursor(); }} />
      <Select data-testid="node-monitoring-filter" aria-label={t("nodes.monitoringStatus")} allowClear placeholder={t("nodes.monitoringStatus")} value={monitoringActive} options={[{ value: true, label: t("nodes.monitoringActive") }, { value: false, label: t("nodes.monitoringInactive") }]} onChange={(value) => { setMonitoringActive(value); resetCursor(); }} />
    </Space>{lifecycle === "active" && api.registerNode && <Button data-testid="node-register" type="primary" disabled={nodeRegistrationDisabled} onClick={() => openNodeForm("register")}>{t("nodes.registerNode")}</Button>}</Flex>
    {drivers.error && <Alert type="warning" showIcon message={t("nodes.driverUnavailable")} />}
    <ResourceFrame loading={nodes.isPending} error={nodes.error} retry={() => void nodes.refetch()} t={t}>{nodes.data && nodes.data.items.length > 0 ? <Table<NodeAsset> rowKey="instanceId" size="small" scroll={{ x: 1260 }} pagination={false} dataSource={nodes.data.items} columns={columns} /> : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t("nodes.emptyNodes")} />}</ResourceFrame>
    {!nodes.error && <Flex justify="end" gap={8}><Button data-testid="node-previous-page" disabled={cursorHistory.length === 1} onClick={() => setCursorHistory((history) => history.slice(0, -1))}>{t("nodes.previousPage")}</Button><Button data-testid="node-next-page" disabled={!nodes.data?.nextCursor} onClick={() => nodes.data?.nextCursor && setCursorHistory((history) => [...history, nodes.data!.nextCursor!])}>{t("nodes.nextPage")}</Button></Flex>}
  </Flex></Card></div>
  <Modal open={Boolean(nodeModal)} title={nodeModal === "register" ? t("nodes.formTitleRegister") : nodeModal === "edit" ? t("nodes.formTitleEdit") : t("nodes.formTitleReplace")} okText={t("nodes.save")} cancelText={t("nodes.cancel")} okButtonProps={{ "data-testid": "node-form-submit" }} cancelButtonProps={{ "data-testid": "node-form-cancel" }} confirmLoading={nodeBusy} onCancel={() => setNodeModal(undefined)} onOk={() => void nodeForm.submit()} destroyOnHidden><Form form={nodeForm} layout="vertical" onFinish={(values) => void submitNode(values)}>
    {nodeModal !== "edit" && <Form.Item name="new_instance_id" label={t("nodes.newInstanceId")} extra={t("nodes.generated")}><Input data-testid={nodeModal === "register" ? "node-register-instance-id" : "node-replace-instance-id"} readOnly /></Form.Item>}
    <Form.Item name="display_name" label={t("nodes.displayName")} rules={[{ required: true }]}><Input data-testid="node-form-display-name" maxLength={100} /></Form.Item>
    <Form.Item name="management_endpoint" label={t("nodes.endpoint")} rules={[{ required: true, message: t("nodes.validHttp") }, { validator: (_, value) => { const message = validateInternalHttpEndpoint(value); return message ? Promise.reject(new Error(message)) : Promise.resolve(); } }]}><Input data-testid="node-form-management-endpoint" placeholder="http://node:8317" /></Form.Item>
    {nodeModal !== "edit" && <><Form.Item name="node_type" label={t("nodes.nodeTypeLabel")} rules={[{ required: true }]}><Select data-testid="node-register-node-type" options={nodeTypeOptions} onChange={(value) => { const next = activeDrivers.find((driver) => driver.nodeType === value); nodeForm.setFieldsValue({ driver_contract_version: next?.driverContractVersion ?? "", capabilities: (next?.capabilities ?? []) as NodeCapability[] }); }} /></Form.Item><Form.Item name="driver_contract_version" label={t("nodes.contract")} rules={[{ required: true }]}><Select data-testid="node-register-driver-contract" options={contractOptions} onChange={(value) => { const next = activeDrivers.find((driver) => driver.nodeType === nodeTypeValue && driver.driverContractVersion === value); nodeForm.setFieldsValue({ capabilities: (next?.capabilities ?? []) as NodeCapability[] }); }} /></Form.Item><Form.Item name="capabilities" label={t("nodes.capabilities")} rules={[{ required: true }]}><Select data-testid="node-register-capabilities" mode="multiple" options={(selectedDriver?.capabilities ?? []).map((value) => ({ value, label: value }))} /></Form.Item></>}
    <Form.Item name="credential" label={t("nodes.managementCredential")} dependencies={["credential_action"]} rules={[({ getFieldValue }) => ({ validator: async (_, value) => { if (nodeModal === "edit" && getFieldValue("credential_action") === "set" && !value) throw new Error(t("nodes.requiredCredential")); } })]}><Input.Password data-testid="node-credential-input" autoComplete="new-password" /></Form.Item>
    {nodeModal === "edit" && <Form.Item name="credential_action" label={t("nodes.credentialAction")}><Select data-testid="node-credential-action" options={[{ value: "keep", label: t("nodes.credentialKeep") }, { value: "set", label: t("nodes.credentialSet") }, { value: "clear", label: t("nodes.credentialClear") }]} /></Form.Item>}
  </Form></Modal>
  <Modal data-testid="node-detail-dialog" open={Boolean(nodeDetail)} title={t("nodes.nodeDetails")} closeIcon={<span data-testid="node-detail-close">×</span>} footer={null} onCancel={() => { nodeDetailRequestRef.current += 1; setNodeDetail(undefined); setProbeObservation(undefined); setMonitoringResult(undefined); setNodeOperationBusy(undefined); setNodeMessage(undefined); }}>
    {nodeDetail && <><Descriptions column={1} size="small" bordered><Descriptions.Item label={t("nodes.instanceId")}><Text code>{nodeDetail.asset.instanceId}</Text></Descriptions.Item><Descriptions.Item label={t("nodes.status")}>{nodeDetail.asset.lifecycleStatus}</Descriptions.Item><Descriptions.Item label={t("nodes.revision")}>{nodeDetail.asset.revision}</Descriptions.Item><Descriptions.Item label={t("nodes.credential")}>{nodeDetail.asset.secretConfigured ? t("nodes.configured") : t("nodes.notConfigured")}</Descriptions.Item><Descriptions.Item label={t("nodes.monitoring")}>{nodeDetail.asset.monitoringActive ? t("nodes.activated") : t("nodes.inactive")}</Descriptions.Item><Descriptions.Item label={t("nodes.predecessorLabel")}>{nodeDetail.predecessor?.oldInstanceId ?? "—"}</Descriptions.Item><Descriptions.Item label={t("nodes.successorLabel")}>{nodeDetail.successor?.newInstanceId ?? "—"}</Descriptions.Item></Descriptions>{nodeDetail.asset.lifecycleStatus === "active" && <Flex vertical gap={12} data-testid="node-management-operations" style={{ marginTop: 16 }}><Space wrap><Button data-testid="node-health-button" loading={nodeOperationBusy === "health"} disabled={Boolean(nodeOperationBusy)} onClick={() => void runProbe("health")}>{t("nodes.health")}</Button><Button data-testid="node-connection-test-button" loading={nodeOperationBusy === "connection-test"} disabled={Boolean(nodeOperationBusy)} onClick={() => void runProbe("connection-test")}>{t("nodes.connectionTest")}</Button><Button data-testid="node-monitoring-enable-button" loading={nodeOperationBusy === "monitoring-enable"} disabled={Boolean(nodeOperationBusy)} onClick={() => void runMonitoringCommand("monitoring-enable")}>{t("nodes.monitoringEnable")}</Button><Popconfirm title={t("nodes.disableMonitoringTitle")} description={t("nodes.disableMonitoringDescription")} okText={t("nodes.disableMonitoring")} cancelText={t("nodes.cancel")} okButtonProps={{ "data-testid": "node-monitoring-disable-confirm" }} onConfirm={() => void runMonitoringCommand("monitoring-disable")}><Button data-testid="node-monitoring-disable-button" loading={nodeOperationBusy === "monitoring-disable"} disabled={Boolean(nodeOperationBusy)}>{t("nodes.disableMonitoring")}</Button></Popconfirm></Space>{probeObservation?.kind === "health" && <Alert data-testid="node-health-result" type={probeObservation.result.result === "success" ? "success" : "warning"} message={t("nodes.healthResult")} description={t("nodes.healthDescription", { reachability: probeObservation.result.reachable ? t("nodes.reachable") : t("nodes.unreachable"), reason: probeObservation.result.reason, latency: probeObservation.result.latencyMs })} showIcon />}{probeObservation?.kind === "connection-test" && <Alert data-testid="node-connection-test-result" type={probeObservation.result.result === "success" ? "success" : "warning"} message={t("nodes.connectionResult")} description={t("nodes.healthDescription", { reachability: probeObservation.result.reachable ? t("nodes.reachable") : t("nodes.unreachable"), reason: probeObservation.result.reason, latency: probeObservation.result.latencyMs })} showIcon />}{monitoringResult && <Alert data-testid="node-monitoring-result" type="success" message={t("nodes.monitoringResult", { result: monitoringResult.result })} description={t("nodes.monitoringDescription", { state: monitoringResult.monitoringActive ? "active" : "inactive", closed: monitoringResult.closedMonitoringCount, cancelled: monitoringResult.cancelledFutureMonitoringCount })} showIcon />}</Flex>}</>}
  </Modal></Flex>;
}
