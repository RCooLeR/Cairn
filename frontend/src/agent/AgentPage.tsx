import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Bot,
  CheckCircle2,
  FilePenLine,
  ListChecks,
  RefreshCw,
  Send,
  Square,
  Trash2,
} from "lucide-react";

import type {
  AgentChatResponse,
  AgentFileEditResult,
  AgentProjectAnalysis,
  AgentStatus,
  AgentToolResult,
  CommandPlan,
  ProjectSummary,
} from "../../bindings/github.com/RCooLeR/Cairn/internal/models/models.js";
import { AgentService, SettingsService } from "../api/services";
import { Badge, Button, Card, CardBody, EmptyState } from "../components/ui";

type AgentPageProps = {
  projects: ProjectSummary[];
};

type AgentMode = "ask" | "agent";

type ChatMessage = {
  id: string;
  role: "user" | "assistant" | "system";
  content: string;
  model?: string;
  toolResults?: AgentToolResult[];
};

const defaultEndpoint = "http://127.0.0.1:11434";

export function AgentPage({ projects }: AgentPageProps) {
  const [status, setStatus] = useState<AgentStatus | null>(null);
  const [provider, setProvider] = useState("ollama");
  const [endpoint, setEndpoint] = useState(defaultEndpoint);
  const [model, setModel] = useState("");
  const [projectID, setProjectID] = useState("");
  const [mode, setMode] = useState<AgentMode>("ask");
  const [prompt, setPrompt] = useState("");
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [lastToolResults, setLastToolResults] = useState<AgentToolResult[]>([]);
  const [analysis, setAnalysis] = useState<AgentProjectAnalysis | null>(null);
  const [loadingStatus, setLoadingStatus] = useState(true);
  const [analysisLoading, setAnalysisLoading] = useState(false);
  const [sending, setSending] = useState(false);
  const [savingKey, setSavingKey] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [editPath, setEditPath] = useState(".env");
  const [editInstruction, setEditInstruction] = useState(
    "Create/update placeholders for detected app environment variables.",
  );
  const [editContent, setEditContent] = useState("");
  const [editPlan, setEditPlan] = useState<CommandPlan | null>(null);
  const [editResult, setEditResult] = useState<AgentFileEditResult | null>(
    null,
  );
  const [editBusy, setEditBusy] = useState(false);
  const [editError, setEditError] = useState<string | null>(null);
  const requestRef = useRef<ReturnType<typeof AgentService.Chat> | null>(null);
  const stoppedRef = useRef(false);

  const selectedProject = projects.find((project) => project.id === projectID);
  const availableModels = useMemo(
    () =>
      uniqueOptions([status?.model, model, ...(status?.availableModels ?? [])]),
    [model, status],
  );
  const canSend =
    Boolean(prompt.trim()) &&
    Boolean(status?.enabled) &&
    Boolean(status?.reachable) &&
    availableModels.length > 0 &&
    !sending;

  const refreshAgent = useCallback(async (showSpinner = true) => {
    if (showSpinner) {
      setLoadingStatus(true);
    }
    setError(null);
    try {
      const nextStatus = await AgentService.Status();
      setStatus(nextStatus);
      setProvider(nextStatus?.provider || "ollama");
      setEndpoint(nextStatus?.endpoint || defaultEndpoint);
      setModel(nextStatus?.model || "");
    } catch (nextError) {
      setError(errorMessage(nextError, "Unable to load local agent"));
    } finally {
      setLoadingStatus(false);
    }
  }, []);

  useEffect(() => {
    let cancelled = false;
    AgentService.Status()
      .then((nextStatus) => {
        if (cancelled) {
          return;
        }
        setStatus(nextStatus);
        setProvider(nextStatus?.provider || "ollama");
        setEndpoint(nextStatus?.endpoint || defaultEndpoint);
        setModel(nextStatus?.model || "");
      })
      .catch((nextError) => {
        if (!cancelled) {
          setError(errorMessage(nextError, "Unable to load local agent"));
        }
      })
      .finally(() => {
        if (!cancelled) {
          setLoadingStatus(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const saveAgentSetting = async (key: string, value: string) => {
    setSavingKey(key);
    setError(null);
    try {
      await SettingsService.SetSetting(key, value);
      await refreshAgent(false);
    } catch (nextError) {
      setError(errorMessage(nextError, "Unable to save local agent setting"));
    } finally {
      setSavingKey(null);
    }
  };

  const saveEndpoint = () => {
    const nextEndpoint = endpoint.trim() || defaultEndpoint;
    setEndpoint(nextEndpoint);
    if (nextEndpoint !== status?.endpoint) {
      void saveAgentSetting("agent.endpoint", nextEndpoint);
    }
  };

  const changeProject = (nextProjectID: string) => {
    setProjectID(nextProjectID);
    setAnalysis(null);
    setEditPlan(null);
    setEditResult(null);
    setEditError(null);
    if (nextProjectID) {
      void loadProjectAnalysis(nextProjectID);
    }
  };

  const loadProjectAnalysis = async (targetProjectID = projectID) => {
    if (!targetProjectID) {
      return;
    }
    setAnalysisLoading(true);
    setEditError(null);
    try {
      const nextAnalysis = await AgentService.AnalyzeProject(targetProjectID);
      setAnalysis(nextAnalysis);
    } catch (nextError) {
      setEditError(errorMessage(nextError, "Unable to analyze project"));
    } finally {
      setAnalysisLoading(false);
    }
  };

  const draftProjectFile = async () => {
    if (!projectID || !editPath.trim() || !editInstruction.trim()) {
      return;
    }
    setEditBusy(true);
    setEditError(null);
    setEditPlan(null);
    setEditResult(null);
    try {
      const draft = await AgentService.DraftProjectFile({
        projectID,
        path: editPath.trim(),
        instruction: editInstruction.trim(),
      });
      setEditContent(draft?.content ?? "");
      if (draft?.path) {
        setEditPath(draft.path);
      }
      setLastToolResults([
        {
          toolID: "agent.draft_file",
          title: "Draft file",
          summary: draft?.path ?? editPath.trim(),
        },
      ]);
    } catch (nextError) {
      setEditError(errorMessage(nextError, "Unable to draft file"));
    } finally {
      setEditBusy(false);
    }
  };

  const previewFileEdit = async () => {
    if (!projectID || !editPath.trim() || !editContent.trim()) {
      return;
    }
    setEditBusy(true);
    setEditError(null);
    setEditResult(null);
    try {
      const plan = await AgentService.PlanFileEdit({
        projectID,
        path: editPath.trim(),
        content: editContent,
        reason: editInstruction.trim(),
      });
      setEditPlan(plan);
    } catch (nextError) {
      setEditError(errorMessage(nextError, "Unable to preview file edit"));
    } finally {
      setEditBusy(false);
    }
  };

  const applyFileEdit = async () => {
    if (!editPlan) {
      return;
    }
    setEditBusy(true);
    setEditError(null);
    try {
      const result = await AgentService.ApplyFileEdit(editPlan.planID, "");
      setEditResult(result);
      setEditPlan(null);
      setLastToolResults([
        {
          toolID: "agent.file_edit",
          title: "Applied file edit",
          summary: result
            ? `${result.path} (${result.bytesWritten} bytes)`
            : "",
        },
      ]);
      if (projectID) {
        void loadProjectAnalysis(projectID);
      }
    } catch (nextError) {
      setEditError(errorMessage(nextError, "Unable to apply file edit"));
    } finally {
      setEditBusy(false);
    }
  };

  const sendPrompt = async () => {
    const text = prompt.trim();
    if (!canSend || !text) {
      return;
    }
    stoppedRef.current = false;
    const userMessage: ChatMessage = {
      id: crypto.randomUUID(),
      role: "user",
      content: text,
    };
    setMessages((current) => [...current, userMessage]);
    setPrompt("");
    setSending(true);
    setError(null);

    const request = AgentService.Chat({
      prompt: buildAgentPrompt(mode, messages, text),
      scope: { projectID: projectID || undefined },
      toolIDs: shouldUseAgentToolContext(text) ? undefined : [],
    });
    requestRef.current = request;
    try {
      const response = await request;
      if (stoppedRef.current) {
        return;
      }
      setLastToolResults(response?.toolResults ?? []);
      appendAssistantResponse(response);
      if (response?.model) {
        setModel(response.model);
        setStatus((current) =>
          current
            ? { ...current, model: response.model ?? current.model }
            : current,
        );
      }
    } catch (nextError) {
      if (!stoppedRef.current) {
        setError(errorMessage(nextError, "Local agent request failed"));
      }
    } finally {
      if (!stoppedRef.current) {
        setSending(false);
      }
      requestRef.current = null;
    }
  };

  const stopPrompt = () => {
    stoppedRef.current = true;
    void requestRef.current?.cancel?.("Stopped by user");
    requestRef.current = null;
    setSending(false);
    setMessages((current) => [
      ...current,
      {
        id: crypto.randomUUID(),
        role: "system",
        content: "Stopped.",
      },
    ]);
  };

  const appendAssistantResponse = (response: AgentChatResponse | null) => {
    setMessages((current) => [
      ...current,
      {
        id: crypto.randomUUID(),
        role: "assistant",
        content: response?.message?.trim() || "No response returned.",
        model: response?.model,
        toolResults: response?.toolResults,
      },
    ]);
  };

  const planItems = buildPlanItems({
    endpoint,
    mode,
    model,
    project: selectedProject,
    provider,
    reachable: Boolean(status?.reachable),
  });

  return (
    <div className="flex min-h-[calc(100vh-120px)] flex-col gap-3">
      <Card>
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border px-4 py-3">
          <div className="inline-flex items-center gap-2 text-sm font-semibold">
            <Bot size={16} />
            Local Agent
          </div>
          <div className="flex items-center gap-2">
            <AgentStatusBadge loading={loadingStatus} status={status} />
            <Button
              icon={<RefreshCw size={15} />}
              loading={loadingStatus}
              onClick={() => {
                void refreshAgent();
              }}
              size="sm"
              variant="secondary"
            >
              Refresh
            </Button>
          </div>
        </div>
        <CardBody className="space-y-3">
          <div className="grid gap-3 xl:grid-cols-[180px_minmax(220px,1fr)_240px_260px]">
            <AgentSelect
              disabled={savingKey === "agent.provider"}
              label="Provider"
              onChange={(value) => {
                setProvider(value);
                void saveAgentSetting("agent.provider", value);
              }}
              options={[
                ["ollama", "Ollama"],
                ["openai_compatible", "OpenAI-compatible"],
              ]}
              value={provider}
            />
            <label className="block min-w-0">
              <span className="text-xs font-medium uppercase text-text-muted">
                Endpoint
              </span>
              <input
                className="mt-1 h-10 w-full rounded-control border border-border bg-bg-inset px-3 text-sm text-text-primary outline-none focus:border-accent"
                disabled={savingKey === "agent.endpoint"}
                onBlur={saveEndpoint}
                onChange={(event) => setEndpoint(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === "Enter") {
                    saveEndpoint();
                    event.currentTarget.blur();
                  }
                }}
                value={endpoint}
              />
            </label>
            <AgentSelect
              disabled={
                savingKey === "agent.model" || availableModels.length === 0
              }
              label="Model"
              onChange={(value) => {
                setModel(value);
                void saveAgentSetting("agent.model", value);
              }}
              options={availableModels.map((item) => [item, item])}
              value={model}
            />
            <AgentSelect
              label="Project"
              onChange={changeProject}
              options={[
                ["", "Any project"],
                ...projects.map(
                  (project) => [project.id, project.name] as const,
                ),
              ]}
              value={projectID}
            />
          </div>
          {status?.error ? (
            <div className="rounded-card border border-warn/30 bg-warn/10 px-3 py-2 text-sm text-warn">
              {status.error}
            </div>
          ) : null}
          {error ? (
            <div className="rounded-card border border-error/30 bg-error/10 px-3 py-2 text-sm text-error">
              {error}
            </div>
          ) : null}
        </CardBody>
      </Card>

      <div className="grid gap-3 xl:grid-cols-[minmax(0,1fr)_360px]">
        <Card>
          <div className="flex items-center gap-2 border-b border-border px-4 py-3 text-sm font-semibold">
            <ListChecks size={16} />
            Plan
          </div>
          <CardBody className="space-y-3">
            <div className="grid gap-2 sm:grid-cols-2 xl:grid-cols-4">
              {planItems.map((item, index) => (
                <div
                  className="rounded-card border border-border bg-bg-inset p-3 text-sm"
                  key={item}
                >
                  <div className="text-xs font-medium uppercase text-text-muted">
                    Step {index + 1}
                  </div>
                  <div className="mt-1 text-text-primary">{item}</div>
                </div>
              ))}
            </div>
            {selectedProject ? (
              <ProjectConfigPanel
                analysis={analysis}
                analysisLoading={analysisLoading}
                editBusy={editBusy}
                editContent={editContent}
                editError={editError}
                editInstruction={editInstruction}
                editPath={editPath}
                editPlan={editPlan}
                editResult={editResult}
                onAnalyze={() => {
                  void loadProjectAnalysis();
                }}
                onApply={() => {
                  void applyFileEdit();
                }}
                onDraft={() => {
                  void draftProjectFile();
                }}
                onPreview={() => {
                  void previewFileEdit();
                }}
                onSetContent={setEditContent}
                onSetInstruction={setEditInstruction}
                onSetPath={(path) => {
                  setEditPath(path);
                  setEditPlan(null);
                  setEditResult(null);
                }}
                project={selectedProject}
              />
            ) : (
              <div className="rounded-card border border-border bg-bg-inset px-3 py-2 text-sm text-text-muted">
                Select a project to analyze app files and draft config edits.
              </div>
            )}
          </CardBody>
        </Card>

        <Card>
          <div className="border-b border-border px-4 py-3 text-sm font-semibold">
            Log
          </div>
          <CardBody className="space-y-2">
            {sending ? (
              <LogLine tone="accent" text="Request running..." />
            ) : null}
            {!sending && lastToolResults.length === 0 ? (
              <LogLine tone="neutral" text="No agent run yet." />
            ) : null}
            {lastToolResults.slice(0, 6).map((tool) => (
              <LogLine
                key={`${tool.toolID}-${tool.title}`}
                text={`${tool.title}${tool.summary ? `: ${tool.summary}` : ""}`}
                tone={tool.error ? "error" : "ok"}
              />
            ))}
          </CardBody>
        </Card>
      </div>

      <Card className="flex min-h-[520px] flex-1 flex-col">
        <div className="flex items-center justify-between gap-3 border-b border-border px-4 py-3">
          <div className="text-sm font-semibold">Conversation</div>
          <Button
            disabled={messages.length === 0 || sending}
            icon={<Trash2 size={15} />}
            onClick={() => {
              setMessages([]);
              setLastToolResults([]);
            }}
            size="sm"
            variant="ghost"
          >
            Clear
          </Button>
        </div>
        <CardBody className="flex flex-1 flex-col gap-3">
          <div className="min-h-0 flex-1 space-y-3 overflow-auto rounded-card border border-border bg-bg-inset p-3">
            {messages.length === 0 ? (
              <EmptyState
                body="Choose a model, optionally scope to a project, then ask a Docker question."
                icon={<Bot size={28} />}
                title="Start a conversation"
              />
            ) : null}
            {messages.map((message) => (
              <ChatBubble key={message.id} message={message} />
            ))}
          </div>

          <div className="rounded-card border border-border bg-bg-card p-3">
            <div className="mb-3 flex flex-wrap gap-2">
              <ModeButton
                active={mode === "ask"}
                label="Ask"
                onClick={() => setMode("ask")}
              />
              <ModeButton
                active={mode === "agent"}
                label="Agent"
                onClick={() => setMode("agent")}
              />
            </div>
            <div className="flex gap-2">
              <textarea
                className="min-h-16 flex-1 resize-none rounded-control border border-border bg-bg-inset px-3 py-2 text-sm text-text-primary outline-none focus:border-accent"
                onChange={(event) => setPrompt(event.target.value)}
                onKeyDown={(event) => {
                  if (
                    (event.ctrlKey || event.metaKey) &&
                    event.key === "Enter"
                  ) {
                    void sendPrompt();
                  }
                }}
                placeholder={
                  mode === "agent"
                    ? "Ask the agent to diagnose, plan, and explain next Docker steps..."
                    : "Ask a Docker question..."
                }
                value={prompt}
              />
              {sending ? (
                <Button
                  className="self-end"
                  icon={<Square size={15} />}
                  onClick={stopPrompt}
                  variant="danger"
                >
                  Stop
                </Button>
              ) : (
                <Button
                  className="self-end"
                  disabled={!canSend}
                  disabledReason={sendDisabledReason(
                    status,
                    availableModels,
                    prompt,
                  )}
                  icon={<Send size={15} />}
                  onClick={() => {
                    void sendPrompt();
                  }}
                  variant="primary"
                >
                  Send
                </Button>
              )}
            </div>
          </div>
        </CardBody>
      </Card>
    </div>
  );
}

function ProjectConfigPanel({
  analysis,
  analysisLoading,
  editBusy,
  editContent,
  editError,
  editInstruction,
  editPath,
  editPlan,
  editResult,
  onAnalyze,
  onApply,
  onDraft,
  onPreview,
  onSetContent,
  onSetInstruction,
  onSetPath,
  project,
}: {
  analysis: AgentProjectAnalysis | null;
  analysisLoading: boolean;
  editBusy: boolean;
  editContent: string;
  editError: string | null;
  editInstruction: string;
  editPath: string;
  editPlan: CommandPlan | null;
  editResult: AgentFileEditResult | null;
  onAnalyze: () => void;
  onApply: () => void;
  onDraft: () => void;
  onPreview: () => void;
  onSetContent: (value: string) => void;
  onSetInstruction: (value: string) => void;
  onSetPath: (value: string) => void;
  project: ProjectSummary;
}) {
  const recommendations = analysis?.recommendations ?? [];
  const envVars = analysis?.envVars ?? [];
  const ports = analysis?.ports ?? [];
  const canDraft =
    Boolean(editPath.trim() && editInstruction.trim()) && !editBusy;
  const canPreview =
    Boolean(editPath.trim() && editContent.trim()) && !editBusy;
  return (
    <div className="rounded-card border border-border bg-bg-inset p-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="min-w-0">
          <div className="inline-flex items-center gap-2 text-sm font-semibold text-text-primary">
            <FilePenLine size={16} />
            Project config
          </div>
          <div className="truncate text-xs text-text-muted">{project.name}</div>
        </div>
        <Button
          icon={<RefreshCw size={15} />}
          loading={analysisLoading}
          onClick={onAnalyze}
          size="sm"
          variant="secondary"
        >
          Analyze
        </Button>
      </div>

      {analysis ? (
        <div className="mt-3 grid gap-2 lg:grid-cols-3">
          <ConfigHint
            label="Stack"
            value={
              analysis.stacks && analysis.stacks.length > 0
                ? analysis.stacks.join(", ")
                : "Unknown"
            }
          />
          <ConfigHint
            label="Env vars"
            value={
              envVars.length > 0
                ? envVars
                    .slice(0, 6)
                    .map((item) => item.name)
                    .join(", ")
                : "None detected"
            }
          />
          <ConfigHint
            label="Ports"
            value={
              ports.length > 0
                ? ports
                    .slice(0, 6)
                    .map((item) => item.value)
                    .join(", ")
                : "None detected"
            }
          />
        </div>
      ) : null}

      {recommendations.length > 0 ? (
        <div className="mt-3 space-y-1">
          {recommendations.slice(0, 3).map((item) => (
            <div
              className="rounded-control border border-info/20 bg-info/10 px-3 py-2 text-xs text-info"
              key={item}
            >
              {item}
            </div>
          ))}
        </div>
      ) : null}

      <div className="mt-3 grid gap-3 lg:grid-cols-[220px_minmax(0,1fr)]">
        <label className="block">
          <span className="text-xs font-medium uppercase text-text-muted">
            File
          </span>
          <input
            className="mt-1 h-9 w-full rounded-control border border-border bg-bg-card px-3 text-sm text-text-primary outline-none focus:border-accent"
            onChange={(event) => onSetPath(event.target.value)}
            placeholder=".env"
            value={editPath}
          />
        </label>
        <label className="block">
          <span className="text-xs font-medium uppercase text-text-muted">
            Instruction
          </span>
          <input
            className="mt-1 h-9 w-full rounded-control border border-border bg-bg-card px-3 text-sm text-text-primary outline-none focus:border-accent"
            onChange={(event) => onSetInstruction(event.target.value)}
            placeholder="Draft Compose env settings with safe placeholders"
            value={editInstruction}
          />
        </label>
      </div>

      <textarea
        className="mt-3 min-h-36 w-full resize-y rounded-control border border-border bg-bg-card px-3 py-2 font-mono text-xs text-text-primary outline-none focus:border-accent"
        onChange={(event) => onSetContent(event.target.value)}
        placeholder="Drafted or manually edited file content appears here."
        value={editContent}
      />

      {editError ? (
        <div className="mt-2 rounded-card border border-error/30 bg-error/10 px-3 py-2 text-sm text-error">
          {editError}
        </div>
      ) : null}
      {editResult ? (
        <div className="mt-2 rounded-card border border-ok/30 bg-ok/10 px-3 py-2 text-sm text-ok">
          Applied {editResult.path} ({editResult.bytesWritten} bytes).
        </div>
      ) : null}
      {editPlan ? (
        <div className="mt-2 rounded-card border border-warn/30 bg-warn/10 px-3 py-2 text-sm text-warn">
          Preview ready: {editPlan.title}. {editPlan.effects?.join(" ")}
        </div>
      ) : null}

      <div className="mt-3 flex flex-wrap justify-end gap-2">
        <Button
          disabled={!canDraft}
          loading={editBusy}
          onClick={onDraft}
          size="sm"
          variant="secondary"
        >
          Draft
        </Button>
        <Button
          disabled={!canPreview}
          loading={editBusy}
          onClick={onPreview}
          size="sm"
          variant="secondary"
        >
          Preview edit
        </Button>
        <Button
          disabled={!editPlan || editBusy}
          loading={editBusy}
          onClick={onApply}
          size="sm"
          variant="primary"
        >
          Apply edit
        </Button>
      </div>
    </div>
  );
}

function ConfigHint({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-control border border-border bg-bg-card px-3 py-2 text-sm">
      <div className="text-xs font-medium uppercase text-text-muted">
        {label}
      </div>
      <div className="mt-1 break-words text-text-primary">{value}</div>
    </div>
  );
}

function AgentStatusBadge({
  loading,
  status,
}: {
  loading: boolean;
  status: AgentStatus | null;
}) {
  if (loading) {
    return <Badge tone="neutral">checking</Badge>;
  }
  if (!status?.enabled) {
    return <Badge tone="neutral">disabled</Badge>;
  }
  if (!status.reachable) {
    return <Badge tone="error">offline</Badge>;
  }
  if ((status.availableModels?.length ?? 0) === 0) {
    return <Badge tone="warn">no models</Badge>;
  }
  return (
    <Badge tone="ok">
      <CheckCircle2 size={13} />
      ready
    </Badge>
  );
}

function AgentSelect({
  disabled,
  label,
  onChange,
  options,
  value,
}: {
  disabled?: boolean;
  label: string;
  onChange: (value: string) => void;
  options: readonly (readonly [string, string])[];
  value: string;
}) {
  return (
    <label className="block min-w-0">
      <span className="text-xs font-medium uppercase text-text-muted">
        {label}
      </span>
      <select
        className="mt-1 h-10 w-full rounded-control border border-border bg-bg-inset px-3 text-sm text-text-primary outline-none focus:border-accent"
        disabled={disabled}
        onChange={(event) => onChange(event.target.value)}
        value={value}
      >
        {options.map(([id, name]) => (
          <option key={id || "empty"} value={id}>
            {name}
          </option>
        ))}
      </select>
    </label>
  );
}

function ModeButton({
  active,
  label,
  onClick,
}: {
  active: boolean;
  label: string;
  onClick: () => void;
}) {
  return (
    <button
      className={[
        "h-8 rounded-control border px-3 text-sm font-medium",
        active
          ? "border-accent bg-accent text-bg-app"
          : "border-border bg-bg-inset text-text-secondary hover:text-text-primary",
      ].join(" ")}
      onClick={onClick}
      type="button"
    >
      {label}
    </button>
  );
}

function ChatBubble({ message }: { message: ChatMessage }) {
  const isUser = message.role === "user";
  const isSystem = message.role === "system";
  return (
    <div
      className={[
        "flex",
        isUser ? "justify-end" : "justify-start",
        isSystem ? "justify-center" : "",
      ].join(" ")}
    >
      <div
        className={[
          "max-w-[min(860px,90%)] rounded-card border px-4 py-3 text-sm leading-6",
          isUser
            ? "border-accent/30 bg-accent/10 text-text-primary"
            : isSystem
              ? "border-border bg-bg-card text-text-muted"
              : "border-border bg-bg-card text-text-primary",
        ].join(" ")}
      >
        <div className="mb-1 text-xs font-medium uppercase text-text-muted">
          {isUser ? "You" : isSystem ? "System" : "Agent"}
          {message.model ? ` - ${message.model}` : ""}
        </div>
        <div className="whitespace-pre-wrap">{message.content}</div>
      </div>
    </div>
  );
}

function LogLine({
  text,
  tone,
}: {
  text: string;
  tone: "accent" | "error" | "neutral" | "ok";
}) {
  return (
    <div className="flex items-start gap-2 text-sm">
      <span
        className={[
          "mt-1.5 h-2 w-2 shrink-0 rounded-full",
          tone === "ok"
            ? "bg-ok"
            : tone === "error"
              ? "bg-error"
              : tone === "accent"
                ? "bg-accent"
                : "bg-text-muted",
        ].join(" ")}
      />
      <span className="min-w-0 break-words text-text-secondary">{text}</span>
    </div>
  );
}

function buildPlanItems({
  endpoint,
  mode,
  model,
  project,
  provider,
  reachable,
}: {
  endpoint: string;
  mode: AgentMode;
  model: string;
  project?: ProjectSummary;
  provider: string;
  reachable: boolean;
}) {
  return [
    reachable
      ? `${providerLabel(provider)} at ${endpoint}`
      : "Connect to the local model endpoint",
    model ? `Use ${model}` : "Pick an installed model",
    project ? `Scope to ${project.name}` : "Use all Docker context",
    mode === "agent"
      ? "Use context when relevant"
      : "Answer directly with safe next steps",
  ];
}

function buildAgentPrompt(
  mode: AgentMode,
  messages: ChatMessage[],
  prompt: string,
) {
  const history = messages
    .slice(-6)
    .map((message) => `${message.role}: ${message.content}`)
    .join("\n");
  const modeInstruction =
    mode === "agent"
      ? "Agent mode: use Cairn context when it helps, outline a concise plan for troubleshooting or implementation requests, then answer with concrete next steps. For capability, identity, greeting, or conceptual questions, answer directly without diagnosing current Docker state. Do not execute mutations."
      : "Ask mode: answer directly and concisely with Docker-specific guidance.";
  return [
    modeInstruction,
    history ? `Recent conversation:\n${history}` : "",
    `Current request:\n${prompt}`,
  ]
    .filter(Boolean)
    .join("\n\n");
}

function shouldUseAgentToolContext(prompt: string) {
  const normalized = prompt.trim().toLowerCase().replace(/[?!.]+$/g, "");
  if (!normalized) {
    return false;
  }
  const exactMetaPhrases = [
    "what are you",
    "can you code",
    "can you edit code",
    "can you change code",
    "can you write files",
    "can you edit files",
    "how can you help",
    "what do you do",
    "hello",
    "hi",
    "hey",
  ];
  if (exactMetaPhrases.some((phrase) => normalized === phrase)) {
    return false;
  }
  const containedMetaPhrases = [
    "who are you",
    "what can you do",
    "can you write code",
  ];
  return !containedMetaPhrases.some(
    (phrase) => normalized === phrase || normalized.includes(phrase),
  );
}

function sendDisabledReason(
  status: AgentStatus | null,
  availableModels: string[],
  prompt: string,
) {
  if (status?.enabled === false) {
    return "Enable the local agent in Settings";
  }
  if (status?.reachable === false) {
    return "Local agent endpoint is not reachable";
  }
  if (availableModels.length === 0) {
    return "No local model is installed";
  }
  if (!prompt.trim()) {
    return "Enter a prompt";
  }
  return undefined;
}

function uniqueOptions(values: Array<string | undefined>) {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const value of values) {
    const trimmed = value?.trim();
    if (!trimmed || seen.has(trimmed)) {
      continue;
    }
    seen.add(trimmed);
    out.push(trimmed);
  }
  return out;
}

function providerLabel(provider: string | undefined) {
  if (provider === "openai_compatible") {
    return "OpenAI-compatible";
  }
  return "Ollama";
}

function errorMessage(error: unknown, fallback: string) {
  return error instanceof Error ? error.message : fallback;
}
