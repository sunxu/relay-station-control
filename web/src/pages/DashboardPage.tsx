import { Alert, Button, Card, Descriptions, Flex, Skeleton, Space, Tag } from "antd";
import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { DashboardApiError, generatedDashboardApi } from "../api/dashboard-api";
import type { DashboardApi, DashboardHealth, DashboardPollCapacity } from "../api/dashboard-api";
import { useAuth } from "../auth/AuthContext";
import { formatDateTime, formatNumber } from "../foundation/format";
import { useAppLocale } from "../foundation/FrontendFoundationProvider";
import { PageShell } from "../foundation/PageShell";

type ResourceState<T> = { status: "loading" | "ready" | "error"; value?: T };
type Source = "control" | "gateway" | "node" | "pollCapacity";

function useDashboardResource<T>(source: Source, loader: () => Promise<T>, clearSession: () => void) {
  const [state, setState] = useState<ResourceState<T>>({ status: "loading" });
  const [attempt, setAttempt] = useState(0);
  const retry = useCallback(() => setAttempt((value) => value + 1), []);

  useEffect(() => {
    let active = true;
    setState({ status: "loading" });
    void loader().then((value) => {
      if (active) setState({ status: "ready", value });
    }).catch((error: unknown) => {
      if (!active) return;
      if (error instanceof DashboardApiError && error.status === 401) clearSession();
      setState({ status: "error" });
    });
    return () => { active = false; };
  }, [attempt, clearSession, loader, source]);

  return { state, retry };
}

function SourceCard<T>({ id, title, state, retry, retryTestId, unavailable, retryLabel, children }: {
  id: string;
  title: string;
  state: ResourceState<T>;
  retry: () => void;
  retryTestId: string;
  unavailable: string;
  retryLabel: string;
  children: (value: T) => React.ReactNode;
}) {
  return (
    <Card title={title} data-testid={id}>
      {state.status === "loading" ? <Skeleton active paragraph={{ rows: 2 }} /> : null}
      {state.status === "error" ? <Alert type="warning" showIcon message={unavailable} action={<Button data-testid={retryTestId} onClick={retry}>{retryLabel}</Button>} /> : null}
      {state.status === "ready" && state.value !== undefined ? children(state.value) : null}
    </Card>
  );
}

function CountContent({ counts, label, labels }: { counts: { total: string; active: string; retired: string }; label: string; labels: { total: string; active: string; retired: string } }) {
  return <Descriptions column={1} size="small" aria-label={label}>
    <Descriptions.Item label={labels.total}><strong>{counts.total}</strong></Descriptions.Item>
    <Descriptions.Item label={labels.active}>{counts.active}</Descriptions.Item>
    <Descriptions.Item label={labels.retired}>{counts.retired}</Descriptions.Item>
  </Descriptions>;
}

export default function DashboardPage({ api = generatedDashboardApi }: { api?: DashboardApi }) {
  const { t } = useTranslation();
  const { locale } = useAppLocale();
  const auth = useAuth();
  const control = useDashboardResource("control", api.health, auth.clearSession);
  const gateway = useDashboardResource("gateway", api.gatewayCounts, auth.clearSession);
  const node = useDashboardResource("node", api.nodeCounts, auth.clearSession);
  const pollCapacity = useDashboardResource("pollCapacity", api.pollCapacity, auth.clearSession);
  const cardProps = { unavailable: t("dashboard.unavailable"), retryLabel: t("dashboard.retry") };
  const countLabels = { total: t("dashboard.total"), active: t("dashboard.active"), retired: t("dashboard.retired") };

  return (
    <PageShell className="dashboard-page" testId="dashboard-page" title={t("dashboard.title")} description={t("dashboard.description")}>
      <section aria-labelledby="dashboard-overview-heading">
        <h2 id="dashboard-overview-heading">{t("dashboard.overview")}</h2>
        <div className="dashboard-summary-grid">
          <SourceCard {...cardProps} retryTestId="dashboard-retry-control" id="dashboard-control-summary" title={t("dashboard.control")} state={control.state} retry={control.retry}>
            {(value: DashboardHealth) => <Descriptions column={1} size="small"><Descriptions.Item label={t("dashboard.status")}>{t("dashboard.available")}</Descriptions.Item><Descriptions.Item label={t("dashboard.version")}>{value.version}</Descriptions.Item></Descriptions>}
          </SourceCard>
          <SourceCard {...cardProps} retryTestId="dashboard-retry-gateway" id="dashboard-gateway-summary" title={t("dashboard.gatewayAssets")} state={gateway.state} retry={gateway.retry}>
            {(value) => <CountContent counts={{ active: formatNumber(value.active, locale), retired: formatNumber(value.retired, locale), total: formatNumber(value.total, locale) }} label={t("dashboard.gatewayAssets")} labels={countLabels} />}
          </SourceCard>
          <SourceCard {...cardProps} retryTestId="dashboard-retry-node" id="dashboard-node-summary" title={t("dashboard.nodeAssets")} state={node.state} retry={node.retry}>
            {(value) => <CountContent counts={{ active: formatNumber(value.active, locale), retired: formatNumber(value.retired, locale), total: formatNumber(value.total, locale) }} label={t("dashboard.nodeAssets")} labels={countLabels} />}
          </SourceCard>
          <SourceCard {...cardProps} retryTestId="dashboard-retry-poll-capacity" id="dashboard-poll-capacity-summary" title={t("dashboard.pollCapacity")} state={pollCapacity.state} retry={pollCapacity.retry}>
            {(value: DashboardPollCapacity) => <Space direction="vertical" size="small"><Tag color={value.status === "ready" ? "green" : value.status === "disabled" ? "default" : "orange"}>{t(`dashboard.capacityStatus.${value.status}`)}</Tag><Descriptions column={1} size="small"><Descriptions.Item label={t("dashboard.eligibleNodes")}>{formatNumber(value.eligibleNodeCount, locale)}</Descriptions.Item><Descriptions.Item label={t("dashboard.effectiveCapacity")}>{formatNumber(value.effectiveCapacity, locale)}</Descriptions.Item><Descriptions.Item label={t("dashboard.evaluatedAt")}>{formatDateTime(value.evaluatedAt, locale)}</Descriptions.Item></Descriptions></Space>}
          </SourceCard>
        </div>
      </section>
      <section aria-labelledby="dashboard-entries-heading" className="dashboard-work-entries">
        <h2 id="dashboard-entries-heading">{t("dashboard.workEntries")}</h2>
        <Flex gap="middle" wrap>
          {(["accounts", "operations", "problems"] as const).map((route) => (
            <Card key={route} title={t(`dashboard.entries.${route}.title`)}>
              <p>{t(`dashboard.entries.${route}.description`)}</p>
              <Button data-testid={`dashboard-nav-${route}`} onClick={() => auth.navigate(route)}>{t("dashboard.open")}</Button>
            </Card>
          ))}
        </Flex>
      </section>
    </PageShell>
  );
}
