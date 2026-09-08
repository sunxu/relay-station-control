import { Descriptions, Drawer, Tabs, Tag, Typography } from "antd";
import type { AccountRequestHistoryApi } from "../api/account-request-history-types";
import { AccountRequestHistorySection } from "../pages/AccountRequestHistorySection";
import type { AccountListRow } from "./AccountList";

const { Text } = Typography;

export function AccountDetailsDrawer({ api, instanceId, row, accountKey, onClose, onUnauthorized }: {
  api: AccountRequestHistoryApi;
  instanceId?: string;
  row?: AccountListRow;
  accountKey?: string;
  onClose: () => void;
  onUnauthorized: () => void;
}) {
  const identity = row?.account_key ?? accountKey;
  return <Drawer title={identity ? `账号详情 · ${row?.email || identity}` : "账号详情"} open={Boolean(identity && instanceId)} onClose={onClose} width={720} destroyOnHidden>
    {identity && instanceId && <>
      <Tabs items={[{ key: "history", label: "请求历史", children: <AccountRequestHistorySection key={`${instanceId}:${identity}`} api={api} instanceId={instanceId} accountKey={identity} onUnauthorized={onUnauthorized} /> }, { key: "inventory", label: "采集信息", children: row ? <Descriptions column={2} size="small" bordered>
        <Descriptions.Item label="Account key"><Text copyable>{row.account_key}</Text></Descriptions.Item>
        <Descriptions.Item label="Provider"><Tag>{row.provider}</Tag></Descriptions.Item>
        <Descriptions.Item label="生命周期">{row.lifecycle}</Descriptions.Item>
        <Descriptions.Item label="基础状态">{row.basic_status}</Descriptions.Item>
        <Descriptions.Item label="质量"><Tag>{row.quality}</Tag></Descriptions.Item>
        <Descriptions.Item label="连续缺失">{row.consecutive_missing_count ?? 0}</Descriptions.Item>
        <Descriptions.Item label="首次出现">{row.first_seen_at ?? "—"}</Descriptions.Item>
        <Descriptions.Item label="最近出现">{row.last_seen_at ?? "—"}</Descriptions.Item>
        <Descriptions.Item label="最近刷新">{row.last_refresh_at ?? "—"}</Descriptions.Item>
        <Descriptions.Item label="下次重试">{row.next_retry_at ?? "—"}</Descriptions.Item>
        <Descriptions.Item label="Provider 快照">{row.provider_last_complete_at ?? "—"}</Descriptions.Item>
        <Descriptions.Item label="Snapshot freshness">{row.snapshot_freshness ?? "—"}</Descriptions.Item>
        <Descriptions.Item label="Provider health">{row.provider_degraded ? <Tag color="orange">degraded</Tag> : <Tag color="green">normal</Tag>}</Descriptions.Item>
      </Descriptions> : <Typography.Text type="secondary">该账号不在当前列表页，采集信息未加载。</Typography.Text> }]}/>
    </>}
  </Drawer>;
}
