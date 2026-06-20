import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ErrorBoundary } from "./ErrorBoundary";

function BrokenChild() {
  throw new Error("boom");
  return null;
}

function preventExpectedRenderError(event: ErrorEvent) {
  event.preventDefault();
}

describe("ErrorBoundary", () => {
  let consoleError: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    consoleError = vi.spyOn(console, "error").mockImplementation(() => {});
    window.addEventListener("error", preventExpectedRenderError);
  });

  afterEach(() => {
    window.removeEventListener("error", preventExpectedRenderError);
    consoleError.mockRestore();
  });

  it("renders a recovery fallback when a child render fails", () => {
    render(
      <ErrorBoundary>
        <BrokenChild />
      </ErrorBoundary>,
    );

    expect(screen.getByRole("alert")).toHaveTextContent(
      "Cairn hit a UI error",
    );
    expect(screen.getByText("boom")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Reload Cairn" })).toBeEnabled();
  });
});
