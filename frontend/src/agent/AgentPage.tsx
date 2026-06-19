import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import {
  Bot,
  CheckCircle2,
  Circle,
  FilePenLine,
  ListChecks,
  Loader2,
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

export type AgentProjectAction =
  | "pull"
  | "redeploy"
  | "restart"
  | "start"
  | "stop";

export type AgentPruneKind =
  | "build-cache"
  | "containers"
  | "images"
  | "networks"
  | "system"
  | "volumes";

type AgentPageProps = {
  onCheckUpdates?: () => Promise<void> | void;
  onOpenUpdates?: () => void;
  onPlanProjectUpdate?: (project: ProjectSummary) => Promise<void> | void;
  onPlanPrune?: (kind: AgentPruneKind) => Promise<void> | void;
  onProjectAction?: (
    action: AgentProjectAction,
    project: ProjectSummary,
  ) => Promise<void> | void;
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

type AgentPlanItem = {
  text: string;
  status: "done" | "in_progress" | "todo";
};

type AgentLogItem = {
  id: string;
  text: string;
  tone: "accent" | "error" | "neutral" | "ok";
};

type AgentActionSuggestion =
  | {
      description: string;
      id: string;
      kind: "check_updates" | "open_updates";
      label: string;
    }
  | {
      description: string;
      id: string;
      kind: "plan_project_update";
      label: string;
      project: ProjectSummary;
    }
  | {
      action: AgentProjectAction;
      description: string;
      id: string;
      kind: "project_action";
      label: string;
      project: ProjectSummary;
    }
  | {
      description: string;
      id: string;
      kind: "prune";
      label: string;
      pruneKind: AgentPruneKind;
    };

const defaultEndpoint = "http://127.0.0.1:11434";

export function AgentPage({
  onCheckUpdates,
  onOpenUpdates,
  onPlanProjectUpdate,
  onPlanPrune,
  onProjectAction,
  projects,
}: AgentPageProps) {
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
  const [agentActionBusyID, setAgentActionBusyID] = useState("");
  const [agentActionMessage, setAgentActionMessage] = useState<string | null>(
    null,
  );
  const requestRef = useRef<ReturnType<typeof AgentService.Chat> | null>(null);
  const stoppedRef = useRef(false);
  const transcriptRef = useRef<HTMLDivElement | null>(null);

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

  const llmPlanItems = useMemo(
    () => extractLatestPlanItems(messages),
    [messages],
  );
  const logItems = useMemo(
    () => buildAgentLogItems(messages, lastToolResults, sending),
    [lastToolResults, messages, sending],
  );
  const latestUserPrompt = useMemo(
    () => latestUserContent(messages),
    [messages],
  );
  const agentActions = useMemo(
    () =>
      detectAgentActionSuggestions({
        hasCheckUpdates: Boolean(onCheckUpdates),
        hasOpenUpdates: Boolean(onOpenUpdates),
        hasPlanProjectUpdate: Boolean(onPlanProjectUpdate),
        hasPlanPrune: Boolean(onPlanPrune),
        hasProjectAction: Boolean(onProjectAction),
        projects,
        prompt: latestUserPrompt,
        selectedProject,
      }),
    [
      latestUserPrompt,
      onCheckUpdates,
      onOpenUpdates,
      onPlanProjectUpdate,
      onPlanPrune,
      onProjectAction,
      projects,
      selectedProject,
    ],
  );

  useEffect(() => {
    const transcript = transcriptRef.current;
    if (!transcript) {
      return;
    }
    transcript.scrollTop = transcript.scrollHeight;
  }, [messages, sending]);

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
    setAgentActionMessage(null);
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

  const runAgentAction = async (action: AgentActionSuggestion) => {
    setAgentActionBusyID(action.id);
    setAgentActionMessage(null);
    try {
      if (action.kind === "check_updates") {
        await onCheckUpdates?.();
      } else if (action.kind === "open_updates") {
        onOpenUpdates?.();
      } else if (action.kind === "plan_project_update") {
        await onPlanProjectUpdate?.(action.project);
      } else if (action.kind === "project_action") {
        await onProjectAction?.(action.action, action.project);
      } else if (action.kind === "prune") {
        await onPlanPrune?.(action.pruneKind);
      }
      setAgentActionMessage(agentActionSuccessMessage(action));
    } catch (nextError) {
      setAgentActionMessage(errorMessage(nextError, "Agent action failed"));
    } finally {
      setAgentActionBusyID("");
    }
  };

  return (
    <div className="flex h-full min-h-0 flex-col gap-3 overflow-hidden">
      <Card className="shrink-0">
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

      <div className="grid min-h-0 flex-1 gap-3 overflow-auto xl:grid-cols-[minmax(0,1fr)_360px] xl:overflow-hidden">
        <Card className="order-2 flex min-h-[420px] min-w-0 flex-col overflow-hidden xl:order-1 xl:min-h-0">
          <div className="flex items-center justify-between gap-3 border-b border-border px-4 py-3">
            <div className="text-sm font-semibold">Conversation</div>
            <Button
              disabled={messages.length === 0 || sending}
              icon={<Trash2 size={15} />}
              onClick={() => {
                setMessages([]);
                setLastToolResults([]);
                setAgentActionMessage(null);
              }}
              size="sm"
              variant="ghost"
            >
              Clear
            </Button>
          </div>
          <CardBody className="flex min-h-0 flex-1 flex-col gap-3">
            <div
              className="min-h-0 flex-1 space-y-3 overflow-auto rounded-card border border-border bg-bg-inset p-3"
              data-testid="agent-transcript"
              ref={transcriptRef}
            >
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

            <AgentActionsPanel
              actions={agentActions}
              busyID={agentActionBusyID}
              message={agentActionMessage}
              onRun={(action) => {
                void runAgentAction(action);
              }}
            />

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
                    if (event.key === "Enter" && !event.shiftKey) {
                      event.preventDefault();
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

        <div className="order-1 grid min-h-0 gap-3 xl:order-2 xl:grid-rows-[minmax(160px,0.8fr)_minmax(180px,1fr)_auto] xl:overflow-hidden">
          <AgentPlanPanel items={llmPlanItems} />
          <AgentLogPanel items={logItems} />
          <ProjectConfigDrawer
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
        </div>
      </div>
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

function AgentPlanPanel({ items }: { items: AgentPlanItem[] }) {
  return (
    <Card className="flex min-h-0 flex-col overflow-hidden">
      <div
        className="flex items-center gap-2 border-b border-border px-4 py-3 text-sm font-semibold"
        data-testid="agent-plan-panel"
      >
        <ListChecks size={16} />
        Plan
      </div>
      <CardBody className="min-h-0 flex-1 overflow-auto">
        <div data-testid="agent-plan-content">
          {items.length === 0 ? (
            <div className="rounded-card border border-border bg-bg-inset px-3 py-2 text-sm text-text-muted">
              No plan in the latest answer.
            </div>
          ) : (
            <ol className="space-y-1.5">
              {items.map((item, index) => (
                <li
                  className="flex items-start gap-2 rounded-control px-2 py-1.5 text-sm hover:bg-bg-inset"
                  key={`${item.status}-${item.text}-${index}`}
                >
                  <PlanStatusIcon status={item.status} />
                  <span className="min-w-0 flex-1 break-words text-text-primary">
                    {item.text}
                  </span>
                </li>
              ))}
            </ol>
          )}
        </div>
      </CardBody>
    </Card>
  );
}

function PlanStatusIcon({ status }: { status: AgentPlanItem["status"] }) {
  if (status === "done") {
    return (
      <CheckCircle2
        aria-label="Done"
        className="mt-0.5 shrink-0 text-ok"
        size={16}
      />
    );
  }
  if (status === "in_progress") {
    return (
      <Loader2
        aria-label="In progress"
        className="mt-0.5 shrink-0 animate-spin text-accent"
        size={16}
      />
    );
  }
  return (
    <Circle
      aria-label="Todo"
      className="mt-0.5 shrink-0 text-text-muted"
      size={16}
    />
  );
}

function AgentActionsPanel({
  actions,
  busyID,
  message,
  onRun,
}: {
  actions: AgentActionSuggestion[];
  busyID: string;
  message: string | null;
  onRun: (action: AgentActionSuggestion) => void;
}) {
  if (actions.length === 0 && !message) {
    return null;
  }
  return (
    <div
      className="shrink-0 rounded-card border border-border bg-bg-card p-3"
      data-testid="agent-actions-panel"
    >
      <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
        <div className="text-sm font-semibold text-text-primary">
          Cairn actions
        </div>
        <Badge tone="info">Cairn workflow</Badge>
      </div>
      {actions.length > 0 ? (
        <div className="grid gap-2 sm:grid-cols-2 xl:grid-cols-3">
          {actions.map((action) => (
            <button
              className="rounded-control border border-border bg-bg-inset px-3 py-2 text-left text-sm hover:border-accent disabled:cursor-not-allowed disabled:opacity-60"
              disabled={Boolean(busyID)}
              key={action.id}
              onClick={() => onRun(action)}
              type="button"
            >
              <div className="font-medium text-text-primary">
                {busyID === action.id ? "Working..." : action.label}
              </div>
              <div className="mt-1 text-xs text-text-muted">
                {action.description}
              </div>
            </button>
          ))}
        </div>
      ) : null}
      {message ? (
        <div className="mt-2 rounded-control border border-info/20 bg-info/10 px-3 py-2 text-sm text-info">
          {message}
        </div>
      ) : null}
    </div>
  );
}

function AgentLogPanel({ items }: { items: AgentLogItem[] }) {
  return (
    <Card className="flex min-h-0 flex-col overflow-hidden">
      <div
        className="border-b border-border px-4 py-3 text-sm font-semibold"
        data-testid="agent-log-panel"
      >
        Log
      </div>
      <CardBody className="min-h-0 flex-1 space-y-2 overflow-auto">
        {items.map((item) => (
          <LogLine key={item.id} text={item.text} tone={item.tone} />
        ))}
      </CardBody>
    </Card>
  );
}

function ProjectConfigDrawer({
  project,
  ...props
}: Omit<Parameters<typeof ProjectConfigPanel>[0], "project"> & {
  project?: ProjectSummary;
}) {
  if (!project) {
    return (
      <Card className="shrink-0">
        <CardBody className="text-sm text-text-muted">
          Select a project to analyze app files and draft config edits.
        </CardBody>
      </Card>
    );
  }
  return (
    <Card className="shrink-0 overflow-hidden">
      <details>
        <summary className="cursor-pointer border-b border-border px-4 py-3 text-sm font-semibold text-text-primary">
          Project config tools
        </summary>
        <CardBody>
          <ProjectConfigPanel project={project} {...props} />
        </CardBody>
      </details>
    </Card>
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
  const displayContent =
    isUser || isSystem
      ? message.content
      : stripExtractedPlanSection(message.content);

  if (!displayContent.trim() && !isUser && !isSystem) {
    return null;
  }

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
        <MarkdownContent content={displayContent} />
      </div>
    </div>
  );
}

type MarkdownListItem = {
  checked?: boolean;
  text: string;
};

type MarkdownTable = {
  alignments: Array<"center" | "left" | "right" | undefined>;
  headers: string[];
  rows: string[][];
};

type MarkdownBlock =
  | { kind: "code"; code: string; language?: string }
  | { kind: "heading"; level: number; text: string }
  | { kind: "list"; items: MarkdownListItem[]; ordered: boolean }
  | { kind: "paragraph"; text: string }
  | { kind: "table"; table: MarkdownTable };

function MarkdownContent({ content }: { content: string }) {
  const blocks = parseMarkdownBlocks(content);
  return (
    <div className="space-y-3 break-words">
      {blocks.map((block, index) => (
        <MarkdownBlockView block={block} key={`${block.kind}-${index}`} />
      ))}
    </div>
  );
}

function MarkdownBlockView({ block }: { block: MarkdownBlock }) {
  if (block.kind === "heading") {
    const className =
      block.level <= 2
        ? "text-base font-semibold text-text-primary"
        : "text-sm font-semibold text-text-primary";
    return <div className={className}>{renderInlineMarkdown(block.text)}</div>;
  }
  if (block.kind === "code") {
    return (
      <pre className="overflow-auto rounded-control border border-border bg-bg-inset p-3 font-mono text-xs leading-5 text-text-secondary">
        <code>{block.code}</code>
      </pre>
    );
  }
  if (block.kind === "list") {
    const ListTag = block.ordered ? "ol" : "ul";
    return (
      <ListTag
        className={[
          "space-y-1 pl-5",
          block.ordered ? "list-decimal" : "list-disc",
        ].join(" ")}
      >
        {block.items.map((item, index) => (
          <li className="text-text-primary" key={`${item.text}-${index}`}>
            {item.checked === undefined ? null : (
              <input
                checked={item.checked}
                className="mr-2 align-middle"
                disabled
                readOnly
                type="checkbox"
              />
            )}
            {renderInlineMarkdown(item.text)}
          </li>
        ))}
      </ListTag>
    );
  }
  if (block.kind === "table") {
    return (
      <div className="overflow-auto rounded-control border border-border">
        <table
          className="min-w-full border-collapse text-left text-xs"
          data-testid="agent-markdown-table"
        >
          <thead className="bg-bg-inset text-text-muted">
            <tr>
              {block.table.headers.map((header, index) => (
                <th
                  className="border-b border-border px-3 py-2 font-semibold"
                  key={`${header}-${index}`}
                  style={{ textAlign: block.table.alignments[index] }}
                >
                  {renderInlineMarkdown(header)}
                </th>
              ))}
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {block.table.rows.map((row, rowIndex) => (
              <tr key={`row-${rowIndex}`}>
                {block.table.headers.map((_, cellIndex) => (
                  <td
                    className="max-w-80 px-3 py-2 align-top text-text-primary"
                    key={`cell-${rowIndex}-${cellIndex}`}
                    style={{
                      textAlign: block.table.alignments[cellIndex],
                    }}
                  >
                    <span className="break-words">
                      {renderInlineMarkdown(row[cellIndex] ?? "")}
                    </span>
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    );
  }
  return (
    <p className="whitespace-pre-wrap text-text-primary">
      {renderInlineMarkdown(block.text)}
    </p>
  );
}

function parseMarkdownBlocks(content: string): MarkdownBlock[] {
  const lines = content.replace(/\r\n/g, "\n").split("\n");
  const blocks: MarkdownBlock[] = [];
  let paragraph: string[] = [];
  let list: { items: MarkdownListItem[]; ordered: boolean } | null = null;
  let codeLines: string[] | null = null;
  let codeLanguage = "";

  const flushParagraph = () => {
    if (paragraph.length === 0) {
      return;
    }
    blocks.push({ kind: "paragraph", text: paragraph.join("\n") });
    paragraph = [];
  };
  const flushList = () => {
    if (!list) {
      return;
    }
    blocks.push({ kind: "list", items: list.items, ordered: list.ordered });
    list = null;
  };

  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index];
    const trimmed = line.trim();
    const fence = trimmed.match(/^```([\w-]*)/);
    if (codeLines) {
      if (fence) {
        blocks.push({
          kind: "code",
          code: codeLines.join("\n"),
          language: codeLanguage || undefined,
        });
        codeLines = null;
        codeLanguage = "";
      } else {
        codeLines.push(line);
      }
      continue;
    }
    if (fence) {
      flushParagraph();
      flushList();
      codeLines = [];
      codeLanguage = fence[1] ?? "";
      continue;
    }
    if (!trimmed) {
      flushParagraph();
      flushList();
      continue;
    }
    const heading = trimmed.match(/^(#{1,4})\s+(.+)$/);
    if (heading) {
      flushParagraph();
      flushList();
      blocks.push({
        kind: "heading",
        level: heading[1].length,
        text: heading[2],
      });
      continue;
    }
    const table = parseMarkdownTable(lines, index);
    if (table) {
      flushParagraph();
      flushList();
      blocks.push({ kind: "table", table: table.table });
      index = table.nextIndex;
      continue;
    }
    const unordered = line.match(/^\s*[-*]\s+(?:\[([ xX~-])\]\s+)?(.+)$/);
    const ordered = line.match(/^\s*\d+[.)]\s+(.+)$/);
    if (unordered || ordered) {
      flushParagraph();
      const isOrdered = Boolean(ordered);
      const checkedToken = unordered?.[1];
      const itemText = unordered?.[2] ?? ordered?.[1] ?? "";
      if (!list || list.ordered !== isOrdered) {
        flushList();
        list = { items: [], ordered: isOrdered };
      }
      list.items.push({
        checked:
          checkedToken === undefined
            ? undefined
            : checkedToken.toLowerCase() === "x",
        text: itemText,
      });
      continue;
    }
    flushList();
    paragraph.push(line);
  }

  if (codeLines) {
    blocks.push({
      kind: "code",
      code: codeLines.join("\n"),
      language: codeLanguage || undefined,
    });
  }
  flushParagraph();
  flushList();
  return blocks.length > 0 ? blocks : [{ kind: "paragraph", text: content }];
}

function parseMarkdownTable(
  lines: string[],
  startIndex: number,
): { nextIndex: number; table: MarkdownTable } | null {
  const headerLine = lines[startIndex]?.trim() ?? "";
  const separatorLine = lines[startIndex + 1]?.trim() ?? "";
  if (
    !isMarkdownTableRow(headerLine) ||
    !isMarkdownTableSeparator(separatorLine)
  ) {
    return null;
  }

  const headers = splitMarkdownTableRow(headerLine);
  const alignments = splitMarkdownTableRow(separatorLine).map(tableAlignment);
  if (headers.length === 0 || alignments.length === 0) {
    return null;
  }

  const rows: string[][] = [];
  let nextIndex = startIndex + 2;
  for (; nextIndex < lines.length; nextIndex += 1) {
    const rowLine = lines[nextIndex].trim();
    if (!isMarkdownTableRow(rowLine) || isMarkdownTableSeparator(rowLine)) {
      break;
    }
    rows.push(splitMarkdownTableRow(rowLine));
  }

  return {
    nextIndex: nextIndex - 1,
    table: {
      alignments,
      headers,
      rows,
    },
  };
}

function isMarkdownTableRow(line: string) {
  return line.includes("|") && splitMarkdownTableRow(line).length >= 2;
}

function isMarkdownTableSeparator(line: string) {
  const cells = splitMarkdownTableRow(line);
  return cells.length >= 2 && cells.every((cell) => /^:?-{3,}:?$/.test(cell));
}

function splitMarkdownTableRow(line: string) {
  const trimmed = line.trim().replace(/^\|/, "").replace(/\|$/, "");
  return trimmed.split("|").map((cell) => cell.trim());
}

function tableAlignment(cell: string): MarkdownTable["alignments"][number] {
  const left = cell.startsWith(":");
  const right = cell.endsWith(":");
  if (left && right) {
    return "center";
  }
  if (right) {
    return "right";
  }
  return "left";
}

function renderInlineMarkdown(text: string): ReactNode[] {
  const pattern = /(`[^`]+`|\*\*[^*]+\*\*|\[[^\]]+\]\(https?:\/\/[^)\s]+\))/g;
  const nodes: ReactNode[] = [];
  let lastIndex = 0;
  for (const match of text.matchAll(pattern)) {
    const token = match[0];
    if (match.index > lastIndex) {
      nodes.push(text.slice(lastIndex, match.index));
    }
    if (token.startsWith("`")) {
      nodes.push(
        <code
          className="rounded border border-border bg-bg-inset px-1 py-0.5 font-mono text-[0.85em]"
          key={`code-${match.index}`}
        >
          {token.slice(1, -1)}
        </code>,
      );
    } else if (token.startsWith("**")) {
      nodes.push(
        <strong key={`strong-${match.index}`}>{token.slice(2, -2)}</strong>,
      );
    } else {
      const link = token.match(/^\[([^\]]+)\]\((https?:\/\/[^)\s]+)\)$/);
      if (link) {
        nodes.push(
          <a
            className="text-info underline decoration-info/40 underline-offset-2 hover:text-accent"
            href={link[2]}
            key={`link-${match.index}`}
            rel="noreferrer"
            target="_blank"
          >
            {link[1]}
          </a>,
        );
      } else {
        nodes.push(token);
      }
    }
    lastIndex = match.index + token.length;
  }
  if (lastIndex < text.length) {
    nodes.push(text.slice(lastIndex));
  }
  return nodes;
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
      ? 'Agent mode: use Cairn context when it helps, then answer with concrete next steps. For larger troubleshooting, implementation, migration, or debugging requests, include a Markdown section named "Plan" with one task per line using bare checkboxes: [ ] todo, [-] in progress, and [x] done where those statuses are known. For simple capability, identity, greeting, or conceptual questions, answer directly without a plan and without diagnosing current Docker state. Do not claim that mutations were executed. If the request asks to update, pull, redeploy, restart, stop, start, prune, or otherwise mutate Docker state, explain that Cairn will show safe action buttons and confirmation previews for supported actions.'
      : 'Ask mode: answer directly and concisely with Docker-specific guidance. For larger troubleshooting, implementation, migration, or debugging requests, include a Markdown section named "Plan" with one task per line using bare checkboxes: [ ] todo, [-] in progress, and [x] done where those statuses are known. For simple questions, skip the plan. Do not claim that mutations were executed; Cairn shows action buttons and confirmation previews for supported mutations.';
  return [
    modeInstruction,
    history ? `Recent conversation:\n${history}` : "",
    `Current request:\n${prompt}`,
  ]
    .filter(Boolean)
    .join("\n\n");
}

function latestUserContent(messages: ChatMessage[]) {
  return (
    [...messages].reverse().find((message) => message.role === "user")
      ?.content ?? ""
  );
}

function detectAgentActionSuggestions({
  hasCheckUpdates,
  hasOpenUpdates,
  hasPlanProjectUpdate,
  hasPlanPrune,
  hasProjectAction,
  projects,
  prompt,
  selectedProject,
}: {
  hasCheckUpdates: boolean;
  hasOpenUpdates: boolean;
  hasPlanProjectUpdate: boolean;
  hasPlanPrune: boolean;
  hasProjectAction: boolean;
  projects: ProjectSummary[];
  prompt: string;
  selectedProject?: ProjectSummary;
}): AgentActionSuggestion[] {
  const normalized = prompt.toLowerCase();
  if (!normalized.trim()) {
    return [];
  }
  const suggestions: AgentActionSuggestion[] = [];
  const add = (action: AgentActionSuggestion) => {
    if (!suggestions.some((item) => item.id === action.id)) {
      suggestions.push(action);
    }
  };

  const updateIntent =
    /\b(upgrade|update|updates|newer|latest|outdated)\b/.test(normalized) &&
    /\b(image|images|container|containers|service|services|project|projects|compose|stack|stacks|all|everything)\b/.test(
      normalized,
    );
  if (updateIntent) {
    if (hasCheckUpdates) {
      add({
        description: "Runs Cairn's update detector against known projects.",
        id: "check-updates",
        kind: "check_updates",
        label: "Check updates",
      });
    }
    if (hasOpenUpdates) {
      add({
        description: "Opens the Updates page for review and history.",
        id: "open-updates",
        kind: "open_updates",
        label: "Open Updates",
      });
    }
    if (hasPlanProjectUpdate) {
      for (const project of updateActionProjects(projects, selectedProject)) {
        add({
          description: `${projectActionableUpdateCount(project)} actionable update${projectActionableUpdateCount(project) === 1 ? "" : "s"}; opens Cairn's update preview.`,
          id: `plan-project-update:${project.id}`,
          kind: "plan_project_update",
          label: `Plan update: ${project.name}`,
          project,
        });
      }
    }
  }

  if (hasProjectAction) {
    const project =
      selectedProject ?? (projects.length === 1 ? projects[0] : undefined);
    if (project) {
      const projectAction = projectActionIntent(normalized);
      if (projectAction) {
        add({
          action: projectAction,
          description: projectActionDescription(projectAction, project.name),
          id: `project-action:${projectAction}:${project.id}`,
          kind: "project_action",
          label: `${projectActionLabel(projectAction)} ${project.name}`,
          project,
        });
      }
    }
  }

  if (
    hasPlanPrune &&
    /\b(prune|cleanup|clean up|remove unused|dangling)\b/.test(normalized)
  ) {
    const pruneKind = pruneKindIntent(normalized);
    add({
      description: "Opens Cairn's command-plan confirmation before pruning.",
      id: `prune:${pruneKind}`,
      kind: "prune",
      label: `Plan prune: ${pruneKindLabel(pruneKind)}`,
      pruneKind,
    });
  }

  return suggestions.slice(0, 8);
}

function updateActionProjects(
  projects: ProjectSummary[],
  selectedProject?: ProjectSummary,
) {
  if (selectedProject && projectActionableUpdateCount(selectedProject) > 0) {
    return [selectedProject];
  }
  const actionable = projects.filter(
    (project) => projectActionableUpdateCount(project) > 0,
  );
  if (actionable.length > 0) {
    return actionable.slice(0, 5);
  }
  return selectedProject
    ? [selectedProject]
    : projects.length === 1
      ? [projects[0]]
      : [];
}

function projectActionableUpdateCount(project: ProjectSummary) {
  const badges = project.updateBadges;
  return (
    (badges?.imageUpdates ?? 0) +
    (badges?.baseUpdates ?? 0) +
    (badges?.rebuildNeeded ?? 0)
  );
}

function projectActionIntent(value: string): AgentProjectAction | null {
  if (/\b(redeploy|recreate|rebuild)\b/.test(value)) {
    return "redeploy";
  }
  if (/\b(restart|reload)\b/.test(value)) {
    return "restart";
  }
  if (/\b(stop|shutdown|shut down)\b/.test(value)) {
    return "stop";
  }
  if (/\b(start|run|up)\b/.test(value)) {
    return "start";
  }
  if (/\b(pull|fetch)\b/.test(value)) {
    return "pull";
  }
  return null;
}

function projectActionLabel(action: AgentProjectAction) {
  switch (action) {
    case "pull":
      return "Pull";
    case "redeploy":
      return "Redeploy";
    case "restart":
      return "Restart";
    case "start":
      return "Start";
    case "stop":
      return "Stop";
  }
}

function projectActionDescription(
  action: AgentProjectAction,
  projectName: string,
) {
  switch (action) {
    case "pull":
      return `Runs compose pull for ${projectName}.`;
    case "redeploy":
      return `Creates a redeploy preview for ${projectName}.`;
    case "restart":
      return `Restarts ${projectName}.`;
    case "start":
      return `Starts ${projectName}.`;
    case "stop":
      return `Stops ${projectName}.`;
  }
}

function pruneKindIntent(value: string): AgentPruneKind {
  if (/\b(volume|volumes)\b/.test(value)) {
    return "volumes";
  }
  if (/\b(container|containers)\b/.test(value)) {
    return "containers";
  }
  if (/\b(network|networks)\b/.test(value)) {
    return "networks";
  }
  if (/\b(build cache|builder|cache)\b/.test(value)) {
    return "build-cache";
  }
  if (/\b(system|everything|all)\b/.test(value)) {
    return "system";
  }
  return "images";
}

function pruneKindLabel(kind: AgentPruneKind) {
  return kind === "build-cache" ? "build cache" : kind;
}

function agentActionSuccessMessage(action: AgentActionSuggestion) {
  if (action.kind === "check_updates") {
    return "Update check started.";
  }
  if (action.kind === "open_updates") {
    return "Opened Updates.";
  }
  if (action.kind === "plan_project_update") {
    return `Opened update preview for ${action.project.name}.`;
  }
  if (action.kind === "project_action") {
    return `${projectActionLabel(action.action)} requested for ${action.project.name}.`;
  }
  if (action.kind === "prune") {
    return `Opened prune preview for ${pruneKindLabel(action.pruneKind)}.`;
  }
  return "Action requested.";
}

function shouldUseAgentToolContext(prompt: string) {
  const normalized = prompt
    .trim()
    .toLowerCase()
    .replace(/[?!.]+$/g, "");
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

function extractLatestPlanItems(messages: ChatMessage[]) {
  const latestAssistant = [...messages]
    .reverse()
    .find((message) => message.role === "assistant");
  return latestAssistant ? extractPlanItems(latestAssistant.content) : [];
}

function extractPlanItems(content: string): AgentPlanItem[] {
  return findExtractedPlanSection(content)?.items.slice(0, 12) ?? [];
}

function stripExtractedPlanSection(content: string) {
  const section = findExtractedPlanSection(content);
  if (!section) {
    return content;
  }
  const lines = content.replace(/\r\n/g, "\n").split("\n");
  const visibleLines = [
    ...lines.slice(0, section.startIndex),
    ...lines.slice(section.endIndex),
  ];
  return visibleLines
    .join("\n")
    .replace(/\n{3,}/g, "\n\n")
    .trim();
}

function findExtractedPlanSection(content: string): {
  endIndex: number;
  items: AgentPlanItem[];
  startIndex: number;
} | null {
  const lines = content.replace(/\r\n/g, "\n").split("\n");
  const items: AgentPlanItem[] = [];
  let inPlan = false;
  let status: AgentPlanItem["status"] = "todo";
  let startIndex = -1;
  let inFence = false;

  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index];
    const trimmed = line.trim();
    if (trimmed.startsWith("```")) {
      inFence = !inFence;
      continue;
    }
    if (inFence) {
      continue;
    }
    if (!trimmed) {
      continue;
    }
    const heading = trimmed.match(/^#{1,4}\s+(.+)$/);
    const plainPlan = trimmed.match(/^plan\s*:?\s*$/i);
    if (heading && /^plan\b/i.test(heading[1])) {
      inPlan = true;
      status = "todo";
      startIndex = index;
      continue;
    }
    if (plainPlan) {
      inPlan = true;
      status = "todo";
      startIndex = index;
      continue;
    }
    if (!inPlan) {
      continue;
    }
    if (heading) {
      const headingStatus = planStatusFromText(heading[1]);
      if (headingStatus) {
        status = headingStatus;
        continue;
      }
      return items.length > 0 ? { endIndex: index, items, startIndex } : null;
    }
    if (isPlanSectionBoundary(trimmed)) {
      return items.length > 0 ? { endIndex: index, items, startIndex } : null;
    }
    const sectionStatus = planStatusFromText(trimmed.replace(/[:*]+$/g, ""));
    if (sectionStatus) {
      status = sectionStatus;
      continue;
    }
    const parsed = parsePlanLine(trimmed, status);
    if (parsed) {
      items.push(parsed);
    }
  }

  return items.length > 0
    ? { endIndex: lines.length, items, startIndex }
    : null;
}

function parsePlanLine(
  line: string,
  fallbackStatus: AgentPlanItem["status"],
): AgentPlanItem | null {
  const bareTask = line.match(/^\[([ xX~-])\]\s+(.+)$/);
  if (bareTask) {
    return {
      status: taskStatus(bareTask[1]),
      text: stripInlineMarkdown(bareTask[2]),
    };
  }
  const task = line.match(/^[-*]\s+\[([ xX~-])\]\s+(.+)$/);
  if (task) {
    return {
      status: taskStatus(task[1]),
      text: stripInlineMarkdown(task[2]),
    };
  }
  const labeled = line.match(
    /^[-*]\s+(todo|to do|in progress|doing|done|complete|completed)\s*[:-]\s*(.+)$/i,
  );
  if (labeled) {
    return {
      status: planStatusFromText(labeled[1]) ?? fallbackStatus,
      text: stripInlineMarkdown(labeled[2]),
    };
  }
  return null;
}

function taskStatus(token: string): AgentPlanItem["status"] {
  if (token.toLowerCase() === "x") {
    return "done";
  }
  if (token === "-" || token === "~") {
    return "in_progress";
  }
  return "todo";
}

function planStatusFromText(value: string): AgentPlanItem["status"] | null {
  const normalized = value.toLowerCase().trim();
  if (["done", "complete", "completed"].includes(normalized)) {
    return "done";
  }
  if (["doing", "in progress", "in-progress", "current"].includes(normalized)) {
    return "in_progress";
  }
  if (["todo", "to do", "next", "pending"].includes(normalized)) {
    return "todo";
  }
  return null;
}

function isPlanSectionBoundary(value: string) {
  const normalized = value
    .toLowerCase()
    .replace(/[:*]+$/g, "")
    .trim();
  return [
    "answer",
    "analysis",
    "diagnosis",
    "explanation",
    "questions",
    "recommendation",
    "recommendations",
    "summary",
  ].includes(normalized);
}

function stripInlineMarkdown(value: string) {
  return value
    .replace(/`([^`]+)`/g, "$1")
    .replace(/\*\*([^*]+)\*\*/g, "$1")
    .replace(/\[([^\]]+)\]\((https?:\/\/[^)\s]+)\)/g, "$1")
    .trim();
}

function buildAgentLogItems(
  messages: ChatMessage[],
  toolResults: AgentToolResult[],
  sending: boolean,
): AgentLogItem[] {
  const items: AgentLogItem[] = [];

  let latestUserIndex = -1;
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    if (messages[index]?.role === "user") {
      latestUserIndex = index;
      break;
    }
  }

  const latestUser = latestUserIndex >= 0 ? messages[latestUserIndex] : null;
  if (!latestUser) {
    items.push({
      id: "empty",
      text: "No agent run yet.",
      tone: "neutral",
    });
    return items;
  }

  items.push({
    id: "understanding",
    text: `Understanding request: ${truncateLogText(latestUser.content)}`,
    tone: sending ? "accent" : "ok",
  });

  if (sending) {
    items.push({
      id: "planning",
      text: "Creating plan or direct answer shape",
      tone: "accent",
    });
    items.push({
      id: "context",
      text: shouldUseAgentToolContext(latestUser.content)
        ? "Preparing Docker context tools"
        : "Using direct answer mode without tools",
      tone: "neutral",
    });
    items.push({
      id: "answering",
      text: "Waiting for model response",
      tone: "accent",
    });
    return items;
  }

  const messagesAfterLatestUser = messages.slice(latestUserIndex + 1);
  const latestAssistant = [...messagesAfterLatestUser]
    .reverse()
    .find((message) => message.role === "assistant");
  const latestSystem = [...messagesAfterLatestUser]
    .reverse()
    .find((message) => message.role === "system");

  if (!latestAssistant) {
    items.push({
      id: "not-completed",
      text:
        latestSystem?.content === "Stopped."
          ? "Request stopped before final answer"
          : "No final answer returned",
      tone: latestSystem?.content === "Stopped." ? "neutral" : "error",
    });
    return items;
  }

  const planItems = latestAssistant
    ? extractPlanItems(latestAssistant.content)
    : [];
  items.push({
    id: "plan",
    text:
      planItems.length > 0
        ? `Created plan with ${planItems.length} task${planItems.length === 1 ? "" : "s"}`
        : "Plan not needed for this answer",
    tone: planItems.length > 0 ? "ok" : "neutral",
  });

  if (toolResults.length > 0) {
    for (const [index, tool] of toolResults.slice(0, 8).entries()) {
      items.push({
        id: `tool-${tool.toolID}-${index}`,
        text: tool.error
          ? `Tool failed: ${tool.title}`
          : `Used tool: ${tool.title}`,
        tone: tool.error ? "error" : "ok",
      });
    }
  } else {
    items.push({
      id: "tools-none",
      text: "Used no tools",
      tone: "neutral",
    });
  }

  if (toolResults.length > 8) {
    items.push({
      id: "tools-extra",
      text: `Used ${toolResults.length - 8} more tool${toolResults.length - 8 === 1 ? "" : "s"}`,
      tone: "neutral",
    });
  }

  items.push({
    id: "final-answer",
    text: latestAssistant?.model
      ? `Provided final answer with ${latestAssistant.model}`
      : "Provided final answer",
    tone: "ok",
  });

  return items;
}

function truncateLogText(value: string) {
  const normalized = value.replace(/\s+/g, " ").trim();
  return normalized.length > 90 ? `${normalized.slice(0, 87)}...` : normalized;
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

function errorMessage(error: unknown, fallback: string) {
  return error instanceof Error ? error.message : fallback;
}
