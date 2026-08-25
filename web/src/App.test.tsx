import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import App from "./App";

vi.mock("./api/generated/control", () => ({
  useGetHealthz: () => ({
    isPending: false,
    isError: false,
    data: {
      data: { status: "ok", version: "test" },
      status: 200,
      headers: new Headers(),
    },
  }),
}));

describe("App", () => {
  it("renders the healthy Control version", () => {
    render(
      <QueryClientProvider client={new QueryClient()}>
        <App />
      </QueryClientProvider>,
    );

    expect(screen.getByText("Control API 正常")).toBeInTheDocument();
    expect(screen.getByText("版本：test")).toBeInTheDocument();
  });
});
