import { render, screen } from "@testing-library/react";
import { Pagination } from "antd";
import { useTranslation } from "react-i18next";
import { describe, expect, it } from "vitest";
import { FrontendFoundationProvider } from "./FrontendFoundationProvider";
import { antdLocaleFor } from "./theme";

function LanguageProbe() {
  const { i18n, t } = useTranslation();
  return (
    <>
      <span data-testid="foundation-language">{i18n.language}</span>
      <span data-testid="foundation-locale-label">{t("common.locale.label")}</span>
      <Pagination total={20} current={1} />
    </>
  );
}

describe("FrontendFoundationProvider", () => {
  it("binds the English AppLocale to i18next and renders children", () => {
    render(
      <FrontendFoundationProvider initialLocale="en">
        <LanguageProbe />
      </FrontendFoundationProvider>,
    );

    expect(screen.getByTestId("foundation-language")).toHaveTextContent("en");
    expect(screen.getByTestId("foundation-locale-label")).toHaveTextContent("Language");
  });

  it("binds zh-CN through the same provider", () => {
    render(
      <FrontendFoundationProvider initialLocale="zh-CN">
        <LanguageProbe />
      </FrontendFoundationProvider>,
    );

    expect(screen.getByTestId("foundation-language")).toHaveTextContent("zh-CN");
    expect(screen.getByTestId("foundation-locale-label")).toHaveTextContent("语言");
  });

  it("maps AppLocale to the corresponding Ant Design locale", () => {
    expect(antdLocaleFor("zh-CN").locale).toBe("zh-cn");
    expect(antdLocaleFor("en").locale).toBe("en");
  });
});
