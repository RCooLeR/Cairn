import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  AgentChatResponse,
  AgentStatus,
  AgentToolResult,
  AgentToolSpec,
  ProjectSummary,
} from "../../bindings/github.com/RCooLeR/Cairn/internal/models/models.js";

const agentServiceMock = vi.hoisted(() => ({
  AnalyzeProject: vi.fn(),
  Chat: vi.fn(),
  ExecuteTool: vi.fn(),
  Status: vi.fn(),
  ToolCatalog: vi.fn(),
}));
const settingsServiceMock = vi.hoisted(() => ({
  SetSetting: vi.fn(),
}));

import {
  AgentPage,
  invalidateAgentConversationForTest,
  resetAgentSessionForTest,
} from "./AgentPage";

vi.mock("../api/services", () => ({
  AgentService: agentServiceMock,
  SettingsService: settingsServiceMock,
}));

const projectA = "provider/project-a";
const projectB = "provider/project-b";

describe("AgentPage conversation ownership", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    agentServiceMock.Status.mockResolvedValue(
      new AgentStatus({
        availableModels: ["qwen2.5"],
        enabled: true,
        endpoint: "http://127.0.0.1:11434",
        model: "qwen2.5",
        provider: "ollama",
        reachable: true,
      }),
    );
    agentServiceMock.ToolCatalog.mockResolvedValue([
      new AgentToolSpec({
        description: "List Docker containers.",
        id: "docker.containers",
        name: "List containers",
        readOnly: true,
        requiresApproval: true,
      }),
    ]);
    agentServiceMock.AnalyzeProject.mockResolvedValue(null);
    agentServiceMock.ExecuteTool.mockResolvedValue(
      new AgentToolResult({
        summary: "Container inventory loaded.",
        title: "List containers",
        toolID: "docker.containers",
      }),
    );
    settingsServiceMock.SetSetting.mockResolvedValue(undefined);
  });

  afterEach(() => {
    act(() => resetAgentSessionForTest());
  });

  it("keeps a late request and its approved tool bound to the originating project", async () => {
    const firstChat = deferred<AgentChatResponse>();
    agentServiceMock.Chat.mockReturnValueOnce(
      firstChat.promise,
    ).mockResolvedValueOnce(
      new AgentChatResponse({
        message: "Project A has one running container.",
        model: "qwen2.5",
        toolResults: [],
      }),
    );
    render(<AgentPage projects={agentProjects()} />);

    const project = screen.getByLabelText("Project");
    fireEvent.change(project, { target: { value: projectA } });
    await waitFor(() =>
      expect(agentServiceMock.AnalyzeProject).toHaveBeenCalledWith(projectA),
    );

    const composer = screen.getByRole("textbox", {
      name: "Message to Cairn Agent",
    });
    fireEvent.change(composer, {
      target: { value: "Inspect this project's containers" },
    });
    const send = screen.getByRole("button", { name: "Send" });
    await waitFor(() => expect(send).toBeEnabled());
    fireEvent.click(send);

    expect(project).toBeDisabled();
    fireEvent.change(project, { target: { value: projectB } });
    expect(agentServiceMock.AnalyzeProject).not.toHaveBeenCalledWith(projectB);
    expect(agentServiceMock.Chat).toHaveBeenNthCalledWith(
      1,
      expect.objectContaining({ scope: { projectID: projectA } }),
    );

    await act(async () => {
      firstChat.resolve(toolRequestResponse());
      await firstChat.promise;
    });

    const dialog = await screen.findByRole("dialog", {
      name: "Allow Agent Tool?",
    });
    await waitFor(() => expect(project).toHaveValue(projectA));
    expect(project).toBeDisabled();

    fireEvent.change(project, { target: { value: projectB } });
    expect(agentServiceMock.AnalyzeProject).not.toHaveBeenCalledWith(projectB);
    fireEvent.click(within(dialog).getByRole("button", { name: "Allow" }));

    await waitFor(() =>
      expect(agentServiceMock.ExecuteTool).toHaveBeenCalledWith(
        expect.objectContaining({
          reason: "Inspect the selected project's containers.",
          scope: { projectID: projectA },
          toolID: "docker.containers",
        }),
      ),
    );
    expect(
      JSON.parse(agentServiceMock.ExecuteTool.mock.calls[0][0].arguments),
    ).toEqual({ all: true });
    await waitFor(() => expect(agentServiceMock.Chat).toHaveBeenCalledTimes(2));
    expect(agentServiceMock.Chat.mock.calls[1][0]).toEqual(
      expect.objectContaining({ scope: { projectID: projectA } }),
    );
  });

  it("keeps an approved tool claimed while the Agent page is remounted", async () => {
    const toolExecution = deferred<AgentToolResult>();
    agentServiceMock.Chat.mockResolvedValueOnce(
      toolRequestResponse(),
    ).mockResolvedValueOnce(
      new AgentChatResponse({
        message: "The container inventory is ready.",
        model: "qwen2.5",
        toolResults: [],
      }),
    );
    agentServiceMock.ExecuteTool.mockReturnValueOnce(toolExecution.promise);
    const firstView = render(<AgentPage projects={agentProjects()} />);

    fireEvent.change(screen.getByLabelText("Project"), {
      target: { value: projectA },
    });
    const composer = screen.getByRole("textbox", {
      name: "Message to Cairn Agent",
    });
    fireEvent.change(composer, { target: { value: "Inspect project A" } });
    const send = screen.getByRole("button", { name: "Send" });
    await waitFor(() => expect(send).toBeEnabled());
    fireEvent.click(send);

    const firstDialog = await screen.findByRole("dialog", {
      name: "Allow Agent Tool?",
    });
    fireEvent.click(within(firstDialog).getByRole("button", { name: "Allow" }));
    await waitFor(() =>
      expect(agentServiceMock.ExecuteTool).toHaveBeenCalledTimes(1),
    );

    firstView.unmount();
    render(<AgentPage projects={agentProjects()} />);

    const remountedDialog = await screen.findByRole("dialog", {
      name: "Allow Agent Tool?",
    });
    const remountedAllow = within(remountedDialog).getByRole("button", {
      name: "Allow",
    });
    expect(remountedAllow).toBeDisabled();
    expect(remountedAllow).toHaveAttribute("aria-busy", "true");
    fireEvent.click(remountedAllow);
    expect(agentServiceMock.ExecuteTool).toHaveBeenCalledTimes(1);

    await act(async () => {
      toolExecution.resolve(
        new AgentToolResult({
          summary: "Container inventory loaded.",
          title: "List containers",
          toolID: "docker.containers",
        }),
      );
      await toolExecution.promise;
    });

    await waitFor(() => expect(agentServiceMock.Chat).toHaveBeenCalledTimes(2));
    await waitFor(() =>
      expect(
        screen.queryByRole("dialog", { name: "Allow Agent Tool?" }),
      ).not.toBeInTheDocument(),
    );
    expect(agentServiceMock.ExecuteTool).toHaveBeenCalledTimes(1);
  });

  it("drops a late response after the conversation generation changes", async () => {
    const chat = deferred<AgentChatResponse>();
    agentServiceMock.Chat.mockReturnValueOnce(chat.promise);
    render(<AgentPage projects={agentProjects()} />);

    fireEvent.change(screen.getByLabelText("Project"), {
      target: { value: projectA },
    });
    const composer = screen.getByRole("textbox", {
      name: "Message to Cairn Agent",
    });
    fireEvent.change(composer, { target: { value: "Inspect project A" } });
    const send = screen.getByRole("button", { name: "Send" });
    await waitFor(() => expect(send).toBeEnabled());
    fireEvent.click(send);

    act(() => invalidateAgentConversationForTest(projectB));
    await act(async () => {
      chat.resolve(toolRequestResponse());
      await chat.promise;
    });

    await waitFor(() => expect(screen.getByLabelText("Project")).toBeEnabled());
    expect(screen.getByLabelText("Project")).toHaveValue(projectB);
    expect(
      screen.queryByRole("dialog", { name: "Allow Agent Tool?" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByText("I need to inspect the selected project."),
    ).not.toBeInTheDocument();
    expect(agentServiceMock.ExecuteTool).not.toHaveBeenCalled();
  });

  it("rejects approval when a pending tool no longer owns the conversation", async () => {
    agentServiceMock.Chat.mockResolvedValueOnce(toolRequestResponse());
    render(<AgentPage projects={agentProjects()} />);

    fireEvent.change(screen.getByLabelText("Project"), {
      target: { value: projectA },
    });
    const composer = screen.getByRole("textbox", {
      name: "Message to Cairn Agent",
    });
    fireEvent.change(composer, { target: { value: "Inspect project A" } });
    const send = screen.getByRole("button", { name: "Send" });
    await waitFor(() => expect(send).toBeEnabled());
    fireEvent.click(send);

    const dialog = await screen.findByRole("dialog", {
      name: "Allow Agent Tool?",
    });
    act(() => invalidateAgentConversationForTest(projectB));
    expect(screen.getByLabelText("Project")).toHaveValue(projectB);
    fireEvent.click(within(dialog).getByRole("button", { name: "Allow" }));

    await waitFor(() =>
      expect(
        screen.queryByRole("dialog", { name: "Allow Agent Tool?" }),
      ).not.toBeInTheDocument(),
    );
    expect(agentServiceMock.ExecuteTool).not.toHaveBeenCalled();
  });

  it("does not let an older status refresh overwrite a newer settings refresh", async () => {
    render(<AgentPage projects={agentProjects()} />);

    const provider = await screen.findByLabelText("Provider");
    await waitFor(() => expect(provider).toHaveValue("ollama"));

    const olderRefresh = deferred<AgentStatus>();
    agentServiceMock.Status.mockReturnValueOnce(
      olderRefresh.promise,
    ).mockResolvedValueOnce(
      new AgentStatus({
        availableModels: ["fresh-model"],
        enabled: true,
        endpoint: "http://127.0.0.1:22434",
        model: "fresh-model",
        provider: "openai_compatible",
        reachable: true,
      }),
    );

    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
    fireEvent.change(provider, {
      target: { value: "openai_compatible" },
    });

    await waitFor(() =>
      expect(agentServiceMock.Status).toHaveBeenCalledTimes(3),
    );
    await waitFor(() => expect(provider).toHaveValue("openai_compatible"));
    expect(screen.getByRole("textbox", { name: /Endpoint/ })).toHaveValue(
      "http://127.0.0.1:22434",
    );

    await act(async () => {
      olderRefresh.resolve(
        new AgentStatus({
          availableModels: ["stale-model"],
          enabled: true,
          endpoint: "http://127.0.0.1:11434",
          model: "stale-model",
          provider: "ollama",
          reachable: true,
        }),
      );
      await olderRefresh.promise;
    });

    expect(provider).toHaveValue("openai_compatible");
    expect(screen.getByRole("textbox", { name: /Endpoint/ })).toHaveValue(
      "http://127.0.0.1:22434",
    );
  });
});

function agentProjects() {
  return [
    new ProjectSummary({ id: projectA, name: "Project A" }),
    new ProjectSummary({ id: projectB, name: "Project B" }),
  ];
}

function toolRequestResponse() {
  return new AgentChatResponse({
    message: [
      "I need to inspect the selected project.",
      "",
      "```cairn-tool",
      JSON.stringify({
        arguments: { all: true },
        reason: "Inspect the selected project's containers.",
        toolID: "docker.containers",
      }),
      "```",
    ].join("\n"),
    model: "qwen2.5",
    toolResults: [],
  });
}

function deferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((nextResolve, nextReject) => {
    resolve = nextResolve;
    reject = nextReject;
  });
  return { promise, reject, resolve };
}
