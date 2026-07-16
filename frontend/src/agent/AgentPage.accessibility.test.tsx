import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  AgentChatResponse,
  AgentStatus,
} from "../../bindings/github.com/RCooLeR/Cairn/internal/models/models.js";

const agentServiceMock = vi.hoisted(() => ({
  Chat: vi.fn(),
  Status: vi.fn(),
  ToolCatalog: vi.fn(),
}));

import { AgentPage, resetAgentSessionForTest } from "./AgentPage";

vi.mock("../api/services", () => ({
  AgentService: agentServiceMock,
  SettingsService: {
    SetSetting: vi.fn(),
  },
}));

describe("AgentPage accessibility", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    agentServiceMock.Status.mockImplementation(
      () => new Promise(() => undefined),
    );
    agentServiceMock.ToolCatalog.mockImplementation(
      () => new Promise(() => undefined),
    );
  });

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

  it("announces generation progress and response completion without streaming transcript chatter", async () => {
    let resolveChat!: (response: AgentChatResponse) => void;
    const chat = new Promise<AgentChatResponse>((resolve) => {
      resolveChat = resolve;
    });
    agentServiceMock.Status.mockResolvedValueOnce(
      new AgentStatus({
        availableModels: ["qwen2.5"],
        enabled: true,
        endpoint: "http://127.0.0.1:11434",
        model: "qwen2.5",
        provider: "ollama",
        reachable: true,
      }),
    );
    agentServiceMock.ToolCatalog.mockResolvedValueOnce([]);
    agentServiceMock.Chat.mockReturnValueOnce(chat);
    render(<AgentPage projects={[]} />);

    const composer = screen.getByRole("textbox", {
      name: "Message to Cairn Agent",
    });
    fireEvent.change(composer, { target: { value: "Check Docker health" } });
    const send = screen.getByRole("button", { name: "Send" });
    await waitFor(() => expect(send).toBeEnabled());
    fireEvent.click(send);

    expect(
      screen.getByRole("status", { name: "Agent request status" }),
    ).toHaveTextContent("Cairn Agent is generating a response.");

    await act(async () => {
      resolveChat(
        new AgentChatResponse({
          message: "Docker is healthy.",
          model: "qwen2.5",
          toolResults: [],
        }),
      );
      await chat;
    });

    expect(
      screen.getByRole("status", { name: "Agent request status" }),
    ).toHaveTextContent("Cairn Agent response complete.");
  });

  it("announces an actionable agent load failure", async () => {
    agentServiceMock.Status.mockRejectedValueOnce(
      new Error("Local agent endpoint is unavailable"),
    );
    agentServiceMock.ToolCatalog.mockResolvedValueOnce([]);
    render(<AgentPage projects={[]} />);

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Local agent endpoint is unavailable",
    );
  });
});
