import { useAppLocale } from "./FrontendFoundationProvider";

export function LocaleSwitcher() {
  const { locale, setLocale } = useAppLocale();

  return (
    <label>
      <span className="sr-only">Language</span>
      <select
        aria-label="Language"
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
