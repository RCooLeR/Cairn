export const APP_ERROR_CODES = [
  "E_DOCKER_UNREACHABLE",
  "E_PROVIDER_NOT_READY",
  "E_PROVIDER_DETECT_FAILED",
  "E_COMPOSE_NOT_FOUND",
  "E_COMPOSE_INVALID",
  "E_WORKDIR_MISSING",
  "E_PERMISSION_DENIED",
  "E_REGISTRY_AUTH",
  "E_REGISTRY_RATE_LIMIT",
  "E_REGISTRY_UNREACHABLE",
  "E_NOT_FOUND",
  "E_CONFLICT",
  "E_PLAN_EXPIRED",
  "E_CONFIRMATION_REQUIRED",
  "E_TIMEOUT",
  "E_CANCELLED",
  "E_INTERNAL",
] as const;

export type AppErrorCode = (typeof APP_ERROR_CODES)[number];
export type ErrorTone = "error" | "warn" | "info";
export type ErrorSurface =
  | "global"
  | "inline"
  | "modal"
  | "permission"
  | "row"
  | "toast";

export type AppErrorPresentation = {
  code: AppErrorCode;
  title: string;
  body: string;
  tone: ErrorTone;
  surface: ErrorSurface;
  action: "confirm" | "dismiss" | "repair" | "retry" | "signin" | "silent";
};

const presentations: Record<
  AppErrorCode,
  Omit<AppErrorPresentation, "code">
> = {
  E_DOCKER_UNREACHABLE: {
    title: "Docker is not reachable",
    body: "Show the global banner with Repair and Retry actions.",
    tone: "warn",
    surface: "global",
    action: "repair",
  },
  E_PROVIDER_NOT_READY: {
    title: "Provider is not ready",
    body: "Show the global banner and route to provider repair or onboarding.",
    tone: "warn",
    surface: "global",
    action: "repair",
  },
  E_PROVIDER_DETECT_FAILED: {
    title: "Provider detection failed",
    body: "Render inline provider check details with a retry action.",
    tone: "error",
    surface: "inline",
    action: "retry",
  },
  E_COMPOSE_NOT_FOUND: {
    title: "Compose plugin missing",
    body: "Render inline provider repair guidance for installing Docker Compose.",
    tone: "error",
    surface: "inline",
    action: "repair",
  },
  E_COMPOSE_INVALID: {
    title: "Compose file is invalid",
    body: "Render validation output inline beside the project import or Compose tab.",
    tone: "error",
    surface: "inline",
    action: "retry",
  },
  E_WORKDIR_MISSING: {
    title: "Project folder missing",
    body: "Render inline project repair guidance and disable project actions.",
    tone: "warn",
    surface: "inline",
    action: "repair",
  },
  E_PERMISSION_DENIED: {
    title: "Docker socket permission denied",
    body: "Open the Linux permission options dialog with sudo, group, and rootless choices.",
    tone: "error",
    surface: "permission",
    action: "repair",
  },
  E_REGISTRY_AUTH: {
    title: "Registry login required",
    body: "Render per-row registry status with a login action.",
    tone: "warn",
    surface: "row",
    action: "signin",
  },
  E_REGISTRY_RATE_LIMIT: {
    title: "Registry rate limit reached",
    body: "Render per-row registry status and keep modal noise out of update checks.",
    tone: "warn",
    surface: "row",
    action: "retry",
  },
  E_REGISTRY_UNREACHABLE: {
    title: "Registry is unreachable",
    body: "Render per-row registry status with retry affordance.",
    tone: "warn",
    surface: "row",
    action: "retry",
  },
  E_NOT_FOUND: {
    title: "Object disappeared",
    body: "Silently refetch cached data and show a lightweight toast.",
    tone: "info",
    surface: "toast",
    action: "silent",
  },
  E_CONFLICT: {
    title: "Action conflicts with current state",
    body: "Render inline conflict details and keep destructive force paths explicit.",
    tone: "warn",
    surface: "inline",
    action: "confirm",
  },
  E_PLAN_EXPIRED: {
    title: "Command plan expired",
    body: "Keep the confirmation modal open and require a fresh preview.",
    tone: "warn",
    surface: "modal",
    action: "retry",
  },
  E_CONFIRMATION_REQUIRED: {
    title: "Confirmation required",
    body: "Keep the confirmation modal focused and require the missing confirmation input.",
    tone: "error",
    surface: "modal",
    action: "confirm",
  },
  E_TIMEOUT: {
    title: "Operation timed out",
    body: "Render inline timeout details with retry.",
    tone: "warn",
    surface: "inline",
    action: "retry",
  },
  E_CANCELLED: {
    title: "Operation cancelled",
    body: "Show a lightweight informational toast and leave cached state intact.",
    tone: "info",
    surface: "toast",
    action: "dismiss",
  },
  E_INTERNAL: {
    title: "Unexpected app error",
    body: "Render inline details with a retry action and diagnostic affordance.",
    tone: "error",
    surface: "inline",
    action: "retry",
  },
};

export type ParsedAppError = {
  code?: AppErrorCode;
  title: string;
  body: string;
  detail?: string;
};

export function appErrorPresentation(code: AppErrorCode): AppErrorPresentation {
  return { code, ...presentations[code] };
}

export function isAppErrorCode(code: string): code is AppErrorCode {
  return APP_ERROR_CODES.includes(code as AppErrorCode);
}

export function parseAppErrorText(text: string): ParsedAppError {
  const raw = text.trim();
  const structured = parseStructuredAppError(raw);
  const code = structured.code ?? codeFromMessage(structured.message ?? raw);
  if (code) {
    const presentation = appErrorPresentation(code);
    return {
      code,
      title: presentation.title,
      body: cleanAppErrorMessage(structured.message ?? raw, code),
      detail: structured.detail,
    };
  }
  return {
    title: "Action failed",
    body: structured.message ?? raw,
    detail: structured.detail,
  };
}

function parseStructuredAppError(text: string): {
  code?: AppErrorCode;
  message?: string;
  detail?: string;
} {
  if (!text.startsWith("{")) {
    return {};
  }
  try {
    const parsed = JSON.parse(text) as {
      message?: unknown;
      cause?: { code?: unknown; message?: unknown; detail?: unknown };
    };
    const causeCode =
      typeof parsed.cause?.code === "string" ? parsed.cause.code : "";
    const message =
      typeof parsed.cause?.message === "string"
        ? parsed.cause.message
        : typeof parsed.message === "string"
          ? parsed.message
          : undefined;
    const detail =
      typeof parsed.cause?.detail === "string" ? parsed.cause.detail : "";
    return {
      code: isAppErrorCode(causeCode) ? causeCode : undefined,
      message,
      detail: detail.trim() || undefined,
    };
  } catch {
    return {};
  }
}

function codeFromMessage(message: string): AppErrorCode | undefined {
  const match = message.match(/\b(E_[A-Z_]+)\b/);
  const code = match?.[1] ?? "";
  return isAppErrorCode(code) ? code : undefined;
}

function cleanAppErrorMessage(message: string, code: AppErrorCode) {
  const trimmed = message.trim();
  const prefix = `${code}:`;
  if (trimmed.startsWith(prefix)) {
    return trimmed.slice(prefix.length).trim();
  }
  return trimmed;
}
