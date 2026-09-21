import { Alert, Button, Card, Flex, Select, Spin } from "antd";
import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo, useState } from "react";
import { generatedAccountInventoryApi } from "../api/account-inventory-api";
import { generatedAccountOperationsApi } from "../api/account-operations-api";
import { generatedAssetApi } from "../api/asset-api";
import { useNodeAsset, useNodeAssets } from "../api/asset-hooks";
import { generatedTopologyApi } from "../api/topology-api";
import { useTopologyProviders } from "../api/topology-hooks";
import { useAuth } from "../auth/AuthContext";
import { AssetApiError } from "../api/asset-types";
import { TopologyApiError } from "../api/topology-types";
import { AccountInventoryCapacity } from "../components/AccountInventoryCapacity";
import { AccountWorkspace } from "../components/AccountWorkspace";
import { useOptionalAppLocale } from "../foundation/FrontendFoundationProvider";
import { PageShell } from "../foundation/PageShell";
import { resources } from "../foundation/resources";

function initialInstanceId(): string | undefined {
  return new URLSearchParams(window.location.search).get("instance_id") ?? undefined;
}

export default function AccountsPage() {
  const auth = useAuth();
  const cache = useQueryClient();
  const locale = useOptionalAppLocale()?.locale ?? "zh-CN";
  const copy = resources[locale].translation.accounts;
  const [instanceId, setInstanceId] = useState(initialInstanceId);
  const [nodeCursor, setNodeCursor] = useState<string>();
  const nodes = useNodeAssets(generatedAssetApi, { limit: 200, cursor: nodeCursor });
  const selectedNode = useNodeAsset(generatedAssetApi, instanceId);
  const providers = useTopologyProviders(generatedTopologyApi, instanceId);
  const expireSession = useCallback(() => {
    void cache.cancelQueries();
    cache.clear();
    auth.clearSession();
  }, [auth, cache]);

  useEffect(() => {
    const assetUnauthorized = [nodes.error, selectedNode.error].some((error) => error instanceof AssetApiError && error.status === 401);
    const topologyUnauthorized = providers.error instanceof TopologyApiError && providers.error.status === 401;
    if (assetUnauthorized || topologyUnauthorized) expireSession();
  }, [expireSession, nodes.error, providers.error, selectedNode.error]);

  useEffect(() => {
    const onPopState = () => {
      setInstanceId(initialInstanceId());
      setNodeCursor(undefined);
    };
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, []);

  const nodeOptions = useMemo(() => {
    const options = (nodes.data?.items ?? []).map((node) => ({ value: node.instanceId, label: `${node.displayName} · ${node.instanceId}` }));
    if (selectedNode.data && !options.some((option) => option.value === selectedNode.data.instanceId)) options.unshift({ value: selectedNode.data.instanceId, label: `${selectedNode.data.displayName} · ${selectedNode.data.instanceId}` });
    return options;
  }, [nodes.data, selectedNode.data]);

  const selectNode = (next: string) => {
    setInstanceId(next);
    setNodeCursor(undefined);
    window.history.pushState(null, "", `/accounts?instance_id=${encodeURIComponent(next)}`);
  };

  if (!auth.session) return null;
  return <PageShell className="management-page" testId="accounts-page" title={copy.title} description={copy.description}>
    <Flex vertical gap={16}>
      <Card title={copy.node} data-testid="accounts-node-context">
        {nodes.isPending && <Flex justify="center"><Spin /></Flex>}
        {nodes.error && <Alert type="error" title={copy.nodeListUnavailable} action={<Button data-testid="accounts-node-retry" onClick={() => void nodes.refetch()}>{copy.retry}</Button>} />}
        <Select data-testid="accounts-node-selector" aria-label={copy.selectNode} placeholder={copy.selectNode} value={instanceId} onChange={selectNode} options={nodeOptions} style={{ width: "100%", maxWidth: 620 }} />
        {!nodes.error && nodes.data?.items.length === 0 && <div data-testid="accounts-node-empty"><Alert type="info" message={copy.empty} /></div>}
        <Flex justify="end" gap={8} style={{ marginTop: 8 }}>
          <Button data-testid="accounts-node-first" disabled={!nodeCursor} onClick={() => setNodeCursor(undefined)}>{copy.nodeFirstPage}</Button>
          <Button data-testid="accounts-node-next" disabled={!nodes.data?.nextCursor || nodes.isFetching} onClick={() => setNodeCursor(nodes.data?.nextCursor ?? undefined)}>{copy.nodeNextPage}</Button>
        </Flex>
        {selectedNode.error && (selectedNode.error as AssetApiError).status === 404 && <Alert type="warning" message={copy.selectedNodeNotFound} />}
      </Card>
      <AccountInventoryCapacity api={generatedAccountInventoryApi} csrfToken={auth.session.csrf_token} onUnauthorized={expireSession} surface="accounts" />
      <AccountWorkspace key={instanceId ?? "none"} api={generatedTopologyApi} accountOperationsApi={generatedAccountOperationsApi} csrfToken={auth.session.csrf_token} instanceId={instanceId} providers={providers.data?.providers ?? []} providerError={Boolean(providers.error)} onUnauthorized={expireSession} surface="accounts" />
    </Flex>
  </PageShell>;
}
