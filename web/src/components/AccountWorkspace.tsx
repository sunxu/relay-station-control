import { Alert, Button, Card, Empty, Flex, Input, Select, Spin } from "antd";
import { useCallback, useEffect, useMemo, useState } from "react";
import { AccountQualityIncidentsSection } from "../pages/AccountQualityIncidentsSection";
import { AccountDetailsDrawer } from "./AccountDetailsDrawer";
import { AccountList, type AccountListRow } from "./AccountList";
import { UploadNewAccountAction } from "./AccountOperationsPanel";
import { useAccountListQuery } from "../api/account-list-hooks";
import type { AccountInventoryBasicStatus } from "../api/account-inventory-types";
import { accountInventoryBasicStatuses, accountInventoryLifecycles } from "../api/account-inventory-types";
import type { AccountOperationsApi } from "../api/account-operations-api";
import type { AccountQualityFilter, AccountQualityLifecycle, AccountQualityWindow, AccountListFilters } from "../api/account-quality-types";
import type { TopologyApi, TopologyProviderState } from "../api/topology-types";
import { TopologyApiError } from "../api/topology-types";
import { resources } from "../foundation/resources";
import { useOptionalAppLocale } from "../foundation/FrontendFoundationProvider";

function accountReadError(error: unknown, copy: { invalidAccountFilter: string; forbiddenAccountList: string; selectedNodeNotFound: string; unsupportedAccountList: string; readUnavailable: string; unavailableAccountList: string }): string {
  if (error instanceof TopologyApiError) {
    if (error.status === 400) return copy.invalidAccountFilter;
    if (error.status === 403) return copy.forbiddenAccountList;
    if (error.status === 404) return copy.selectedNodeNotFound;
    if (error.status === 409) return copy.unsupportedAccountList;
    if (error.status === 503) return copy.readUnavailable;
  }
  return copy.unavailableAccountList;
}

function toAccountRow(item: TopologyApi["accountList"] extends (...args: never[]) => Promise<infer Response> ? Response extends { items: (infer Item)[] } ? Item : never : never): AccountListRow {
  const extended = item as typeof item & { inventory?: Record<string, unknown>; recent_requests?: AccountListRow["recent_requests"] };
  const inventory = extended.inventory ?? {};
  return {
    account_key: item.account_key,
    email: item.email,
    provider: item.provider,
    basic_status: String(inventory.basic_status ?? "unknown"),
    lifecycle: String(inventory.lifecycle ?? "present"),
    quality: item.quality,
    request_count: item.request_count,
    success_count: item.success_count,
    failure_count: item.failure_count,
    success_rate: item.success_rate,
    p95_latency_ms: item.p95_latency_ms,
    last_success_at: item.last_success_at,
    last_failure_at: item.last_failure_at,
    last_failure_class: item.last_failure_class,
    recent_requests: extended.recent_requests ?? [],
    consecutive_missing_count: Number(inventory.consecutive_missing_count ?? 0),
    first_seen_at: typeof inventory.first_seen_at === "string" ? inventory.first_seen_at : undefined,
    last_seen_at: typeof inventory.last_seen_at === "string" ? inventory.last_seen_at : undefined,
    last_refresh_at: typeof inventory.last_refresh_at === "string" ? inventory.last_refresh_at : null,
    next_retry_at: typeof inventory.next_retry_at === "string" ? inventory.next_retry_at : null,
    provider_last_complete_at: typeof inventory.provider_last_complete_at === "string" ? inventory.provider_last_complete_at : null,
    provider_degraded: inventory.provider_degraded === true,
    snapshot_freshness: typeof inventory.snapshot_freshness === "string" ? inventory.snapshot_freshness : undefined,
    availability: "availability" in item ? item.availability : undefined,
    token_state: item.token_state,
    expected_valid_until: item.expected_valid_until,
  };
}

