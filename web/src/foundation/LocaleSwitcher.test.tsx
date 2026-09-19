import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { useTranslation } from "react-i18next";
import { FrontendFoundationProvider } from "./FrontendFoundationProvider";
import { LocaleSwitcher } from "./LocaleSwitcher";

function SwitcherProbe() {
  const { i18n } = useTranslation();
  return (
    <>
      <LocaleSwitcher />
      <span data-testid="current-language">{i18n.language}</span>
    </>
  );
}

describe("LocaleSwitcher", () => {
  it("offers stable locale controls and persists an explicit selection", () => {
    render(
      <FrontendFoundationProvider initialLocale="zh-CN">
        <SwitcherProbe />
      </FrontendFoundationProvider>,
    );

    expect(screen.getByTestId("locale-selector")).toBeInTheDocument();
    expect(screen.getByTestId("locale-option-zh-CN")).toHaveTextContent("中文");
    expect(screen.getByTestId("locale-option-en")).toHaveTextContent("English");

    fireEvent.change(screen.getByTestId("locale-selector"), { target: { value: "en" } });

    expect(screen.getByTestId("locale-selector")).toHaveValue("en");
    expect(screen.getByTestId("current-language")).toHaveTextContent("en");
    expect(localStorage.getItem("relay-control.locale")).toBe("en");
  });

  it("keeps the current session switch when storage write fails", () => {
    const originalStorage = window.localStorage;
    Object.defineProperty(window, "localStorage", {
      configurable: true,
      value: { getItem: () => null, setItem: () => { throw new Error("write failed"); } },
    });

    try {
      render(
        <FrontendFoundationProvider initialLocale="zh-CN">
          <SwitcherProbe />
        </FrontendFoundationProvider>,
      );
      fireEvent.change(screen.getByTestId("locale-selector"), { target: { value: "en" } });
      expect(screen.getByTestId("current-language")).toHaveTextContent("en");
    } finally {
      Object.defineProperty(window, "localStorage", { configurable: true, value: originalStorage });
    }
  });
});
