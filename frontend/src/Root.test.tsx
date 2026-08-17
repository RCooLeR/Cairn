import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { Root } from "./Root";

vi.mock("./App", () => ({
  default: () => <button type="button">App action</button>,
}));

vi.mock("./components/CairnLoader", () => ({
  default: ({ onDone }: { onDone: () => void }) => (
    <button onClick={onDone} type="button">
      Finish intro
    </button>
  ),
}));

describe("Root boot gate", () => {
  beforeEach(() => {
    document.body.innerHTML = "";
  });

  it("keeps the initializing app inert and hidden from assistive technology", () => {
    render(<Root />);

    const appAction = screen.getByRole("button", {
      hidden: true,
      name: "App action",
    });
    const appGate = appAction.parentElement;
    expect(appGate).toHaveAttribute("inert");
    expect(appGate).toHaveAttribute("aria-hidden", "true");
    expect(
      screen.queryByRole("button", { name: "App action" }),
    ).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Finish intro" }));

    expect(appGate).not.toHaveAttribute("inert");
    expect(appGate).not.toHaveAttribute("aria-hidden");
    expect(
      screen.getByRole("button", { name: "App action" }),
    ).toBeInTheDocument();
  });
});
