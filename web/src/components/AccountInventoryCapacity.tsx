import { useEffect } from "react";
import { Alert, Button, Card, Flex, Space, Spin, Tag, Typography } from "antd";
import { useAccountInventoryPollCapacity } from "../api/account-inventory-hooks";
import { AccountInventoryApiError } from "../api/account-inventory-types";
import type { AccountInventoryApi, AccountInventoryPollCapacity } from "../api/account-inventory-types";
import { formatDateTime } from "../foundation/format";
import { useOptionalAppLocale } from "../foundation/FrontendFoundationProvider";
import { resources } from "../foundation/resources";
import type { TranslationResource } from "../foundation/resources";

const { Text } = Typography;

function capacityStatusLabel(value: AccountInventoryPollCapacity["status"], copy: TranslationResource["accounts"]): string {
  if (value === "ready") return copy.ready;
  if (value === "capacity_exceeded") return copy.capacityExceededLabel;
  return copy.disabled;
}

export function AccountInventoryCapacity({ api, csrfToken, onUnauthorized }: {
  api: AccountInventoryApi; csrfToken: string; onUnauthorized: () => void;
}) {
  const capacity = useAccountInventoryPollCapacity(api, csrfToken);
  const locale = useOptionalAppLocale()?.locale ?? "zh-CN";
  const copy = resources[locale].translation.accounts;
  useEffect(() => {
    if (capacity.error instanceof AccountInventoryApiError && capacity.error.status === 401) onUnauthorized();
  }, [capacity.error, onUnauthorized]);
  return <Card title={copy.capacityTitle} data-testid="account-inventory-capacity">
    <Flex justify="space-between" align="center" gap={12} wrap>
      <Text type="secondary">{copy.capacityDescription}</Text>
      <Button onClick={() => capacity.mutate()} loading={capacity.isPending}>{copy.refreshCapacity}</Button>
    </Flex>
    {capacity.isPending && <Flex role="status" aria-label={copy.readingCapacity} justify="center" style={{ marginTop: 12 }}><Spin /></Flex>}
    {capacity.error && <Alert style={{ marginTop: 12 }} type="error" showIcon message={copy.capacityUnavailable} />}
    {!capacity.error && capacity.data && <CapacitySummary value={capacity.data} copy={copy} locale={locale} />}
  </Card>;
}

function CapacitySummary({ value, copy, locale }: { value: AccountInventoryPollCapacity; copy: TranslationResource["accounts"]; locale: "zh-CN" | "en" }) {
  return <Flex vertical gap={8} style={{ marginTop: 12 }} data-testid="account-inventory-capacity-summary">
    {value.status === "capacity_exceeded" && <Alert type="warning" showIcon message={copy.capacityExceeded} description={copy.capacityExceededDescription} />}

    <Space wrap>
      <Tag color={value.status === "ready" ? "green" : value.status === "capacity_exceeded" ? "orange" : "default"}>{capacityStatusLabel(value.status, copy)}</Tag>
      <Text>{copy.enabled}: {value.enabled ? copy.yes : copy.no}</Text>
      <Text>{copy.eligibleNodes}：{value.eligibleNodeCount}</Text>
      <Text>{copy.effectiveCapacity}：{value.effectiveCapacity}</Text>
      <Text>{copy.concurrency}: {value.concurrency}</Text>
    </Space>
    <Space wrap>
      <Text type="secondary">{copy.requestBudget} {value.requestTimeoutMs}ms</Text>
      <Text type="secondary">{copy.finalizeBudget} {value.finalizeTimeoutMs}ms</Text>
      <Text type="secondary">{copy.lifecycleBudget} {value.lifecycleTimeoutMs}ms</Text>
      <Text type="secondary">{copy.claimBudget} {value.claimTimeoutMs}ms</Text>
      <Text type="secondary">{copy.dispatchMargin} {value.dispatchMarginMs}ms</Text>
      <Text type="secondary">{copy.pollGrace} {value.pollStartGraceMs}ms</Text>
    </Space>
    <Text type="secondary">{copy.evaluatedSlot}：{formatDateTime(value.evaluatedSlot, locale)}；{copy.evaluatedAt}：{formatDateTime(value.evaluatedAt, locale)}</Text>
  </Flex>;
}
