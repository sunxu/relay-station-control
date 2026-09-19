import { formatDateTime } from "../foundation/format";
import { useEffect, useState } from "react";
import { Alert, Button, Card, Checkbox, Flex, Typography } from "antd";
import { useTranslation } from "react-i18next";
import { useAuth } from "../auth/AuthContext";
import { useAppLocale } from "../foundation/FrontendFoundationProvider";

const { Title, Paragraph, Text } = Typography;

export default function OneTimeMaterialPage() {
  const auth = useAuth();
  const { locale } = useAppLocale();
  const { t } = useTranslation();
  const [confirmed, setConfirmed] = useState(false);
  const material = auth.oneTime;

  useEffect(() => {
    const discard = () => auth.discardOneTime();
    const discardFromCache = (event: PageTransitionEvent) => {
      if (event.persisted) auth.discardOneTime();
    };
    window.addEventListener("pagehide", discard);
    window.addEventListener("beforeunload", discard);
    window.addEventListener("pageshow", discardFromCache);
    return () => {
      window.removeEventListener("pagehide", discard);
      window.removeEventListener("beforeunload", discard);
      window.removeEventListener("pageshow", discardFromCache);
      auth.discardOneTime();
    };
  }, [auth]);

  if (!material) return null;
  const recovery = material.kind === "recovery_codes";

  return (
    <main className="centered-page" data-testid="one-time-page">
      <Card className="auth-card one-time-card">
        <Title level={2}>{recovery ? t("auth.oneTime.recoveryTitle") : t("auth.oneTime.activationTitle")}</Title>
        <Alert
          type="warning"
          showIcon
          message={t("auth.oneTime.onlyShownOnce")}
          description={recovery ? t("auth.oneTime.recoveryDescription") : t("auth.oneTime.activationDescription")}
        />
        {recovery ? (
          <ul data-testid="recovery-codes" className="secret-list">
            {material.values.map((value, index) => <li key={`${index}-${value}`}><Text code>{value}</Text></li>)}
          </ul>
        ) : (
          <div className="secret-panel">
            <Text code copyable={{ text: material.values[0] }}>{material.values[0]}</Text>
            {material.expiresAt && <Text type="secondary">{t("auth.oneTime.validUntil", { date: formatDateTime(material.expiresAt, locale) })}</Text>}
          </div>
        )}
        <Flex vertical gap={12}>
          <Checkbox data-testid="one-time-confirmation" checked={confirmed} onChange={(event) => setConfirmed(event.target.checked)}>
            {t("auth.oneTime.confirmation")}
          </Checkbox>
          <Button data-testid="one-time-leave" type="primary" disabled={!confirmed} onClick={auth.leaveOneTime}>{t("auth.oneTime.leave")}</Button>
          <Paragraph type="secondary">{t("auth.oneTime.noCache")}</Paragraph>
        </Flex>
      </Card>
    </main>
  );
}
