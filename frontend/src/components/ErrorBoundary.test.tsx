import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { allowConsoleErrorOnce } from "../test/setup";
import { ErrorBoundary } from "./ErrorBoundary";

function BrokenChild() {
  throw new Error("boom");
  return null;
}

function preventExpectedRenderError(event: ErrorEvent) {
  event.preventDefault();
}

describe("ErrorBoundary", () => {
  beforeEach(() => {
    window.addEventListener("error", preventExpectedRenderError);
  });

  afterEach(() => {
    window.removeEventListener("error", preventExpectedRenderError);
  });

  it("renders a recovery fallback when a child render fails", () => {
    allowConsoleErrorOnce(
      "ErrorBoundary reporting the tested render failure",
      (arguments_) =>
        arguments_[0] === "Cairn UI render failure" &&
        arguments_[1] instanceof Error &&
        arguments_[1].message === "boom" &&
        typeof arguments_[2] === "string",
    );

    render(
      <ErrorBoundary>
        <BrokenChild />
      </ErrorBoundary>,
    );

    expect(screen.getByRole("alert")).toHaveTextContent("Cairn hit a UI error");
    expect(screen.getByText("boom")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Reload Cairn" })).toBeEnabled();
  });
});
