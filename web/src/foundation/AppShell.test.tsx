import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AppShell } from "./AppShell";
import { FrontendFoundationProvider } from "./FrontendFoundationProvider";
import type { AuthenticatedRoute } from "./navigation";

const navigate = vi.fn();
const logout = vi.fn().mockResolvedValue(undefined);

vi.mock("../auth/AuthContext", () => ({
  useAuth: () => ({
    route: "dashboard",
    session: {
      csrf_token: "c".repeat(32),
      administrator: { display_name: "测试管理员", login_name: "admin.one" },
    },
    api: { logout },
    navigate,
    clearSession: vi.fn(),
  }),
}));

function renderShell(locale: "zh-CN" | "en" = "zh-CN", route: AuthenticatedRoute = "dashboard") {
  return render(
    <FrontendFoundationProvider initialLocale={locale}>
      <AppShell route={route}>
        <div data-testid="foundation-content">content</div>
      </AppShell>
    </FrontendFoundationProvider>,
  );
}

describe("AppShell", () => {
  it("renders the seven localized sidebar entries and authenticated identity", () => {
    renderShell();
    expect(screen.getByTestId("app-shell")).toBeInTheDocument();
    expect(screen.getByTestId("admin-identity")).toHaveTextContent("测试管理员 · admin.one");
    expect(screen.getByTestId("sidebar-dashboard")).toHaveTextContent("仪表盘");
    expect(screen.getByTestId("sidebar-accounts")).toHaveTextContent("账号");
    expect(screen.getByTestId("sidebar-nodes")).toHaveTextContent("节点");
    expect(screen.getByTestId("sidebar-operations")).toHaveTextContent("操作");
    expect(screen.getByTestId("sidebar-monitoring")).toHaveTextContent("监控");
    expect(screen.getByTestId("sidebar-problems")).toHaveTextContent("问题");
    expect(screen.getByTestId("sidebar-settings")).toHaveTextContent("设置");
    for (const item of ["dashboard", "accounts", "nodes", "operations", "monitoring", "problems", "settings"]) {
      expect(screen.getByTestId(`sidebar-icon-${item}`)).toBeInTheDocument();
    }
    expect(screen.getByText("Relay Station")).toBeInTheDocument();
    expect(screen.getByText("控制台")).toBeInTheDocument();
    expect(screen.queryByText("Gateway")).not.toBeInTheDocument();
  });

  it.each([
    ["jobs", "operations"],
    ["topology", "monitoring"],
    ["settings", "settings"],
    ["management", "settings"],
  ] as const)("maps %s to the %s active item", (route, active) => {
    renderShell("zh-CN", route);
    expect(screen.getByTestId(`sidebar-${active}`)).toHaveAttribute("aria-current", "page");
  });

  it("uses static navigation search without calling a business API", () => {
    renderShell("en");
    const search = screen.getByTestId("navigation-search-input");
    fireEvent.change(search, { target: { value: "Gateway" } });
    expect(screen.getByTestId("search-result-assets")).toHaveTextContent("Gateway");
    expect(logout).not.toHaveBeenCalled();
  });

  it("navigates with stable test ids and switches locale live", () => {
    renderShell();
    fireEvent.click(screen.getByTestId("sidebar-accounts"));
    expect(navigate).toHaveBeenCalledWith("accounts");
    fireEvent.change(screen.getByTestId("locale-selector"), { target: { value: "en" } });
    expect(screen.getByTestId("sidebar-dashboard")).toHaveTextContent("Dashboard");
  });
});
