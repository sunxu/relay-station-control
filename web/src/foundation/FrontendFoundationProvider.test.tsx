import { fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider, useQueryClient } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import { useTranslation } from "react-i18next";
import { FrontendFoundationProvider, useAppLocale } from "./FrontendFoundationProvider";
import { antdLocales } from "./theme";

function FoundationProbe() {
  const { i18n, t } = useTranslation();

  return (
    <>
      <span data-testid="child">{t("common.locale.label")}</span>
      <span data-testid="language">{i18n.language}</span>
    </>
  );
}

describe("FrontendFoundationProvider", () => {
  it("renders children and binds zh-CN to i18next and Ant Design", () => {
    render(
      <FrontendFoundationProvider initialLocale="zh-CN">
        <FoundationProbe />
      </FrontendFoundationProvider>,
    );

    expect(screen.getByTestId("child")).toHaveTextContent("语言");
    expect(screen.getByTestId("language")).toHaveTextContent("zh-CN");
    expect(antdLocales["zh-CN"].locale).toBe("zh-cn");
  });

  it("binds en to i18next and Ant Design", () => {
    render(
      <FrontendFoundationProvider initialLocale="en">
        <FoundationProbe />
      </FrontendFoundationProvider>,
    );

    expect(screen.getByTestId("child")).toHaveTextContent("Language");
    expect(screen.getByTestId("language")).toHaveTextContent("en");
    expect(antdLocales.en.locale).toBe("en");
  });

  it("does not take ownership of an external QueryClient", () => {
    const queryClient = new QueryClient();
    const observed: unknown[] = [];

    function QueryClientProbe() {
      const current = useQueryClient();
      const { setLocale } = useAppLocale();
      observed.push(current);
      return <button data-testid="switch-locale" onClick={() => setLocale("en")}>switch</button>;
    }

    render(
      <QueryClientProvider client={queryClient}>
        <FrontendFoundationProvider initialLocale="zh-CN">
          <QueryClientProbe />
        </FrontendFoundationProvider>
      </QueryClientProvider>,
    );

    fireEvent.click(screen.getByTestId("switch-locale"));
    expect(observed[0]).toBe(queryClient);
    expect(observed.at(-1)).toBe(queryClient);
  });
});
