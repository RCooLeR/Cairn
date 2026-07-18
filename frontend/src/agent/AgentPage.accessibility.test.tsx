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
  ProjectSummary,
} from "../../bindings/github.com/RCooLeR/Cairn/internal/models/models.js";

const agentServiceMock = vi.hoisted(() => ({
  ApplyFileEdit: vi.fn(),
  Chat: vi.fn(),
  DraftProjectFile: vi.fn(),
  PlanFileEdit: vi.fn(),
  Status: vi.fn(),
  ToolCatalog: vi.fn(),
}));
const settingsServiceMock = vi.hoisted(() => ({
  SetSetting: vi.fn(),
}));

import { AgentPage, resetAgentSessionForTest } from "./AgentPage";

vi.mock("../api/services", () => ({
  AgentService: agentServiceMock,
  SettingsService: settingsServiceMock,
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

  it("shows and repairs a sanitized invalid legacy endpoint", async () => {
    agentServiceMock.Status.mockResolvedValueOnce(
      new AgentStatus({
        availableModels: [],
        enabled: true,
        endpoint: "",
        error: "Local agent endpoint is not allowed",
        model: "gemma4:12b-it-q8_0",
        provider: "ollama",
        reachable: false,
      }),
    );
    agentServiceMock.ToolCatalog.mockResolvedValueOnce([]);
    settingsServiceMock.SetSetting.mockResolvedValueOnce(undefined);
    render(<AgentPage projects={[]} />);

    const endpoint = screen.getByRole("textbox", {
      name: /Endpoint \(loopback only\)/,
    });
    await waitFor(() => expect(endpoint).toHaveValue(""));
    fireEvent.blur(endpoint);

    await waitFor(() =>
      expect(settingsServiceMock.SetSetting).toHaveBeenCalledWith(
        "agent.endpoint",
        "http://127.0.0.1:11434",
      ),
    );
  });

  it("disables the quarantined project file-edit workflow", () => {
    render(
      <AgentPage
        projects={[new ProjectSummary({ id: "provider/demo", name: "demo" })]}
      />,
    );

    fireEvent.change(screen.getByLabelText("Project"), {
      target: { value: "provider/demo" },
    });

    expect(
      screen.getByText(
        /Draft, preview, and apply are temporarily unavailable.*manually/i,
      ),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Draft" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Preview edit" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Apply edit" })).toBeDisabled();
    expect(screen.getByPlaceholderText(".env")).toBeDisabled();
    expect(
      screen.getByPlaceholderText(
        "Drafted or manually edited file content appears here.",
      ),
    ).toBeDisabled();

    fireEvent.click(screen.getByRole("button", { name: "Draft" }));
    fireEvent.click(screen.getByRole("button", { name: "Preview edit" }));
    fireEvent.click(screen.getByRole("button", { name: "Apply edit" }));
    expect(agentServiceMock.DraftProjectFile).not.toHaveBeenCalled();
    expect(agentServiceMock.PlanFileEdit).not.toHaveBeenCalled();
    expect(agentServiceMock.ApplyFileEdit).not.toHaveBeenCalled();
  });
});
