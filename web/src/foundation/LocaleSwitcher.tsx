import { useTranslation } from "react-i18next";
import { useOptionalAppLocale } from "./FrontendFoundationProvider";

export function LocaleSwitcher() {
  const context = useOptionalAppLocale();
  if (!context) return null;

  const { locale, setLocale } = context;
  const { t } = useTranslation();

  return (
    <label>
      <select
        aria-label={t("common.locale.label")}
        data-testid="locale-selector"
        value={locale}
        onChange={(event) => setLocale(event.target.value as typeof locale)}
      >
        <option data-testid="locale-option-zh-CN" value="zh-CN">中文</option>
        <option data-testid="locale-option-en" value="en">English</option>
      </select>
    </label>
  );
}
