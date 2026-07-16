import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { AgentPage, resetAgentSessionForTest } from "./AgentPage";

vi.mock("../api/services", () => ({
  AgentService: {
    Status: vi.fn(() => new Promise(() => undefined)),
    ToolCatalog: vi.fn(() => new Promise(() => undefined)),
  },
  SettingsService: {
    SetSetting: vi.fn(),
  },
}));

describe("AgentPage accessibility", () => {
  afterEach(() => {
    act(() => resetAgentSessionForTest());
  });

  it("keeps a persistent accessible name on the message composer", () => {
    render(<AgentPage projects={[]} />);

    const composer = screen.getByRole("textbox", {
      name: "Message to Cairn Agent",
    });
    fireEvent.change(composer, {
      target: { value: "Inspect the selected project" },
    });

    expect(
      screen.getByRole("textbox", { name: "Message to Cairn Agent" }),
    ).toHaveValue("Inspect the selected project");
  });
});