export function AccountWorkspace({ api, accountOperationsApi, csrfToken, instanceId, providers, providerError, onUnauthorized, copyOverrides, queryTestId = "accounts-query" }: {
  api: TopologyApi;
  accountOperationsApi?: AccountOperationsApi;
  csrfToken: string;
  instanceId?: string;
  providers: TopologyProviderState[];
  providerError: boolean;
  onUnauthorized: () => void;
  copyOverrides?: Partial<Record<keyof typeof resources["zh-CN"]["translation"]["accounts"], string>>;
  queryTestId?: string;
}) {
  const locale = useOptionalAppLocale()?.locale ?? "zh-CN";
  const copy = { ...resources[locale].translation.accounts, ...copyOverrides };
  const [qualityWindow, setQualityWindow] = useState<AccountQualityWindow>("15m");
  const [qualityProvider, setQualityProvider] = useState<string>();
  const [qualityFilter, setQualityFilter] = useState<AccountQualityFilter>();
  const [qualityLifecycle, setQualityLifecycle] = useState<AccountQualityLifecycle | undefined>("present");
  const [qualityBasicStatus, setQualityBasicStatus] = useState<AccountInventoryBasicStatus>();
  const [qualityEmail, setQualityEmail] = useState("");
  const [qualityPageSize, setQualityPageSize] = useState<25 | 50 | 100>(25);
  const [qualityCursor, setQualityCursor] = useState<string>();
  const [qualityCursorHistory, setQualityCursorHistory] = useState<Array<string | undefined>>([undefined]);
  const [detailsRow, setDetailsRow] = useState<AccountListRow>();
  const [detailsAccountKey, setDetailsAccountKey] = useState<string>();
  const accountQuery = useAccountListQuery(api, csrfToken);
  const { mutate: queryAccounts, reset: resetAccountQuery } = accountQuery;

  const executeAccountQuery = useCallback((cursor?: string, overrides?: Partial<AccountListFilters>) => {
    if (!instanceId) return;
    queryAccounts({
      instanceId,
      window: qualityWindow,
      provider: qualityProvider?.trim().toLowerCase() || undefined,
      quality: qualityFilter,
      lifecycle: qualityLifecycle,
      basicStatus: qualityBasicStatus,
      email: qualityEmail.trim().toLowerCase() || undefined,
      cursor,
      limit: qualityPageSize,
      ...overrides,
    });
  }, [instanceId, qualityBasicStatus, qualityEmail, qualityFilter, qualityLifecycle, qualityPageSize, qualityProvider, qualityWindow, queryAccounts]);

  const resetQuery = useCallback(() => {
    setQualityCursor(undefined);
    setQualityCursorHistory([undefined]);
    setDetailsRow(undefined);
    setDetailsAccountKey(undefined);
    resetAccountQuery();
  }, [resetAccountQuery]);

  useEffect(() => {
    if (accountQuery.error instanceof TopologyApiError && accountQuery.error.status === 401) onUnauthorized();
  }, [accountQuery.error, onUnauthorized]);
  useEffect(() => {
    let active = true;
    resetQuery();
    if (instanceId) {
      queueMicrotask(() => {
        if (active) executeAccountQuery(undefined, { window: "15m", lifecycle: "present", limit: 25 });
      });
    }
    return () => {
      active = false;
      resetAccountQuery();
    };
  }, [instanceId, resetAccountQuery, resetQuery]);

  const accountRows = useMemo(() => (accountQuery.data?.items ?? []).map(toAccountRow), [accountQuery.data]);
  const updateFilter = <T,>(setter: (value: T) => void, value: T) => {
    setter(value);
    resetQuery();
  };
  const selectAccount = (row: AccountListRow) => {
    setDetailsAccountKey(row.account_key);
    setDetailsRow(row);
  };

  if (!instanceId) return <Empty data-testid="accounts-no-node" description={copy.noInstance} />;

  return <Flex vertical gap={16} data-testid="accounts-workspace">
    <Card title={copy.accountQuality} role="region" aria-label={copy.accountQuality} data-testid="accounts-list-card">
      {accountOperationsApi && <Flex justify="end" style={{ marginBottom: 12 }}><UploadNewAccountAction api={accountOperationsApi} csrf={csrfToken} nodeInstanceId={instanceId} onUnauthorized={onUnauthorized} /></Flex>}
      <Flex gap={8} wrap>
        <Input data-testid="accounts-provider-filter" aria-label={copy.providerExact} placeholder={copy.providerPlaceholder} value={qualityProvider ?? ""} disabled={accountQuery.isPending} maxLength={64} onChange={(event) => updateFilter(setQualityProvider, event.target.value || undefined)} style={{ width: 190 }} />
        <Select data-testid="accounts-lifecycle-filter" allowClear aria-label={copy.lifecycle} placeholder={copy.allLifecycle} value={qualityLifecycle} disabled={accountQuery.isPending} options={accountInventoryLifecycles.map((value) => ({ value, label: value }))} onChange={(value) => updateFilter(setQualityLifecycle, value)} style={{ width: 180 }} />
        <Select data-testid="accounts-basic-status-filter" allowClear aria-label={copy.basicStatus} placeholder={copy.allBasicStatus} value={qualityBasicStatus} disabled={accountQuery.isPending} options={accountInventoryBasicStatuses.map((value) => ({ value, label: value }))} onChange={(value) => updateFilter(setQualityBasicStatus, value)} style={{ width: 200 }} />
        <Input data-testid="accounts-email-filter" aria-label={copy.emailExact} placeholder={copy.emailPlaceholder} value={qualityEmail} disabled={accountQuery.isPending} maxLength={320} autoComplete="off" onChange={(event) => updateFilter(setQualityEmail, event.target.value)} style={{ width: 240 }} />
        <Select data-testid="accounts-quality-window" aria-label={copy.qualityWindow} value={qualityWindow} disabled={accountQuery.isPending} options={[{ value: "15m", label: copy.recent15m }, { value: "1h", label: copy.recent1h }]} onChange={(value) => updateFilter(setQualityWindow, value)} />
        <Select data-testid="accounts-quality-filter" allowClear aria-label={copy.quality} placeholder={copy.allQuality} value={qualityFilter} disabled={accountQuery.isPending} options={[{ value: "good", label: copy.good }, { value: "degraded", label: copy.degraded }, { value: "bad", label: copy.bad }, { value: "unknown", label: copy.unknown }]} onChange={(value) => updateFilter(setQualityFilter, value)} />
        <Select data-testid="accounts-page-size" aria-label={copy.perPage} value={qualityPageSize} disabled={accountQuery.isPending} options={[25, 50, 100].map((value) => ({ value, label: `${value}${copy.pageSuffix}` }))} onChange={(value) => updateFilter(setQualityPageSize, value)} />
        <Button data-testid={queryTestId} type="primary" loading={accountQuery.isPending} onClick={() => { resetQuery(); executeAccountQuery(); }}>{copy.query}</Button>
      </Flex>
      {accountQuery.isPending && <Flex role="status" aria-label={copy.readingAccounts} justify="center" style={{ marginTop: 12 }}><Spin /></Flex>}
      {accountQuery.error && !accountQuery.isPending && <Alert type="error" title={accountReadError(accountQuery.error, copy)} action={<Button data-testid="accounts-retry" onClick={() => executeAccountQuery(qualityCursor)}>{copy.retry}</Button>} />}
      {accountQuery.data && !accountQuery.error && <div data-testid={accountQuery.data.items.length === 0 ? "accounts-no-results" : "accounts-results"}><AccountList rows={accountRows} onSelectAccount={selectAccount} emptyDescription={copy.noAccounts} /><Flex justify="end" gap={8} style={{ marginTop: 8 }}><Button data-testid="accounts-first" disabled={qualityCursorHistory.length === 1 || accountQuery.isPending} onClick={() => { const previous = qualityCursorHistory.slice(0, -1); const cursor = previous.at(-1); setQualityCursorHistory(previous); setQualityCursor(cursor); executeAccountQuery(cursor); }}>{copy.previousAccounts}</Button><Button data-testid="accounts-next" disabled={!accountQuery.data.next_cursor || accountQuery.isPending} onClick={() => { const next = accountQuery.data.next_cursor ?? undefined; setQualityCursorHistory((items) => [...items, next]); setQualityCursor(next); executeAccountQuery(next); }}>{copy.nextAccounts}</Button></Flex></div>}
    </Card>
    <AccountDetailsDrawer api={api} accountOperationsApi={accountOperationsApi} csrfToken={csrfToken} instanceId={instanceId} row={detailsRow} accountKey={detailsAccountKey} onClose={() => { setDetailsRow(undefined); setDetailsAccountKey(undefined); }} onUnauthorized={onUnauthorized} />
    <AccountQualityIncidentsSection key={instanceId} api={api} instanceId={instanceId} providers={providers} providerError={providerError} onSelectAccount={(accountKey) => { setDetailsAccountKey(accountKey); setDetailsRow(accountRows.find((item) => item.account_key === accountKey)); }} onUnauthorized={onUnauthorized} />
  </Flex>;
}
