import { Badge, Button, Modal } from "../ui";

type BadgeTone = "ok" | "warn" | "error" | "info" | "neutral" | "accent";

export type CleanupState = {
  open: boolean;
  includeImages: boolean;
  includeContainers: boolean;
  includeBuildCache: boolean;
  includeVolumes: boolean;
  typedName: string;
  results: CleanupStepResult[];
  busy: boolean;
  error?: string;
};

export type CleanupStepResult = {
  kind: string;
  label: string;
  status: "pending" | "running" | "success" | "error";
  message?: string;
};

export const emptyCleanup: CleanupState = {
  open: false,
  includeImages: true,
  includeContainers: true,
  includeBuildCache: true,
  includeVolumes: false,
  typedName: "",
  results: [],
  busy: false,
};

export function cleanupKinds(state: CleanupState) {
  const kinds: string[] = [];
  if (state.includeImages) {
    kinds.push("images");
  }
  if (state.includeContainers) {
    kinds.push("containers");
  }
  if (state.includeBuildCache) {
    kinds.push("build-cache");
  }
  if (state.includeVolumes) {
    kinds.push("volumes");
  }
  return kinds;
}

export function cleanupKindLabel(kind: string) {
  switch (kind) {
    case "images":
      return "Unused images";
    case "containers":
      return "Stopped containers";
    case "build-cache":
      return "Build cache";
    case "volumes":
      return "Unused volumes";
    default:
      return kind;
  }
}

export function CleanupModal({
  onChange,
  onConfirm,
  onClose,
  reclaimableLabel,
  state,
}: {
  state: CleanupState;
  reclaimableLabel: string;
  onChange: (patch: Partial<CleanupState>) => void;
  onConfirm: (state: CleanupState) => void;
  onClose: () => void;
}) {
  const selectedKinds = cleanupKinds(state);
  const hasSelection = selectedKinds.length > 0;
  const requiresTypedName = hasSelection;
  const typedReady = !requiresTypedName || state.typedName === "prune";
  const disabledReason = !hasSelection
    ? "Choose at least one cleanup target"
    : requiresTypedName && !typedReady
      ? "Type prune to confirm"
      : undefined;
  return (
    <Modal
      busy={state.busy}
      onClose={onClose}
      open={state.open}
      size="md"
      title="Clean Up Docker Space"
    >
      <div className="space-y-4">
        <div className="rounded-control border border-warn/30 bg-warn/10 p-3 text-sm text-warn">
          {reclaimableLabel} is currently reclaimable.
        </div>
        <div className="grid gap-2">
          {[
            ["includeImages", "Unused images"],
            ["includeContainers", "Stopped containers"],
            ["includeBuildCache", "Build cache"],
            ["includeVolumes", "Unused volumes"],
          ].map(([key, label]) => (
            <label
              className="flex items-center gap-3 rounded-control border border-border bg-bg-inset px-3 py-2 text-sm"
              key={key}
            >
              <input
                checked={Boolean(state[key as keyof CleanupState])}
                onChange={(event) =>
                  onChange({
                    [key]: event.target.checked,
                    typedName: "",
                    results: [],
                  } as Partial<CleanupState>)
                }
                type="checkbox"
              />
              {label}
            </label>
          ))}
        </div>
        {requiresTypedName ? (
          <label className="block text-sm">
            <span className="mb-1 block text-text-secondary">
              Type prune to confirm
            </span>
            <input
              className="h-10 w-full rounded-control border border-border bg-bg-inset px-3 text-text-primary outline-none"
              onChange={(event) => onChange({ typedName: event.target.value })}
              value={state.typedName}
            />
          </label>
        ) : null}
        <div className="rounded-control border border-border bg-bg-inset p-3 font-mono text-xs text-text-muted">
          {cleanupPreviewCommands(state).map((line) => (
            <div key={line}>$ {line}</div>
          ))}
        </div>
        {state.results.length > 0 ? (
          <div className="space-y-2">
            {state.results.map((result) => (
              <div
                className="flex items-center justify-between gap-3 rounded-control border border-border bg-bg-inset px-3 py-2 text-sm"
                key={result.kind}
              >
                <span className="min-w-0 truncate">{result.label}</span>
                <div className="flex min-w-0 items-center gap-2">
                  {result.message ? (
                    <span className="min-w-0 truncate text-xs text-text-muted">
                      {result.message}
                    </span>
                  ) : null}
                  <Badge tone={cleanupStatusTone(result.status)}>
                    {result.status}
                  </Badge>
                </div>
              </div>
            ))}
          </div>
        ) : null}
        {state.error ? (
          <div className="rounded-control border border-error/30 bg-error/10 p-3 text-sm text-error">
            {state.error}
          </div>
        ) : null}
        <div className="flex justify-end gap-2">
          <Button disabled={state.busy} onClick={onClose} variant="ghost">
            Cancel
          </Button>
          <Button
            disabled={!hasSelection || !typedReady || state.busy}
            disabledReason={disabledReason}
            loading={state.busy}
            onClick={() => onConfirm(state)}
            variant="danger"
          >
            Clean up
          </Button>
        </div>
      </div>
    </Modal>
  );
}

function cleanupStatusTone(status: CleanupStepResult["status"]): BadgeTone {
  switch (status) {
    case "success":
      return "ok";
    case "error":
      return "error";
    case "running":
      return "info";
    default:
      return "neutral";
  }
}

function cleanupPreviewCommands(state: CleanupState) {
  const commands = cleanupKinds(state).map((kind) => {
    switch (kind) {
      case "images":
        return "docker image prune --all";
      case "containers":
        return "docker container prune";
      case "build-cache":
        return "docker builder prune";
      case "volumes":
        return "docker volume prune";
      default:
        return `docker ${kind} prune`;
    }
  });
  return commands.length > 0 ? commands : ["docker system df"];
}
