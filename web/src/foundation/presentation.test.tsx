import { fireEvent, render, screen } from "@testing-library/react";
import type { ReactElement } from "react";
import { describe, expect, it, vi } from "vitest";
import { PageShell } from "./PageShell";
import { ReadState } from "./ReadState";
import { FrontendFoundationProvider } from "./FrontendFoundationProvider";

function renderReadState(ui: ReactElement, locale: "zh-CN" | "en" = "zh-CN") {
  return render(<FrontendFoundationProvider initialLocale={locale}>{ui}</FrontendFoundationProvider>);
}

describe("PageShell and PageHeader", () => {
  it("renders a semantic heading, description, actions, and content", () => {
    render(
      <PageShell title="Foundation" description="A narrow foundation" actions={<button>Action</button>}>
        <p>Content</p>
      </PageShell>,
    );

    expect(screen.getByRole("heading", { level: 1, name: "Foundation" })).toBeInTheDocument();
    expect(screen.getByText("A narrow foundation")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Action" })).toBeInTheDocument();
    expect(screen.getByText("Content")).toBeInTheDocument();
  });
});

describe("ReadState", () => {
  it("exposes loading as a semantic status", () => {
    renderReadState(<ReadState status="loading" />);
    expect(screen.getByRole("status")).toHaveTextContent("加载中");
  });

  it("localizes defaults for English", () => {
    renderReadState(<ReadState status="error" onRetry={() => undefined} />, "en");
    expect(screen.getByRole("alert")).toHaveTextContent("Unavailable");
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
  });

  it("keeps explicit caller messages and a keyboard-reachable retry action", () => {
    const onRetry = vi.fn();
    renderReadState(<ReadState status="error" message="Domain unavailable" onRetry={onRetry} />);

    expect(screen.getByRole("alert")).toHaveTextContent("Domain unavailable");
    fireEvent.click(screen.getByRole("button", { name: "重试" }));
    expect(onRetry).toHaveBeenCalledOnce();
  });

  it("renders empty and content states without owning domain semantics", () => {
    const { rerender } = renderReadState(<ReadState status="empty" />);
    expect(screen.getByRole("status")).toHaveTextContent("暂无记录");

    rerender(<FrontendFoundationProvider initialLocale="zh-CN"><ReadState status="content"><span>Loaded content</span></ReadState></FrontendFoundationProvider>);
    expect(screen.getByText("Loaded content")).toBeInTheDocument();
  });
});
