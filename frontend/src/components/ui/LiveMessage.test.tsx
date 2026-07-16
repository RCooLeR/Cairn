import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { LiveMessage } from "./LiveMessage";

describe("LiveMessage", () => {
  it("announces progress and completion politely as an atomic message", () => {
    render(<LiveMessage level="status">Settings saved</LiveMessage>);

    expect(screen.getByRole("status")).toHaveAttribute("aria-atomic", "true");
    expect(screen.getByRole("status")).toHaveAttribute("aria-live", "polite");
  });

  it("announces actionable failures assertively as an atomic message", () => {
    render(
      <LiveMessage level="error">Settings could not be saved</LiveMessage>,
    );

    expect(screen.getByRole("alert")).toHaveAttribute("aria-atomic", "true");
    expect(screen.getByRole("alert")).toHaveAttribute("aria-live", "assertive");
  });
});
