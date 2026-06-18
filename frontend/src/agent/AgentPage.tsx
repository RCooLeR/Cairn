import { useEffect, useMemo, useState } from "react";
import {
  Bot,
  CheckCircle2,
  RefreshCw,
  Send,
  Sparkles,
  Wrench,
} from "lucide-react";

import type {
  AgentChatResponse,
  AgentStatus,
  AgentToolSpec,
  ContainerSummary,
  ImageSummary,
  NetworkSummary,
  ProjectSummary,
} from "../../bindings/github.com/RCooLeR/Cairn/internal/models/models.js";
import { AgentService } from "../api/services";
import {
  Badge,
  Button,
  Card,
  CardBody,
  CardHeader,
  EmptyState,
} from "../components/ui";

type AgentPageProps = {
  projects: ProjectSummary[];
  containers: ContainerSummary[];
  networks: NetworkSummary[];
  images: ImageSummary[];
};

type ScopeState = {
  projectID: string;
  containerID: string;
  networkID: string;
  imageID: string;
};

const samplePrompts = [
  "Review this Compose project for local development problems.",
  "Explain why this container is failing and what to inspect next.",
  "Suggest Dockerfile hardening and image size improvements.",
  "Check logs and networking clues for a port mapping issue.",
];

export function AgentPage({
  projects,
  containers,
  networks,
  images,
}: AgentPageProps) {
  const [status, setStatus] = useState<AgentStatus | null>(null);
  const [tools, setTools] = useState<AgentToolSpec[]>([]);
  const [selectedToolIDs, setSelectedToolIDs] = useState<string[]>([]);
  const [scope, setScope] = useState<ScopeState>({
    projectID: "",
    containerID: "",
    networkID: "",
    imageID: "",
  });
  const [prompt, setPrompt] = useState("");
  const [response, setResponse] = useState<AgentChatResponse | null>(null);
  const [loadingStatus, setLoadingStatus] = useState(true);
  const [sending, setSending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const projectContainers = useMemo(() => {
    if (!scope.projectID) {
      return containers;
    }
    return containers.filter(
      (container) => container.projectID === scope.projectID,
    );
  }, [containers, scope.projectID]);

  const effectiveContainerID = useMemo(() => {
    if (!scope.containerID) {
      return "";
    }
    const container = containers.find((item) => item.id === scope.containerID);
    if (!container) {
      return "";
    }
    if (scope.projectID && container.projectID !== scope.projectID) {
      return "";
    }
    return scope.containerID;
  }, [containers, scope.containerID, scope.projectID]);

  const selectedProject = projects.find(
    (project) => project.id === scope.projectID,
  );
  const selectedContainer = containers.find(
    (container) => container.id === effectiveContainerID,
  );

  const canAsk =
    Boolean(prompt.trim()) &&
    Boolean(status?.enabled) &&
    Boolean(status?.reachable) &&
    (status?.availableModels?.length ?? 0) > 0 &&
    !sending;

  const refreshAgent = async () => {
    setLoadingStatus(true);
    setError(null);
    try {
      const [nextStatus, nextTools] = await Promise.all([
        AgentService.Status(),
        AgentService.ToolCatalog(),
      ]);
      setStatus(nextStatus);
      setTools(nextTools);
    } catch (nextError) {
      setError(errorMessage(nextError, "Unable to load local agent"));
    } finally {
      setLoadingStatus(false);
    }
  };

  useEffect(() => {
    let cancelled = false;
    Promise.all([AgentService.Status(), AgentService.ToolCatalog()])
      .then(([nextStatus, nextTools]) => {
        if (cancelled) {
          return;
        }
        setStatus(nextStatus);
        setTools(nextTools);
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

  const askAgent = async () => {
    if (!canAsk) {
      return;
    }
    setSending(true);
    setError(null);
    try {
      const nextResponse = await AgentService.Chat({
        prompt: prompt.trim(),
        scope: {
          projectID: scope.projectID || undefined,
          containerID: effectiveContainerID || undefined,
          networkID: scope.networkID || undefined,
          imageID: scope.imageID || undefined,
        },
        toolIDs: selectedToolIDs.length > 0 ? selectedToolIDs : undefined,
      });
      setResponse(nextResponse);
      if (nextResponse?.model && nextResponse.model !== status?.model) {
        setStatus((current) =>
          current
            ? { ...current, model: nextResponse.model ?? current.model }
            : current,
        );
      }
    } catch (nextError) {
      setError(errorMessage(nextError, "Local agent request failed"));
    } finally {
      setSending(false);
    }
  };

  const toggleTool = (toolID: string) => {
    setSelectedToolIDs((current) =>
      current.includes(toolID)
        ? current.filter((id) => id !== toolID)
        : [...current, toolID],
    );
  };

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader
          actions={
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
          }
          status={<AgentStatusBadge status={status} loading={loadingStatus} />}
          title={
            <span className="inline-flex items-center gap-2">
              <Bot size={17} />
              Local Agent
            </span>
          }
        />
        <CardBody className="space-y-4">
          <div className="grid gap-3 lg:grid-cols-4">
            <AgentStat
              label="Provider"
              value={providerLabel(status?.provider)}
            />
            <AgentStat label="Endpoint" value={status?.endpoint || "-"} />
            <AgentStat label="Model" value={status?.model || "-"} />
            <AgentStat
              label="Installed models"
              value={String(status?.availableModels?.length ?? 0)}
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
          <div className="flex flex-wrap gap-2">
            {(status?.availableModels ?? []).slice(0, 12).map((model) => (
              <Badge
                key={model}
                tone={model === status?.model ? "accent" : "neutral"}
              >
                {model}
              </Badge>
            ))}
          </div>
        </CardBody>
      </Card>

      <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_360px]">
        <Card>
          <CardHeader title="Ask" />
          <CardBody className="space-y-4">
            <div className="grid gap-3 md:grid-cols-2">
              <AgentSelect
                label="Project"
                onChange={(projectID) =>
                  setScope((current) => ({
                    ...current,
                    projectID,
                    containerID:
                      current.containerID &&
                      containers.find((item) => item.id === current.containerID)
                        ?.projectID !== projectID
                        ? ""
                        : current.containerID,
                  }))
                }
                options={projects.map((project) => [project.id, project.name])}
                placeholder="Any project"
                value={scope.projectID}
              />
              <AgentSelect
                label="Container"
                onChange={(containerID) =>
                  setScope((current) => ({ ...current, containerID }))
                }
                options={projectContainers.map((container) => [
                  container.id,
                  `${container.name} (${container.state})`,
                ])}
                placeholder="Any container"
                value={effectiveContainerID}
              />
              <AgentSelect
                label="Network"
                onChange={(networkID) =>
                  setScope((current) => ({ ...current, networkID }))
                }
                options={networks.map((network) => [network.id, network.name])}
                placeholder="Any network"
                value={scope.networkID}
              />
              <AgentSelect
                label="Image"
                onChange={(imageID) =>
                  setScope((current) => ({ ...current, imageID }))
                }
                options={images.map((image) => [
                  image.id,
                  image.repoTags?.[0] ?? image.id,
                ])}
                placeholder="Any image"
                value={scope.imageID}
              />
            </div>

            <textarea
              className="min-h-40 w-full resize-y rounded-control border border-border bg-bg-inset px-3 py-2 text-sm text-text-primary outline-none focus:border-accent"
              onChange={(event) => setPrompt(event.target.value)}
              placeholder="Ask about Dockerfiles, Compose, logs, networking, runtime errors, image hardening..."
              value={prompt}
            />
            <div className="flex flex-wrap gap-2">
              {samplePrompts.map((sample) => (
                <Button
                  key={sample}
                  onClick={() => setPrompt(sample)}
                  size="sm"
                  variant="secondary"
                >
                  {sample}
                </Button>
              ))}
            </div>
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div className="text-sm text-text-muted">
                {selectedProject ? selectedProject.name : "No project scope"}
                {selectedContainer ? ` / ${selectedContainer.name}` : ""}
              </div>
              <Button
                disabled={!canAsk}
                disabledReason={
                  status?.enabled === false
                    ? "Enable the local agent in Settings"
                    : status?.reachable === false
                      ? "Local agent endpoint is not reachable"
                      : (status?.availableModels?.length ?? 0) === 0
                        ? "No local model is installed"
                        : "Enter a prompt"
                }
                icon={<Send size={15} />}
                loading={sending}
                onClick={() => {
                  void askAgent();
                }}
                variant="primary"
              >
                Ask agent
              </Button>
            </div>
          </CardBody>
        </Card>

        <Card>
          <CardHeader
            status={<Badge tone="ok">read-only</Badge>}
            title={
              <span className="inline-flex items-center gap-2">
                <Wrench size={16} />
                Tools
              </span>
            }
          />
          <CardBody className="space-y-2">
            {tools.map((tool) => (
              <label
                className="flex cursor-pointer items-start gap-3 rounded-card border border-border bg-bg-inset p-3 text-sm"
                key={tool.id}
              >
                <input
                  checked={selectedToolIDs.includes(tool.id)}
                  className="mt-1"
                  onChange={() => toggleTool(tool.id)}
                  type="checkbox"
                />
                <span className="min-w-0">
                  <span className="block font-medium text-text-primary">
                    {tool.name}
                  </span>
                  <span className="block text-xs text-text-muted">
                    {tool.description}
                  </span>
                </span>
              </label>
            ))}
            <Button
              disabled={selectedToolIDs.length === 0}
              onClick={() => setSelectedToolIDs([])}
              size="sm"
              variant="ghost"
            >
              Use automatic tools
            </Button>
          </CardBody>
        </Card>
      </div>

      <Card>
        <CardHeader
          status={
            response?.model ? (
              <Badge tone="accent">{response.model}</Badge>
            ) : null
          }
          title={
            <span className="inline-flex items-center gap-2">
              <Sparkles size={16} />
              Answer
            </span>
          }
        />
        <CardBody className="space-y-4">
          {!response && !sending ? (
            <EmptyState
              body="Select a scope and ask a Docker question."
              icon={<Bot size={28} />}
              title="No answer yet"
            />
          ) : null}
          {response ? (
            <>
              <pre className="max-h-[520px] overflow-auto whitespace-pre-wrap rounded-card border border-border bg-bg-inset p-4 text-sm leading-6 text-text-primary">
                {response.message}
              </pre>
              <div className="space-y-2">
                {(response.toolResults ?? []).map((tool) => (
                  <details
                    className="rounded-card border border-border bg-bg-inset px-3 py-2 text-sm"
                    key={tool.toolID}
                  >
                    <summary className="cursor-pointer font-medium text-text-primary">
                      {tool.title}
                      {tool.error ? (
                        <span className="ml-2 text-error">{tool.error}</span>
                      ) : tool.summary ? (
                        <span className="ml-2 text-text-muted">
                          {tool.summary}
                        </span>
                      ) : null}
                    </summary>
                    {tool.data ? (
                      <pre className="mt-2 max-h-72 overflow-auto whitespace-pre-wrap text-xs text-text-secondary">
                        {tool.data}
                      </pre>
                    ) : null}
                  </details>
                ))}
              </div>
            </>
          ) : null}
        </CardBody>
      </Card>
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

function AgentStat({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0 rounded-card border border-border bg-bg-inset p-3 text-sm">
      <div className="text-xs font-medium uppercase text-text-muted">
        {label}
      </div>
      <div className="mt-1 truncate font-medium text-text-primary">{value}</div>
    </div>
  );
}

function AgentSelect({
  label,
  onChange,
  options,
  placeholder,
  value,
}: {
  label: string;
  onChange: (value: string) => void;
  options: Array<[string, string]>;
  placeholder: string;
  value: string;
}) {
  return (
    <label className="block">
      <span className="text-xs font-medium uppercase text-text-muted">
        {label}
      </span>
      <select
        className="mt-1 h-9 w-full rounded-control border border-border bg-bg-inset px-3 text-sm text-text-primary outline-none focus:border-accent"
        onChange={(event) => onChange(event.target.value)}
        value={value}
      >
        <option value="">{placeholder}</option>
        {options.map(([id, name]) => (
          <option key={id} value={id}>
            {name}
          </option>
        ))}
      </select>
    </label>
  );
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
