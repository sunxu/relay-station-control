import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { PageShell } from "./PageShell";
import { ReadState } from "./ReadState";

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
    render(<ReadState status="loading" />);
    expect(screen.getByRole("status")).toHaveTextContent("Loading");
  });

  it("exposes error and a keyboard-reachable retry action", () => {
    const onRetry = vi.fn();
    render(<ReadState status="error" message="Unavailable" onRetry={onRetry} />);

    expect(screen.getByRole("alert")).toHaveTextContent("Unavailable");
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(onRetry).toHaveBeenCalledOnce();
  });

  it("renders empty and content states without owning domain semantics", () => {
    const { rerender } = render(<ReadState status="empty" message="No records" />);
    expect(screen.getByRole("status")).toHaveTextContent("No records");

    rerender(<ReadState status="content"><span>Loaded content</span></ReadState>);
    expect(screen.getByText("Loaded content")).toBeInTheDocument();
  });
});
