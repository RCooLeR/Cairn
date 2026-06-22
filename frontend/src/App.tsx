import type {
  AuditEntry,
  BackupSummary,
  CommandPlan,
  ContainerDetail,
  ContainerFileListing,
  ContainerSummary,
  DashboardMetrics,
  DockerContextInfo,
  ExportResult,
  GPUMetrics,
  HubSearchResult,
  ImageDetail,
  ImageLineage,
  ImageSummary,
  ImageUpdate,
  LogLine,
  MountSpec,
  NetworkDetail,
  NetworkSummary,
  Notification,
  PortMapping,
  PortBinding,
  ProviderProblem,
  ProviderStatus,
  ProviderWarning,
  ProjectDetail,
  ProjectSummary,
  ProviderSummary,
  RegistryAccount,
  RegistryAuthStatus,
  RegistryPreset,
  RunImageRequest,
  TerminalSessionInfo,
  UpdateHistoryItem,
  UpdatePlan,
  VolumeDetail,
  VolumeSummary,
  WSLDistroInfo,
} from "../bindings/github.com/RCooLeR/Cairn/internal/models/models.js";
import {
  Risk,
  UpdateKind,
  UpdateStatus,
} from "../bindings/github.com/RCooLeR/Cairn/internal/models/models.js";

import {
  AlertTriangle,
  ArrowDown,
  Bell,
  Bot,
  Box,
  CheckCircle2,
  Clock3,
  Container,
  Copy,
  Cpu,
  Database,
  Download,
  Eye,
  FileJson,
  Filter,
  FolderOpen,
  Gauge,
  Layers,
  LayoutGrid,
  List,
  LogIn,
  MemoryStick,
  PackagePlus,
  PanelLeftClose,
  PanelLeftOpen,
  Pencil,
  Plus,
  Network,
  Pause,
  Play,
  RefreshCw,
  RotateCw,
  ScrollText,
  Search,
  Server,
  Settings as SettingsIcon,
  ShieldAlert,
  Skull,
  Square,
  Tag,
  Terminal,
  Trash2,
  Undo2,
  Upload,
  Wifi,
  Wrench,
  WrapText,
  X,
} from "lucide-react";
import {
  type RefObject,
  type ReactNode,
  lazy,
  Suspense,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  Area,
  AreaChart,
  CartesianGrid,
  Cell,
  Line,
  LineChart,
  Pie,
  PieChart as RechartsPieChart,
  ResponsiveContainer,
  Tooltip as RechartsTooltip,
  XAxis,
  YAxis,
} from "recharts";
import { Browser, Clipboard, Dialogs, Events } from "@wailsio/runtime";

import { getAppVersion } from "./api/app";
import { parseAppErrorText } from "./api/errors";
import {
  BackupService,
  DockerService,
  ImageLineageService,
  LogsService,
  MetricsService,
  ProviderService,
  ProjectService,
  RegistryService,
  SettingsService,
  TerminalService,
  UpdateService,
} from "./api/services";
import {
  Badge,
  Button,
  Card,
  CardBody,
  CardHeader,
  DataTable,
  EmptyState,
  Modal,
  Skeleton,
  StatusDot,
  StatusPill,
  TableSkeleton,
  ToastViewport,
  Tooltip,
} from "./components/ui";
import { NotificationCenter } from "./components/notifications/NotificationCenter";
import {
  CleanupModal,
  cleanupKindLabel,
  cleanupKinds,
  emptyCleanup,
  type CleanupState,
  type CleanupStepResult,
} from "./components/overview/CleanupModal";
import {
  CommandPalette,
  TerminalPage,
  type TerminalCommandRequest,
} from "./components/terminal/TerminalPage";
import { AgentPage } from "./agent/AgentPage";
import { useAppStore } from "./state/appStore";
import { useInventoryStore } from "./state/inventoryStore";
import {
  type AppSettings,
  type PermissionMode,
  normalizeBoolSetting,
  normalizeIntSetting,
  normalizePermissionMode,
  normalizeStringSetting,
  normalizeThemePreference,
  settingString,
} from "./settings/appSettings";
import {
  ColimaPathRecommendation,
  LinuxPathRecommendation,
  PathRecommendation,
  SettingsPage,
  auditMetadataString,
  type AuditFilterState,
  type AuditRangeID,
  type SettingsSectionID,
} from "./settings/SettingsPage";
import {
  normalizeRegistryHostForUI,
  registryStorageLabel,
} from "./settings/registryUi";
import { useDebouncedRuntimeEvent } from "./hooks/useDebouncedRuntimeEvent";
import { useToastQueue, type ToastInput } from "./hooks/useToastQueue";
import {
  chartColors,
  containerStatusChartSegments,
  emptyContainerStatusChartSegment,
} from "./overview/dashboardData";
import { dateMillis, formatDate, relativeTime, toDate } from "./utils/time";
import { riskTone, type BadgeTone } from "./utils/tones";
import { frontendVersion } from "./version";
import type { NavItem, PageID } from "./types/navigation";

const logoUrl = "/cairn-logo.png";
const iconUrl = "/cairn-icon.png";
const MonacoEditor = lazy(() => import("@monaco-editor/react"));

type AppUpdateNotice = {
  version: string;
  url: string;
  name?: string;
  publishedAt?: string;
};
type ObjectsChangedEventPayload = {
  kind?: string;
  ids?: string[];
};
type FilterID = string;
type StatusToneID = "ok" | "warn" | "error" | "info" | "neutral";
type LoadStatus = "idle" | "loading" | "ready" | "error";
type ProjectViewMode = "grid" | "list";
type ProjectSortID = "name" | "activity" | "cpu";
type ProjectTabID =
  | "overview"
  | "services"
  | "containers"
  | "logs"
  | "updates"
  | "compose"
  | "backups";
type ContainerDrilldownTabID =
  | "overview"
  | "logs"
  | "files"
  | "terminal"
  | "inspect";
type NetworkTabID = "overview" | "containers" | "labels" | "raw";
type ComposeServiceRow = NonNullable<ProjectDetail["services"]>[number];
type LogScope = "all" | "project" | "service" | "container";
type LogLevelFilter = "error" | "warn" | "info" | "debug" | "log" | "unknown";
type SetupStepID =
  | "welcome"
  | "backend"
  | "checks"
  | "install"
  | "verify"
  | "projects"
  | "done";
type SetupPlatformID = "windows" | "linux" | "macos";
type SetupBackendID =
  | "windows_wsl_ubuntu"
  | "linux_native"
  | "macos_colima"
  | "existing_context";

type InspectState = {
  open: boolean;
  title: string;
  subtitle?: string;
  rows: Array<[string, string]>;
  lineage?: ImageLineage | null;
  raw?: string;
  loading?: boolean;
  error?: string;
};

type UpdatesTabID = "current" | "history" | "ignored";
type UpdatePlanTarget =
  | {
      kind: "service";
      projectID: string;
      projectName?: string;
      service: string;
    }
  | { kind: "project"; projectID: string; projectName?: string };

type UpdateProgressEntry = {
  jobID?: string;
  phase?: string;
  message?: string;
  pct?: number;
};

type ProjectJobEvent = UpdateProgressEntry & {
  projectID?: string;
  action?: string;
  command?: string;
  result?: string;
  error?: string;
};

type ProjectCommandOutputLine = {
  id: string;
  ts: number;
  phase: string;
  message: string;
  tone: "muted" | "info" | "ok" | "error";
};

type ProjectCommandOutputState = {
  projectID: string;
  jobID: string;
  action?: string;
  command?: string;
  status: "running" | "success" | "failed";
  startedAt: number;
  updatedAt: number;
  lines: ProjectCommandOutputLine[];
  result?: string;
  error?: string;
};

type UpdatePlanState = {
  open: boolean;
  mode: "update" | "rollback";
  plan: UpdatePlan | null;
  target: UpdatePlanTarget | null;
  backupVolumesFirst: boolean;
  watchHealth: boolean;
  rollbackOnFailure: boolean;
  busy: boolean;
  applying: boolean;
  jobID?: string;
  progress: UpdateProgressEntry[];
  result?: string;
  error?: string;
};

type IgnoreUpdateState = {
  open: boolean;
  update: ImageUpdate | null;
  reason: string;
  busy: boolean;
  error?: string;
};

type ContainerAction = "start" | "stop" | "restart" | "kill" | "remove";
type ProjectAction =
  | "start"
  | "stop"
  | "restart"
  | "pull"
  | "redeploy"
  | "down"
  | "down-volumes"
  | "remove";
type ConfirmPlanKind =
  | "container"
  | "project"
  | "run-image"
  | "backup"
  | "backup-delete"
  | "restore"
  | "provider";

type ConfirmState = {
  open: boolean;
  plan: CommandPlan | null;
  planKind: ConfirmPlanKind;
  targetName: string;
  typedName: string;
  busy: boolean;
  error?: string;
};

type RemoveProjectState = {
  open: boolean;
  project: ProjectSummary | null;
  busy: boolean;
  error?: string;
};

type RenameState = {
  open: boolean;
  container: ContainerSummary | null;
  name: string;
  busy: boolean;
  error?: string;
};

type RunImageState = {
  open: boolean;
  step: 1 | 2;
  imageRef: string;
  imageLocked: boolean;
  name: string;
  pullIfMissing: boolean;
  portsText: string;
  envText: string;
  volumesText: string;
  networkID: string;
  restartPolicy: string;
  commandText: string;
  user: string;
  hubQuery: string;
  hubResults: HubSearchResult[];
  hubLoading: boolean;
  hubError?: string;
  busy: boolean;
  error?: string;
};

type PullImageState = {
  open: boolean;
  ref: string;
  tag: string;
  query: string;
  results: HubSearchResult[];
  loadingResults: boolean;
  searchError?: string;
  busy: boolean;
  error?: string;
};

type SaveImageState = {
  open: boolean;
  refsText: string;
  destPath: string;
  busy: boolean;
  error?: string;
};

type LoadImageState = {
  open: boolean;
  srcPath: string;
  busy: boolean;
  error?: string;
};

type ImageProgressPayload = {
  streamID: string;
  layerID?: string;
  status: string;
  current?: number;
  total?: number;
};

type TagImageState = {
  open: boolean;
  image: ImageSummary | null;
  newRef: string;
  busy: boolean;
  error?: string;
};

type PushImageState = {
  open: boolean;
  image: ImageSummary | null;
  ref: string;
  confirmed: boolean;
  streamID?: string;
  progress: ImageProgressPayload[];
  busy: boolean;
  success: boolean;
  error?: string;
};

type RegistryLoginState = {
  open: boolean;
  registry: string;
  username: string;
  secret: string;
  secretKind: "password" | "token";
  busy: boolean;
  error?: string;
};

type CreateVolumeState = {
  open: boolean;
  name: string;
  driver: string;
  driverOptsText: string;
  labelsText: string;
  busy: boolean;
  error?: string;
};

type BackupVolumeState = {
  open: boolean;
  volume: VolumeSummary | null;
  destPath: string;
  busy: boolean;
  error?: string;
};

type RestoreVolumeState = {
  open: boolean;
  volume: VolumeSummary | null;
  backups: BackupSummary[];
  backupID: string;
  sourcePath: string;
  targetName: string;
  overwrite: boolean;
  loading: boolean;
  busy: boolean;
  error?: string;
};

type CreateNetworkState = {
  open: boolean;
  name: string;
  driver: string;
  customDriver: string;
  subnet: string;
  gateway: string;
  internal: boolean;
  attachable: boolean;
  labelsText: string;
  busy: boolean;
  error?: string;
};

type ImportProjectState = {
  open: boolean;
  folderPath: string;
  busy: boolean;
  error?: string;
  imported?: ProjectDetail | null;
};

type ProviderInstallProgressPayload = {
  planID: string;
  streamID: string;
  step: number;
  totalSteps: number;
  message: string;
  done: boolean;
  error?: string;
};

type ProviderSetupState = {
  open: boolean;
  step: SetupStepID;
  platform: SetupPlatformID;
  backend: SetupBackendID;
  distro: string;
  colimaProfile: string;
  colimaCPU: number;
  colimaMemoryGB: number;
  colimaDiskGB: number;
  detecting: boolean;
  detection: ProviderStatus | null;
  detectError?: string;
  plan: CommandPlan | null;
  planning: boolean;
  installing: boolean;
  installStreamID?: string;
  progress: ProviderInstallProgressPayload[];
  detectedProjects: ProjectSummary[];
  selectedProjectIDs: string[];
  detectingProjects: boolean;
  projectDetectError?: string;
  error?: string;
};

type ProviderInstallSession = {
  planID: string;
  streamID?: string;
  backend: SetupBackendID;
};

type ExportLogsState = {
  open: boolean;
  path: string;
  format: "log" | "jsonl";
  range: "buffer" | "tail";
  busy: boolean;
  error?: string;
  result?: ExportResult | null;
};

type LogLinesPayload = {
  streamID: string;
  lines?: LogLine[];
};

type LogErrorPayload = {
  streamID: string;
  error?: string;
};

type DashboardMetricID = "cpu" | "gpu" | "memory" | "network";
type DashboardRangeID = "5m" | "1h" | "24h";

type StatsSample = {
  projectID?: string;
  serviceID?: string;
  containerID: string;
  containerName?: string;
  health?: string;
  restartCount?: number;
  uptimeSeconds?: number;
  cpuPercent: number;
  gpuDeviceIDs?: string[];
  gpuMemoryBytes?: number;
  gpuUtilizationPercent?: number;
  memoryBytes: number;
  memoryLimitBytes?: number;
  networkRxRate: number;
  networkTxRate: number;
  sampledAt: unknown;
};

type StatsSamplePayload = {
  streamID: string;
  samples?: StatsSample[];
  gpu?: GPUMetrics;
};

type DashboardChartPoint = {
  ts: number;
  label: string;
  cpu: number;
  gpu: number;
  memory: number;
  netRx: number;
  netTx: number;
};

type SparkPoint = {
  label: string;
  value: number;
};

type SparkPointMap = Record<string, SparkPoint[]>;
type ProjectMetricSparks = Record<DashboardMetricID, SparkPointMap>;

const maxProjectCommandOutputLines = 300;
const projectStatsFrameMs = 2 * 1000;
const navItems: NavItem[] = [
  { id: "overview", label: "Overview", icon: Gauge },
  { id: "projects", label: "Projects", icon: LayoutGrid },
  { id: "updates", label: "Updates", icon: RefreshCw },
  { id: "containers", label: "Containers", icon: Container },
  { id: "images", label: "Images", icon: Box },
  { id: "volumes", label: "Volumes", icon: Database },
  { id: "networks", label: "Networks", icon: Network },
  { id: "logs", label: "Logs", icon: ScrollText },
  { id: "terminal", label: "Terminal", icon: Terminal },
  { id: "agent", label: "Agent", icon: Bot },
  { id: "settings", label: "Settings", icon: SettingsIcon },
];

const emptyInspect: InspectState = {
  open: false,
  title: "",
  rows: [],
};

const emptyUpdatePlan: UpdatePlanState = {
  open: false,
  mode: "update",
  plan: null,
  target: null,
  backupVolumesFirst: false,
  watchHealth: true,
  rollbackOnFailure: true,
  busy: false,
  applying: false,
  progress: [],
};

const emptyIgnoreUpdate: IgnoreUpdateState = {
  open: false,
  update: null,
  reason: "",
  busy: false,
};

const emptyConfirm: ConfirmState = {
  open: false,
  plan: null,
  planKind: "container",
  targetName: "",
  typedName: "",
  busy: false,
};

const emptyRemoveProject: RemoveProjectState = {
  open: false,
  project: null,
  busy: false,
};

const emptyRename: RenameState = {
  open: false,
  container: null,
  name: "",
  busy: false,
};

const emptyRunImage: RunImageState = {
  open: false,
  step: 1,
  imageRef: "",
  imageLocked: false,
  name: "",
  pullIfMissing: true,
  portsText: "",
  envText: "",
  volumesText: "",
  networkID: "",
  restartPolicy: "no",
  commandText: "",
  user: "",
  hubQuery: "",
  hubResults: [],
  hubLoading: false,
  busy: false,
};

const emptyPullImage: PullImageState = {
  open: false,
  ref: "",
  tag: "latest",
  query: "",
  results: [],
  loadingResults: false,
  busy: false,
};

const emptySaveImage: SaveImageState = {
  open: false,
  refsText: "",
  destPath: "",
  busy: false,
};

const emptyLoadImage: LoadImageState = {
  open: false,
  srcPath: "",
  busy: false,
};

const emptyTagImage: TagImageState = {
  open: false,
  image: null,
  newRef: "",
  busy: false,
};

const emptyPushImage: PushImageState = {
  open: false,
  image: null,
  ref: "",
  confirmed: false,
  progress: [],
  busy: false,
  success: false,
};

const emptyRegistryLogin: RegistryLoginState = {
  open: false,
  registry: "docker.io",
  username: "",
  secret: "",
  secretKind: "password",
  busy: false,
};

const emptyCreateVolume: CreateVolumeState = {
  open: false,
  name: "",
  driver: "local",
  driverOptsText: "",
  labelsText: "",
  busy: false,
};

const emptyBackupVolume: BackupVolumeState = {
  open: false,
  volume: null,
  destPath: "",
  busy: false,
};

const emptyRestoreVolume: RestoreVolumeState = {
  open: false,
  volume: null,
  backups: [],
  backupID: "",
  sourcePath: "",
  targetName: "",
  overwrite: false,
  loading: false,
  busy: false,
};

const emptyCreateNetwork: CreateNetworkState = {
  open: false,
  name: "",
  driver: "bridge",
  customDriver: "",
  subnet: "",
  gateway: "",
  internal: false,
  attachable: false,
  labelsText: "",
  busy: false,
};

const emptyImportProject: ImportProjectState = {
  open: false,
  folderPath: "",
  busy: false,
  imported: null,
};

const windowsWSLProviderID = "windows_wsl_ubuntu";
const linuxNativeProviderID = "linux_native";
const macOSColimaProviderID = "macos_colima";
const providerSetupStorageKey = "cairn.providerSetup.v1";

const emptyProviderSetup: ProviderSetupState = {
  open: false,
  step: "welcome",
  platform: "windows",
  backend: "windows_wsl_ubuntu",
  distro: "Ubuntu",
  colimaProfile: "default",
  colimaCPU: 2,
  colimaMemoryGB: 4,
  colimaDiskGB: 60,
  detecting: false,
  detection: null,
  plan: null,
  planning: false,
  installing: false,
  progress: [],
  detectedProjects: [],
  selectedProjectIDs: [],
  detectingProjects: false,
};

function detectClientSetupPlatform(): SetupPlatformID {
  const platform =
    (navigator as Navigator & { userAgentData?: { platform?: string } })
      .userAgentData?.platform ||
    navigator.platform ||
    navigator.userAgent ||
    "";
  const lower = platform.toLowerCase();
  if (lower.includes("mac")) {
    return "macos";
  }
  if (lower.includes("linux")) {
    return "linux";
  }
  return "windows";
}

function setupPlatformFromProvider(
  provider: ProviderSummary | null | undefined,
): SetupPlatformID | null {
  switch (provider?.kind) {
    case "macos_colima":
      return "macos";
    case "linux_native":
      return "linux";
    case "wsl":
    case "windows_wsl_ubuntu":
      return "windows";
    default:
      return null;
  }
}

function recommendedSetupBackend(platform: SetupPlatformID): SetupBackendID {
  switch (platform) {
    case "macos":
      return "macos_colima";
    case "linux":
      return "linux_native";
    default:
      return "windows_wsl_ubuntu";
  }
}

function setupPlatformForBackend(
  backend: SetupBackendID,
  current: SetupPlatformID,
): SetupPlatformID {
  switch (backend) {
    case "macos_colima":
      return "macos";
    case "linux_native":
      return "linux";
    case "windows_wsl_ubuntu":
      return "windows";
    default:
      return current;
  }
}

function providerIDForSetupBackend(backend: SetupBackendID): string | null {
  switch (backend) {
    case "windows_wsl_ubuntu":
      return windowsWSLProviderID;
    case "linux_native":
      return linuxNativeProviderID;
    case "macos_colima":
      return macOSColimaProviderID;
    default:
      return null;
  }
}

function normalizedSetupStep(value: unknown): SetupStepID {
  return value === "backend" ||
    value === "checks" ||
    value === "install" ||
    value === "verify" ||
    value === "projects" ||
    value === "done"
    ? value
    : "welcome";
}

function normalizedSetupPlatform(value: unknown): SetupPlatformID {
  return value === "linux" || value === "macos" || value === "windows"
    ? value
    : "windows";
}

function normalizedSetupBackend(value: unknown): SetupBackendID {
  return value === "linux_native" ||
    value === "macos_colima" ||
    value === "existing_context" ||
    value === "windows_wsl_ubuntu"
    ? value
    : "windows_wsl_ubuntu";
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function stringSetting(value: unknown, fallback: string) {
  return typeof value === "string" && value.trim() ? value : fallback;
}

function numericSetting(value: unknown, fallback: number) {
  return typeof value === "number" && Number.isFinite(value) ? value : fallback;
}

function stringArraySetting(value: unknown) {
  return Array.isArray(value)
    ? value.filter((item): item is string => typeof item === "string")
    : [];
}

function restoredCommandPlan(value: unknown): CommandPlan | null {
  if (!isRecord(value)) {
    return null;
  }
  if (
    typeof value.planID !== "string" ||
    typeof value.title !== "string" ||
    !Array.isArray(value.commands) ||
    !Array.isArray(value.effects)
  ) {
    return null;
  }
  return value as unknown as CommandPlan;
}

function restoredProviderStatus(value: unknown): ProviderStatus | null {
  if (!isRecord(value)) {
    return null;
  }
  return value as unknown as ProviderStatus;
}

function restoredInstallProgress(
  value: unknown,
): ProviderInstallProgressPayload[] {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.filter((item): item is ProviderInstallProgressPayload => {
    if (!isRecord(item)) {
      return false;
    }
    return (
      typeof item.planID === "string" &&
      typeof item.streamID === "string" &&
      typeof item.step === "number" &&
      Number.isFinite(item.step) &&
      typeof item.totalSteps === "number" &&
      Number.isFinite(item.totalSteps) &&
      typeof item.message === "string" &&
      typeof item.done === "boolean"
    );
  });
}

function restoredProjectSummaries(value: unknown): ProjectSummary[] {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.filter((item): item is ProjectSummary => {
    if (!isRecord(item)) {
      return false;
    }
    return (
      typeof item.id === "string" &&
      typeof item.name === "string" &&
      typeof item.providerID === "string"
    );
  });
}

function restoreProviderSetupState(): ProviderSetupState {
  try {
    const raw = window.localStorage.getItem(providerSetupStorageKey);
    if (!raw) {
      return emptyProviderSetup;
    }
    const parsed = JSON.parse(raw) as unknown;
    if (!isRecord(parsed)) {
      return emptyProviderSetup;
    }
    const platform = normalizedSetupPlatform(parsed.platform);
    return {
      ...emptyProviderSetup,
      open: Boolean(parsed.open),
      step: normalizedSetupStep(parsed.step),
      platform,
      backend: normalizedSetupBackend(parsed.backend),
      distro: stringSetting(parsed.distro, "Ubuntu"),
      colimaProfile: stringSetting(parsed.colimaProfile, "default"),
      colimaCPU: numericSetting(parsed.colimaCPU, 2),
      colimaMemoryGB: numericSetting(parsed.colimaMemoryGB, 4),
      colimaDiskGB: numericSetting(parsed.colimaDiskGB, 60),
      detection: restoredProviderStatus(parsed.detection),
      detectError:
        typeof parsed.detectError === "string" ? parsed.detectError : undefined,
      plan: restoredCommandPlan(parsed.plan),
      progress: restoredInstallProgress(parsed.progress),
      detecting: false,
      planning: false,
      installing: false,
      detectingProjects: false,
      detectedProjects: restoredProjectSummaries(parsed.detectedProjects),
      selectedProjectIDs: stringArraySetting(parsed.selectedProjectIDs),
      projectDetectError:
        typeof parsed.projectDetectError === "string"
          ? parsed.projectDetectError
          : undefined,
      error: typeof parsed.error === "string" ? parsed.error : undefined,
    };
  } catch {
    window.localStorage.removeItem(providerSetupStorageKey);
    return emptyProviderSetup;
  }
}

function persistProviderSetupState(setup: ProviderSetupState) {
  if (!setup.open) {
    window.localStorage.removeItem(providerSetupStorageKey);
    return;
  }
  const persistable: ProviderSetupState = {
    ...setup,
    detecting: false,
    planning: false,
    installing: false,
    installStreamID: undefined,
    detectingProjects: false,
  };
  window.localStorage.setItem(
    providerSetupStorageKey,
    JSON.stringify(persistable),
  );
}

const emptyExportLogs: ExportLogsState = {
  open: false,
  path: "",
  format: "jsonl",
  range: "buffer",
  busy: false,
  result: null,
};

function App() {
  const version = useAppStore((state) => state.version);
  const versionLoading = useAppStore((state) => state.versionLoading);
  const setVersion = useAppStore((state) => state.setVersion);
  const setVersionError = useAppStore((state) => state.setVersionError);
  const setVersionLoading = useAppStore((state) => state.setVersionLoading);
  const inventoryStatus = useInventoryStore((state) => state.status);
  const inventoryError = useInventoryStore((state) => state.error);
  const lastLoadedAt = useInventoryStore((state) => state.lastLoadedAt);
  const providers = useInventoryStore((state) => state.providers);
  const dockerInfo = useInventoryStore((state) => state.dockerInfo);
  const dockerVersion = useInventoryStore((state) => state.dockerVersion);
  const diskUsage = useInventoryStore((state) => state.diskUsage);
  const containers = useInventoryStore((state) => state.containers);
  const images = useInventoryStore((state) => state.images);
  const volumes = useInventoryStore((state) => state.volumes);
  const networks = useInventoryStore((state) => state.networks);
  const volumeDetails = useInventoryStore((state) => state.volumeDetails);
  const networkDetails = useInventoryStore((state) => state.networkDetails);
  const refreshInventory = useInventoryStore((state) => state.refresh);
  const refreshContainers = useInventoryStore(
    (state) => state.refreshContainers,
  );
  const refreshImages = useInventoryStore((state) => state.refreshImages);
  const refreshVolumes = useInventoryStore((state) => state.refreshVolumes);
  const refreshNetworks = useInventoryStore((state) => state.refreshNetworks);

  const [activePage, setActivePage] = useState<PageID>("overview");
  const [sidebarCollapsed, setSidebarCollapsed] = useState(() => {
    return window.localStorage.getItem("cairn.sidebar.collapsed") === "true";
  });
  const [dashboardRefreshToken, setDashboardRefreshToken] = useState(0);
  const [settingsSection, setSettingsSection] =
    useState<SettingsSectionID>("providers");
  const [search, setSearch] = useState("");
  const searchInputRef = useRef<HTMLInputElement>(null);
  const [projects, setProjects] = useState<ProjectSummary[]>([]);
  const [projectsStatus, setProjectsStatus] = useState<LoadStatus>("idle");
  const [projectsError, setProjectsError] = useState<string | null>(null);
  const [activeProjectID, setActiveProjectID] = useState<string | null>(null);
  const [projectDetail, setProjectDetail] = useState<ProjectDetail | null>(
    null,
  );
  const [projectDetailStatus, setProjectDetailStatus] =
    useState<LoadStatus>("idle");
  const [projectDetailError, setProjectDetailError] = useState<string | null>(
    null,
  );
  const [projectCommandOutputs, setProjectCommandOutputs] = useState<
    Record<string, ProjectCommandOutputState>
  >({});
  const [projectTab, setProjectTab] = useState<ProjectTabID>("overview");
  const [projectFilter, setProjectFilter] = useState<FilterID>("all");
  const [projectSort, setProjectSort] = useState<ProjectSortID>("name");
  const [projectView, setProjectView] = useState<ProjectViewMode>(() => {
    const saved = window.localStorage.getItem("cairn.projects.view");
    return saved === "list" ? "list" : "grid";
  });
  const [containerFilter, setContainerFilter] = useState<FilterID>("all");
  const [activeContainerID, setActiveContainerID] = useState<string | null>(
    null,
  );
  const [activeContainerFallback, setActiveContainerFallback] =
    useState<ContainerSummary | null>(null);
  const [containerDetailTab, setContainerDetailTab] =
    useState<ContainerDrilldownTabID>("overview");
  const [imageFilter, setImageFilter] = useState<FilterID>("all");
  const [volumeFilter, setVolumeFilter] = useState<FilterID>("all");
  const [activeNetworkID, setActiveNetworkID] = useState<string | null>(null);
  const [networkTab, setNetworkTab] = useState<NetworkTabID>("overview");
  const [inspect, setInspect] = useState<InspectState>(emptyInspect);
  const [confirm, setConfirm] = useState<ConfirmState>(emptyConfirm);
  const [removeProject, setRemoveProject] =
    useState<RemoveProjectState>(emptyRemoveProject);
  const [rename, setRename] = useState<RenameState>(emptyRename);
  const [runImage, setRunImage] = useState<RunImageState>(emptyRunImage);
  const [pullImage, setPullImage] = useState<PullImageState>(emptyPullImage);
  const [tagImage, setTagImage] = useState<TagImageState>(emptyTagImage);
  const [pushImage, setPushImage] = useState<PushImageState>(emptyPushImage);
  const [saveImage, setSaveImage] = useState<SaveImageState>(emptySaveImage);
  const [loadImage, setLoadImage] = useState<LoadImageState>(emptyLoadImage);
  const [registryAccounts, setRegistryAccounts] = useState<RegistryAccount[]>(
    [],
  );
  const [registryAccountsStatus, setRegistryAccountsStatus] =
    useState<LoadStatus>("idle");
  const [registryAccountsError, setRegistryAccountsError] = useState<
    string | null
  >(null);
  const [registryStatuses, setRegistryStatuses] = useState<
    Record<string, RegistryAuthStatus>
  >({});
  const [registryPresets, setRegistryPresets] = useState<RegistryPreset[]>([]);
  const [registryLogin, setRegistryLogin] =
    useState<RegistryLoginState>(emptyRegistryLogin);
  const [registryBusyKeys, setRegistryBusyKeys] = useState(
    () => new Set<string>(),
  );
  const [createVolume, setCreateVolume] =
    useState<CreateVolumeState>(emptyCreateVolume);
  const [backupVolume, setBackupVolume] =
    useState<BackupVolumeState>(emptyBackupVolume);
  const [restoreVolume, setRestoreVolume] =
    useState<RestoreVolumeState>(emptyRestoreVolume);
  const [backups, setBackups] = useState<BackupSummary[]>([]);
  const [backupsStatus, setBackupsStatus] = useState<LoadStatus>("idle");
  const [backupsError, setBackupsError] = useState<string | null>(null);
  const [updates, setUpdates] = useState<ImageUpdate[]>([]);
  const [updatesStatus, setUpdatesStatus] = useState<LoadStatus>("idle");
  const [updatesError, setUpdatesError] = useState<string | null>(null);
  const [updateHistory, setUpdateHistory] = useState<UpdateHistoryItem[]>([]);
  const [updateHistoryStatus, setUpdateHistoryStatus] =
    useState<LoadStatus>("idle");
  const [updateHistoryError, setUpdateHistoryError] = useState<string | null>(
    null,
  );
  const [ignoredUpdates, setIgnoredUpdates] = useState<ImageUpdate[]>([]);
  const [ignoredUpdatesStatus, setIgnoredUpdatesStatus] =
    useState<LoadStatus>("idle");
  const [ignoredUpdatesError, setIgnoredUpdatesError] = useState<string | null>(
    null,
  );
  const [updatesTab, setUpdatesTab] = useState<UpdatesTabID>("current");
  const [updateFilter, setUpdateFilter] = useState<FilterID>("all");
  const [lastUpdateCheckAt, setLastUpdateCheckAt] = useState<number | null>(
    null,
  );
  const [updateCheckJobID, setUpdateCheckJobID] = useState<string | null>(null);
  const [updateCheckProgress, setUpdateCheckProgress] =
    useState<UpdateProgressEntry | null>(null);
  const [updatePlan, setUpdatePlan] =
    useState<UpdatePlanState>(emptyUpdatePlan);
  const [ignoreUpdate, setIgnoreUpdate] =
    useState<IgnoreUpdateState>(emptyIgnoreUpdate);
  const [projectLineage, setProjectLineage] = useState<
    Record<string, ImageLineage[]>
  >({});
  const [projectLineageStatus, setProjectLineageStatus] = useState<
    Record<string, LoadStatus>
  >({});
  const [createNetwork, setCreateNetwork] =
    useState<CreateNetworkState>(emptyCreateNetwork);
  const [importProject, setImportProject] =
    useState<ImportProjectState>(emptyImportProject);
  const [selectedContainerIDs, setSelectedContainerIDs] = useState(
    () => new Set<string>(),
  );
  const [busyActionIDs, setBusyActionIDs] = useState(() => new Set<string>());
  const [terminalInitialSession, setTerminalInitialSession] =
    useState<TerminalSessionInfo | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const parsedActionError = useMemo(
    () => (actionError ? parseAppErrorText(actionError) : null),
    [actionError],
  );
  const [providerActionBusy, setProviderActionBusy] = useState(false);
  const [repairOpen, setRepairOpen] = useState(false);
  const [repairError, setRepairError] = useState<string | null>(null);
  const [repairSaving, setRepairSaving] = useState(false);
  const [permissionMode, setPermissionMode] = useState<PermissionMode>("ask");
  const [appSettings, setAppSettings] = useState<AppSettings>({});
  const [wslDistro, setWSLDistro] = useState("Ubuntu");
  const [colimaProfile, setColimaProfile] = useState("default");
  const [colimaCPU, setColimaCPU] = useState(2);
  const [colimaMemoryGB, setColimaMemoryGB] = useState(4);
  const [colimaDiskGB, setColimaDiskGB] = useState(60);
  const [dockerContexts, setDockerContexts] = useState<DockerContextInfo[]>([]);
  const [dockerContextsLoading, setDockerContextsLoading] = useState(false);
  const [dockerContextsError, setDockerContextsError] = useState<string | null>(
    null,
  );
  const [wslDistros, setWSLDistros] = useState<WSLDistroInfo[]>([]);
  const [wslDistrosLoading, setWSLDistrosLoading] = useState(false);
  const [wslDistrosError, setWSLDistrosError] = useState<string | null>(null);
  const [providerAutostart, setProviderAutostart] = useState(true);
  const [settingsSaving, setSettingsSaving] = useState(false);
  const [settingsMessage, setSettingsMessage] = useState<string | null>(null);
  const [settingsError, setSettingsError] = useState<string | null>(null);
  const { pushToast, toasts } = useToastQueue();
  const [settingsLoaded, setSettingsLoaded] = useState(false);
  const [auditEntries, setAuditEntries] = useState<AuditEntry[]>([]);
  const [auditLoading, setAuditLoading] = useState(false);
  const [auditError, setAuditError] = useState<string | null>(null);
  const [auditFilter, setAuditFilter] = useState<AuditFilterState>({
    range: "7d",
    action: "",
    status: "",
    projectID: "",
  });
  const [appUpdateNotice, setAppUpdateNotice] =
    useState<AppUpdateNotice | null>(null);
  const [appUpdateNotificationRead, setAppUpdateNotificationRead] =
    useState(false);
  const [setup, setSetup] = useState<ProviderSetupState>(
    restoreProviderSetupState,
  );
  const providerInstallSessionRef = useRef<ProviderInstallSession | null>(null);
  const [notifications, setNotifications] = useState<Notification[]>([]);
  const [notificationsOpen, setNotificationsOpen] = useState(false);
  const [notificationsLoading, setNotificationsLoading] = useState(false);
  const [notificationsError, setNotificationsError] = useState<string | null>(
    null,
  );
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [queuedTerminalCommand, setQueuedTerminalCommand] =
    useState<TerminalCommandRequest | null>(null);
  const queuedTerminalCommandID = useRef(0);
  const [chartPaused, setChartPaused] = useState(false);
  const chartPausedRef = useRef(false);
  const statsStreamIDRef = useRef<string | null>(null);
  const lastProjectStatsFrameAtRef = useRef(0);
  const [statsStreamError, setStatsStreamError] = useState<string | null>(null);
  const [chartPoints, setChartPoints] = useState<DashboardChartPoint[]>([]);
  const [latestSamples, setLatestSamples] = useState<
    Record<string, StatsSample>
  >({});
  const latestSamplesRef = useRef<Record<string, StatsSample>>({});
  const [liveGPU, setLiveGPU] = useState<GPUMetrics | null>(null);
  const [containerSparks, setContainerSparks] = useState<
    Record<string, SparkPoint[]>
  >({});
  const [projectSparks, setProjectSparks] = useState<
    Record<string, SparkPoint[]>
  >({});
  const [projectMetricSparks, setProjectMetricSparks] =
    useState<ProjectMetricSparks>(() => emptyProjectMetricSparks());
  const themePreference = normalizeThemePreference(
    appSettings["general.theme"],
  );

  const navigate = useCallback((page: PageID) => {
    setActionError(null);
    setActivePage(page);
    setActiveContainerID(null);
    setContainerDetailTab("overview");
    setActiveNetworkID(null);
    setNetworkTab("overview");
    setSearch("");
  }, []);

  useEffect(() => {
    window.localStorage.setItem(
      "cairn.sidebar.collapsed",
      String(sidebarCollapsed),
    );
  }, [sidebarCollapsed]);

  useEffect(() => {
    const root = document.documentElement;
    const mediaQuery = window.matchMedia?.("(prefers-color-scheme: dark)");
    const applyTheme = () => {
      const resolvedTheme =
        themePreference === "system"
          ? mediaQuery?.matches
            ? "dark"
            : "light"
          : themePreference;
      root.dataset.theme = themePreference;
      root.style.colorScheme = resolvedTheme;
    };

    applyTheme();
    if (themePreference !== "system" || !mediaQuery) {
      return undefined;
    }

    if (mediaQuery.addEventListener) {
      mediaQuery.addEventListener("change", applyTheme);
    } else {
      mediaQuery.addListener(applyTheme);
    }
    return () => {
      if (mediaQuery.removeEventListener) {
        mediaQuery.removeEventListener("change", applyTheme);
      } else {
        mediaQuery.removeListener(applyTheme);
      }
    };
  }, [themePreference]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setPaletteOpen(true);
        return;
      }
      if (
        event.key === "/" &&
        !event.ctrlKey &&
        !event.metaKey &&
        !event.altKey &&
        !isEditableElement(event.target)
      ) {
        event.preventDefault();
        searchInputRef.current?.focus();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  const runPaletteCommand = useCallback(
    (command: string) => {
      queuedTerminalCommandID.current += 1;
      setQueuedTerminalCommand({
        id: queuedTerminalCommandID.current,
        command,
      });
      navigate("terminal");
    },
    [navigate],
  );

  const showContainers = useCallback(
    (filter: FilterID = "all") => {
      setContainerFilter(filter);
      navigate("containers");
    },
    [navigate],
  );

  const refreshProjects = useCallback(async () => {
    setProjectsStatus("loading");
    setProjectsError(null);
    try {
      const nextProjects = await ProjectService.RefreshProjects();
      setProjects(
        applyStatsSamplesToProjects(
          nextProjects ?? [],
          Object.values(latestSamplesRef.current),
        ),
      );
      setProjectsStatus("ready");
    } catch (error: unknown) {
      setProjectsError(
        error instanceof Error ? error.message : "Unable to refresh projects",
      );
      setProjectsStatus("error");
    }
  }, []);

  const refreshProjectDetail = useCallback(async (projectID: string) => {
    setProjectDetailStatus("loading");
    setProjectDetailError(null);
    try {
      const detail = await ProjectService.GetProject(projectID);
      if (!detail) {
        throw new Error("Project was not found");
      }
      setProjectDetail(
        applyStatsSamplesToProjectDetail(
          detail,
          Object.values(latestSamplesRef.current),
        ),
      );
      setProjectDetailStatus("ready");
    } catch (error: unknown) {
      setProjectDetail(null);
      setProjectDetailError(
        error instanceof Error ? error.message : "Unable to load project",
      );
      setProjectDetailStatus("error");
    }
  }, []);

  const refreshBackups = useCallback(async () => {
    setBackupsStatus("loading");
    setBackupsError(null);
    try {
      const nextBackups = await BackupService.ListBackups({ limit: 500 });
      setBackups(nextBackups ?? []);
      setBackupsStatus("ready");
    } catch (error: unknown) {
      setBackupsError(
        error instanceof Error ? error.message : "Unable to load backups",
      );
      setBackupsStatus("error");
    }
  }, []);

  const refreshUpdates = useCallback(async () => {
    setUpdatesStatus("loading");
    setUpdatesError(null);
    try {
      const nextUpdates = await UpdateService.ListCurrentUpdates({});
      setUpdates(nextUpdates ?? []);
      setUpdatesStatus("ready");
    } catch (error: unknown) {
      setUpdatesError(
        error instanceof Error ? error.message : "Unable to load updates",
      );
      setUpdatesStatus("error");
    }
  }, []);

  const refreshIgnoredUpdates = useCallback(async () => {
    setIgnoredUpdatesStatus("loading");
    setIgnoredUpdatesError(null);
    try {
      const nextUpdates = await UpdateService.ListCurrentUpdates({
        status: [UpdateStatus.UpdateStatusIgnored],
      });
      setIgnoredUpdates(nextUpdates ?? []);
      setIgnoredUpdatesStatus("ready");
    } catch (error: unknown) {
      setIgnoredUpdatesError(
        error instanceof Error
          ? error.message
          : "Unable to load ignored updates",
      );
      setIgnoredUpdatesStatus("error");
    }
  }, []);

  const refreshUpdateHistory = useCallback(async () => {
    setUpdateHistoryStatus("loading");
    setUpdateHistoryError(null);
    try {
      const nextHistory = await UpdateService.ListUpdateHistory({ limit: 200 });
      setUpdateHistory(nextHistory ?? []);
      setUpdateHistoryStatus("ready");
    } catch (error: unknown) {
      setUpdateHistoryError(
        error instanceof Error
          ? error.message
          : "Unable to load update history",
      );
      setUpdateHistoryStatus("error");
    }
  }, []);

  const refreshUpdateSurfaces = useCallback(async () => {
    await Promise.all([
      refreshUpdates(),
      refreshIgnoredUpdates(),
      refreshUpdateHistory(),
    ]);
  }, [refreshIgnoredUpdates, refreshUpdateHistory, refreshUpdates]);

  const refreshProjectLineage = useCallback(async (projectID: string) => {
    setProjectLineageStatus((current) => ({
      ...current,
      [projectID]: "loading",
    }));
    try {
      const rows = await ImageLineageService.GetProjectLineage(projectID);
      setProjectLineage((current) => ({ ...current, [projectID]: rows ?? [] }));
      setProjectLineageStatus((current) => ({
        ...current,
        [projectID]: "ready",
      }));
    } catch {
      setProjectLineage((current) => ({ ...current, [projectID]: [] }));
      setProjectLineageStatus((current) => ({
        ...current,
        [projectID]: "error",
      }));
    }
  }, []);

  const openProjectDetail = useCallback(
    (project: ProjectSummary) => {
      setActiveProjectID(project.id);
      setProjectTab("overview");
      void refreshProjectDetail(project.id);
      void refreshProjectLineage(project.id);
    },
    [refreshProjectDetail, refreshProjectLineage],
  );

  const closeProjectDetail = useCallback(() => {
    setActiveProjectID(null);
    setProjectDetail(null);
    setProjectDetailStatus("idle");
    setProjectDetailError(null);
  }, []);

  useEffect(() => {
    let active = true;
    setVersionLoading(true);

    getAppVersion()
      .then((nextVersion) => {
        if (active) {
          setVersion(nextVersion);
        }
      })
      .catch((error: unknown) => {
        if (active) {
          setVersion({
            version: frontendVersion,
            goVersion: "Unavailable",
          });
          setVersionError(
            error instanceof Error
              ? error.message
              : "Unable to load app version",
          );
        }
      })
      .finally(() => {
        if (active) {
          setVersionLoading(false);
        }
      });

    return () => {
      active = false;
    };
  }, [setVersion, setVersionError, setVersionLoading]);

  useEffect(() => {
    let active = true;
    SettingsService.GetSettings()
      .then((settings) => {
        if (!active) {
          return;
        }
        const nextSettings = settings ?? {};
        setAppSettings(nextSettings);
        setPermissionMode(
          normalizePermissionMode(nextSettings["linux.sudo_mode"]),
        );
        setWSLDistro(
          normalizeStringSetting(nextSettings["windows.wsl_distro"], "Ubuntu"),
        );
        setColimaProfile(
          normalizeStringSetting(
            nextSettings["macos.colima_profile"],
            "default",
          ),
        );
        setColimaCPU(normalizeIntSetting(nextSettings["macos.colima_cpu"], 2));
        setColimaMemoryGB(
          normalizeIntSetting(nextSettings["macos.colima_memory_gb"], 4),
        );
        setColimaDiskGB(
          normalizeIntSetting(nextSettings["macos.colima_disk_gb"], 60),
        );
        setProviderAutostart(
          normalizeBoolSetting(
            nextSettings["provider.autostart_backend"],
            true,
          ),
        );
        setSettingsLoaded(true);
      })
      .catch(() => {
        if (active) {
          setAppSettings({});
          setPermissionMode("ask");
          setWSLDistro("Ubuntu");
          setColimaProfile("default");
          setColimaCPU(2);
          setColimaMemoryGB(4);
          setColimaDiskGB(60);
          setProviderAutostart(true);
          setSettingsLoaded(true);
        }
      });
    return () => {
      active = false;
    };
  }, []);

  useEffect(() => {
    const off = Events.On("provider:install:progress", (event) => {
      const payload = eventPayload<ProviderInstallProgressPayload>(event);
      const session = providerInstallSessionRef.current;
      if (!payload || !session) {
        return;
      }
      const matchesPlan = payload.planID === session.planID;
      const matchesStream = Boolean(
        session.streamID && payload.streamID === session.streamID,
      );
      if (!matchesPlan && !matchesStream) {
        return;
      }

      providerInstallSessionRef.current = {
        ...session,
        streamID: payload.streamID || session.streamID,
      };
      setSetup((current) => {
        if (
          current.plan?.planID !== payload.planID &&
          current.installStreamID !== payload.streamID
        ) {
          return current;
        }
        const alreadyRecorded = current.progress.some(
          (entry) =>
            entry.streamID === payload.streamID &&
            entry.step === payload.step &&
            entry.message === payload.message &&
            entry.done === payload.done &&
            entry.error === payload.error,
        );
        return {
          ...current,
          installStreamID: payload.streamID || current.installStreamID,
          step: payload.done && !payload.error ? "verify" : current.step,
          installing: !payload.done,
          error: current.error,
          progress: alreadyRecorded
            ? current.progress
            : current.progress.concat(payload),
        };
      });

      if (payload.done) {
        providerInstallSessionRef.current = null;
      }
      if (payload.done && !payload.error) {
        const providerID = providerIDForSetupBackend(session.backend);
        if (providerID) {
          void ProviderService.Detect(providerID)
            .then((status) => {
              setSetup((current) => ({
                ...current,
                detection: status ?? current.detection,
                detectError: undefined,
              }));
            })
            .catch((error: unknown) => {
              setSetup((current) => ({
                ...current,
                detectError:
                  error instanceof Error
                    ? error.message
                    : "Provider verification failed",
              }));
            });
        }
        void refreshInventory();
        void refreshProjects();
      }
    });
    return () => off();
  }, [refreshInventory, refreshProjects]);

  const refreshNotifications = useCallback(async () => {
    setNotificationsLoading(true);
    setNotificationsError(null);
    try {
      const nextNotifications = await SettingsService.GetNotifications(false);
      setNotifications(nextNotifications ?? []);
    } catch (error: unknown) {
      setNotificationsError(
        error instanceof Error ? error.message : "Unable to load notifications",
      );
    } finally {
      setNotificationsLoading(false);
    }
  }, []);

  useEffect(() => {
    void refreshNotifications();
  }, [refreshNotifications]);

  useEffect(() => {
    const off = Events.On("notification", (event) => {
      const notification = eventPayload<Notification>(event);
      if (!notification?.id) {
        void refreshNotifications();
        return;
      }
      setNotifications((current) => [
        notification,
        ...current.filter((item) => item.id !== notification.id),
      ]);
    });
    return () => off();
  }, [refreshNotifications]);

  const markAllNotificationsRead = useCallback(async () => {
    setNotificationsLoading(true);
    setNotificationsError(null);
    setAppUpdateNotificationRead(true);
    try {
      await SettingsService.MarkNotificationsRead([]);
      setNotifications((current) =>
        current.map((notification) => ({ ...notification, read: true })),
      );
      await refreshNotifications();
    } catch (error: unknown) {
      setNotificationsError(
        error instanceof Error
          ? error.message
          : "Unable to mark notifications read",
      );
    } finally {
      setNotificationsLoading(false);
    }
  }, [refreshNotifications]);

  const refreshAuditLog = useCallback(async () => {
    setAuditLoading(true);
    setAuditError(null);
    try {
      const entries = await SettingsService.GetAuditLog({
        topic: auditFilter.action.trim(),
        limit: 500,
      });
      setAuditEntries(entries ?? []);
    } catch (error: unknown) {
      setAuditError(
        error instanceof Error ? error.message : "Unable to load audit log",
      );
    } finally {
      setAuditLoading(false);
    }
  }, [auditFilter.action]);

  useEffect(() => {
    if (activePage === "settings") {
      void refreshAuditLog();
    }
  }, [activePage, refreshAuditLog]);

  useEffect(() => {
    if (
      !settingsLoaded ||
      !version?.version ||
      !normalizeBoolSetting(appSettings["updates.notify"], true)
    ) {
      return undefined;
    }
    let active = true;
    SettingsService.CheckAppUpdate(version.version)
      .then((notice) => {
        if (!active) {
          return;
        }
        setAppUpdateNotice(notice);
        if (notice) {
          setAppUpdateNotificationRead(false);
        }
      })
      .catch(() => {
        if (active) {
          setAppUpdateNotice(null);
        }
      });
    return () => {
      active = false;
    };
  }, [appSettings, settingsLoaded, version?.version]);

  useEffect(() => {
    void refreshInventory();
  }, [refreshInventory]);

  useEffect(() => {
    void refreshProjects();
  }, [refreshProjects]);

  useEffect(() => {
    void refreshBackups();
  }, [refreshBackups]);

  useEffect(() => {
    void refreshUpdateSurfaces();
  }, [refreshUpdateSurfaces]);

  const refreshRuntimeSurfaces = useCallback(() => {
    void refreshInventory();
    void refreshProjects();
    void refreshBackups();
    void refreshUpdateSurfaces();
    setDashboardRefreshToken((current) => current + 1);
    if (activeProjectID) {
      void refreshProjectDetail(activeProjectID);
      void refreshProjectLineage(activeProjectID);
    }
  }, [
    activeProjectID,
    refreshBackups,
    refreshInventory,
    refreshProjectDetail,
    refreshProjectLineage,
    refreshProjects,
    refreshUpdateSurfaces,
  ]);

  const refreshRuntimeSurfacesForObjects = useCallback(
    (event: unknown) => {
      const payload = eventPayload<ObjectsChangedEventPayload>(event);
      const kind = payload?.kind?.trim().toLowerCase();
      const ids = Array.isArray(payload?.ids) ? payload.ids : [];
      if (!kind) {
        refreshRuntimeSurfaces();
        return;
      }

      setDashboardRefreshToken((current) => current + 1);
      switch (kind) {
        case "container":
          void refreshContainers();
          void refreshProjects();
          if (activeProjectID) {
            void refreshProjectDetail(activeProjectID);
          }
          break;
        case "image":
          void refreshImages();
          void refreshUpdateSurfaces();
          if (activeProjectID) {
            void refreshProjectLineage(activeProjectID);
          }
          break;
        case "volume":
          void refreshVolumes();
          void refreshBackups();
          break;
        case "network":
          void refreshNetworks();
          break;
        case "project":
          void refreshInventory();
          void refreshProjects();
          void refreshUpdateSurfaces();
          if (
            activeProjectID &&
            (ids.length === 0 || ids.includes(activeProjectID))
          ) {
            void refreshProjectDetail(activeProjectID);
            void refreshProjectLineage(activeProjectID);
          }
          break;
        default:
          refreshRuntimeSurfaces();
      }
    },
    [
      activeProjectID,
      refreshBackups,
      refreshContainers,
      refreshImages,
      refreshInventory,
      refreshNetworks,
      refreshProjectDetail,
      refreshProjectLineage,
      refreshProjects,
      refreshRuntimeSurfaces,
      refreshUpdateSurfaces,
      refreshVolumes,
    ],
  );

  useDebouncedRuntimeEvent(
    "objects:changed",
    500,
    refreshRuntimeSurfacesForObjects,
  );
  useDebouncedRuntimeEvent("provider:changed", 250, refreshRuntimeSurfaces);

  useEffect(() => {
    const offCheck = Events.On("updates:check:progress", (event) => {
      const payload = eventPayload<
        UpdateProgressEntry & {
          done?: number;
          total?: number;
          current?: string;
        }
      >(event);
      if (!payload) {
        return;
      }
      setUpdateCheckJobID(payload.jobID ?? null);
      setUpdateCheckProgress({
        jobID: payload.jobID,
        phase: "check",
        message:
          payload.current ??
          (payload.done && payload.total
            ? `${payload.done}/${payload.total} checked`
            : "Checking updates"),
        pct:
          payload.done && payload.total
            ? Math.round((payload.done / payload.total) * 100)
            : undefined,
      });
      if (payload.done && payload.total && payload.done >= payload.total) {
        setLastUpdateCheckAt(Date.now());
        window.setTimeout(() => setUpdateCheckProgress(null), 1200);
        void refreshUpdateSurfaces();
      }
    });
    const offApplied = Events.On("updates:applied", () => {
      void refreshUpdateSurfaces();
      void refreshProjects();
      if (activeProjectID) {
        void refreshProjectDetail(activeProjectID);
        void refreshProjectLineage(activeProjectID);
      }
    });
    const offJobProgress = Events.On("job:progress", (event) => {
      const payload = eventPayload<ProjectJobEvent>(event);
      if (!payload?.jobID) {
        return;
      }
      if (payload.projectID) {
        setProjectCommandOutputs((current) =>
          appendProjectCommandProgress(current, payload),
        );
      }
      setUpdatePlan((current) => {
        if (!current.jobID || current.jobID !== payload.jobID) {
          return current;
        }
        return {
          ...current,
          progress: current.progress.concat(payload),
        };
      });
    });
    const offJobDone = Events.On("job:done", (event) => {
      const payload = eventPayload<ProjectJobEvent>(event);
      if (!payload?.jobID) {
        return;
      }
      if (payload.projectID) {
        setProjectCommandOutputs((current) =>
          appendProjectCommandDone(current, payload),
        );
      }
      setUpdatePlan((current) => {
        if (!current.jobID || current.jobID !== payload.jobID) {
          return current;
        }
        return {
          ...current,
          applying: false,
          busy: false,
          result: payload.result ?? payload.message ?? "done",
          progress: current.progress.concat(payload),
        };
      });
      void refreshUpdateSurfaces();
      void refreshProjects();
      if (activeProjectID) {
        void refreshProjectDetail(activeProjectID);
        void refreshProjectLineage(activeProjectID);
      }
    });
    return () => {
      offCheck();
      offApplied();
      offJobProgress();
      offJobDone();
    };
  }, [
    activeProjectID,
    refreshProjectDetail,
    refreshProjectLineage,
    refreshProjects,
    refreshUpdateSurfaces,
  ]);

  useEffect(() => {
    const query = pullImage.query.trim();
    if (!pullImage.open || query.length < 3) {
      setPullImage((current) => ({
        ...current,
        results: [],
        loadingResults: false,
        searchError: undefined,
      }));
      return undefined;
    }
    const timer = window.setTimeout(() => {
      setPullImage((current) => ({
        ...current,
        loadingResults: true,
        searchError: undefined,
      }));
      DockerService.SearchHub(query, 10)
        .then((results) => {
          setPullImage((current) => ({
            ...current,
            loadingResults: false,
            results,
            searchError: undefined,
          }));
        })
        .catch((error: unknown) => {
          setPullImage((current) => ({
            ...current,
            loadingResults: false,
            results: [],
            searchError:
              error instanceof Error
                ? error.message
                : "Docker Hub search is offline",
          }));
        });
    }, 300);
    return () => window.clearTimeout(timer);
  }, [pullImage.open, pullImage.query]);

  useEffect(() => {
    const off = Events.On("image:push:progress", (event) => {
      const payload = eventPayload<ImageProgressPayload>(event);
      if (!payload?.streamID) {
        return;
      }
      setPushImage((current) => {
        if (current.streamID && current.streamID !== payload.streamID) {
          return current;
        }
        if (!current.streamID && !current.busy) {
          return current;
        }
        return {
          ...current,
          streamID: current.streamID ?? payload.streamID,
          progress: mergeImageProgress(current.progress, payload),
          success: current.success || payload.status === "done",
        };
      });
    });
    return () => off();
  }, []);

  useEffect(() => {
    const query = runImage.hubQuery.trim();
    if (!runImage.open || query.length < 3) {
      setRunImage((current) => ({
        ...current,
        hubResults: [],
        hubLoading: false,
        hubError: undefined,
      }));
      return undefined;
    }
    const timer = window.setTimeout(() => {
      setRunImage((current) => ({
        ...current,
        hubLoading: true,
        hubError: undefined,
      }));
      DockerService.SearchHub(query, 10)
        .then((results) => {
          setRunImage((current) => ({
            ...current,
            hubLoading: false,
            hubResults: results,
            hubError: undefined,
          }));
        })
        .catch((error: unknown) => {
          setRunImage((current) => ({
            ...current,
            hubLoading: false,
            hubResults: [],
            hubError:
              error instanceof Error
                ? error.message
                : "Docker Hub search is offline",
          }));
        });
    }, 300);
    return () => window.clearTimeout(timer);
  }, [runImage.hubQuery, runImage.open]);

  const activeProvider = useMemo(
    () => activeProviderSummary(providers),
    [providers],
  );
  const runningContainers = containers.filter(
    (container) => container.state === "running",
  ).length;
  const unhealthyContainers = containers.filter(
    (container) => container.health === "unhealthy",
  ).length;
  const diskTotal = diskUsage?.totalBytes ?? 0;
  const diskReclaimable = diskUsage?.reclaimable ?? 0;
  const versionLabel = version?.version
    ? `v${version.version}`
    : versionLoading
      ? "Version loading"
      : "Version unavailable";
  const pageTitle =
    navItems.find((item) => item.id === activePage)?.label ?? "Overview";
  const providerStatus = activeProvider?.status;
  const providerProblems = providerStatus?.problems ?? [];
  const providerWarnings = providerStatus?.warnings ?? [];
  const permissionProblem =
    providerProblems.find((problem) => problem.code === "PERM_SOCKET") ?? null;
  const providerRepairNeeded = providerProblems.length > 0;
  const dockerRunning =
    !inventoryError &&
    Boolean(dockerInfo || dockerVersion || providerStatus?.dockerRunning);
  const noProviderConfigured =
    inventoryStatus !== "loading" && providers.length === 0;
  const dockerStopped =
    Boolean(activeProvider && providerStatus?.installed) &&
    !dockerRunning &&
    !providerRepairNeeded;
  const staleMode =
    !dockerRunning &&
    Boolean(lastLoadedAt) &&
    containers.length +
      images.length +
      networks.length +
      projects.length +
      volumes.length >
      0;
  const mutationsDisabled = !dockerRunning;
  const mutationDisabledReason = providerRepairNeeded
    ? "Repair the Docker provider before running Docker actions"
    : dockerStopped
      ? "Start Docker Engine before running Docker actions"
      : noProviderConfigured
        ? "Set up a Docker provider before running Docker actions"
        : "Docker is not reachable";
  const providerName = activeProvider?.name ?? "No provider selected";
  const statusLabel = dockerRunning
    ? "Running"
    : providerRepairNeeded
      ? "Error"
      : "Stopped";
  const statusTone: StatusToneID = dockerRunning
    ? "ok"
    : providerRepairNeeded
      ? "error"
      : "neutral";

  useEffect(() => {
    chartPausedRef.current = chartPaused;
  }, [chartPaused]);

  useEffect(() => {
    const off = Events.On("stats:sample", (event) => {
      const payload = eventPayload<StatsSamplePayload>(event);
      if (!payload || payload.streamID !== statsStreamIDRef.current) {
        return;
      }
      if (payload.gpu) {
        setLiveGPU(payload.gpu);
      }
      const samples = (payload.samples ?? []).filter(isStatsSample);
      if (samples.length === 0) {
        return;
      }
      const label = sampleLabel(samples[0]);
      const receivedAt = Date.now();
      const shouldUpdateProjectFrame =
        receivedAt - lastProjectStatsFrameAtRef.current >= projectStatsFrameMs;
      const mergedSamples = mergeStatsSamples(
        latestSamplesRef.current,
        samples,
      );
      const allSamples = Object.values(mergedSamples);
      latestSamplesRef.current = mergedSamples;
      useInventoryStore.setState((current) => ({
        containers: applyStatsSamplesToContainers(current.containers, samples),
      }));
      setLatestSamples(mergedSamples);
      setContainerSparks((current) =>
        appendSparkEntries(
          current,
          samples.map((sample) => ({
            id: sample.containerID,
            label,
            value: sample.cpuPercent,
          })),
        ),
      );
      if (shouldUpdateProjectFrame) {
        lastProjectStatsFrameAtRef.current = receivedAt;
        setProjects((current) =>
          applyStatsSamplesToProjects(current, allSamples),
        );
        setProjectDetail((current) =>
          current
            ? applyStatsSamplesToProjectDetail(current, allSamples)
            : current,
        );
        setProjectSparks((current) =>
          appendSparkEntries(current, projectSparkEntries(allSamples, label)),
        );
        setProjectMetricSparks((current) =>
          appendProjectMetricSparkEntries(current, allSamples, label),
        );
        if (!chartPausedRef.current) {
          setChartPoints((current) =>
            trimChartPoints(
              current.concat(aggregateChartPoint(allSamples, label)),
            ),
          );
        }
      }
    });
    return () => off();
  }, []);

  useEffect(() => {
    if (!dockerRunning) {
      statsStreamIDRef.current = null;
      lastProjectStatsFrameAtRef.current = 0;
      setStatsStreamError(null);
      latestSamplesRef.current = {};
      setLatestSamples({});
      setContainerSparks({});
      setProjectSparks({});
      setProjectMetricSparks(emptyProjectMetricSparks());
      setChartPoints([]);
      setLiveGPU(null);
      return undefined;
    }
    let cancelled = false;
    let activeStreamID: string | null = null;
    MetricsService.StartStatsStream({ kind: "all", ids: [] })
      .then((streamID) => {
        if (cancelled) {
          void MetricsService.StopStream(streamID);
          return;
        }
        activeStreamID = streamID;
        statsStreamIDRef.current = streamID;
        setStatsStreamError(null);
      })
      .catch((error: unknown) => {
        setStatsStreamError(
          error instanceof Error ? error.message : "Unable to start metrics",
        );
      });
    return () => {
      cancelled = true;
      statsStreamIDRef.current = null;
      if (activeStreamID) {
        void MetricsService.StopStream(activeStreamID);
      }
    };
  }, [dockerRunning]);

  const appUpdateNotification = useMemo<Notification | null>(() => {
    if (!appUpdateNotice || appUpdateNotificationRead) {
      return null;
    }
    return {
      id: -1,
      level: "info",
      title: `Cairn ${appUpdateNotice.version} is available`,
      body: "A new desktop app release is ready to download.",
      topic: "app-update",
      read: false,
      createdAt: appUpdateNotice.publishedAt || new Date().toISOString(),
    };
  }, [appUpdateNotice, appUpdateNotificationRead]);
  const notificationsForDisplay = useMemo(
    () =>
      appUpdateNotification
        ? [appUpdateNotification, ...notifications]
        : notifications,
    [appUpdateNotification, notifications],
  );
  const unreadNotifications = notificationsForDisplay.filter(
    (notification) => !notification.read,
  ).length;
  const visibleAuditEntries = useMemo(
    () => filterAuditEntries(auditEntries, auditFilter),
    [auditEntries, auditFilter],
  );

  const imageUseCounts = useMemo(
    () => imageUsageCounts(containers),
    [containers],
  );

  const setActionBusy = useCallback((key: string, busy: boolean) => {
    setBusyActionIDs((current) => {
      const next = new Set(current);
      if (busy) {
        next.add(key);
      } else {
        next.delete(key);
      }
      return next;
    });
  }, []);

  const setRegistryBusy = useCallback((key: string, busy: boolean) => {
    setRegistryBusyKeys((current) => {
      const next = new Set(current);
      if (busy) {
        next.add(key);
      } else {
        next.delete(key);
      }
      return next;
    });
  }, []);

  const refreshAfterAction = useCallback(async () => {
    await refreshInventory();
  }, [refreshInventory]);

  const changeProjectView = useCallback((view: ProjectViewMode) => {
    setProjectView(view);
    window.localStorage.setItem("cairn.projects.view", view);
  }, []);

  const retryProviderDetection = useCallback(async () => {
    setProviderActionBusy(true);
    setActionError(null);
    try {
      if (activeProvider?.id) {
        await ProviderService.Detect(activeProvider.id);
      }
      await refreshInventory();
      await refreshProjects();
    } catch (error: unknown) {
      setActionError(
        error instanceof Error ? error.message : "Provider detection failed",
      );
    } finally {
      setProviderActionBusy(false);
    }
  }, [activeProvider?.id, refreshInventory, refreshProjects]);

  const startProvider = useCallback(async () => {
    if (!activeProvider?.id) {
      setRepairOpen(true);
      return;
    }
    setProviderActionBusy(true);
    setActionError(null);
    try {
      await ProviderService.Start(activeProvider.id);
      await ProviderService.Detect(activeProvider.id);
      await refreshInventory();
      await refreshProjects();
    } catch (error: unknown) {
      setActionError(
        error instanceof Error ? error.message : "Unable to start Docker",
      );
    } finally {
      setProviderActionBusy(false);
    }
  }, [activeProvider?.id, refreshInventory, refreshProjects]);

  const restartProvider = useCallback(async () => {
    if (!activeProvider?.id) {
      setRepairOpen(true);
      return;
    }
    setProviderActionBusy(true);
    setActionError(null);
    try {
      const plan = await ProviderService.PlanRestart(activeProvider.id);
      setConfirm({
        ...emptyConfirm,
        open: true,
        plan,
        planKind: "provider",
        targetName: activeProvider.name,
      });
    } catch (error: unknown) {
      setActionError(
        error instanceof Error
          ? error.message
          : "Unable to plan Docker restart",
      );
    } finally {
      setProviderActionBusy(false);
    }
  }, [activeProvider?.id, activeProvider?.name]);

  const savePermissionMode = useCallback(async () => {
    setRepairSaving(true);
    setRepairError(null);
    try {
      await SettingsService.SetSetting("linux.sudo_mode", permissionMode);
      setRepairOpen(false);
      await retryProviderDetection();
    } catch (error: unknown) {
      setRepairError(
        error instanceof Error
          ? error.message
          : "Unable to save permission mode",
      );
    } finally {
      setRepairSaving(false);
    }
  }, [permissionMode, retryProviderDetection]);

  const saveSetting = useCallback(
    async (key: string, value: unknown) => {
      setSettingsSaving(true);
      setSettingsError(null);
      setSettingsMessage(null);
      try {
        await SettingsService.SetSetting(key, value);
        setAppSettings((current) => ({ ...current, [key]: value }));
        if (key === "linux.sudo_mode") {
          setPermissionMode(normalizePermissionMode(value));
        }
        setSettingsMessage("Setting saved");
        pushToast({
          body: "Your preference was saved.",
          level: "ok",
          title: "Setting saved",
        });
        if (key === "windows.wsl_distro") {
          await ProviderService.SetActiveProvider(windowsWSLProviderID);
          await refreshInventory();
          await refreshProjects();
          await refreshUpdateSurfaces();
        }
        if (key === "linux.sudo_mode" || key === "linux.socket_path") {
          if (activeProvider?.id === linuxNativeProviderID) {
            await ProviderService.SetActiveProvider(linuxNativeProviderID);
          } else {
            await ProviderService.Detect(linuxNativeProviderID).catch(
              () => null,
            );
          }
          await refreshInventory();
          await refreshProjects();
          await refreshUpdateSurfaces();
        }
        if (String(key).startsWith("macos.colima_")) {
          if (activeProvider?.id === macOSColimaProviderID) {
            await ProviderService.SetActiveProvider(macOSColimaProviderID);
          } else {
            await ProviderService.Detect(macOSColimaProviderID).catch(
              () => null,
            );
          }
          await refreshInventory();
          await refreshProjects();
          await refreshUpdateSurfaces();
        }
      } catch (error: unknown) {
        const message =
          error instanceof Error ? error.message : "Unable to save setting";
        setSettingsError(message);
        pushToast({
          body: message,
          level: "error",
          title: "Setting failed",
        });
      } finally {
        setSettingsSaving(false);
      }
    },
    [
      activeProvider?.id,
      pushToast,
      refreshInventory,
      refreshProjects,
      refreshUpdateSurfaces,
    ],
  );

  const saveWSLDistro = useCallback(async () => {
    const nextDistro = wslDistro.trim() || "Ubuntu";
    setWSLDistro(nextDistro);
    await saveSetting("windows.wsl_distro", nextDistro);
  }, [saveSetting, wslDistro]);

  const selectWSLDistro = useCallback(
    async (distro: string) => {
      const nextDistro = distro.trim() || "Ubuntu";
      setWSLDistro(nextDistro);
      await saveSetting("windows.wsl_distro", nextDistro);
    },
    [saveSetting],
  );

  const saveColimaProfile = useCallback(async () => {
    const nextProfile = colimaProfile.trim() || "default";
    setColimaProfile(nextProfile);
    await saveSetting("macos.colima_profile", nextProfile);
  }, [colimaProfile, saveSetting]);

  const saveColimaNumberSetting = useCallback(
    async (
      key:
        | "macos.colima_cpu"
        | "macos.colima_memory_gb"
        | "macos.colima_disk_gb",
      value: number,
      setter: (value: number) => void,
      fallback: number,
    ) => {
      const nextValue = Number.isFinite(value) && value > 0 ? value : fallback;
      setter(nextValue);
      await saveSetting(key, nextValue);
    },
    [saveSetting],
  );

  const refreshDockerContexts = useCallback(async () => {
    setDockerContextsLoading(true);
    setDockerContextsError(null);
    try {
      const contexts = await ProviderService.ListDockerContexts();
      setDockerContexts(contexts ?? []);
    } catch (error: unknown) {
      setDockerContexts([]);
      setDockerContextsError(
        error instanceof Error
          ? error.message
          : "Unable to list Docker contexts",
      );
    } finally {
      setDockerContextsLoading(false);
    }
  }, []);

  const refreshWSLDistros = useCallback(async () => {
    setWSLDistrosLoading(true);
    setWSLDistrosError(null);
    try {
      const distros = await ProviderService.ListWSLDistros();
      setWSLDistros(distros ?? []);
    } catch (error: unknown) {
      setWSLDistros([]);
      setWSLDistrosError(
        error instanceof Error ? error.message : "Unable to list WSL distros",
      );
    } finally {
      setWSLDistrosLoading(false);
    }
  }, []);

  const refreshRegistryAccounts = useCallback(async () => {
    setRegistryAccountsStatus((current) =>
      current === "ready" ? current : "loading",
    );
    setRegistryAccountsError(null);
    try {
      const accounts = await RegistryService.ListRegistryAccounts();
      setRegistryAccounts(accounts ?? []);
      setRegistryAccountsStatus("ready");
    } catch (error: unknown) {
      setRegistryAccounts([]);
      setRegistryAccountsStatus("error");
      setRegistryAccountsError(
        error instanceof Error
          ? error.message
          : "Unable to list registry accounts",
      );
    }
  }, []);

  const openRegistryLogin = useCallback((registry = "docker.io") => {
    setRegistryLogin({
      ...emptyRegistryLogin,
      open: true,
      registry: registry.trim() || "docker.io",
    });
  }, []);

  const submitRegistryLogin = useCallback(async () => {
    setRegistryLogin((current) => ({
      ...current,
      busy: true,
      error: undefined,
    }));
    try {
      await RegistryService.Login({
        registry: registryLogin.registry,
        username: registryLogin.username,
        secret: registryLogin.secret,
        secretKind: registryLogin.secretKind,
      });
      setRegistryLogin(emptyRegistryLogin);
      await refreshRegistryAccounts();
    } catch (error: unknown) {
      setRegistryLogin((current) => ({
        ...current,
        busy: false,
        error:
          error instanceof Error ? error.message : "Unable to log in registry",
      }));
    }
  }, [
    refreshRegistryAccounts,
    registryLogin.registry,
    registryLogin.secret,
    registryLogin.secretKind,
    registryLogin.username,
  ]);

  const testRegistryAuth = useCallback(
    async (registry: string) => {
      const key = `test:${registry}`;
      setRegistryBusy(key, true);
      try {
        const status = await RegistryService.TestAuth(registry);
        if (status) {
          setRegistryStatuses((current) => ({
            ...current,
            [normalizeRegistryHostForUI(status.registry || registry)]: status,
          }));
        }
      } catch (error: unknown) {
        setRegistryStatuses((current) => ({
          ...current,
          [normalizeRegistryHostForUI(registry)]: {
            registry,
            loggedIn: false,
            error:
              error instanceof Error ? error.message : "Registry auth failed",
          },
        }));
      } finally {
        setRegistryBusy(key, false);
      }
    },
    [setRegistryBusy],
  );

  const logoutRegistry = useCallback(
    async (registry: string) => {
      const key = `logout:${registry}`;
      setRegistryBusy(key, true);
      try {
        await RegistryService.Logout(registry);
        setRegistryStatuses((current) => {
          const next = { ...current };
          delete next[normalizeRegistryHostForUI(registry)];
          return next;
        });
        await refreshRegistryAccounts();
      } catch (error: unknown) {
        setRegistryAccountsError(
          error instanceof Error ? error.message : "Unable to log out registry",
        );
      } finally {
        setRegistryBusy(key, false);
      }
    },
    [refreshRegistryAccounts, setRegistryBusy],
  );

  const activateDockerContext = useCallback(
    async (name: string) => {
      setSettingsSaving(true);
      setSettingsError(null);
      setSettingsMessage(null);
      try {
        await ProviderService.SetDockerContext(name);
        setSettingsMessage(`Using Docker context ${name}`);
        pushToast({
          body: `Using Docker context ${name}.`,
          level: "ok",
          title: "Docker context saved",
        });
        await refreshDockerContexts();
        await refreshInventory();
        await refreshProjects();
      } catch (error: unknown) {
        const message =
          error instanceof Error
            ? error.message
            : "Unable to use Docker context";
        setSettingsError(message);
        pushToast({
          body: message,
          level: "error",
          title: "Docker context failed",
        });
      } finally {
        setSettingsSaving(false);
      }
    },
    [pushToast, refreshDockerContexts, refreshInventory, refreshProjects],
  );

  const changeProviderAutostart = useCallback(
    (enabled: boolean) => {
      setProviderAutostart(enabled);
      void saveSetting("provider.autostart_backend", enabled);
    },
    [saveSetting],
  );

  useEffect(() => {
    if (activePage === "settings") {
      void refreshDockerContexts();
      void refreshWSLDistros();
    }
  }, [activePage, refreshDockerContexts, refreshWSLDistros]);

  useEffect(() => {
    RegistryService.KnownRegistries()
      .then((presets) => setRegistryPresets(presets ?? []))
      .catch(() => setRegistryPresets([]));
  }, []);

  useEffect(() => {
    if (activePage === "settings" || pushImage.open) {
      void refreshRegistryAccounts();
    }
  }, [activePage, pushImage.open, refreshRegistryAccounts]);

  useEffect(() => {
    persistProviderSetupState(setup);
  }, [setup]);

  const openProviderSetup = useCallback(() => {
    const platform =
      setupPlatformFromProvider(activeProvider) ?? detectClientSetupPlatform();
    const backend = recommendedSetupBackend(platform);
    setSetup({
      ...emptyProviderSetup,
      open: true,
      platform,
      backend,
      distro: wslDistro.trim() || "Ubuntu",
      colimaProfile: colimaProfile.trim() || "default",
      colimaCPU,
      colimaMemoryGB,
      colimaDiskGB,
    });
  }, [
    activeProvider,
    colimaCPU,
    colimaDiskGB,
    colimaMemoryGB,
    colimaProfile,
    wslDistro,
  ]);

  const openProviderSetupForRepair = useCallback(() => {
    const platform =
      setupPlatformFromProvider(activeProvider) ?? detectClientSetupPlatform();
    const backend =
      activeProvider?.kind === "linux_native" ||
      activeProvider?.kind === "macos_colima" ||
      activeProvider?.kind === "windows_wsl_ubuntu"
        ? activeProvider.kind
        : recommendedSetupBackend(platform);
    setRepairOpen(false);
    setSetup({
      ...emptyProviderSetup,
      open: true,
      step: "checks",
      platform: setupPlatformForBackend(backend, platform),
      backend,
      distro: wslDistro.trim() || "Ubuntu",
      colimaProfile: colimaProfile.trim() || "default",
      colimaCPU,
      colimaMemoryGB,
      colimaDiskGB,
    });
  }, [
    activeProvider,
    colimaCPU,
    colimaDiskGB,
    colimaMemoryGB,
    colimaProfile,
    wslDistro,
  ]);

  const closeProviderSetup = useCallback(() => {
    providerInstallSessionRef.current = null;
    window.localStorage.removeItem(providerSetupStorageKey);
    setSetup(emptyProviderSetup);
  }, []);

  const finishProviderSetup = useCallback(() => {
    providerInstallSessionRef.current = null;
    window.localStorage.removeItem(providerSetupStorageKey);
    setSetup(emptyProviderSetup);
    navigate("overview");
  }, [navigate]);

  const changeSetupBackend = useCallback((backend: SetupBackendID) => {
    setSetup((current) => ({
      ...current,
      backend,
      platform: setupPlatformForBackend(backend, current.platform),
      step: "checks",
      detection: null,
      detectError: undefined,
      plan: null,
      progress: [],
      detectedProjects: [],
      selectedProjectIDs: [],
      projectDetectError: undefined,
      error: undefined,
    }));
  }, []);

  const useExistingDockerContext = useCallback(() => {
    changeSetupBackend("existing_context");
  }, [changeSetupBackend]);

  const openDockerContextsSettings = useCallback(() => {
    closeProviderSetup();
    setSettingsSection("contexts");
    navigate("settings");
    void refreshDockerContexts();
  }, [closeProviderSetup, navigate, refreshDockerContexts]);

  const runProviderSetupChecks = useCallback(async () => {
    const providerID = providerIDForSetupBackend(setup.backend);
    if (!providerID) {
      setSetup((current) => ({ ...current, step: "checks" }));
      return;
    }
    const distro = setup.distro.trim() || "Ubuntu";
    const profile = setup.colimaProfile.trim() || "default";
    setSetup((current) => ({
      ...current,
      distro,
      colimaProfile: profile,
      step: "checks",
      detecting: true,
      detectError: undefined,
      detection: null,
      plan: null,
      progress: [],
      error: undefined,
    }));
    try {
      if (setup.backend === "macos_colima") {
        await SettingsService.SetSetting("macos.colima_profile", profile);
        await SettingsService.SetSetting("macos.colima_cpu", setup.colimaCPU);
        await SettingsService.SetSetting(
          "macos.colima_memory_gb",
          setup.colimaMemoryGB,
        );
        await SettingsService.SetSetting(
          "macos.colima_disk_gb",
          setup.colimaDiskGB,
        );
        setColimaProfile(profile);
        setColimaCPU(setup.colimaCPU);
        setColimaMemoryGB(setup.colimaMemoryGB);
        setColimaDiskGB(setup.colimaDiskGB);
        setAppSettings((current) => ({
          ...current,
          "macos.colima_profile": profile,
          "macos.colima_cpu": setup.colimaCPU,
          "macos.colima_memory_gb": setup.colimaMemoryGB,
          "macos.colima_disk_gb": setup.colimaDiskGB,
        }));
      } else if (setup.backend === "windows_wsl_ubuntu") {
        await SettingsService.SetSetting("windows.wsl_distro", distro);
        setWSLDistro(distro);
        setAppSettings((current) => ({
          ...current,
          "windows.wsl_distro": distro,
        }));
      }
      const status = await ProviderService.Detect(providerID);
      setSetup((current) => ({
        ...current,
        detection: status ?? null,
        detecting: false,
        step: status?.healthy ? "verify" : "checks",
      }));
      await refreshInventory();
    } catch (error: unknown) {
      setSetup((current) => ({
        ...current,
        detecting: false,
        detectError:
          error instanceof Error ? error.message : "Provider checks failed",
      }));
    }
  }, [
    refreshInventory,
    setup.backend,
    setup.colimaCPU,
    setup.colimaDiskGB,
    setup.colimaMemoryGB,
    setup.colimaProfile,
    setup.distro,
  ]);

  const planProviderInstall = useCallback(async () => {
    const providerID = providerIDForSetupBackend(setup.backend);
    if (!providerID) {
      return;
    }
    const distro = setup.distro.trim() || "Ubuntu";
    const profile = setup.colimaProfile.trim() || "default";
    setSetup((current) => ({
      ...current,
      distro,
      colimaProfile: profile,
      planning: true,
      error: undefined,
    }));
    try {
      const extra =
        setup.backend === "macos_colima"
          ? {
              profile,
              cpu: String(setup.colimaCPU),
              memoryGB: String(setup.colimaMemoryGB),
              diskGB: String(setup.colimaDiskGB),
            }
          : setup.backend === "linux_native"
            ? {
                socketPath: settingString(
                  appSettings,
                  "linux.socket_path",
                  "/var/run/docker.sock",
                ),
              }
            : { distro };
      const plan = await ProviderService.PlanInstall(providerID, {
        backend: providerID,
        extra,
      });
      if (!plan) {
        throw new Error("Install plan was empty");
      }
      setSetup((current) => ({
        ...current,
        step: "install",
        plan,
        planning: false,
      }));
    } catch (error: unknown) {
      setSetup((current) => ({
        ...current,
        planning: false,
        error:
          error instanceof Error
            ? error.message
            : "Unable to create install plan",
      }));
    }
  }, [
    appSettings,
    setup.backend,
    setup.colimaCPU,
    setup.colimaDiskGB,
    setup.colimaMemoryGB,
    setup.colimaProfile,
    setup.distro,
  ]);

  const applyProviderInstall = useCallback(async () => {
    if (!setup.plan?.planID) {
      return;
    }
    const planID = setup.plan.planID;
    const backend = setup.backend;
    providerInstallSessionRef.current = { planID, backend };
    setSetup((current) => ({
      ...current,
      installing: true,
      installStreamID: undefined,
      progress: [
        {
          planID,
          streamID: "pending",
          step: 0,
          totalSteps: setup.plan?.commands.length ?? 0,
          message: "Starting auto repair",
          done: false,
        },
      ],
      error: undefined,
    }));
    try {
      const handle = await ProviderService.ApplyInstall(planID);
      const currentSession = providerInstallSessionRef.current;
      if (currentSession?.planID === planID) {
        providerInstallSessionRef.current = {
          ...currentSession,
          streamID: handle?.streamID,
        };
      }
      setSetup((current) => ({
        ...current,
        installStreamID: handle?.streamID,
      }));
    } catch (error: unknown) {
      providerInstallSessionRef.current = null;
      setSetup((current) => ({
        ...current,
        installing: false,
        error:
          error instanceof Error ? error.message : "Unable to start install",
      }));
    }
  }, [setup.backend, setup.plan]);

  const detectSetupProjects = useCallback(async () => {
    setSetup((current) => ({
      ...current,
      step: "projects",
      detectingProjects: true,
      projectDetectError: undefined,
    }));
    setProjectsStatus("loading");
    setProjectsError(null);
    try {
      const nextProjects = await ProjectService.RefreshProjects();
      const detected = nextProjects ?? [];
      const liveDetected = applyStatsSamplesToProjects(
        detected,
        Object.values(latestSamplesRef.current),
      );
      setProjects(liveDetected);
      setProjectsStatus("ready");
      setSetup((current) => ({
        ...current,
        detectedProjects: liveDetected,
        selectedProjectIDs: liveDetected.map((project) => project.id),
        detectingProjects: false,
      }));
    } catch (error: unknown) {
      const message =
        error instanceof Error ? error.message : "Unable to detect projects";
      setProjectsError(message);
      setProjectsStatus("error");
      setSetup((current) => ({
        ...current,
        detectingProjects: false,
        projectDetectError: message,
      }));
    }
  }, []);

  const toggleSetupProjectSelection = useCallback((projectID: string) => {
    setSetup((current) => {
      const selected = new Set(current.selectedProjectIDs);
      if (selected.has(projectID)) {
        selected.delete(projectID);
      } else {
        selected.add(projectID);
      }
      return {
        ...current,
        selectedProjectIDs: Array.from(selected),
      };
    });
  }, []);

  const openSetupProjectImport = useCallback(() => {
    closeProviderSetup();
    setImportProject({ ...emptyImportProject, open: true });
  }, [closeProviderSetup]);

  const ensureDockerReady = useCallback(() => {
    if (!mutationsDisabled) {
      return true;
    }
    setActionError(mutationDisabledReason);
    if (providerRepairNeeded || noProviderConfigured) {
      setRepairOpen(true);
    }
    return false;
  }, [
    mutationDisabledReason,
    mutationsDisabled,
    noProviderConfigured,
    providerRepairNeeded,
  ]);

  const checkAllUpdates = useCallback(async () => {
    if (!ensureDockerReady()) {
      return;
    }
    setUpdatesError(null);
    setUpdateCheckProgress({
      phase: "check",
      message: "Starting update check",
    });
    try {
      const jobID = await UpdateService.CheckAllUpdates();
      setUpdateCheckJobID(jobID);
      setUpdateCheckProgress({
        jobID,
        phase: "check",
        message: "Checking updates",
      });
    } catch (error: unknown) {
      setUpdateCheckProgress(null);
      setUpdatesError(
        error instanceof Error ? error.message : "Unable to check updates",
      );
    }
  }, [ensureDockerReady]);

  const openUpdatePlan = useCallback(
    async (target: UpdatePlanTarget) => {
      if (!ensureDockerReady()) {
        return;
      }
      setUpdatePlan({
        ...emptyUpdatePlan,
        open: true,
        target,
        busy: true,
      });
      try {
        const plan =
          target.kind === "project"
            ? await UpdateService.PlanProjectUpdate(target.projectID)
            : await UpdateService.PlanServiceUpdate(
                target.projectID,
                target.service,
              );
        if (!plan) {
          throw new Error("Update plan was empty");
        }
        setUpdatePlan({
          ...emptyUpdatePlan,
          open: true,
          target,
          plan,
        });
      } catch (error: unknown) {
        setUpdatePlan((current) => ({
          ...current,
          busy: false,
          error:
            error instanceof Error
              ? error.message
              : "Unable to create update plan",
        }));
      }
    },
    [ensureDockerReady],
  );

  const applyUpdatePlan = useCallback(async () => {
    if (!updatePlan.plan) {
      return;
    }
    setUpdatePlan((current) => ({
      ...current,
      busy: true,
      applying: true,
      error: undefined,
      result: undefined,
      progress: [
        {
          phase: "apply",
          message:
            updatePlan.mode === "rollback"
              ? "Starting rollback"
              : "Starting update",
        },
      ],
    }));
    try {
      const jobID =
        updatePlan.mode === "rollback"
          ? await UpdateService.ApplyRollback(updatePlan.plan.planID)
          : await UpdateService.ApplyUpdate({
              planID: updatePlan.plan.planID,
              backupVolumesFirst: updatePlan.backupVolumesFirst,
              watchHealth: updatePlan.watchHealth,
              rollbackOnFailure: updatePlan.rollbackOnFailure,
            });
      setUpdatePlan((current) => ({
        ...current,
        jobID,
        progress: current.progress.concat({
          jobID,
          phase: updatePlan.mode === "rollback" ? "rollback" : "apply",
          message:
            updatePlan.mode === "rollback"
              ? "Rollback job started"
              : "Update job started",
        }),
      }));
    } catch (error: unknown) {
      setUpdatePlan((current) => ({
        ...current,
        busy: false,
        applying: false,
        error:
          error instanceof Error
            ? error.message
            : updatePlan.mode === "rollback"
              ? "Unable to roll back update"
              : "Unable to apply update",
      }));
    }
  }, [
    updatePlan.backupVolumesFirst,
    updatePlan.mode,
    updatePlan.plan,
    updatePlan.rollbackOnFailure,
    updatePlan.watchHealth,
  ]);

  const openIgnoreUpdate = useCallback((update: ImageUpdate) => {
    setIgnoreUpdate({
      ...emptyIgnoreUpdate,
      open: true,
      update,
    });
  }, []);

  const submitIgnoreUpdate = useCallback(async () => {
    if (!ignoreUpdate.update) {
      return;
    }
    setIgnoreUpdate((current) => ({
      ...current,
      busy: true,
      error: undefined,
    }));
    try {
      await UpdateService.IgnoreUpdate({
        id: ignoreUpdate.update.id,
        reason: ignoreUpdate.reason.trim() || undefined,
      });
      setIgnoreUpdate(emptyIgnoreUpdate);
      await refreshUpdateSurfaces();
      await refreshProjects();
    } catch (error: unknown) {
      setIgnoreUpdate((current) => ({
        ...current,
        busy: false,
        error:
          error instanceof Error ? error.message : "Unable to ignore update",
      }));
    }
  }, [
    ignoreUpdate.reason,
    ignoreUpdate.update,
    refreshProjects,
    refreshUpdateSurfaces,
  ]);

  const unignoreUpdate = useCallback(
    async (id: number) => {
      try {
        await UpdateService.UnignoreUpdate(id);
        await refreshUpdateSurfaces();
        await refreshProjects();
      } catch (error: unknown) {
        setIgnoredUpdatesError(
          error instanceof Error ? error.message : "Unable to unignore update",
        );
      }
    },
    [refreshProjects, refreshUpdateSurfaces],
  );

  const rollbackUpdate = useCallback(
    async (historyID: number) => {
      if (!ensureDockerReady()) {
        return;
      }
      try {
        const plan = await UpdateService.PlanRollback(historyID);
        if (!plan) {
          throw new Error("Rollback plan was empty");
        }
        setUpdatePlan({
          ...emptyUpdatePlan,
          open: true,
          mode: "rollback",
          target: {
            kind: "project",
            projectID: plan.projectID,
            projectName: projectNameForID(projects, plan.projectID),
          },
          plan,
        });
      } catch (error: unknown) {
        setUpdateHistoryError(
          error instanceof Error ? error.message : "Unable to roll back update",
        );
      }
    },
    [ensureDockerReady, projects],
  );

  const runContainerAction = useCallback(
    async (action: ContainerAction, container: ContainerSummary) => {
      if (!ensureDockerReady()) {
        return;
      }
      const key = `${action}:${container.id}`;
      setActionError(null);
      setActionBusy(key, true);
      try {
        if (action === "start") {
          await DockerService.StartContainer(container.id);
        } else if (action === "stop") {
          await DockerService.StopContainer(container.id, 10);
        } else if (action === "restart") {
          await DockerService.RestartContainer(container.id, 10);
        } else if (action === "kill") {
          const plan = await DockerService.PlanKillContainer(container.id);
          if (!plan) {
            throw new Error("Kill plan was empty");
          }
          setConfirm({
            open: true,
            plan,
            planKind: "container",
            targetName: container.name,
            typedName: "",
            busy: false,
          });
          return;
        } else {
          const plan = await DockerService.PlanRemoveContainer(container.id, {
            force: containerCanStop(container),
            removeVolumes: false,
          });
          if (!plan) {
            throw new Error("Remove plan was empty");
          }
          setConfirm({
            open: true,
            plan,
            planKind: "container",
            targetName: container.name,
            typedName: "",
            busy: false,
          });
          return;
        }
        setSelectedContainerIDs((current) => {
          const next = new Set(current);
          next.delete(container.id);
          return next;
        });
        await refreshAfterAction();
      } catch (error: unknown) {
        setActionError(
          error instanceof Error ? error.message : "Container action failed",
        );
      } finally {
        setActionBusy(key, false);
      }
    },
    [ensureDockerReady, refreshAfterAction, setActionBusy],
  );

  const runBulkContainerAction = useCallback(
    async (action: Exclude<ContainerAction, "kill" | "remove">) => {
      if (!ensureDockerReady()) {
        return;
      }
      const ids = Array.from(selectedContainerIDs);
      if (ids.length === 0) {
        return;
      }
      const key = `bulk:${action}`;
      setActionError(null);
      setActionBusy(key, true);
      try {
        const result = await DockerService.BulkContainerAction(ids, action);
        setSelectedContainerIDs(new Set<string>());
        await refreshAfterAction();
        if (result && result.failed > 0) {
          setActionError(
            `${result.failed} of ${result.total} container actions failed`,
          );
        }
      } catch (error: unknown) {
        setActionError(
          error instanceof Error
            ? error.message
            : "Bulk container action failed",
        );
      } finally {
        setActionBusy(key, false);
      }
    },
    [
      ensureDockerReady,
      refreshAfterAction,
      selectedContainerIDs,
      setActionBusy,
    ],
  );

  const applyConfirmedPlan = useCallback(async () => {
    if (!confirm.plan) {
      return;
    }
    setConfirm((current) => ({ ...current, busy: true, error: undefined }));
    try {
      if (confirm.planKind === "project") {
        await ProjectService.ApplyProjectPlan(
          confirm.plan.planID,
          confirm.typedName,
        );
      } else if (confirm.planKind === "backup") {
        await BackupService.ApplyBackup(confirm.plan.planID);
      } else if (confirm.planKind === "backup-delete") {
        await BackupService.ApplyDeleteBackup(confirm.plan.planID);
      } else if (confirm.planKind === "restore") {
        await BackupService.ApplyRestore(
          confirm.plan.planID,
          confirm.typedName,
        );
      } else if (confirm.planKind === "provider") {
        await ProviderService.ApplyProviderPlan(
          confirm.plan.planID,
          confirm.typedName,
        );
      } else if (confirm.planKind === "run-image") {
        await DockerService.ApplyRunImagePlan(
          confirm.plan.planID,
          confirm.typedName,
        );
        setRunImage(emptyRunImage);
      } else {
        await DockerService.ApplyContainerPlan(
          confirm.plan.planID,
          confirm.typedName,
        );
      }
      setConfirm(emptyConfirm);
      setSelectedContainerIDs(new Set<string>());
      if (confirm.planKind === "project") {
        await refreshProjects();
        if (activeProjectID) {
          await refreshProjectDetail(activeProjectID);
        }
      } else if (confirm.planKind === "provider") {
        await refreshInventory();
        await refreshProjects();
        await refreshUpdateSurfaces();
      } else if (confirm.planKind === "run-image") {
        await refreshAfterAction();
        setActivePage("containers");
      } else if (
        confirm.planKind === "backup" ||
        confirm.planKind === "backup-delete" ||
        confirm.planKind === "restore"
      ) {
        await refreshBackups();
        await refreshInventory();
        if (activeProjectID) {
          await refreshProjectDetail(activeProjectID);
        }
      } else {
        await refreshAfterAction();
      }
    } catch (error: unknown) {
      setConfirm((current) => ({
        ...current,
        busy: false,
        error: error instanceof Error ? error.message : "Unable to apply plan",
      }));
    }
  }, [
    confirm.plan,
    confirm.planKind,
    confirm.typedName,
    activeProjectID,
    refreshBackups,
    refreshInventory,
    refreshAfterAction,
    refreshProjectDetail,
    refreshProjects,
    refreshUpdateSurfaces,
  ]);

  const runProjectAction = useCallback(
    async (action: ProjectAction, project: ProjectSummary) => {
      if (action === "remove") {
        setActionError(null);
        setRemoveProject({
          open: true,
          project,
          busy: false,
        });
        return;
      }
      if (!ensureDockerReady()) {
        return;
      }
      const key = projectActionBusyKey(action, project.id);
      setActionError(null);
      setActionBusy(key, true);
      try {
        if (action === "start") {
          await ProjectService.StartProject(project.id);
        } else if (action === "stop") {
          await ProjectService.StopProject(project.id);
        } else if (action === "restart") {
          await ProjectService.RestartProject(project.id);
        } else if (action === "pull") {
          await ProjectService.PullProject(project.id);
        } else {
          const plan =
            action === "redeploy"
              ? await ProjectService.PlanRedeployProject(project.id)
              : await ProjectService.PlanDownProject(
                  project.id,
                  action === "down-volumes",
                );
          if (!plan) {
            throw new Error("Project plan was empty");
          }
          setConfirm({
            open: true,
            plan,
            planKind: "project",
            targetName: project.name,
            typedName: "",
            busy: false,
          });
          return;
        }
        await refreshProjects();
        if (activeProjectID === project.id) {
          await refreshProjectDetail(project.id);
        }
      } catch (error: unknown) {
        setActionError(
          error instanceof Error ? error.message : "Project action failed",
        );
      } finally {
        setActionBusy(key, false);
      }
    },
    [
      activeProjectID,
      ensureDockerReady,
      refreshProjectDetail,
      refreshProjects,
      setActionBusy,
    ],
  );

  const confirmRemoveProject = useCallback(async () => {
    const project = removeProject.project;
    if (!project) {
      return;
    }
    const key = projectActionBusyKey("remove", project.id);
    setActionError(null);
    setActionBusy(key, true);
    setRemoveProject((current) => ({ ...current, busy: true, error: "" }));
    try {
      await ProjectService.RemoveProjectFromList(project.id);
      pushToast({
        body: project.name,
        level: "ok",
        title: "Project removed from list",
      });
      if (activeProjectID === project.id) {
        closeProjectDetail();
      }
      setRemoveProject(emptyRemoveProject);
      await refreshProjects();
      await refreshUpdateSurfaces();
    } catch (error: unknown) {
      setRemoveProject((current) => ({
        ...current,
        busy: false,
        error:
          error instanceof Error ? error.message : "Unable to remove project",
      }));
    } finally {
      setActionBusy(key, false);
    }
  }, [
    activeProjectID,
    closeProjectDetail,
    pushToast,
    refreshProjects,
    refreshUpdateSurfaces,
    removeProject.project,
    setActionBusy,
  ]);

  const openRunImageModal = useCallback((image?: ImageSummary) => {
    const ref = image ? primaryImageRef(image) : "";
    setRunImage({
      ...emptyRunImage,
      open: true,
      imageRef: ref,
      imageLocked: Boolean(image),
      name: ref ? suggestContainerName(ref) : "",
      hubQuery: ref ? "" : "",
    });
  }, []);

  const submitRunImage = useCallback(async () => {
    if (!ensureDockerReady()) {
      return;
    }
    setRunImage((current) => ({ ...current, busy: true, error: undefined }));
    try {
      const req = buildRunImageRequest(runImage);
      const plan = await DockerService.PlanRunImage(req);
      if (!plan) {
        throw new Error("Run image plan was empty");
      }
      if (plan.risk === Risk.RiskSafe) {
        await DockerService.ApplyRunImagePlan(plan.planID, "");
        setRunImage(emptyRunImage);
        await refreshAfterAction();
        setActivePage("containers");
        return;
      }
      setRunImage((current) => ({ ...current, busy: false }));
      setConfirm({
        open: true,
        plan,
        planKind: "run-image",
        targetName: plan.requiresTypedName || req.name || req.imageRef,
        typedName: "",
        busy: false,
      });
    } catch (error: unknown) {
      setRunImage((current) => ({
        ...current,
        busy: false,
        error: error instanceof Error ? error.message : "Unable to run image",
      }));
    }
  }, [ensureDockerReady, refreshAfterAction, runImage]);

  const openRenameModal = useCallback((container: ContainerSummary) => {
    setRename({
      ...emptyRename,
      open: true,
      container,
      name: container.name,
    });
  }, []);

  const submitRename = useCallback(async () => {
    if (!ensureDockerReady()) {
      return;
    }
    if (!rename.container) {
      return;
    }
    setRename((current) => ({ ...current, busy: true, error: undefined }));
    try {
      await DockerService.RenameContainer(rename.container.id, rename.name);
      setRename(emptyRename);
      await refreshAfterAction();
    } catch (error: unknown) {
      setRename((current) => ({
        ...current,
        busy: false,
        error:
          error instanceof Error ? error.message : "Unable to rename container",
      }));
    }
  }, [ensureDockerReady, refreshAfterAction, rename.container, rename.name]);

  const submitPullImage = useCallback(async () => {
    if (!ensureDockerReady()) {
      return;
    }
    const ref = imageRefWithTag(pullImage.ref, pullImage.tag);
    setPullImage((current) => ({ ...current, busy: true, error: undefined }));
    try {
      await DockerService.PullImage(ref);
      setPullImage(emptyPullImage);
      await refreshAfterAction();
    } catch (error: unknown) {
      setPullImage((current) => ({
        ...current,
        busy: false,
        error: error instanceof Error ? error.message : "Unable to pull image",
      }));
    }
  }, [ensureDockerReady, pullImage.ref, pullImage.tag, refreshAfterAction]);

  const openTagImageModal = useCallback((image: ImageSummary) => {
    const ref = taggableImageRef(image);
    setTagImage({
      ...emptyTagImage,
      open: true,
      image,
      newRef: ref,
    });
  }, []);

  const submitTagImage = useCallback(async () => {
    if (!ensureDockerReady() || !tagImage.image) {
      return;
    }
    setTagImage((current) => ({ ...current, busy: true, error: undefined }));
    try {
      await DockerService.TagImage(tagImage.image.id, tagImage.newRef);
      setTagImage(emptyTagImage);
      await refreshAfterAction();
    } catch (error: unknown) {
      setTagImage((current) => ({
        ...current,
        busy: false,
        error: error instanceof Error ? error.message : "Unable to tag image",
      }));
    }
  }, [ensureDockerReady, refreshAfterAction, tagImage.image, tagImage.newRef]);

  const openPushImageModal = useCallback((image: ImageSummary) => {
    const ref = pushableImageRef(image);
    setPushImage({
      ...emptyPushImage,
      open: true,
      image,
      ref,
      error: ref ? undefined : "Create a registry tag before pushing.",
    });
  }, []);

  const submitPushImage = useCallback(async () => {
    if (!ensureDockerReady()) {
      return;
    }
    setPushImage((current) => ({
      ...current,
      busy: true,
      error: undefined,
      success: false,
      progress: [],
      streamID: undefined,
    }));
    try {
      const plan = await DockerService.PlanPushImage(pushImage.ref);
      if (!plan) {
        throw new Error("Push plan was empty");
      }
      const streamID = await DockerService.ApplyPushImagePlan(plan.planID);
      setPushImage((current) => ({
        ...current,
        busy: false,
        streamID,
        success: true,
      }));
      await refreshAfterAction();
    } catch (error: unknown) {
      setPushImage((current) => ({
        ...current,
        busy: false,
        error: error instanceof Error ? error.message : "Unable to push image",
      }));
    }
  }, [ensureDockerReady, pushImage.ref, refreshAfterAction]);

  const openSaveImageModal = useCallback((image: ImageSummary) => {
    const ref = primaryImageRef(image);
    setSaveImage({
      ...emptySaveImage,
      open: true,
      refsText: ref,
      destPath: `${ref.replace(/[/:@]/g, "_") || "image"}.tar`,
    });
  }, []);

  const submitSaveImage = useCallback(async () => {
    if (!ensureDockerReady()) {
      return;
    }
    setSaveImage((current) => ({ ...current, busy: true, error: undefined }));
    try {
      await DockerService.SaveImage(
        splitRefs(saveImage.refsText),
        saveImage.destPath,
      );
      setSaveImage(emptySaveImage);
    } catch (error: unknown) {
      setSaveImage((current) => ({
        ...current,
        busy: false,
        error: error instanceof Error ? error.message : "Unable to save image",
      }));
    }
  }, [ensureDockerReady, saveImage.destPath, saveImage.refsText]);

  const submitLoadImage = useCallback(async () => {
    if (!ensureDockerReady()) {
      return;
    }
    setLoadImage((current) => ({ ...current, busy: true, error: undefined }));
    try {
      await DockerService.LoadImage(loadImage.srcPath);
      setLoadImage(emptyLoadImage);
      await refreshAfterAction();
    } catch (error: unknown) {
      setLoadImage((current) => ({
        ...current,
        busy: false,
        error: error instanceof Error ? error.message : "Unable to load image",
      }));
    }
  }, [ensureDockerReady, loadImage.srcPath, refreshAfterAction]);

  const openRemoveImagePlan = useCallback(
    async (image: ImageSummary) => {
      if (!ensureDockerReady()) {
        return;
      }
      const label = primaryImageRef(image);
      setActionError(null);
      try {
        const inUse = image.inUse || (imageUseCounts[image.id] ?? 0) > 0;
        const plan = await DockerService.PlanRemoveImage(image.id, inUse);
        if (!plan) {
          throw new Error("Image removal plan was empty");
        }
        setConfirm({
          ...emptyConfirm,
          open: true,
          plan,
          planKind: "container",
          targetName: label,
        });
      } catch (error: unknown) {
        setActionError(
          error instanceof Error
            ? error.message
            : "Unable to plan image removal",
        );
      }
    },
    [ensureDockerReady, imageUseCounts],
  );

  const submitCreateVolume = useCallback(async () => {
    if (!ensureDockerReady()) {
      return;
    }
    setCreateVolume((current) => ({
      ...current,
      busy: true,
      error: undefined,
    }));
    try {
      await DockerService.CreateVolume({
        name: createVolume.name,
        driver: createVolume.driver,
        driverOpts: parseKeyValueLines(createVolume.driverOptsText),
        labels: parseKeyValueLines(createVolume.labelsText),
      });
      setCreateVolume(emptyCreateVolume);
      await refreshAfterAction();
    } catch (error: unknown) {
      setCreateVolume((current) => ({
        ...current,
        busy: false,
        error:
          error instanceof Error ? error.message : "Unable to create volume",
      }));
    }
  }, [
    createVolume.driver,
    createVolume.driverOptsText,
    createVolume.labelsText,
    createVolume.name,
    ensureDockerReady,
    refreshAfterAction,
  ]);

  const openRemoveVolumePlan = useCallback(
    async (volume: VolumeSummary) => {
      if (!ensureDockerReady()) {
        return;
      }
      setActionError(null);
      try {
        const plan = await DockerService.PlanRemoveVolume(volume.name, false);
        if (!plan) {
          throw new Error("Volume removal plan was empty");
        }
        setConfirm({
          ...emptyConfirm,
          open: true,
          plan,
          planKind: "container",
          targetName: volume.name,
        });
      } catch (error: unknown) {
        setActionError(
          error instanceof Error
            ? error.message
            : "Unable to plan volume removal",
        );
      }
    },
    [ensureDockerReady],
  );

  const openBackupVolume = useCallback(
    (volume: VolumeSummary) => {
      setBackupVolume({
        ...emptyBackupVolume,
        open: true,
        volume,
        destPath: normalizeStringSetting(appSettings["backups.directory"], ""),
      });
    },
    [appSettings],
  );

  const submitBackupVolume = useCallback(async () => {
    if (!backupVolume.volume || !ensureDockerReady()) {
      return;
    }
    setBackupVolume((current) => ({
      ...current,
      busy: true,
      error: undefined,
    }));
    try {
      const plan = await BackupService.PlanBackupVolume({
        volumeName: backupVolume.volume.name,
        destPath: backupVolume.destPath,
        projectID: projectIDForVolume(backupVolume.volume, projects),
      });
      if (!plan) {
        throw new Error("Backup plan was empty");
      }
      setBackupVolume(emptyBackupVolume);
      setConfirm({
        ...emptyConfirm,
        open: true,
        plan,
        planKind: "backup",
        targetName: backupVolume.volume.name,
      });
    } catch (error: unknown) {
      setBackupVolume((current) => ({
        ...current,
        busy: false,
        error: error instanceof Error ? error.message : "Unable to plan backup",
      }));
    }
  }, [backupVolume.destPath, backupVolume.volume, ensureDockerReady, projects]);

  const openRestoreVolume = useCallback(
    async (volume: VolumeSummary, selectedBackup?: BackupSummary) => {
      setRestoreVolume({
        ...emptyRestoreVolume,
        open: true,
        volume,
        backupID: selectedBackup?.id ?? "",
        sourcePath: selectedBackup?.path ?? "",
        targetName: volume.name,
        overwrite: Boolean(selectedBackup),
        loading: true,
      });
      try {
        const volumeBackups = await BackupService.ListBackups({
          volumeName: volume.name,
          limit: 100,
        });
        setRestoreVolume((current) => ({
          ...current,
          backups: volumeBackups ?? [],
          loading: false,
          backupID: selectedBackup?.id ?? current.backupID,
          sourcePath: selectedBackup?.path ?? current.sourcePath,
        }));
      } catch (error: unknown) {
        setRestoreVolume((current) => ({
          ...current,
          loading: false,
          error:
            error instanceof Error ? error.message : "Unable to load backups",
        }));
      }
    },
    [],
  );

  const openRestoreBackup = useCallback(
    (backup: BackupSummary) => {
      const volume =
        volumes.find((item) => item.name === backup.volumeName) ??
        ({
          name: backup.volumeName,
          driver: "local",
          labels: backup.projectID
            ? { [composeProjectLabel]: backup.projectID }
            : {},
          inUse: false,
        } as VolumeSummary);
      void openRestoreVolume(volume, backup);
    },
    [openRestoreVolume, volumes],
  );

  const submitRestoreVolume = useCallback(async () => {
    if (!restoreVolume.volume || !ensureDockerReady()) {
      return;
    }
    setRestoreVolume((current) => ({
      ...current,
      busy: true,
      error: undefined,
    }));
    try {
      const plan = await BackupService.PlanRestoreVolume({
        backupID: restoreVolume.backupID,
        sourcePath: restoreVolume.sourcePath,
        volumeName: restoreVolume.targetName,
        overwrite: restoreVolume.overwrite,
      });
      if (!plan) {
        throw new Error("Restore plan was empty");
      }
      setRestoreVolume(emptyRestoreVolume);
      setConfirm({
        ...emptyConfirm,
        open: true,
        plan,
        planKind: "restore",
        targetName: restoreVolume.targetName,
      });
    } catch (error: unknown) {
      setRestoreVolume((current) => ({
        ...current,
        busy: false,
        error:
          error instanceof Error ? error.message : "Unable to plan restore",
      }));
    }
  }, [ensureDockerReady, restoreVolume]);

  const openDeleteBackupPlan = useCallback(
    async (backup: BackupSummary) => {
      if (!ensureDockerReady()) {
        return;
      }
      setActionError(null);
      try {
        const plan = await BackupService.PlanDeleteBackup(backup.id);
        if (!plan) {
          throw new Error("Backup delete plan was empty");
        }
        setConfirm({
          ...emptyConfirm,
          open: true,
          plan,
          planKind: "backup-delete",
          targetName: backup.volumeName,
        });
      } catch (error: unknown) {
        setActionError(
          error instanceof Error
            ? error.message
            : "Unable to plan backup deletion",
        );
      }
    },
    [ensureDockerReady],
  );

  const submitCreateNetwork = useCallback(async () => {
    if (!ensureDockerReady()) {
      return;
    }
    setCreateNetwork((current) => ({
      ...current,
      busy: true,
      error: undefined,
    }));
    try {
      await DockerService.CreateNetwork({
        name: createNetwork.name,
        driver:
          createNetwork.driver === "custom"
            ? createNetwork.customDriver
            : createNetwork.driver,
        subnet: createNetwork.subnet,
        gateway: createNetwork.gateway,
        internal: createNetwork.internal,
        attachable: createNetwork.attachable,
        labels: parseKeyValueLines(createNetwork.labelsText),
      });
      setCreateNetwork(emptyCreateNetwork);
      await refreshAfterAction();
    } catch (error: unknown) {
      setCreateNetwork((current) => ({
        ...current,
        busy: false,
        error:
          error instanceof Error ? error.message : "Unable to create network",
      }));
    }
  }, [createNetwork, ensureDockerReady, refreshAfterAction]);

  const openRemoveNetworkPlan = useCallback(
    async (network: NetworkSummary) => {
      if (!ensureDockerReady()) {
        return;
      }
      setActionError(null);
      try {
        const plan = await DockerService.PlanRemoveNetwork(network.id);
        if (!plan) {
          throw new Error("Network removal plan was empty");
        }
        setConfirm({
          ...emptyConfirm,
          open: true,
          plan,
          planKind: "container",
          targetName: network.name,
        });
      } catch (error: unknown) {
        setActionError(
          error instanceof Error
            ? error.message
            : "Unable to plan network removal",
        );
      }
    },
    [ensureDockerReady],
  );

  const browseImportFolder = useCallback(async () => {
    try {
      const selected = await Dialogs.OpenFile({
        Title: "Import Compose Project",
        Message: "Choose a Compose project folder",
        ButtonText: "Import",
        CanChooseDirectories: true,
        CanChooseFiles: false,
      });
      const folderPath = Array.isArray(selected) ? selected[0] : selected;
      if (folderPath) {
        setImportProject((current) => ({
          ...current,
          folderPath,
          error: undefined,
          imported: null,
        }));
      }
    } catch (error: unknown) {
      setImportProject((current) => ({
        ...current,
        error:
          error instanceof Error
            ? error.message
            : "Unable to open folder picker",
      }));
    }
  }, []);

  const submitImportProject = useCallback(async () => {
    const folderPath = importProject.folderPath.trim();
    if (!folderPath) {
      setImportProject((current) => ({
        ...current,
        error: "Choose a project folder",
      }));
      return;
    }
    setImportProject((current) => ({
      ...current,
      busy: true,
      error: undefined,
      imported: null,
    }));
    try {
      const detail = await ProjectService.ImportProject({
        folderPath,
        composeFilePaths: [],
      });
      setImportProject((current) => ({
        ...current,
        busy: false,
        imported: detail,
        error: undefined,
      }));
      await refreshProjects();
      setActivePage("projects");
    } catch (error: unknown) {
      setImportProject((current) => ({
        ...current,
        busy: false,
        imported: null,
        error:
          error instanceof Error ? error.message : "Unable to import project",
      }));
    }
  }, [importProject.folderPath, refreshProjects]);

  const toggleContainerSelection = useCallback((id: string) => {
    setSelectedContainerIDs((current) => {
      const next = new Set(current);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  }, []);

  const toggleAllContainerSelection = useCallback(
    (ids: string[], selected: boolean) => {
      setSelectedContainerIDs((current) => {
        const next = new Set(current);
        for (const id of ids) {
          if (selected) {
            next.add(id);
          } else {
            next.delete(id);
          }
        }
        return next;
      });
    },
    [],
  );

  const openContainerInspect = useCallback((container: ContainerSummary) => {
    const subtitle = shortID(container.id);
    setInspect({
      open: true,
      title: container.name,
      subtitle,
      rows: containerRows(container),
      loading: true,
    });
    DockerService.InspectContainerRaw(container.id)
      .then((raw) => {
        setInspect((current) => ({
          open: true,
          title: container.name,
          subtitle,
          rows: containerRows(container),
          lineage: current.lineage,
          raw: formatJSON(raw),
        }));
      })
      .catch((error: unknown) => {
        setInspect((current) => ({
          open: true,
          title: container.name,
          subtitle,
          rows: containerRows(container),
          lineage: current.lineage,
          error:
            error instanceof Error
              ? error.message
              : "Unable to inspect container",
        }));
      });
    ImageLineageService.GetContainerLineage(container.id)
      .then((lineage) => {
        setInspect((current) =>
          current.open && current.subtitle === subtitle
            ? { ...current, lineage }
            : current,
        );
      })
      .catch(() => {
        setInspect((current) =>
          current.open && current.subtitle === subtitle
            ? { ...current, lineage: null }
            : current,
        );
      });
  }, []);

  const openContainerTerminal = useCallback(
    async (container: ContainerSummary) => {
      try {
        const session = await TerminalService.OpenContainerTerminal(
          container.id,
          {
            cols: 120,
            rows: 30,
          },
        );
        setTerminalInitialSession(session ?? null);
        pushToast({
          body: container.service || shortID(container.id),
          level: "ok",
          title: "Container terminal opened",
        });
        navigate("terminal");
      } catch (error: unknown) {
        setActionError(
          error instanceof Error
            ? error.message
            : "Unable to open container terminal",
        );
      }
    },
    [navigate, pushToast],
  );

  const openProjectFolder = useCallback(
    async (project: ProjectSummary) => {
      const workdir = project.workingDir?.trim();
      if (!workdir) {
        return;
      }
      try {
        await Browser.OpenURL(hostPathFileURL(workdir));
      } catch {
        await Clipboard.SetText(workdir);
        pushToast({
          body: workdir,
          level: "warn",
          title: "Folder path copied",
        });
      }
    },
    [pushToast],
  );

  const openImageInspect = useCallback(
    (image: ImageSummary) => {
      setInspect({
        open: true,
        title: primaryImageRef(image),
        subtitle: shortID(image.id),
        rows: imageRows(image, imageUseCounts[image.id] ?? 0),
        loading: true,
      });
      DockerService.GetImage(image.id)
        .then((detail) => {
          if (!detail) {
            throw new Error("Image detail was empty");
          }
          setInspect({
            open: true,
            title: primaryImageRef(image),
            subtitle: shortID(image.id),
            rows: imageDetailRows(detail, imageUseCounts[image.id] ?? 0),
            raw: JSON.stringify(detail, null, 2),
          });
        })
        .catch((error: unknown) => {
          setInspect({
            open: true,
            title: primaryImageRef(image),
            subtitle: shortID(image.id),
            rows: imageRows(image, imageUseCounts[image.id] ?? 0),
            error:
              error instanceof Error
                ? error.message
                : "Unable to inspect image",
          });
        });
    },
    [imageUseCounts],
  );

  const openVolumeInspect = useCallback(
    (volume: VolumeSummary) => {
      const detail = volumeDetails[volume.name];
      setInspect({
        open: true,
        title: volume.name,
        subtitle: volume.driver,
        rows: volumeRows(volume, detail),
        raw: detail ? JSON.stringify(detail, null, 2) : undefined,
        loading: !detail,
      });
      if (detail) {
        return;
      }
      DockerService.GetVolume(volume.name)
        .then((nextDetail) => {
          setInspect({
            open: true,
            title: volume.name,
            subtitle: volume.driver,
            rows: volumeRows(volume, nextDetail ?? undefined),
            raw: nextDetail ? JSON.stringify(nextDetail, null, 2) : undefined,
          });
        })
        .catch((error: unknown) => {
          setInspect({
            open: true,
            title: volume.name,
            subtitle: volume.driver,
            rows: volumeRows(volume),
            error:
              error instanceof Error
                ? error.message
                : "Unable to inspect volume",
          });
        });
    },
    [volumeDetails],
  );

  const openNetworkDetail = useCallback(
    (network: NetworkSummary) => {
      setActionError(null);
      setActivePage("networks");
      setActiveContainerID(null);
      setContainerDetailTab("overview");
      setActiveNetworkID(network.id);
      setNetworkTab("overview");
      setSearch("");
      if (!networkDetails[network.id]) {
        void refreshNetworks();
      }
    },
    [networkDetails, refreshNetworks],
  );

  const openContainerDetail = useCallback(
    (
      container: ContainerSummary,
      tab: ContainerDrilldownTabID = "overview",
    ) => {
      setActionError(null);
      setActivePage("containers");
      setActiveContainerID(container.id);
      setActiveContainerFallback(container);
      setContainerDetailTab(tab);
      setActiveNetworkID(null);
      setNetworkTab("overview");
    },
    [],
  );

  const closeContainerDetail = useCallback(() => {
    setActiveContainerID(null);
    setActiveContainerFallback(null);
    setContainerDetailTab("overview");
  }, []);

  const clearProjectCommandOutput = useCallback((projectID: string) => {
    setProjectCommandOutputs((current) => {
      if (!current[projectID]) {
        return current;
      }
      const next = { ...current };
      delete next[projectID];
      return next;
    });
  }, []);

  const content = (() => {
    switch (activePage) {
      case "projects":
        if (activeProjectID) {
          const detailProject = projectDetail?.summary;
          const projectID = detailProject?.id ?? activeProjectID;
          const projectName =
            detailProject?.name ?? projectNameForID(projects, projectID);
          return (
            <ProjectDetailPage
              actionBusyIDs={busyActionIDs}
              backups={backups}
              backupsError={backupsError}
              backupsLoading={backupsStatus === "loading"}
              commandOutput={projectCommandOutputs[projectID] ?? null}
              detail={projectDetail}
              error={projectDetailError}
              loading={projectDetailStatus === "loading"}
              lineage={projectLineage[projectID] ?? []}
              lineageLoading={projectLineageStatus[projectID] === "loading"}
              mutationsDisabled={mutationsDisabled}
              mutationDisabledReason={mutationDisabledReason}
              onAction={runProjectAction}
              onBack={closeProjectDetail}
              onBackupVolume={openBackupVolume}
              onCheckUpdates={checkAllUpdates}
              onClearCommandOutput={clearProjectCommandOutput}
              onContainerAction={runContainerAction}
              onDeleteBackup={openDeleteBackupPlan}
              onIgnoreUpdate={openIgnoreUpdate}
              onOpenContainerDetail={openContainerDetail}
              onOpenContainerTerminal={openContainerTerminal}
              onRefresh={() => {
                void refreshProjectDetail(activeProjectID);
                void refreshBackups();
                void refreshUpdateSurfaces();
                void refreshProjectLineage(activeProjectID);
              }}
              onRestoreBackup={openRestoreBackup}
              onTabChange={setProjectTab}
              onUpdateProject={() =>
                void openUpdatePlan({
                  kind: "project",
                  projectID,
                  projectName,
                })
              }
              onUpdateService={(service) =>
                void openUpdatePlan({
                  kind: "service",
                  projectID,
                  projectName,
                  service,
                })
              }
              projectVolumes={volumes.filter(
                (volume) =>
                  projectDetail &&
                  projectIDForVolume(volume, projects) ===
                    projectDetail.summary.id,
              )}
              dockerRunning={dockerRunning}
              inventoryLoading={inventoryStatus === "loading"}
              onToast={pushToast}
              projectsLoading={projectsStatus === "loading"}
              tab={projectTab}
              updates={updates.filter(
                (update) => update.projectID === projectID,
              )}
            />
          );
        }
        return (
          <ProjectsPage
            error={projectsError}
            actionBusyIDs={busyActionIDs}
            filter={projectFilter}
            loading={projectsStatus === "loading"}
            mutationsDisabled={mutationsDisabled}
            mutationDisabledReason={mutationDisabledReason}
            onAction={runProjectAction}
            onFilterChange={setProjectFilter}
            onImport={() =>
              setImportProject({ ...emptyImportProject, open: true })
            }
            onOpen={openProjectDetail}
            onOpenFolder={openProjectFolder}
            onRefresh={refreshProjects}
            onSortChange={setProjectSort}
            onViewChange={changeProjectView}
            projectSparks={projectSparks}
            projects={projects}
            search={search}
            sort={projectSort}
            view={projectView}
          />
        );
      case "updates":
        return (
          <UpdatesPage
            checkJobID={updateCheckJobID}
            checkProgress={updateCheckProgress}
            error={updatesError}
            filter={updateFilter}
            history={updateHistory}
            historyError={updateHistoryError}
            historyLoading={updateHistoryStatus === "loading"}
            ignored={ignoredUpdates}
            ignoredError={ignoredUpdatesError}
            ignoredLoading={ignoredUpdatesStatus === "loading"}
            lastCheckAt={lastUpdateCheckAt}
            loading={updatesStatus === "loading"}
            mutationsDisabled={mutationsDisabled}
            mutationDisabledReason={mutationDisabledReason}
            onCheckNow={checkAllUpdates}
            onFilterChange={setUpdateFilter}
            onIgnore={openIgnoreUpdate}
            onOpenProject={(projectID) => {
              const project = projects.find((item) => item.id === projectID);
              if (project) {
                openProjectDetail(project);
                setActivePage("projects");
                setProjectTab("updates");
              }
            }}
            onPlanProject={(projectID) => {
              const project = projects.find((item) => item.id === projectID);
              void openUpdatePlan({
                kind: "project",
                projectID,
                projectName: project?.name,
              });
            }}
            onPlanService={(update) =>
              void openUpdatePlan({
                kind: "service",
                projectID: update.projectID ?? "",
                projectName: projectNameForID(projects, update.projectID),
                service: update.service ?? "",
              })
            }
            onRefresh={refreshUpdateSurfaces}
            onRollback={(historyID) => {
              void rollbackUpdate(historyID);
            }}
            onTabChange={setUpdatesTab}
            onUnignore={(id) => {
              void unignoreUpdate(id);
            }}
            projects={projects}
            search={search}
            tab={updatesTab}
            updates={updates}
          />
        );
      case "logs":
        return (
          <LogsPage
            containers={containers}
            dockerRunning={dockerRunning}
            inventoryLoading={inventoryStatus === "loading"}
            onToast={pushToast}
            projects={projects}
            projectsLoading={projectsStatus === "loading"}
          />
        );
      case "terminal":
        return (
          <TerminalPage
            containers={containers}
            initialSession={terminalInitialSession}
            onCommandConsumed={(id) =>
              setQueuedTerminalCommand((current) =>
                current?.id === id ? null : current,
              )
            }
            onInitialSessionConsumed={(id) =>
              setTerminalInitialSession((current) =>
                current?.id === id ? null : current,
              )
            }
            projects={projects}
            queuedCommand={queuedTerminalCommand}
          />
        );
      case "agent":
        return <AgentPage projects={projects} />;
      case "settings":
        return (
          <SettingsPage
            activeProvider={activeProvider}
            auditEntries={visibleAuditEntries}
            auditError={auditError}
            auditFilter={auditFilter}
            auditLoading={auditLoading}
            autostartBackend={providerAutostart}
            colimaCPU={colimaCPU}
            colimaDiskGB={colimaDiskGB}
            colimaMemoryGB={colimaMemoryGB}
            colimaProfile={colimaProfile}
            dockerContexts={dockerContexts}
            dockerContextsError={dockerContextsError}
            dockerContextsLoading={dockerContextsLoading}
            error={settingsError}
            message={settingsMessage}
            onAutostartChange={changeProviderAutostart}
            onColimaCPUChange={setColimaCPU}
            onColimaDiskGBChange={setColimaDiskGB}
            onColimaMemoryGBChange={setColimaMemoryGB}
            onColimaProfileChange={setColimaProfile}
            onDetect={() => {
              void retryProviderDetection();
            }}
            onOpenSetup={openProviderSetup}
            onRefreshDockerContexts={() => {
              void refreshDockerContexts();
            }}
            onRefreshWSLDistros={() => {
              void refreshWSLDistros();
            }}
            onRefreshRegistries={() => {
              void refreshRegistryAccounts();
            }}
            onRegistryLogin={openRegistryLogin}
            onRegistryLogout={(registry) => {
              void logoutRegistry(registry);
            }}
            onRegistryTest={(registry) => {
              void testRegistryAuth(registry);
            }}
            onAuditFilterChange={(patch) => {
              setAuditFilter((current) => ({ ...current, ...patch }));
            }}
            onRefreshAudit={() => {
              void refreshAuditLog();
            }}
            onSettingChange={(key, value) => {
              void saveSetting(key, value);
            }}
            onSaveColimaCPU={() => {
              void saveColimaNumberSetting(
                "macos.colima_cpu",
                colimaCPU,
                setColimaCPU,
                2,
              );
            }}
            onSaveColimaDiskGB={() => {
              void saveColimaNumberSetting(
                "macos.colima_disk_gb",
                colimaDiskGB,
                setColimaDiskGB,
                60,
              );
            }}
            onSaveColimaMemoryGB={() => {
              void saveColimaNumberSetting(
                "macos.colima_memory_gb",
                colimaMemoryGB,
                setColimaMemoryGB,
                4,
              );
            }}
            onSaveColimaProfile={() => {
              void saveColimaProfile();
            }}
            onSaveWSLDistro={() => {
              void saveWSLDistro();
            }}
            onUseDockerContext={(name) => {
              void activateDockerContext(name);
            }}
            onUseWSLDistro={(distro) => {
              void selectWSLDistro(distro);
            }}
            onWSLDistroChange={setWSLDistro}
            providers={providers}
            registryAccounts={registryAccounts}
            registryAccountsError={registryAccountsError}
            registryAccountsLoading={registryAccountsStatus === "loading"}
            registryBusyKeys={registryBusyKeys}
            registryStatuses={registryStatuses}
            saving={settingsSaving}
            section={settingsSection}
            settings={appSettings}
            onSectionChange={setSettingsSection}
            version={version}
            wslDistro={wslDistro}
            wslDistros={wslDistros}
            wslDistrosError={wslDistrosError}
            wslDistrosLoading={wslDistrosLoading}
          />
        );
      case "containers":
        if (activeContainerID) {
          const activeContainer =
            containers.find((item) => item.id === activeContainerID) ??
            (activeContainerFallback?.id === activeContainerID
              ? activeContainerFallback
              : null);
          const activeContainerProject = activeContainer?.projectID
            ? (projects.find(
                (project) => project.id === activeContainer.projectID,
              ) ?? null)
            : null;
          if (!activeContainer) {
            return (
              <EmptyState
                action={
                  <Button onClick={closeContainerDetail}>
                    Back to containers
                  </Button>
                }
                body="The selected container is no longer present in the current inventory."
                icon={<Container size={28} />}
                title="Container not found"
              />
            );
          }
          return (
            <ContainerDetailPage
              actionBusyIDs={busyActionIDs}
              container={activeContainer}
              dockerRunning={dockerRunning}
              inventoryLoading={inventoryStatus === "loading"}
              mutationsDisabled={mutationsDisabled}
              mutationDisabledReason={mutationDisabledReason}
              onAction={runContainerAction}
              onBack={closeContainerDetail}
              onOpenContainerTerminal={openContainerTerminal}
              onTabChange={setContainerDetailTab}
              onToast={pushToast}
              project={activeContainerProject}
              projectsLoading={projectsStatus === "loading"}
              tab={containerDetailTab}
            />
          );
        }
        return (
          <ContainersPage
            actionBusyIDs={busyActionIDs}
            containers={containers}
            filter={containerFilter}
            loading={inventoryStatus === "loading"}
            mutationsDisabled={mutationsDisabled}
            mutationDisabledReason={mutationDisabledReason}
            onAction={runContainerAction}
            onBulkAction={runBulkContainerAction}
            onFilterChange={setContainerFilter}
            onInspect={openContainerInspect}
            onOpen={openContainerDetail}
            onRename={openRenameModal}
            onToggleAllSelection={toggleAllContainerSelection}
            onToggleSelection={toggleContainerSelection}
            search={search}
            selectedIDs={selectedContainerIDs}
          />
        );
      case "images":
        return (
          <ImagesPage
            filter={imageFilter}
            imageUseCounts={imageUseCounts}
            images={images}
            loading={inventoryStatus === "loading"}
            mutationsDisabled={mutationsDisabled}
            mutationDisabledReason={mutationDisabledReason}
            onFilterChange={setImageFilter}
            onInspect={openImageInspect}
            onLoad={() => setLoadImage({ ...emptyLoadImage, open: true })}
            onPull={() => setPullImage({ ...emptyPullImage, open: true })}
            onPush={openPushImageModal}
            onRemove={openRemoveImagePlan}
            onRun={openRunImageModal}
            onSave={openSaveImageModal}
            onTag={openTagImageModal}
            search={search}
          />
        );
      case "volumes":
        return (
          <VolumesPage
            filter={volumeFilter}
            loading={inventoryStatus === "loading"}
            mutationsDisabled={mutationsDisabled}
            mutationDisabledReason={mutationDisabledReason}
            onBackup={openBackupVolume}
            onCreate={() =>
              setCreateVolume({ ...emptyCreateVolume, open: true })
            }
            onFilterChange={setVolumeFilter}
            onInspect={openVolumeInspect}
            onRemove={openRemoveVolumePlan}
            onRestore={(volume) => {
              void openRestoreVolume(volume);
            }}
            search={search}
            volumeDetails={volumeDetails}
            volumes={volumes}
          />
        );
      case "networks":
        if (activeNetworkID) {
          const network =
            networks.find((item) => item.id === activeNetworkID) ??
            networkDetails[activeNetworkID]?.summary;
          return (
            <NetworkDetailPage
              containers={containers}
              detail={
                activeNetworkID ? networkDetails[activeNetworkID] : undefined
              }
              loading={inventoryStatus === "loading"}
              mutationsDisabled={mutationsDisabled}
              mutationDisabledReason={mutationDisabledReason}
              network={network}
              onBack={() => {
                setActiveNetworkID(null);
                setNetworkTab("overview");
              }}
              onOpenContainerInspect={openContainerInspect}
              onOpenContainerTerminal={openContainerTerminal}
              onRefresh={refreshNetworks}
              onRemove={openRemoveNetworkPlan}
              onTabChange={setNetworkTab}
              tab={networkTab}
            />
          );
        }
        return (
          <NetworksPage
            loading={inventoryStatus === "loading"}
            mutationsDisabled={mutationsDisabled}
            mutationDisabledReason={mutationDisabledReason}
            networkDetails={networkDetails}
            networks={networks}
            onCreate={() =>
              setCreateNetwork({ ...emptyCreateNetwork, open: true })
            }
            onInspect={openNetworkDetail}
            onRemove={openRemoveNetworkPlan}
            search={search}
          />
        );
      default:
        return (
          <OverviewPage
            chartPaused={chartPaused}
            chartPoints={chartPoints}
            containerSparks={containerSparks}
            containers={containers}
            diskReclaimable={diskReclaimable}
            diskTotal={diskTotal}
            dockerRunning={dockerRunning}
            images={images}
            latestSamples={latestSamples}
            liveGPU={liveGPU}
            metricsStreamError={statsStreamError}
            mutationsDisabled={mutationsDisabled}
            mutationDisabledReason={mutationDisabledReason}
            onChartPausedChange={setChartPaused}
            onImportProject={() =>
              setImportProject({ ...emptyImportProject, open: true })
            }
            onCheckUpdates={checkAllUpdates}
            onCleanupApplied={async () => {
              await refreshInventory();
              await refreshProjects();
              await refreshUpdateSurfaces();
            }}
            onNavigate={navigate}
            onOpenTerminal={() => navigate("terminal")}
            onOpenProject={openProjectDetail}
            onRestartDocker={restartProvider}
            onShowContainers={showContainers}
            provider={activeProvider}
            projectMetricSparks={projectMetricSparks}
            projectSparks={projectSparks}
            projects={projects}
            projectsLoading={projectsStatus === "loading"}
            refreshToken={dashboardRefreshToken}
            runningContainers={runningContainers}
            unhealthyContainers={unhealthyContainers}
            volumes={volumes}
          />
        );
    }
  })();

  return (
    <main className="h-screen overflow-hidden bg-bg-app text-text-primary">
      <div
        className={[
          "grid h-full min-h-0 transition-[grid-template-columns]",
          sidebarCollapsed
            ? "grid-cols-[72px_1fr]"
            : "grid-cols-1 lg:grid-cols-[236px_1fr]",
        ].join(" ")}
      >
        <aside
          className={[
            "flex min-h-0 flex-col border-border bg-bg-panel",
            sidebarCollapsed
              ? "h-full border-r"
              : "border-b lg:h-full lg:border-b-0 lg:border-r",
          ].join(" ")}
        >
          <div
            className={[
              "flex h-16 items-center gap-3 border-b border-border px-4",
              sidebarCollapsed ? "justify-center px-2" : "",
            ].join(" ")}
          >
            <img
              src={logoUrl}
              alt="Cairn"
              className={[
                "h-9 max-w-32 object-contain",
                sidebarCollapsed ? "hidden" : "",
              ].join(" ")}
            />
            <img
              src={iconUrl}
              alt=""
              aria-hidden="true"
              className={[
                "h-9 w-9 object-contain",
                sidebarCollapsed ? "block" : "hidden",
              ].join(" ")}
            />
            <div
              className={["min-w-0", sidebarCollapsed ? "hidden" : ""].join(
                " ",
              )}
            >
              <div className="truncate text-xs text-text-muted">
                {versionLabel}
              </div>
            </div>
          </div>

          <nav
            className={[
              "flex min-h-0 gap-2 px-2 py-3",
              sidebarCollapsed
                ? "flex-1 flex-col space-y-1 overflow-y-auto overflow-x-hidden px-3"
                : "overflow-x-auto lg:flex-1 lg:flex-col lg:space-y-1 lg:overflow-y-auto lg:overflow-x-hidden",
            ].join(" ")}
            aria-label="Main navigation"
          >
            {navItems.map((item) => {
              const Icon = item.icon;
              const active = activePage === item.id;
              const badge =
                item.id === "containers"
                  ? String(containers.length)
                  : undefined;
              return (
                <button
                  key={item.id}
                  className={[
                    "relative flex h-10 shrink-0 items-center gap-3 rounded-control text-left text-sm transition",
                    sidebarCollapsed
                      ? "w-full justify-center px-0"
                      : "w-auto px-3 lg:w-full",
                    active
                      ? "bg-accent/10 text-accent shadow-[inset_3px_0_0_rgb(45_212_167)]"
                      : "text-text-secondary hover:bg-bg-card hover:text-text-primary",
                  ].join(" ")}
                  onClick={() => navigate(item.id)}
                  title={sidebarCollapsed ? item.label : undefined}
                  type="button"
                >
                  <Icon size={18} strokeWidth={1.8} />
                  <span
                    className={[
                      "flex-1 truncate",
                      sidebarCollapsed ? "sr-only" : "",
                    ].join(" ")}
                  >
                    {item.label}
                  </span>
                  {badge ? (
                    sidebarCollapsed ? (
                      <span className="absolute right-0.5 top-0.5 flex h-4 min-w-4 items-center justify-center rounded-full border border-border bg-bg-inset px-1 text-[10px] font-semibold text-text-secondary">
                        {badge}
                      </span>
                    ) : (
                      <Badge>{badge}</Badge>
                    )
                  ) : null}
                </button>
              );
            })}
          </nav>

          <div
            className={[
              "border-t border-border",
              sidebarCollapsed ? "block p-2" : "hidden p-3 lg:block",
            ].join(" ")}
          >
            <Tooltip
              label={sidebarCollapsed ? "Expand sidebar" : "Collapse sidebar"}
            >
              <Button
                aria-label={
                  sidebarCollapsed ? "Expand sidebar" : "Collapse sidebar"
                }
                className={
                  sidebarCollapsed ? "mx-auto" : "w-full justify-start"
                }
                icon={
                  sidebarCollapsed ? (
                    <PanelLeftOpen size={15} />
                  ) : (
                    <PanelLeftClose size={15} />
                  )
                }
                onClick={() => setSidebarCollapsed((current) => !current)}
                size={sidebarCollapsed ? "icon" : "sm"}
                variant="secondary"
              >
                {sidebarCollapsed ? null : "Collapse sidebar"}
              </Button>
            </Tooltip>
            {sidebarCollapsed ? (
              <Tooltip
                label={`Docker Engine: ${statusLabel}. ${providerName}${
                  dockerVersion?.serverVersion
                    ? `. Engine ${dockerVersion.serverVersion}`
                    : ""
                }`}
              >
                <button
                  aria-label={`Docker Engine ${statusLabel}`}
                  className="mx-auto mt-2 flex h-10 w-10 items-center justify-center rounded-control border border-border bg-bg-inset transition hover:border-border-strong"
                  onClick={() => {
                    if (dockerRunning) {
                      return;
                    }
                    if (dockerStopped) {
                      void startProvider();
                    } else if (noProviderConfigured) {
                      openProviderSetup();
                    } else {
                      setRepairOpen(true);
                    }
                  }}
                  type="button"
                >
                  <StatusDot tone={statusTone} />
                </button>
              </Tooltip>
            ) : (
              <div className="mt-3 rounded-card border border-border bg-bg-inset p-3">
                <div className="flex items-center gap-2 text-sm">
                  <StatusDot tone={statusTone} />
                  <span className="font-medium">Docker Engine</span>
                  <span className="ml-auto text-xs text-text-muted">
                    {statusLabel}
                  </span>
                </div>
                <div className="mt-2 truncate font-mono text-xs text-text-muted">
                  {providerName}
                </div>
                <div className="mt-2 truncate text-xs text-text-muted">
                  {dockerVersion?.serverVersion
                    ? `Engine ${dockerVersion.serverVersion}`
                    : "No engine version"}
                </div>
                {!dockerRunning ? (
                  <div className="mt-3 flex gap-2">
                    <Button
                      icon={<Wrench size={14} />}
                      loading={providerActionBusy}
                      onClick={() => {
                        if (dockerStopped) {
                          void startProvider();
                        } else if (noProviderConfigured) {
                          openProviderSetup();
                        } else {
                          setRepairOpen(true);
                        }
                      }}
                      size="sm"
                      variant="secondary"
                    >
                      {dockerStopped
                        ? "Start"
                        : noProviderConfigured
                          ? "Set up"
                          : "Repair"}
                    </Button>
                  </div>
                ) : null}
              </div>
            )}
          </div>
        </aside>

        <section className="flex h-full min-h-0 min-w-0 flex-col overflow-hidden">
          <header className="flex h-auto shrink-0 flex-col items-stretch gap-3 border-b border-border bg-bg-app px-4 py-3 sm:flex-row sm:items-center sm:justify-between lg:h-16 lg:px-6 lg:py-0">
            <div className="min-w-0">
              <h1 className="truncate text-xl font-semibold tracking-normal">
                {pageTitle}
              </h1>
              <p className="truncate text-sm text-text-muted">
                {dockerInfo?.name ?? providerName}
                {lastLoadedAt
                  ? ` - refreshed ${relativeTime(lastLoadedAt)}`
                  : ""}
              </p>
            </div>
            <div className="flex w-full items-center gap-2 sm:w-auto">
              <SearchBox
                inputRef={searchInputRef}
                value={search}
                onChange={setSearch}
              />
              <Tooltip label="Refresh">
                <Button
                  aria-label="Refresh"
                  icon={<RefreshCw size={17} />}
                  loading={
                    activePage === "projects"
                      ? projectsStatus === "loading"
                      : activePage === "updates"
                        ? updatesStatus === "loading" ||
                          updateHistoryStatus === "loading" ||
                          ignoredUpdatesStatus === "loading"
                        : inventoryStatus === "loading"
                  }
                  onClick={() => {
                    if (activePage === "projects") {
                      void refreshProjects();
                    } else if (activePage === "updates") {
                      void refreshUpdateSurfaces();
                    } else {
                      void refreshInventory();
                    }
                  }}
                  size="icon"
                  variant="secondary"
                />
              </Tooltip>
              <div className="relative">
                <Tooltip label="Notifications">
                  <Button
                    aria-label={
                      unreadNotifications > 0
                        ? `Notifications ${unreadNotifications} unread`
                        : "Notifications"
                    }
                    icon={<Bell size={17} />}
                    onClick={() => setNotificationsOpen((current) => !current)}
                    size="icon"
                    variant="secondary"
                  />
                </Tooltip>
                {unreadNotifications > 0 ? (
                  <span className="absolute -right-1 -top-1 flex h-4 min-w-4 items-center justify-center rounded-full bg-warn px-1 text-[10px] font-semibold text-bg-app">
                    {unreadNotifications > 9 ? "9+" : unreadNotifications}
                  </span>
                ) : null}
                <NotificationCenter
                  error={notificationsError}
                  loading={notificationsLoading}
                  notifications={notificationsForDisplay}
                  onClose={() => setNotificationsOpen(false)}
                  onMarkAllRead={() => {
                    void markAllNotificationsRead();
                  }}
                  onNavigate={(page) => {
                    navigate(page);
                    setNotificationsOpen(false);
                  }}
                  open={notificationsOpen}
                />
              </div>
            </div>
          </header>

          <GlobalStateBanner
            appUpdateNotice={appUpdateNotice}
            busy={providerActionBusy}
            dockerStopped={dockerStopped}
            inventoryError={inventoryError}
            noProviderConfigured={noProviderConfigured}
            onOpenAppUpdate={() => {
              if (appUpdateNotice) {
                window.open(appUpdateNotice.url, "_blank", "noopener");
              }
            }}
            onOpenRepair={() => setRepairOpen(true)}
            onOpenSetup={openProviderSetup}
            onRetry={() => {
              void retryProviderDetection();
            }}
            onStart={() => {
              void startProvider();
            }}
            permissionProblem={permissionProblem}
            providerProblems={providerProblems}
            providerRepairNeeded={providerRepairNeeded}
            providerWarnings={providerWarnings}
          />
          {parsedActionError ? (
            <div
              className="flex items-start gap-3 border-b border-error/20 bg-error/10 px-6 py-3 text-sm text-error"
              role="alert"
            >
              <AlertTriangle className="mt-0.5 shrink-0" size={16} />
              <div className="max-h-40 min-w-0 flex-1 overflow-auto break-words leading-5">
                <div className="font-semibold">{parsedActionError.title}</div>
                <div className="mt-1 text-xs">{parsedActionError.body}</div>
                {parsedActionError.detail ? (
                  <pre className="mt-2 whitespace-pre-wrap font-mono text-xs">
                    {parsedActionError.detail}
                  </pre>
                ) : null}
              </div>
              <Tooltip label="Dismiss error">
                <Button
                  aria-label="Dismiss error"
                  icon={<X size={16} />}
                  onClick={() => setActionError(null)}
                  size="icon"
                  variant="ghost"
                />
              </Tooltip>
            </div>
          ) : null}

          <div
            className="min-h-0 flex-1 overflow-auto p-6"
            data-testid="app-scroll-region"
          >
            <DegradedFrame stale={staleMode}>{content}</DegradedFrame>
          </div>
        </section>
      </div>

      <ToastViewport toasts={toasts} />

      <InspectModal
        inspect={inspect}
        onClose={() => setInspect(emptyInspect)}
      />
      <CommandPalette
        activePage={activePage}
        onClose={() => setPaletteOpen(false)}
        onNavigate={navigate}
        onRunSafeCommand={runPaletteCommand}
        open={paletteOpen}
        pages={navItems}
      />
      <ConfirmPlanModal
        confirm={confirm}
        onApply={() => {
          void applyConfirmedPlan();
        }}
        onChangeTypedName={(typedName) =>
          setConfirm((current) => ({ ...current, typedName }))
        }
        onClose={() => setConfirm(emptyConfirm)}
      />
      <RemoveProjectModal
        onClose={() => setRemoveProject(emptyRemoveProject)}
        onConfirm={() => {
          void confirmRemoveProject();
        }}
        state={removeProject}
      />
      <UpdatePlanModal
        onApply={() => {
          void applyUpdatePlan();
        }}
        onChange={(patch) =>
          setUpdatePlan((current) => ({ ...current, ...patch }))
        }
        onClose={() => setUpdatePlan(emptyUpdatePlan)}
        state={updatePlan}
      />
      <IgnoreUpdateModal
        onChange={(patch) =>
          setIgnoreUpdate((current) => ({ ...current, ...patch }))
        }
        onClose={() => setIgnoreUpdate(emptyIgnoreUpdate)}
        onSubmit={() => {
          void submitIgnoreUpdate();
        }}
        state={ignoreUpdate}
      />
      <RepairProviderModal
        busy={repairSaving || providerActionBusy}
        error={repairError}
        onChangePermissionMode={setPermissionMode}
        onClose={() => setRepairOpen(false)}
        onOpenSetup={openProviderSetupForRepair}
        onRetry={() => {
          void retryProviderDetection();
        }}
        onSavePermission={() => {
          void savePermissionMode();
        }}
        open={repairOpen}
        permissionMode={permissionMode}
        permissionProblem={permissionProblem}
        problems={providerProblems}
        provider={activeProvider}
      />
      <ProviderSetupModal
        onApplyInstall={() => {
          void applyProviderInstall();
        }}
        onAddProjectFolder={openSetupProjectImport}
        onChangeBackend={changeSetupBackend}
        onChangeColimaCPU={(colimaCPU) =>
          setSetup((current) => ({ ...current, colimaCPU }))
        }
        onChangeColimaDiskGB={(colimaDiskGB) =>
          setSetup((current) => ({ ...current, colimaDiskGB }))
        }
        onChangeColimaMemoryGB={(colimaMemoryGB) =>
          setSetup((current) => ({ ...current, colimaMemoryGB }))
        }
        onChangeColimaProfile={(colimaProfile) =>
          setSetup((current) => ({ ...current, colimaProfile }))
        }
        onChangeDistro={(distro) =>
          setSetup((current) => ({ ...current, distro }))
        }
        onChangePermissionMode={setPermissionMode}
        onClose={closeProviderSetup}
        onDetectProjects={() => {
          void detectSetupProjects();
        }}
        onFinish={finishProviderSetup}
        onOpenDockerContexts={openDockerContextsSettings}
        onPlanInstall={() => {
          void planProviderInstall();
        }}
        onRunChecks={() => {
          void runProviderSetupChecks();
        }}
        onSavePermission={() => {
          void saveSetting("linux.sudo_mode", permissionMode);
        }}
        onStep={(step) => setSetup((current) => ({ ...current, step }))}
        onToggleProject={toggleSetupProjectSelection}
        onUseExistingContext={useExistingDockerContext}
        open={setup.open}
        permissionError={settingsError}
        permissionMode={permissionMode}
        permissionSaving={settingsSaving}
        setup={setup}
      />
      <RenameContainerModal
        onChange={(name) => setRename((current) => ({ ...current, name }))}
        onClose={() => setRename(emptyRename)}
        onSubmit={() => {
          void submitRename();
        }}
        state={rename}
      />
      <RunImageModal
        networks={networks}
        onAddAutoPort={() =>
          setRunImage((current) => ({
            ...current,
            portsText: appendLine(current.portsText, "0:80/tcp"),
          }))
        }
        onBack={() => setRunImage((current) => ({ ...current, step: 1 }))}
        onChange={(patch) =>
          setRunImage((current) => ({ ...current, ...patch }))
        }
        onClose={() => setRunImage(emptyRunImage)}
        onSelectHubResult={(result) =>
          setRunImage((current) => ({
            ...current,
            imageRef: `${result.name}:latest`,
            name: current.name || suggestContainerName(result.name),
            hubQuery: result.name,
            hubResults: [],
          }))
        }
        onSubmit={() => {
          if (runImage.step === 1) {
            setRunImage((current) => ({
              ...current,
              step: 2,
              error: undefined,
            }));
            return;
          }
          void submitRunImage();
        }}
        state={runImage}
      />
      <PullImageModal
        onChange={(patch) =>
          setPullImage((current) => ({ ...current, ...patch }))
        }
        onClose={() => setPullImage(emptyPullImage)}
        onSelectResult={(result) =>
          setPullImage((current) => ({
            ...current,
            ref: result.name,
            query: result.name,
            results: [],
          }))
        }
        onSubmit={() => {
          void submitPullImage();
        }}
        state={pullImage}
      />
      <TagImageModal
        onChange={(patch) =>
          setTagImage((current) => ({ ...current, ...patch }))
        }
        onClose={() => setTagImage(emptyTagImage)}
        onSubmit={() => {
          void submitTagImage();
        }}
        state={tagImage}
      />
      <PushImageModal
        accounts={registryAccounts}
        accountsLoading={registryAccountsStatus === "loading"}
        onChange={(patch) =>
          setPushImage((current) => ({ ...current, ...patch }))
        }
        onClose={() => setPushImage(emptyPushImage)}
        onCopyPull={(ref) => {
          void Clipboard.SetText(`docker pull ${ref}`);
        }}
        onLogin={openRegistryLogin}
        onRefreshAccounts={() => {
          void refreshRegistryAccounts();
        }}
        onSubmit={() => {
          void submitPushImage();
        }}
        state={pushImage}
      />
      <SaveImageModal
        onChange={(patch) =>
          setSaveImage((current) => ({ ...current, ...patch }))
        }
        onClose={() => setSaveImage(emptySaveImage)}
        onSubmit={() => {
          void submitSaveImage();
        }}
        state={saveImage}
      />
      <LoadImageModal
        onChange={(patch) =>
          setLoadImage((current) => ({ ...current, ...patch }))
        }
        onClose={() => setLoadImage(emptyLoadImage)}
        onSubmit={() => {
          void submitLoadImage();
        }}
        state={loadImage}
      />
      <RegistryLoginModal
        onChange={(patch) =>
          setRegistryLogin((current) => ({ ...current, ...patch }))
        }
        onClose={() => setRegistryLogin(emptyRegistryLogin)}
        onSubmit={() => {
          void submitRegistryLogin();
        }}
        presets={registryPresets}
        state={registryLogin}
      />
      <CreateVolumeModal
        onChange={(patch) =>
          setCreateVolume((current) => ({ ...current, ...patch }))
        }
        onClose={() => setCreateVolume(emptyCreateVolume)}
        onSubmit={() => {
          void submitCreateVolume();
        }}
        state={createVolume}
      />
      <BackupVolumeModal
        onChange={(patch) =>
          setBackupVolume((current) => ({ ...current, ...patch }))
        }
        onClose={() => setBackupVolume(emptyBackupVolume)}
        onSubmit={() => {
          void submitBackupVolume();
        }}
        state={backupVolume}
      />
      <RestoreVolumeModal
        onChange={(patch) =>
          setRestoreVolume((current) => ({ ...current, ...patch }))
        }
        onClose={() => setRestoreVolume(emptyRestoreVolume)}
        onSubmit={() => {
          void submitRestoreVolume();
        }}
        state={restoreVolume}
      />
      <CreateNetworkModal
        onChange={(patch) =>
          setCreateNetwork((current) => ({ ...current, ...patch }))
        }
        onClose={() => setCreateNetwork(emptyCreateNetwork)}
        onSubmit={() => {
          void submitCreateNetwork();
        }}
        state={createNetwork}
      />
      <ImportProjectModal
        onBrowse={() => {
          void browseImportFolder();
        }}
        onChange={(folderPath) =>
          setImportProject((current) => ({
            ...current,
            folderPath,
            error: undefined,
            imported: null,
          }))
        }
        onClose={() => setImportProject(emptyImportProject)}
        onSubmit={() => {
          void submitImportProject();
        }}
        state={importProject}
      />
    </main>
  );
}

function GlobalStateBanner({
  appUpdateNotice,
  busy,
  dockerStopped,
  inventoryError,
  noProviderConfigured,
  onOpenAppUpdate,
  onOpenRepair,
  onOpenSetup,
  onRetry,
  onStart,
  permissionProblem,
  providerProblems,
  providerRepairNeeded,
  providerWarnings,
}: {
  appUpdateNotice: AppUpdateNotice | null;
  busy: boolean;
  dockerStopped: boolean;
  inventoryError: string | null;
  noProviderConfigured: boolean;
  onOpenAppUpdate: () => void;
  onOpenRepair: () => void;
  onOpenSetup: () => void;
  onRetry: () => void;
  onStart: () => void;
  permissionProblem: ProviderProblem | null;
  providerProblems: ProviderProblem[];
  providerRepairNeeded: boolean;
  providerWarnings: Array<{ code: string; message: string }>;
}) {
  const primaryProblem = permissionProblem ?? providerProblems[0] ?? null;
  const warning = providerWarnings[0] ?? null;
  const state = providerRepairNeeded
    ? {
        tone: "error" as const,
        icon: <ShieldAlert size={17} />,
        title: primaryProblem?.message ?? "Provider repair is required",
        body:
          primaryProblem?.repairHint ??
          "Review the provider checks and choose a repair path.",
        action: null,
      }
    : noProviderConfigured
      ? {
          tone: "warn" as const,
          icon: <AlertTriangle size={17} />,
          title: "No Docker provider configured",
          body: "Set up a provider before running Docker actions.",
          action: null,
        }
      : dockerStopped || inventoryError
        ? {
            tone: "warn" as const,
            icon: <AlertTriangle size={17} />,
            title: "Docker is not reachable",
            body:
              inventoryError ??
              "Cached data is visible; Docker actions are disabled until the engine is running.",
            action: null,
          }
        : warning
          ? {
              tone: "info" as const,
              icon: <AlertTriangle size={17} />,
              title: warning.message,
              body: "Provider warning",
              action: null,
            }
          : appUpdateNotice
            ? {
                tone: "info" as const,
                icon: <Download size={17} />,
                title: `Cairn ${appUpdateNotice.version} is available`,
                body:
                  appUpdateNotice.name ??
                  "A new desktop app release is ready to download.",
                action: {
                  label: "Download",
                  onClick: onOpenAppUpdate,
                },
              }
            : null;

  if (!state) {
    return null;
  }

  const toneClass =
    state.tone === "error"
      ? "border-error/30 bg-error/10 text-error"
      : state.tone === "warn"
        ? "border-warn/30 bg-warn/10 text-warn"
        : "border-info/30 bg-info/10 text-info";

  return (
    <div className={`shrink-0 border-b px-6 py-3 ${toneClass}`}>
      <div className="flex flex-wrap items-center gap-3 text-sm">
        <span className="shrink-0">{state.icon}</span>
        <div className="min-w-0 flex-1">
          <div className="font-medium">{state.title}</div>
          <div className="break-words text-xs opacity-90">{state.body}</div>
        </div>
        {providerRepairNeeded ? (
          <Button
            icon={<Wrench size={15} />}
            onClick={onOpenRepair}
            size="sm"
            variant="secondary"
          >
            Repair
          </Button>
        ) : null}
        {noProviderConfigured ? (
          <Button
            icon={<Wrench size={15} />}
            onClick={onOpenSetup}
            size="sm"
            variant="secondary"
          >
            Set up
          </Button>
        ) : null}
        {dockerStopped ? (
          <Button
            icon={<Play size={15} />}
            loading={busy}
            onClick={onStart}
            size="sm"
            variant="secondary"
          >
            Start
          </Button>
        ) : null}
        {state.action ? (
          <Button
            icon={<Download size={15} />}
            onClick={state.action.onClick}
            size="sm"
            variant="secondary"
          >
            {state.action.label}
          </Button>
        ) : (
          <Button
            icon={<RefreshCw size={15} />}
            loading={busy}
            onClick={onRetry}
            size="sm"
            variant="secondary"
          >
            Retry
          </Button>
        )}
      </div>
    </div>
  );
}

function DegradedFrame({
  children,
  stale,
}: {
  children: ReactNode;
  stale: boolean;
}) {
  if (!stale) {
    return <>{children}</>;
  }
  return (
    <div className="relative">
      <div className="pointer-events-none absolute right-3 top-3 z-10 rounded-control border border-neutral/30 bg-bg-panel/95 px-3 py-1 text-xs font-medium uppercase text-text-muted shadow-lg">
        Stale cached data
      </div>
      <div className="opacity-80 grayscale-[0.25]">{children}</div>
    </div>
  );
}

function RepairProviderModal({
  busy,
  error,
  onChangePermissionMode,
  onClose,
  onOpenSetup,
  onRetry,
  onSavePermission,
  open,
  permissionMode,
  permissionProblem,
  problems,
  provider,
}: {
  busy: boolean;
  error: string | null;
  onChangePermissionMode: (mode: PermissionMode) => void;
  onClose: () => void;
  onOpenSetup: () => void;
  onRetry: () => void;
  onSavePermission: () => void;
  open: boolean;
  permissionMode: PermissionMode;
  permissionProblem: ProviderProblem | null;
  problems: ProviderProblem[];
  provider: ProviderSummary | null;
}) {
  return (
    <Modal
      onClose={onClose}
      open={open}
      size="lg"
      title="Repair Docker Provider"
    >
      <div className="space-y-5">
        <div className="flex items-start gap-3 rounded-card border border-border bg-bg-inset p-4">
          <Wrench className="mt-0.5 text-accent" size={19} />
          <div className="min-w-0">
            <div className="font-medium text-text-primary">
              {provider?.name ?? "No provider selected"}
            </div>
            <div className="mt-1 text-sm text-text-muted">
              Provider checks list the exact failure and repair hint from the
              backend.
            </div>
          </div>
        </div>

        {problems.length > 0 ? (
          <div className="space-y-2">
            {problems.map((problem) => (
              <div
                className="rounded-card border border-error/25 bg-error/10 p-3 text-sm"
                key={`${problem.code}:${problem.message}`}
              >
                <div className="flex items-center gap-2 font-medium text-error">
                  <AlertTriangle size={16} />
                  {problem.message}
                </div>
                {problem.repairHint ? (
                  <div className="mt-2 text-text-secondary">
                    {problem.repairHint}
                  </div>
                ) : null}
                <div className="mt-2 text-xs uppercase text-text-muted">
                  {problem.code}
                </div>
              </div>
            ))}
          </div>
        ) : (
          <EmptyState
            body="Run detection again or set up a provider from onboarding when no provider exists."
            icon={<CheckCircle2 size={28} />}
            title="No provider problems recorded"
          />
        )}

        {permissionProblem ? (
          <section className="space-y-3">
            <div>
              <h3 className="text-sm font-semibold text-text-primary">
                Linux Docker permission options
              </h3>
              <p className="mt-1 text-sm text-text-muted">
                Socket access was denied. Pick how Cairn should work with this
                Linux backend.
              </p>
            </div>
            <div className="grid gap-2">
              <PermissionOption
                checked={permissionMode === "ask"}
                description="Cairn prompts only when an action needs sudo. The sudo password is never stored."
                label="Use sudo per action"
                onChange={() => onChangePermissionMode("ask")}
                value="ask"
              />
              <PermissionOption
                checked={permissionMode === "group"}
                description="Convenient, less isolated. The docker group is root-equivalent and requires signing out and back in."
                label="Add user to docker group"
                onChange={() => onChangePermissionMode("group")}
                value="group"
              />
              <PermissionOption
                checked={permissionMode === "rootless"}
                description="Use the rootless Docker socket when rootless Docker is already configured."
                label="Use rootless Docker socket"
                onChange={() => onChangePermissionMode("rootless")}
                value="rootless"
              />
            </div>
          </section>
        ) : null}

        {error ? <div className="text-sm text-error">{error}</div> : null}

        <div className="flex justify-end gap-2 border-t border-border pt-4">
          <Button disabled={busy} onClick={onClose} variant="secondary">
            Close
          </Button>
          <Button
            icon={<RefreshCw size={15} />}
            loading={busy}
            onClick={onRetry}
            variant="secondary"
          >
            Detect again
          </Button>
          {permissionProblem ? (
            <Button loading={busy} onClick={onSavePermission} variant="primary">
              Save permission mode
            </Button>
          ) : null}
          <Button
            disabled={busy}
            icon={<Wrench size={15} />}
            onClick={onOpenSetup}
          >
            Auto repair / update
          </Button>
        </div>
      </div>
    </Modal>
  );
}

function ProviderSetupModal({
  onApplyInstall,
  onAddProjectFolder,
  onChangeBackend,
  onChangeColimaCPU,
  onChangeColimaDiskGB,
  onChangeColimaMemoryGB,
  onChangeColimaProfile,
  onChangeDistro,
  onChangePermissionMode,
  onClose,
  onDetectProjects,
  onFinish,
  onOpenDockerContexts,
  onPlanInstall,
  onRunChecks,
  onSavePermission,
  onStep,
  onToggleProject,
  onUseExistingContext,
  open,
  permissionError,
  permissionMode,
  permissionSaving,
  setup,
}: {
  open: boolean;
  setup: ProviderSetupState;
  onApplyInstall: () => void;
  onAddProjectFolder: () => void;
  onChangeBackend: (backend: SetupBackendID) => void;
  onChangeColimaCPU: (value: number) => void;
  onChangeColimaDiskGB: (value: number) => void;
  onChangeColimaMemoryGB: (value: number) => void;
  onChangeColimaProfile: (profile: string) => void;
  onChangeDistro: (distro: string) => void;
  onChangePermissionMode: (mode: PermissionMode) => void;
  onClose: () => void;
  onDetectProjects: () => void;
  onFinish: () => void;
  onOpenDockerContexts: () => void;
  onPlanInstall: () => void;
  onRunChecks: () => void;
  onSavePermission: () => void;
  onStep: (step: SetupStepID) => void;
  onToggleProject: (projectID: string) => void;
  onUseExistingContext: () => void;
  permissionError: string | null;
  permissionMode: PermissionMode;
  permissionSaving: boolean;
}) {
  const rows =
    setup.backend === "linux_native"
      ? linuxSetupCheckRows(setup.detection)
      : setup.backend === "macos_colima"
        ? macOSSetupCheckRows(setup.detection)
        : windowsSetupCheckRows(setup.detection);
  const hasProblems = Boolean(setup.detection?.problems?.length);
  const hasBackendUpdates = providerHasWarning(
    setup.detection,
    "DOCKER_PACKAGES_OUTDATED",
  );
  const hasNVIDIARuntimeRepair = providerHasWarning(
    setup.detection,
    "NVIDIA_RUNTIME_MISSING",
  );
  const hasActionableWarnings = hasBackendUpdates || hasNVIDIARuntimeRepair;
  const canPlan = !setup.detecting && Boolean(setup.detection);
  const repairActionLabel = hasProblems
    ? "Review auto repair"
    : hasNVIDIARuntimeRepair
      ? "Review GPU runtime install"
      : hasBackendUpdates
        ? "Review backend update"
        : "Review repair / update";
  const completed =
    setup.detection?.healthy ||
    setup.progress.some((entry) => entry.done && !entry.error);
  const isMac = setup.backend === "macos_colima";
  const isLinux = setup.backend === "linux_native";
  const isWindows = setup.backend === "windows_wsl_ubuntu";
  const permissionProblem =
    setup.detection?.problems?.find(
      (problem) => problem.code === "PERM_SOCKET",
    ) ?? null;
  const selectedProjects = setup.detectedProjects.filter((project) =>
    setup.selectedProjectIDs.includes(project.id),
  );
  const setupSteps: SetupStepID[] = [
    "welcome",
    "backend",
    "checks",
    "install",
    "verify",
    "projects",
    "done",
  ];

  return (
    <Modal
      onClose={onClose}
      open={open}
      size="lg"
      title="Set Up Docker Backend"
    >
      <div className="space-y-5">
        <div className="flex flex-wrap gap-2">
          {setupSteps.map((step, index) => (
            <button
              aria-disabled={setup.installing}
              className={[
                "flex h-8 items-center gap-2 rounded-control border px-3 text-xs font-medium",
                setup.step === step
                  ? "border-accent/40 bg-accent/10 text-accent"
                  : "border-border bg-bg-inset text-text-muted",
                setup.installing ? "cursor-not-allowed opacity-70" : "",
              ].join(" ")}
              key={step}
              onClick={() => {
                if (!setup.installing) {
                  onStep(step);
                }
              }}
              type="button"
            >
              <span>{index + 1}</span>
              <span>{setupStepLabel(step)}</span>
            </button>
          ))}
        </div>

        {setup.step === "welcome" ? (
          <section className="space-y-4">
            <div className="flex items-center gap-4">
              <img alt="Cairn" className="h-14 w-auto" src={logoUrl} />
              <div>
                <h2 className="text-lg font-semibold text-text-primary">
                  Clean control for Docker and Compose
                </h2>
                <p className="mt-1 text-sm text-text-muted">
                  Cairn uses the Docker backend you already trust and keeps
                  provider setup explicit.
                </p>
              </div>
            </div>
            <div className="flex flex-wrap gap-2">
              <Button
                icon={<Play size={15} />}
                onClick={() => onStep("backend")}
              >
                Get started
              </Button>
              <Button
                icon={<RefreshCw size={15} />}
                onClick={onUseExistingContext}
                variant="secondary"
              >
                I already have Docker running
              </Button>
            </div>
          </section>
        ) : null}

        {setup.step === "backend" ? (
          <section className="grid gap-3 md:grid-cols-3">
            {setup.platform === "macos" ? (
              <>
                <BackendChoiceCard
                  badge="Recommended"
                  body="Install or use Colima with Homebrew-managed Docker CLI, Compose, and Buildx."
                  details="Docker CLI, Docker Compose, Colima, selected profile resources, Docker context, and hello-world verification."
                  icon={<Server size={19} />}
                  onSelect={() => onChangeBackend("macos_colima")}
                  title="Colima"
                />
                <BackendChoiceCard
                  body="Use a Docker Desktop, OrbStack, Rancher Desktop, or remote Docker context without changing your global context."
                  details="No packages are installed. Cairn lists contexts, pings the selected one, and runs Docker with --context."
                  icon={<Terminal size={19} />}
                  onSelect={() => onChangeBackend("existing_context")}
                  title="Existing Docker context"
                />
                <BackendChoiceCard
                  body="Remote host setup is outside the v1 MVP flow."
                  disabled
                  icon={<Wifi size={19} />}
                  title="Remote host"
                />
              </>
            ) : setup.platform === "linux" ? (
              <>
                <BackendChoiceCard
                  badge="Recommended"
                  body="Install or use Docker Engine directly on this Linux host with the official apt repository."
                  details="Docker CLI, Docker Engine, containerd, Compose, Buildx, systemd service wiring, socket access, and hello-world verification."
                  icon={<Server size={19} />}
                  onSelect={() => onChangeBackend("linux_native")}
                  title="Native Docker Engine"
                />
                <BackendChoiceCard
                  body="Use an existing Docker context without changing your global Docker context."
                  details="No packages are installed. Cairn runs Docker and Compose with --context."
                  icon={<Terminal size={19} />}
                  onSelect={() => onChangeBackend("existing_context")}
                  title="Existing Docker context"
                />
                <BackendChoiceCard
                  body="Remote hosts are outside the v1 MVP setup flow."
                  disabled
                  icon={<Wifi size={19} />}
                  title="Remote host"
                />
              </>
            ) : (
              <>
                <BackendChoiceCard
                  badge="Recommended"
                  body="Install or use Ubuntu on WSL2 with official Docker Engine packages inside the distro."
                  details="WSL2, Ubuntu, Docker Engine, Compose, Buildx, systemd service wiring, and docker-group access."
                  icon={<Server size={19} />}
                  onSelect={() => onChangeBackend("windows_wsl_ubuntu")}
                  title="Ubuntu on WSL2"
                />
                <BackendChoiceCard
                  body="Use an existing Docker context without changing your global Docker context."
                  details="No packages are installed. Cairn runs Docker and Compose with --context."
                  icon={<Terminal size={19} />}
                  onSelect={() => onChangeBackend("existing_context")}
                  title="Existing Docker context"
                />
                <BackendChoiceCard
                  body="Remote hosts are outside the v1 MVP setup flow."
                  disabled
                  icon={<Wifi size={19} />}
                  title="Remote host"
                />
              </>
            )}
          </section>
        ) : null}

        {setup.step === "checks" ? (
          <section className="space-y-4">
            {setup.backend === "existing_context" ? (
              <EmptyState
                body="Existing contexts are selected from Settings -> Docker contexts so Cairn can switch providers without changing docker context show."
                icon={<Terminal size={28} />}
                title="Use an existing Docker context"
              />
            ) : (
              <>
                <div className="grid gap-3 md:grid-cols-[1fr_auto] md:items-end">
                  {isMac ? (
                    <div className="grid gap-3 sm:grid-cols-4">
                      <label className="block sm:col-span-1">
                        <span className="text-xs font-medium uppercase text-text-muted">
                          Profile
                        </span>
                        <input
                          className="mt-1 h-9 w-full rounded-control border border-border bg-bg-inset px-3 text-sm text-text-primary outline-none"
                          onChange={(event) =>
                            onChangeColimaProfile(event.target.value)
                          }
                          placeholder="default"
                          value={setup.colimaProfile}
                        />
                      </label>
                      <SetupNumberField
                        label="CPU"
                        onChange={onChangeColimaCPU}
                        value={setup.colimaCPU}
                      />
                      <SetupNumberField
                        label="RAM GB"
                        onChange={onChangeColimaMemoryGB}
                        value={setup.colimaMemoryGB}
                      />
                      <SetupNumberField
                        label="Disk GB"
                        onChange={onChangeColimaDiskGB}
                        value={setup.colimaDiskGB}
                      />
                    </div>
                  ) : isWindows ? (
                    <label className="block">
                      <span className="text-xs font-medium uppercase text-text-muted">
                        WSL distro
                      </span>
                      <input
                        className="mt-1 h-9 w-full rounded-control border border-border bg-bg-inset px-3 text-sm text-text-primary outline-none"
                        onChange={(event) => onChangeDistro(event.target.value)}
                        placeholder="Ubuntu"
                        value={setup.distro}
                      />
                    </label>
                  ) : (
                    <div className="rounded-card border border-border bg-bg-inset p-3 text-sm text-text-secondary">
                      <div className="font-medium text-text-primary">
                        Linux socket
                      </div>
                      <div className="mt-1">
                        Cairn checks the configured Linux socket and Docker
                        Engine packages. Socket path and sudo mode remain in
                        Settings so they apply across repairs and future runs.
                      </div>
                    </div>
                  )}
                  <Button
                    icon={<RefreshCw size={15} />}
                    loading={setup.detecting}
                    onClick={onRunChecks}
                  >
                    Run checks
                  </Button>
                </div>
                {isMac ? (
                  <ColimaPathRecommendation />
                ) : isLinux ? (
                  <LinuxPathRecommendation />
                ) : (
                  <PathRecommendation />
                )}
                {isLinux && permissionProblem ? (
                  <section className="space-y-3 rounded-card border border-warn/30 bg-warn/10 p-3">
                    <div>
                      <h3 className="text-sm font-semibold text-text-primary">
                        Linux Docker permission options
                      </h3>
                      <p className="mt-1 text-sm text-text-muted">
                        Socket access was denied. Pick how Cairn should work
                        with this Linux backend, then rerun checks.
                      </p>
                    </div>
                    <div className="grid gap-2">
                      <PermissionOption
                        checked={permissionMode === "ask"}
                        description="Cairn prompts only when an action needs sudo. The sudo password is never stored."
                        label="Use sudo per action"
                        onChange={() => onChangePermissionMode("ask")}
                        value="ask"
                      />
                      <PermissionOption
                        checked={permissionMode === "group"}
                        description="Convenient, less isolated. The docker group is root-equivalent and requires signing out and back in."
                        label="Add user to docker group"
                        onChange={() => onChangePermissionMode("group")}
                        value="group"
                      />
                      <PermissionOption
                        checked={permissionMode === "rootless"}
                        description="Use the rootless Docker socket when rootless Docker is already configured."
                        label="Use rootless Docker socket"
                        onChange={() => onChangePermissionMode("rootless")}
                        value="rootless"
                      />
                    </div>
                    {permissionError ? (
                      <div className="text-sm text-error">
                        {permissionError}
                      </div>
                    ) : null}
                    <div className="flex justify-end">
                      <Button
                        loading={permissionSaving}
                        onClick={onSavePermission}
                      >
                        Save permission mode
                      </Button>
                    </div>
                  </section>
                ) : null}
              </>
            )}
            {setup.detectError ? (
              <div className="rounded-card border border-error/30 bg-error/10 px-3 py-2 text-sm text-error">
                {setup.detectError}
              </div>
            ) : null}
            {setup.backend !== "existing_context" ? (
              <div className="grid gap-2">
                {rows.map((row) => (
                  <SetupCheckRow key={row.label} row={row} />
                ))}
              </div>
            ) : null}
            <div className="flex justify-end gap-2 border-t border-border pt-4">
              <Button onClick={() => onStep("backend")} variant="secondary">
                Back
              </Button>
              {setup.backend === "existing_context" ? (
                <Button onClick={onOpenDockerContexts}>Open Settings</Button>
              ) : null}
              {setup.detection?.healthy && !hasActionableWarnings ? (
                <Button
                  icon={<CheckCircle2 size={15} />}
                  onClick={() => onStep("verify")}
                >
                  Continue
                </Button>
              ) : setup.backend !== "existing_context" ? (
                <Button
                  disabled={!canPlan}
                  disabledReason="Run checks before creating a repair plan"
                  icon={<Wrench size={15} />}
                  loading={setup.planning}
                  onClick={onPlanInstall}
                >
                  {repairActionLabel}
                </Button>
              ) : null}
            </div>
          </section>
        ) : null}

        {setup.step === "install" ? (
          <section className="space-y-4">
            {setup.plan ? (
              <div className="space-y-3">
                <div>
                  <h2 className="text-base font-semibold text-text-primary">
                    {setup.plan.title}
                  </h2>
                  <p className="mt-1 text-sm text-text-muted">
                    {isMac
                      ? "Homebrew may prompt for system approval while packages install."
                      : isLinux
                        ? "Linux may ask for sudo approval while packages install."
                        : "Windows may ask for administrator approval when WSL features are enabled."}
                  </p>
                  <p className="mt-1 text-sm text-text-muted">
                    Cairn will run the selected commands after confirmation.
                    Nothing needs to be copied into a terminal.
                  </p>
                </div>
                <div className="space-y-2">
                  {setup.plan.commands.map((command) => (
                    <details
                      className="rounded-card border border-border bg-bg-inset p-3"
                      key={`${command.order}:${command.command}`}
                    >
                      <summary className="flex cursor-pointer items-center gap-2 text-sm">
                        <Badge tone="warn">Step {command.order}</Badge>
                        <span className="font-medium text-text-primary">
                          {command.explanation}
                        </span>
                      </summary>
                      <div className="mt-3">
                        <CodePreview value={command.command} />
                      </div>
                    </details>
                  ))}
                </div>
              </div>
            ) : (
              <EmptyState
                body="Run checks and create a plan before installation starts."
                icon={<Wrench size={28} />}
                title="No install plan yet"
              />
            )}
            {setup.progress.length > 0 ? (
              <div className="space-y-2">
                {setup.progress.map((entry, index) => (
                  <div
                    className={[
                      "rounded-card border px-3 py-2 text-sm",
                      entry.error
                        ? "border-error/30 bg-error/10 text-error"
                        : entry.done
                          ? "border-ok/30 bg-ok/10 text-ok"
                          : "border-info/30 bg-info/10 text-info",
                    ].join(" ")}
                    key={`${entry.streamID}:${index}`}
                  >
                    {entry.message}
                    {entry.totalSteps ? (
                      <span className="ml-2 text-xs opacity-80">
                        {entry.step}/{entry.totalSteps}
                      </span>
                    ) : null}
                    {entry.error ? (
                      <div className="mt-1">{entry.error}</div>
                    ) : null}
                  </div>
                ))}
              </div>
            ) : null}
            {setup.error ? (
              <div className="rounded-card border border-error/30 bg-error/10 px-3 py-2 text-sm text-error">
                {setup.error}
              </div>
            ) : null}
            <div className="flex justify-end gap-2 border-t border-border pt-4">
              <Button
                disabled={setup.installing}
                onClick={() => onStep("checks")}
                variant="secondary"
              >
                Back
              </Button>
              <Button
                disabled={!setup.plan || setup.installing}
                disabledReason="Create an install plan first"
                icon={<Play size={15} />}
                loading={setup.installing}
                onClick={onApplyInstall}
              >
                Run auto repair
              </Button>
            </div>
          </section>
        ) : null}

        {setup.step === "verify" ? (
          <section className="space-y-4">
            <div className="rounded-card border border-ok/30 bg-ok/10 p-4">
              <div className="flex items-center gap-2 font-medium text-ok">
                <CheckCircle2 size={17} />
                {completed
                  ? setupReadyMessage(setup.backend)
                  : "Provider checks complete"}
              </div>
              <div className="mt-2 grid gap-2 text-sm text-text-secondary sm:grid-cols-2">
                <span>Provider: {setupBackendLabel(setup.backend)}</span>
                <span>{setupBackendDetail(setup)}</span>
                <span>
                  Docker:{" "}
                  {setup.detection?.dockerVersion || "verified after install"}
                </span>
                <span>
                  Compose:{" "}
                  {setup.detection?.composeVersion || "verified after install"}
                </span>
                <span>
                  Context: {setup.detection?.currentContext || "default"}
                </span>
                <span>
                  Host:{" "}
                  {setup.detection?.dockerHost ||
                    setupBackendHostFallback(setup.backend)}
                </span>
                <span>Hello-world: {completed ? "Passed" : "Pending"}</span>
              </div>
            </div>
            {isMac ? (
              <ColimaPathRecommendation />
            ) : isLinux ? (
              <LinuxPathRecommendation />
            ) : (
              <PathRecommendation />
            )}
            <div className="flex justify-end gap-2 border-t border-border pt-4">
              <Button
                onClick={() => onStep(setup.plan ? "install" : "checks")}
                variant="secondary"
              >
                Back
              </Button>
              <Button
                icon={<FolderOpen size={15} />}
                loading={setup.detectingProjects}
                onClick={onDetectProjects}
              >
                Detect projects
              </Button>
            </div>
          </section>
        ) : null}

        {setup.step === "projects" ? (
          <section className="space-y-4">
            <div>
              <h2 className="text-base font-semibold text-text-primary">
                Detect Compose projects
              </h2>
              <p className="mt-1 text-sm text-text-muted">
                Cairn refreshes known Compose projects for the selected backend.
                You can keep the detected projects selected, import another
                folder, or skip this step.
              </p>
            </div>
            <div className="flex flex-wrap gap-2">
              <Button
                icon={<RefreshCw size={15} />}
                loading={setup.detectingProjects}
                onClick={onDetectProjects}
                variant="secondary"
              >
                Refresh detection
              </Button>
              <Button
                icon={<FolderOpen size={15} />}
                onClick={onAddProjectFolder}
                variant="secondary"
              >
                Add folder...
              </Button>
            </div>
            {setup.projectDetectError ? (
              <div className="rounded-card border border-error/30 bg-error/10 px-3 py-2 text-sm text-error">
                {setup.projectDetectError}
              </div>
            ) : null}
            {setup.detectedProjects.length > 0 ? (
              <div className="space-y-2">
                <div className="text-sm font-medium text-text-primary">
                  Found {setup.detectedProjects.length} Compose{" "}
                  {setup.detectedProjects.length === 1 ? "project" : "projects"}
                </div>
                {setup.detectedProjects.map((project) => (
                  <label
                    className="flex items-start gap-3 rounded-card border border-border bg-bg-inset p-3 text-sm"
                    key={project.id}
                  >
                    <input
                      checked={setup.selectedProjectIDs.includes(project.id)}
                      className="mt-1"
                      onChange={() => onToggleProject(project.id)}
                      type="checkbox"
                    />
                    <span className="min-w-0">
                      <span className="block font-medium text-text-primary">
                        {project.name}
                      </span>
                      <span className="block truncate text-text-muted">
                        {project.workingDir || project.id}
                      </span>
                    </span>
                  </label>
                ))}
              </div>
            ) : (
              <EmptyState
                body="No Compose projects are registered yet. You can import a folder manually or continue to the dashboard."
                icon={<FolderOpen size={28} />}
                title="No projects detected"
              />
            )}
            <div className="flex justify-end gap-2 border-t border-border pt-4">
              <Button onClick={() => onStep("verify")} variant="secondary">
                Back
              </Button>
              <Button onClick={() => onStep("done")} variant="secondary">
                Skip
              </Button>
              <Button
                icon={<CheckCircle2 size={15} />}
                onClick={() => onStep("done")}
              >
                Finish
              </Button>
            </div>
          </section>
        ) : null}

        {setup.step === "done" ? (
          <section className="space-y-4">
            <div className="rounded-card border border-ok/30 bg-ok/10 p-4">
              <div className="flex items-center gap-2 font-medium text-ok">
                <CheckCircle2 size={17} />
                Cairn is ready
              </div>
              <div className="mt-3 grid gap-2 text-sm text-text-secondary sm:grid-cols-2">
                <span>Provider: {setupBackendLabel(setup.backend)}</span>
                <span>{setupBackendDetail(setup)}</span>
                <span>Projects selected: {selectedProjects.length}</span>
                <span>
                  Docker:{" "}
                  {setup.detection?.dockerVersion || "verified after install"}
                </span>
              </div>
            </div>
            <div className="flex justify-end gap-2 border-t border-border pt-4">
              <Button onClick={() => onStep("projects")} variant="secondary">
                Back
              </Button>
              <Button onClick={onFinish}>Continue</Button>
            </div>
          </section>
        ) : null}
      </div>
    </Modal>
  );
}

function setupStepLabel(step: SetupStepID) {
  switch (step) {
    case "welcome":
      return "Welcome";
    case "backend":
      return "Backend";
    case "checks":
      return "Checks";
    case "install":
      return "Install";
    case "verify":
      return "Verify";
    case "projects":
      return "Projects";
    case "done":
      return "Done";
    default:
      return step;
  }
}

function setupBackendLabel(backend: SetupBackendID) {
  switch (backend) {
    case "macos_colima":
      return "macOS Colima";
    case "linux_native":
      return "Linux native Docker";
    case "existing_context":
      return "Existing Docker context";
    default:
      return "Windows WSL Ubuntu";
  }
}

function setupReadyMessage(backend: SetupBackendID) {
  switch (backend) {
    case "macos_colima":
      return "macOS Colima backend is ready";
    case "linux_native":
      return "Linux native backend is ready";
    case "existing_context":
      return "Existing Docker context is ready";
    default:
      return "Windows WSL backend is ready";
  }
}

function setupBackendDetail(setup: ProviderSetupState) {
  switch (setup.backend) {
    case "macos_colima":
      return `Profile: ${setup.colimaProfile || "default"}`;
    case "linux_native":
      return "Socket: Linux Docker socket";
    case "existing_context":
      return `Context: ${setup.detection?.currentContext || "selected in Settings"}`;
    default:
      return `Distro: ${setup.distro || "Ubuntu"}`;
  }
}

function setupBackendHostFallback(backend: SetupBackendID) {
  switch (backend) {
    case "macos_colima":
      return "colima context";
    case "linux_native":
      return "unix:///var/run/docker.sock";
    case "existing_context":
      return "selected Docker context";
    default:
      return "wsl+stdio";
  }
}

function BackendChoiceCard({
  badge,
  body,
  details,
  disabled = false,
  icon,
  onSelect,
  title,
}: {
  badge?: string;
  body: string;
  details?: string;
  disabled?: boolean;
  icon: ReactNode;
  onSelect?: () => void;
  title: string;
}) {
  return (
    <button
      className={[
        "rounded-card border p-4 text-left transition",
        disabled
          ? "cursor-not-allowed border-border bg-bg-inset text-text-muted opacity-70"
          : "border-accent/30 bg-accent/10 text-text-primary hover:border-accent",
      ].join(" ")}
      disabled={disabled}
      onClick={onSelect}
      type="button"
    >
      <div className="flex items-center gap-2">
        <span className="text-accent">{icon}</span>
        <span className="font-medium">{title}</span>
        {badge ? <Badge tone="accent">{badge}</Badge> : null}
      </div>
      <div className="mt-3 text-sm text-text-secondary">{body}</div>
      <details className="mt-3 text-xs text-text-muted">
        <summary>What will be installed</summary>
        <div className="mt-2">{details ?? "No packages are installed."}</div>
      </details>
    </button>
  );
}

function SetupNumberField({
  label,
  onChange,
  value,
}: {
  label: string;
  onChange: (value: number) => void;
  value: number;
}) {
  return (
    <label className="block">
      <span className="text-xs font-medium uppercase text-text-muted">
        {label}
      </span>
      <input
        className="mt-1 h-9 w-full rounded-control border border-border bg-bg-inset px-3 text-sm text-text-primary outline-none"
        min={1}
        onChange={(event) => onChange(Number(event.target.value))}
        type="number"
        value={value}
      />
    </label>
  );
}

function SetupCheckRow({
  row,
}: {
  row: {
    label: string;
    state: StatusToneID;
    detail: string;
  };
}) {
  const icon =
    row.state === "ok" ? (
      <CheckCircle2 size={16} />
    ) : row.state === "error" ? (
      <AlertTriangle size={16} />
    ) : row.state === "warn" ? (
      <AlertTriangle size={16} />
    ) : (
      <Clock3 size={16} />
    );
  const toneClass =
    row.state === "ok"
      ? "border-ok/25 bg-ok/10 text-ok"
      : row.state === "error"
        ? "border-error/25 bg-error/10 text-error"
        : row.state === "warn"
          ? "border-warn/25 bg-warn/10 text-warn"
          : "border-border bg-bg-inset text-text-muted";
  return (
    <div className={`rounded-card border px-3 py-2 text-sm ${toneClass}`}>
      <div className="flex items-center gap-2 font-medium">
        {icon}
        {row.label}
      </div>
      <div className="mt-1 text-xs opacity-85">{row.detail}</div>
    </div>
  );
}

function PermissionOption({
  checked,
  description,
  label,
  onChange,
  value,
}: {
  checked: boolean;
  description: string;
  label: string;
  onChange: () => void;
  value: PermissionMode;
}) {
  return (
    <label className="flex cursor-pointer items-start gap-3 rounded-card border border-border bg-bg-card p-3 text-sm hover:border-border-strong">
      <input
        checked={checked}
        className="mt-1"
        name="linux-permission-mode"
        onChange={onChange}
        type="radio"
        value={value}
      />
      <span>
        <span className="block font-medium text-text-primary">{label}</span>
        <span className="mt-1 block text-text-muted">{description}</span>
      </span>
    </label>
  );
}

type OverviewProps = {
  chartPaused: boolean;
  chartPoints: DashboardChartPoint[];
  containerSparks: Record<string, SparkPoint[]>;
  provider: ProviderSummary | null;
  dockerRunning: boolean;
  mutationsDisabled: boolean;
  mutationDisabledReason: string;
  containers: ContainerSummary[];
  images: ImageSummary[];
  latestSamples: Record<string, StatsSample>;
  liveGPU: GPUMetrics | null;
  metricsStreamError: string | null;
  volumes: VolumeSummary[];
  projectMetricSparks: ProjectMetricSparks;
  projectSparks: Record<string, SparkPoint[]>;
  projects: ProjectSummary[];
  projectsLoading: boolean;
  refreshToken: number;
  runningContainers: number;
  unhealthyContainers: number;
  diskTotal: number;
  diskReclaimable: number;
  onImportProject: () => void;
  onCheckUpdates: () => void;
  onChartPausedChange: (paused: boolean) => void;
  onCleanupApplied: () => Promise<void>;
  onNavigate: (page: PageID) => void;
  onOpenTerminal: () => void;
  onOpenProject: (project: ProjectSummary) => void;
  onRestartDocker: () => void;
  onShowContainers: (filter: FilterID) => void;
};

function OverviewPage({
  chartPaused,
  chartPoints,
  containerSparks,
  containers,
  diskReclaimable,
  diskTotal,
  dockerRunning,
  images,
  latestSamples,
  liveGPU,
  metricsStreamError,
  mutationsDisabled,
  mutationDisabledReason,
  onChartPausedChange,
  onCheckUpdates,
  onCleanupApplied,
  onImportProject,
  onNavigate,
  onOpenTerminal,
  onOpenProject,
  onRestartDocker,
  onShowContainers,
  provider,
  projectMetricSparks,
  projectSparks,
  projects,
  projectsLoading,
  refreshToken,
  runningContainers,
  unhealthyContainers,
  volumes,
}: OverviewProps) {
  const [dashboard, setDashboard] = useState<DashboardMetrics | null>(null);
  const [dashboardStatus, setDashboardStatus] = useState<LoadStatus>("loading");
  const [dashboardError, setDashboardError] = useState<string | null>(null);
  const [metric, setMetric] = useState<DashboardMetricID>("cpu");
  const [range, setRange] = useState<DashboardRangeID>("5m");
  const [stacked, setStacked] = useState(false);
  const [logPeek, setLogPeek] = useState<LogLine[]>([]);
  const [cleanup, setCleanup] = useState<CleanupState>(emptyCleanup);
  const logStreamIDRef = useRef<string | null>(null);

  const loadDashboard = useCallback(async () => {
    if (!dockerRunning) {
      setDashboardError(null);
      setDashboardStatus("ready");
      return;
    }
    setDashboardStatus((current) =>
      current === "ready" ? current : "loading",
    );
    setDashboardError(null);
    try {
      const nextDashboard = await MetricsService.GetDashboardMetrics();
      setDashboard(nextDashboard);
      setDashboardStatus("ready");
    } catch (error: unknown) {
      setDashboardError(
        error instanceof Error ? error.message : "Unable to load dashboard",
      );
      setDashboardStatus("error");
    }
  }, [dockerRunning]);

  const applyCleanup = useCallback(
    async (state: CleanupState) => {
      const kinds = cleanupKinds(state);
      const initialResults = kinds.map((kind) => ({
        kind,
        label: cleanupKindLabel(kind),
        status: "pending" as const,
      }));
      setCleanup((current) => ({
        ...current,
        busy: true,
        error: undefined,
        results: initialResults,
      }));
      try {
        for (const kind of kinds) {
          setCleanup((current) => ({
            ...current,
            results: current.results.map((result) =>
              result.kind === kind
                ? { ...result, status: "running", message: undefined }
                : result,
            ),
          }));
          const plan = await DockerService.PlanPrune(kind);
          if (!plan) {
            throw new Error("Cleanup plan was empty");
          }
          await DockerService.ApplyContainerPlan(
            plan.planID,
            plan.requiresTypedName ? state.typedName : "",
          );
          setCleanup((current) => ({
            ...current,
            results: current.results.map((result) =>
              result.kind === kind
                ? { ...result, status: "success", message: "Completed" }
                : result,
            ),
          }));
        }
        await onCleanupApplied();
        await loadDashboard();
        setCleanup(emptyCleanup);
      } catch (error: unknown) {
        const message =
          error instanceof Error ? error.message : "Unable to clean up Docker";
        setCleanup((current) => {
          const running = current.results.find(
            (result) => result.status === "running",
          );
          const failed: CleanupStepResult = running
            ? { ...running, status: "error", message }
            : {
                kind: "cleanup",
                label: "Cleanup",
                status: "error",
                message,
              };
          return {
            ...current,
            busy: false,
            error: message,
            results:
              current.results.length > 0
                ? current.results.map((result) =>
                    result.kind === failed.kind ? failed : result,
                  )
                : [failed],
          };
        });
        try {
          await onCleanupApplied();
          await loadDashboard();
        } catch {
          // Keep the prune failure visible; refresh will retry through normal polling.
        }
      }
    },
    [loadDashboard, onCleanupApplied],
  );

  useEffect(() => {
    const timer = window.setTimeout(() => {
      void loadDashboard();
    }, 0);
    return () => window.clearTimeout(timer);
  }, [loadDashboard, refreshToken]);

  useEffect(() => {
    const offLines = Events.On("logs:lines", (event) => {
      const payload = eventPayload<LogLinesPayload>(event);
      if (!payload || payload.streamID !== logStreamIDRef.current) {
        return;
      }
      const nextLines = (payload.lines ?? []).filter(isLogLine);
      if (nextLines.length === 0) {
        return;
      }
      setLogPeek((current) => current.concat(nextLines).slice(-8));
    });
    return () => offLines();
  }, []);

  useEffect(() => {
    if (!dockerRunning) {
      return undefined;
    }
    let cancelled = false;
    let activeStreamID: string | null = null;
    LogsService.StartLogStream({
      scope: "all",
      ids: [],
      follow: true,
      tail: 8,
      timestamps: true,
    })
      .then((streamID) => {
        if (cancelled) {
          void LogsService.StopStream(streamID);
          return;
        }
        activeStreamID = streamID;
        logStreamIDRef.current = streamID;
      })
      .catch(() => {
        setLogPeek([]);
      });
    return () => {
      cancelled = true;
      logStreamIDRef.current = null;
      if (activeStreamID) {
        void LogsService.StopStream(activeStreamID);
      }
    };
  }, [dockerRunning]);

  const stopped = Math.max(0, containers.length - runningContainers);
  const paused = containers.filter(
    (container) => container.state === "paused",
  ).length;
  const activeProjectSparks = projectMetricSparks[metric] ?? projectSparks;
  const recentContainers = useMemo(
    () =>
      [...containers]
        .sort(
          (left, right) =>
            dateMillis(right.createdAt) - dateMillis(left.createdAt),
        )
        .slice(0, 6),
    [containers],
  );
  const liveProjects = useMemo(
    () =>
      [...projects]
        .sort(
          (left, right) =>
            projectActivityScore(right, activeProjectSparks[right.id]) -
            projectActivityScore(left, activeProjectSparks[left.id]),
        )
        .slice(0, 5),
    [activeProjectSparks, projects],
  );
  const updateSummary = useMemo(
    () => summarizeProjectUpdates(projects),
    [projects],
  );
  const counts = dashboard ?? {
    projects: projects.length,
    containers: containers.length,
    images: images.length,
    volumes: volumes.length,
    diskUsage: {
      images: { count: images.length, active: 0, sizeBytes: 0, reclaimable: 0 },
      containers: {
        count: containers.length,
        active: runningContainers,
        sizeBytes: 0,
        reclaimable: 0,
      },
      volumes: {
        count: volumes.length,
        active: volumes.filter((volume) => volume.inUse).length,
        sizeBytes: 0,
        reclaimable: 0,
      },
      buildCache: { count: 0, active: 0, sizeBytes: 0, reclaimable: 0 },
      totalBytes: diskTotal,
      reclaimable: diskReclaimable,
    },
    gpu: {
      available: false,
      message: "GPU metrics have not been loaded yet",
      deviceCount: 0,
      checkedAt: null,
    },
  };
  const diskBytes = dashboard?.diskUsage.totalBytes ?? diskTotal;
  const reclaimableBytes = dashboard?.diskUsage.reclaimable ?? diskReclaimable;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-end gap-2">
        <Button
          icon={<Terminal size={15} />}
          onClick={onOpenTerminal}
          size="sm"
        >
          Open terminal
        </Button>
        <Button
          disabled={mutationsDisabled}
          disabledReason={mutationDisabledReason}
          icon={<RefreshCw size={15} />}
          onClick={onCheckUpdates}
          size="sm"
        >
          Check updates
        </Button>
        <Button icon={<Upload size={15} />} onClick={onImportProject} size="sm">
          Import project
        </Button>
        <Button
          disabled={mutationsDisabled}
          disabledReason={mutationDisabledReason}
          icon={<Trash2 size={15} />}
          onClick={() => setCleanup({ ...emptyCleanup, open: true })}
          size="sm"
          variant="danger"
        >
          Prune
        </Button>
        <Button
          disabled={mutationsDisabled}
          disabledReason={mutationDisabledReason}
          icon={<RotateCw size={15} />}
          onClick={onRestartDocker}
          size="sm"
        >
          Restart Docker
        </Button>
      </div>

      {dashboardStatus === "error" && dashboardError ? (
        <div className="rounded-card border border-warn/30 bg-warn/10 px-4 py-3 text-sm text-warn">
          {dashboardError}
        </div>
      ) : null}
      {metricsStreamError ? (
        <div className="rounded-card border border-warn/30 bg-warn/10 px-4 py-3 text-sm text-warn">
          {metricsStreamError}
        </div>
      ) : null}

      <section className="grid gap-3 xl:grid-cols-[360px_minmax(0,1fr)]">
        <EngineHeroCard
          dockerRunning={dockerRunning}
          gpu={liveGPU ?? counts.gpu}
          provider={provider}
        />
        <DashboardCountsStrip
          containers={containers}
          counts={counts}
          diskReclaimable={reclaimableBytes}
          diskTotal={diskBytes}
          images={images}
          mutationsDisabled={mutationsDisabled}
          mutationDisabledReason={mutationDisabledReason}
          onCleanUp={() => setCleanup({ ...emptyCleanup, open: true })}
          onNavigate={onNavigate}
          onShowContainers={onShowContainers}
          projectCount={projects.length}
          runningContainers={runningContainers}
          volumes={volumes}
        />
      </section>

      {containers.length === 0 ? (
        <Card>
          <CardBody className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
            <EmptyState
              body="Import a project or run an image to populate local Compose inventory."
              icon={<Container size={28} />}
              title="Run your first container"
            />
            <div className="flex shrink-0 flex-wrap gap-2">
              <Button
                icon={<Upload size={15} />}
                onClick={onImportProject}
                variant="primary"
              >
                Import project
              </Button>
              <Button icon={<Terminal size={15} />} onClick={onOpenTerminal}>
                Open terminal
              </Button>
            </div>
          </CardBody>
        </Card>
      ) : null}

      <section className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_320px]">
        <ResourceUsagePanel
          metric={metric}
          onMetricChange={setMetric}
          onPauseChange={onChartPausedChange}
          onRangeChange={setRange}
          onStackedChange={setStacked}
          paused={chartPaused || !dockerRunning}
          points={chartPoints}
          range={range}
          stacked={stacked}
        />
        <ProjectsMiniList
          loading={projectsLoading}
          metric={metric}
          onOpenProject={onOpenProject}
          onViewAll={() => onNavigate("projects")}
          projectSparks={activeProjectSparks}
          projects={liveProjects}
        />
      </section>

      <section className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_320px]">
        <ContainerHealthPanel
          containerSparks={containerSparks}
          containers={recentContainers}
          latestSamples={latestSamples}
          onShowContainers={onShowContainers}
          paused={paused}
          running={runningContainers}
          stopped={stopped}
          unhealthy={unhealthyContainers}
        />
        <div className="space-y-4">
          <LogsPeekPanel
            lines={logPeek}
            onOpenLogs={() => onNavigate("logs")}
          />
          <UpdatesCard
            onCheckNow={onCheckUpdates}
            onOpenProjects={() => onNavigate("projects")}
            projects={projects}
            summary={updateSummary}
          />
        </div>
      </section>

      <CleanupModal
        onChange={(patch) =>
          setCleanup((current) => ({ ...current, ...patch }))
        }
        onConfirm={applyCleanup}
        onClose={() => setCleanup(emptyCleanup)}
        reclaimableLabel={formatBytes(reclaimableBytes)}
        state={cleanup}
      />
    </div>
  );
}

function EngineHeroCard({
  dockerRunning,
  gpu,
  provider,
}: {
  dockerRunning: boolean;
  gpu: GPUMetrics;
  provider: ProviderSummary | null;
}) {
  const context = provider?.status?.currentContext || "default";
  const version = provider?.status?.dockerVersion || "unknown";
  const gpuValue = formatGPUStatus(gpu);
  const gpuTitle = formatGPUTitle(gpu);
  return (
    <Card
      className={!dockerRunning ? "border-neutral/30 bg-bg-inset" : undefined}
    >
      <CardBody className="space-y-3 p-3">
        <div className="min-w-0">
          <div className="flex items-start gap-3">
            <StatusDot
              pulse={!dockerRunning && provider?.healthy}
              tone={dockerRunning ? "ok" : "neutral"}
            />
            <div className="min-w-0">
              <div className="truncate text-base font-semibold">
                Docker Engine - {dockerRunning ? "Running" : "Stopped"}
              </div>
              <div className="truncate text-sm text-text-muted">
                {provider?.name ?? "No provider selected"}
              </div>
            </div>
          </div>
          <div className="mt-3 grid grid-cols-2 gap-2 text-xs">
            <StatusPill label="Provider" ok={provider?.healthy ?? false} />
            <StatusPill label="Context" ok={dockerRunning} value={context} />
            <StatusPill label="Engine" ok={dockerRunning} value={version} />
            <StatusPill
              label="GPU"
              ok={gpu.available}
              title={gpuTitle}
              value={gpuValue}
            />
          </div>
        </div>
        <div className="sm:hidden">
          <div
            className={
              dockerRunning
                ? "inline-flex items-center gap-2 rounded-md border border-accent/40 bg-accent-soft px-2.5 py-1.5 text-sm font-semibold text-accent"
                : "inline-flex items-center gap-2 rounded-md border border-border bg-bg-inset px-2.5 py-1.5 text-sm font-semibold text-text-muted"
            }
          >
            <Gauge className="h-4 w-4" />
            {dockerRunning ? "Running" : "Stopped"}
          </div>
        </div>
      </CardBody>
    </Card>
  );
}

function DashboardCountsStrip({
  containers,
  counts,
  diskReclaimable,
  diskTotal,
  images,
  mutationsDisabled,
  mutationDisabledReason,
  onCleanUp,
  onNavigate,
  onShowContainers,
  projectCount,
  runningContainers,
  volumes,
}: {
  counts: DashboardMetrics;
  containers: ContainerSummary[];
  images: ImageSummary[];
  volumes: VolumeSummary[];
  runningContainers: number;
  diskTotal: number;
  diskReclaimable: number;
  mutationsDisabled: boolean;
  mutationDisabledReason: string;
  onCleanUp: () => void;
  onNavigate: (page: PageID) => void;
  onShowContainers: (filter: FilterID) => void;
  projectCount: number;
}) {
  const stopped = Math.max(0, containers.length - runningContainers);
  const imageCounts = useMemo(() => imageFilterCounts(images, {}), [images]);
  const volumeCounts = useMemo(() => volumeFilterCounts(volumes), [volumes]);
  return (
    <section
      className="grid gap-3 sm:grid-cols-2 xl:grid-cols-5"
      aria-label="Docker object counts"
    >
      <MetricButton
        hint="Compose stacks"
        label="Projects"
        onClick={() => onNavigate("projects")}
        value={projectCount}
      />
      <MetricButton
        hint={`${runningContainers} running / ${stopped} stopped`}
        label="Containers"
        onClick={() => onShowContainers("all")}
        value={counts.containers}
      />
      <MetricButton
        hint={`${imageCounts.dangling} dangling`}
        label="Images"
        onClick={() => onNavigate("images")}
        value={counts.images}
      />
      <MetricButton
        hint={`${volumeCounts.inUse} in use`}
        label="Volumes"
        onClick={() => onNavigate("volumes")}
        value={counts.volumes}
      />
      <button
        className={[
          "rounded-card border border-border bg-bg-card p-3 text-left transition",
          mutationsDisabled
            ? "cursor-not-allowed opacity-60"
            : "hover:border-border-strong hover:bg-bg-panel",
        ].join(" ")}
        disabled={mutationsDisabled}
        title={mutationsDisabled ? mutationDisabledReason : undefined}
        onClick={onCleanUp}
        type="button"
      >
        <div className="text-sm text-text-secondary">Disk</div>
        <div className="mt-1 text-xl font-semibold">
          {formatBytes(diskTotal)}
        </div>
        <div className="mt-1 text-xs text-text-muted">
          {formatBytes(diskReclaimable)} reclaimable
        </div>
      </button>
    </section>
  );
}

function ResourceUsagePanel({
  metric,
  onMetricChange,
  onPauseChange,
  onRangeChange,
  onStackedChange,
  paused,
  points,
  range,
  stacked,
}: {
  metric: DashboardMetricID;
  range: DashboardRangeID;
  stacked: boolean;
  paused: boolean;
  points: DashboardChartPoint[];
  onMetricChange: (metric: DashboardMetricID) => void;
  onRangeChange: (range: DashboardRangeID) => void;
  onStackedChange: (stacked: boolean) => void;
  onPauseChange: (paused: boolean) => void;
}) {
  const latest = points[points.length - 1];
  const title =
    metric === "cpu"
      ? `${(latest?.cpu ?? 0).toFixed(1)}% CPU`
      : metric === "gpu"
        ? `${formatBytes(latest?.gpu ?? 0)} GPU memory`
        : metric === "memory"
          ? `${formatBytes(latest?.memory ?? 0)} memory`
          : `${formatRate(latest?.netRx ?? 0)} RX / ${formatRate(latest?.netTx ?? 0)} TX`;
  const Icon =
    metric === "cpu"
      ? Cpu
      : metric === "gpu"
        ? Gauge
        : metric === "memory"
          ? MemoryStick
          : Wifi;
  return (
    <Card>
      <CardHeader
        actions={
          <div className="flex flex-wrap items-center justify-end gap-2">
            {(["5m", "1h", "24h"] as DashboardRangeID[]).map((item) => (
              <button
                className={[
                  "h-8 rounded-control border px-2 text-xs transition",
                  range === item
                    ? "border-accent/40 bg-accent/10 text-accent"
                    : "border-border bg-bg-inset text-text-secondary hover:text-text-primary",
                ].join(" ")}
                key={item}
                onClick={() => onRangeChange(item)}
                type="button"
              >
                {item}
              </button>
            ))}
            <button
              aria-pressed={stacked}
              className={[
                "h-8 rounded-control border px-2 text-xs transition",
                stacked
                  ? "border-accent/40 bg-accent/10 text-accent"
                  : "border-border bg-bg-inset text-text-secondary hover:text-text-primary",
              ].join(" ")}
              onClick={() => onStackedChange(!stacked)}
              type="button"
            >
              Stacked
            </button>
          </div>
        }
        title="Resource Usage"
      />
      <CardBody>
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex min-w-0 items-center gap-3">
            <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-control bg-accent/10 text-accent">
              <Icon size={19} />
            </div>
            <div className="min-w-0">
              <div className="text-lg font-semibold">{title}</div>
              <div className="text-xs text-text-muted">
                {paused ? "Paused" : `${points.length}/300 points`}
              </div>
            </div>
          </div>
          <div className="flex rounded-control border border-border bg-bg-inset p-0.5">
            {(["cpu", "gpu", "memory", "network"] as DashboardMetricID[]).map(
              (item) => (
                <button
                  className={[
                    "h-8 rounded-control px-3 text-xs font-medium capitalize transition",
                    metric === item
                      ? "bg-bg-card text-text-primary"
                      : "text-text-secondary hover:text-text-primary",
                  ].join(" ")}
                  key={item}
                  onClick={() => onMetricChange(item)}
                  type="button"
                >
                  {item}
                </button>
              ),
            )}
          </div>
        </div>
        <div
          className="mt-4 h-72"
          onMouseEnter={() => onPauseChange(true)}
          onMouseLeave={() => onPauseChange(false)}
        >
          <ResponsiveContainer height="100%" width="100%">
            <AreaChart
              accessibilityLayer
              aria-label={`${metric} resource usage chart`}
              data={points}
              margin={{ bottom: 0, left: 0, right: 8, top: 8 }}
            >
              <CartesianGrid stroke={chartColors.grid} vertical={false} />
              <XAxis
                dataKey="label"
                minTickGap={28}
                stroke={chartColors.axis}
                tick={{ fontSize: 11 }}
              />
              <YAxis
                stroke={chartColors.axis}
                tick={{ fontSize: 11 }}
                tickFormatter={(value) =>
                  metric === "memory" || metric === "gpu"
                    ? formatBytes(Number(value))
                    : metric === "network"
                      ? formatRate(Number(value))
                      : `${Number(value).toFixed(0)}%`
                }
                width={56}
              />
              <RechartsTooltip
                content={<DashboardChartTooltip metric={metric} />}
              />
              {metric === "cpu" ? (
                <Area
                  dataKey="cpu"
                  fill={chartColors.cpu}
                  fillOpacity={0.22}
                  isAnimationActive={false}
                  name="CPU"
                  stroke={chartColors.cpu}
                  strokeWidth={2}
                  type="monotone"
                />
              ) : null}
              {metric === "memory" ? (
                <Area
                  dataKey="memory"
                  fill={chartColors.memory}
                  fillOpacity={0.2}
                  isAnimationActive={false}
                  name="Memory"
                  stroke={chartColors.memory}
                  strokeWidth={2}
                  type="monotone"
                />
              ) : null}
              {metric === "gpu" ? (
                <Area
                  dataKey="gpu"
                  fill={chartColors.gpu}
                  fillOpacity={0.2}
                  isAnimationActive={false}
                  name="GPU memory"
                  stroke={chartColors.gpu}
                  strokeWidth={2}
                  type="monotone"
                />
              ) : null}
              {metric === "network" ? (
                <>
                  <Area
                    dataKey="netRx"
                    fill={chartColors.networkRx}
                    fillOpacity={0.18}
                    isAnimationActive={false}
                    name="RX"
                    stackId={stacked ? "network" : undefined}
                    stroke={chartColors.networkRx}
                    strokeWidth={2}
                    type="monotone"
                  />
                  <Area
                    dataKey="netTx"
                    fill={chartColors.networkTx}
                    fillOpacity={0.14}
                    isAnimationActive={false}
                    name="TX"
                    stackId={stacked ? "network" : undefined}
                    stroke={chartColors.networkTx}
                    strokeWidth={2}
                    type="monotone"
                  />
                </>
              ) : null}
            </AreaChart>
          </ResponsiveContainer>
        </div>
      </CardBody>
    </Card>
  );
}

function DashboardChartTooltip({
  active,
  label,
  payload,
  metric,
}: {
  active?: boolean;
  label?: string;
  payload?: Array<{ dataKey?: string; name?: string; value?: number }>;
  metric: DashboardMetricID;
}) {
  if (!active || !payload?.length) {
    return null;
  }
  return (
    <div className="rounded-control border border-border bg-bg-panel px-3 py-2 text-xs shadow">
      <div className="mb-1 font-medium text-text-primary">{label}</div>
      {payload.map((entry) => (
        <div className="text-text-secondary" key={entry.dataKey ?? entry.name}>
          {entry.name}:{" "}
          {formatMetricValue(metric, Number(entry.value ?? 0), entry.dataKey)}
        </div>
      ))}
    </div>
  );
}

function ProjectsMiniList({
  loading,
  metric,
  onOpenProject,
  onViewAll,
  projectSparks,
  projects,
}: {
  projects: ProjectSummary[];
  loading: boolean;
  metric: DashboardMetricID;
  projectSparks: Record<string, SparkPoint[]>;
  onOpenProject: (project: ProjectSummary) => void;
  onViewAll: () => void;
}) {
  const sparkColor = dashboardMetricColor(metric);
  const metricLabel = dashboardMetricLabel(metric);
  return (
    <Card>
      <CardHeader
        actions={
          <Button onClick={onViewAll} size="sm" variant="ghost">
            View all
          </Button>
        }
        title="Projects"
      />
      <CardBody>
        {loading && projects.length === 0 ? (
          <Skeleton className="h-32" />
        ) : null}
        {!loading && projects.length === 0 ? (
          <EmptyState
            body="Import a Compose project to track services here."
            icon={<LayoutGrid size={28} />}
            title="No projects"
          />
        ) : null}
        <div className="space-y-3">
          {projects.map((project) => (
            <button
              className="grid w-full grid-cols-[1fr_72px] items-center gap-3 rounded-control border border-border bg-bg-inset p-3 text-left transition hover:border-border-strong"
              key={project.id}
              onClick={() => onOpenProject(project)}
              type="button"
            >
              <div className="min-w-0">
                <div className="flex items-center gap-2">
                  <StatusDot
                    tone={dotTone(projectStatusTone(project.status))}
                  />
                  <span className="truncate font-medium">{project.name}</span>
                </div>
                <div className="mt-1 text-xs text-text-muted">
                  {project.servicesRunning}/{project.servicesTotal} services
                </div>
                <div className="mt-2 flex flex-wrap gap-1">
                  {projectUpdateBadges(project)}
                </div>
              </div>
              <Sparkline
                color={sparkColor}
                label={`${project.name} project ${metricLabel} trend`}
                points={projectSparks[project.id] ?? []}
              />
            </button>
          ))}
        </div>
      </CardBody>
    </Card>
  );
}

function ContainerHealthPanel({
  containerSparks,
  containers,
  latestSamples,
  onShowContainers,
  paused,
  running,
  stopped,
  unhealthy,
}: {
  containers: ContainerSummary[];
  latestSamples: Record<string, StatsSample>;
  containerSparks: Record<string, SparkPoint[]>;
  running: number;
  stopped: number;
  unhealthy: number;
  paused: number;
  onShowContainers: (filter: FilterID) => void;
}) {
  const data = containerStatusChartSegments({
    paused,
    running,
    stopped,
    unhealthy,
  });
  const pieData = data.length > 0 ? data : [emptyContainerStatusChartSegment];
  return (
    <Card>
      <CardHeader
        actions={
          <Badge tone={unhealthy > 0 ? "error" : "ok"}>
            {unhealthy} unhealthy
          </Badge>
        }
        title="Container Status"
      />
      <CardBody>
        <div className="grid gap-4 lg:grid-cols-[220px_1fr]">
          <div className="h-56">
            <ResponsiveContainer height="100%" width="100%">
              <RechartsPieChart
                accessibilityLayer
                aria-label="Container status distribution chart"
              >
                <Pie
                  data={pieData}
                  dataKey="value"
                  innerRadius={58}
                  isAnimationActive={false}
                  nameKey="name"
                  outerRadius={86}
                >
                  {pieData.map((item) => (
                    <Cell fill={item.color} key={item.name} />
                  ))}
                </Pie>
              </RechartsPieChart>
            </ResponsiveContainer>
          </div>
          <div className="min-w-0 space-y-2">
            {[
              ["running", "Running", running, "ok"],
              ["stopped", "Stopped", stopped, "neutral"],
              [
                "unhealthy",
                "Unhealthy",
                unhealthy,
                unhealthy > 0 ? "error" : "neutral",
              ],
              ["paused", "Paused", paused, "warn"],
            ].map(([filter, label, value, tone]) => (
              <button
                className="flex w-full items-center justify-between rounded-control border border-border bg-bg-inset px-3 py-2 text-sm transition hover:border-border-strong"
                key={String(filter)}
                onClick={() => onShowContainers(String(filter))}
                type="button"
              >
                <span className="flex items-center gap-2">
                  <StatusDot tone={dotTone(tone as BadgeTone)} />
                  {label}
                </span>
                <span className="font-medium">{value}</span>
              </button>
            ))}
          </div>
        </div>
        <div className="mt-4 overflow-hidden rounded-control border border-border">
          <table className="w-full table-fixed text-sm">
            <thead className="bg-bg-inset text-xs uppercase text-text-muted">
              <tr>
                <th className="px-3 py-2 text-left font-medium">Name</th>
                <th className="px-3 py-2 text-left font-medium">Project</th>
                <th className="px-3 py-2 text-left font-medium">Status</th>
                <th className="px-3 py-2 text-left font-medium">CPU</th>
                <th className="px-3 py-2 text-left font-medium">Memory</th>
                <th className="px-3 py-2 text-left font-medium">GPU</th>
                <th className="px-3 py-2 text-left font-medium">Uptime</th>
              </tr>
            </thead>
            <tbody>
              {containers.map((container) => {
                const sample = latestSamples[container.id];
                return (
                  <tr
                    className="border-t border-border hover:bg-bg-inset"
                    key={container.id}
                  >
                    <td className="truncate px-3 py-2">{container.name}</td>
                    <td className="truncate px-3 py-2 text-text-muted">
                      {container.projectID || "-"}
                    </td>
                    <td className="px-3 py-2">
                      <Badge tone={containerTone(container)}>
                        {container.state || "unknown"}
                      </Badge>
                    </td>
                    <td className="px-3 py-2">
                      <div className="grid grid-cols-[3.75rem_minmax(0,1fr)] items-center gap-2">
                        <span className="tabular-nums font-medium text-text-primary">
                          {(
                            sample?.cpuPercent ??
                            container.cpuPercent ??
                            0
                          ).toFixed(1)}
                          %
                        </span>
                        <Sparkline
                          color={chartColors.spark}
                          label={`${container.name || shortID(container.id)} container CPU trend`}
                          points={
                            containerSparks[container.id] ??
                            containerSparkPoints(container)
                          }
                        />
                      </div>
                    </td>
                    <td className="truncate px-3 py-2 text-text-muted">
                      {formatBytes(
                        sample?.memoryBytes ?? container.memoryBytes,
                      )}
                    </td>
                    <td className="truncate px-3 py-2 text-text-muted">
                      {formatGPUUsage(
                        sample?.gpuMemoryBytes ?? container.gpuMemoryBytes,
                        sample?.gpuUtilizationPercent ??
                          container.gpuUtilizationPercent,
                      )}
                    </td>
                    <td className="truncate px-3 py-2 text-text-muted">
                      {sample?.uptimeSeconds
                        ? formatDuration(sample.uptimeSeconds)
                        : relativeTime(dateMillis(container.createdAt))}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
          {containers.length === 0 ? (
            <div className="p-4">
              <EmptyState
                body="Import a project or run an image to populate the local inventory."
                icon={<Container size={28} />}
                title="No containers yet"
              />
            </div>
          ) : null}
        </div>
      </CardBody>
    </Card>
  );
}

function LogsPeekPanel({
  lines,
  onOpenLogs,
}: {
  lines: LogLine[];
  onOpenLogs: () => void;
}) {
  return (
    <Card>
      <CardHeader
        actions={
          <Button onClick={onOpenLogs} size="sm" variant="ghost">
            Open Logs
          </Button>
        }
        title="Logs Peek"
      />
      <CardBody>
        <button
          className="block h-48 w-full overflow-hidden rounded-control border border-border bg-bg-inset p-3 text-left font-mono text-xs"
          onClick={onOpenLogs}
          type="button"
        >
          {lines.length === 0 ? (
            <span className="text-text-muted">No log lines yet</span>
          ) : (
            lines.map((line) => (
              <div
                className="grid grid-cols-[auto_1fr] gap-2"
                key={`${line.ts}-${line.text}`}
              >
                <span className={logLevelClass(normalizeLogLevel(line.level))}>
                  {normalizeLogLevel(line.level).toUpperCase()}
                </span>
                <span className="truncate text-text-secondary">
                  {line.text}
                </span>
              </div>
            ))
          )}
        </button>
      </CardBody>
    </Card>
  );
}

function UpdatesCard({
  onCheckNow,
  onOpenProjects,
  projects,
  summary,
}: {
  projects: ProjectSummary[];
  summary: { image: number; base: number; rebuild: number };
  onCheckNow: () => void;
  onOpenProjects: () => void;
}) {
  const updateProjects = projects
    .filter((project) => projectUpdateCount(project) > 0)
    .slice(0, 3);
  const total = summary.image + summary.base;
  return (
    <Card>
      <CardHeader
        actions={
          <Button icon={<RefreshCw size={15} />} onClick={onCheckNow} size="sm">
            Check now
          </Button>
        }
        title="Updates Available"
      />
      <CardBody>
        <div className="text-lg font-semibold">
          {total} updates available - {summary.rebuild} rebuild needed
        </div>
        <div className="mt-3 space-y-2">
          {updateProjects.length === 0 ? (
            <Badge tone="ok">Up to date</Badge>
          ) : (
            updateProjects.map((project) => (
              <div
                className="flex items-center justify-between gap-2 rounded-control border border-border bg-bg-inset px-3 py-2 text-sm"
                key={project.id}
              >
                <span className="min-w-0 truncate">{project.name}</span>
                <span className="flex shrink-0 gap-1">
                  {projectUpdateBadges(project)}
                </span>
              </div>
            ))
          )}
        </div>
        <Button
          className="mt-4 w-full"
          onClick={onOpenProjects}
          variant="secondary"
        >
          Open Updates
        </Button>
      </CardBody>
    </Card>
  );
}

function Sparkline({
  color,
  label = "Metric trend",
  points,
}: {
  points: SparkPoint[];
  color: string;
  label?: string;
}) {
  const data = points.length > 0 ? points : [{ label: "0", value: 0 }];
  return (
    <div className="h-10 w-full min-w-0">
      <ResponsiveContainer height="100%" width="100%">
        <LineChart
          accessibilityLayer
          aria-label={label}
          data={data}
          margin={{ bottom: 2, left: 0, right: 0, top: 2 }}
        >
          <Line
            dataKey="value"
            dot={false}
            isAnimationActive={false}
            stroke={color}
            strokeWidth={2}
            type="monotone"
          />
        </LineChart>
      </ResponsiveContainer>
    </div>
  );
}

const logLevelOptions: Array<{
  id: LogLevelFilter;
  label: string;
  tone: BadgeTone;
}> = [
  { id: "error", label: "ERROR", tone: "error" },
  { id: "warn", label: "WARN", tone: "warn" },
  { id: "info", label: "INFO", tone: "info" },
  { id: "debug", label: "DEBUG", tone: "neutral" },
  { id: "log", label: "LOG", tone: "neutral" },
  { id: "unknown", label: "unknown", tone: "neutral" },
];

const logBufferLimit = 50000;
const logRowOverscan = 8;

type LogsPageProps = {
  containers: ContainerSummary[];
  dockerRunning: boolean;
  initialContainerIDs?: string[];
  initialProjectID?: string;
  initialScope?: LogScope;
  projects: ProjectSummary[];
  inventoryLoading: boolean;
  lockedScope?: boolean;
  projectsLoading: boolean;
  onToast: (toast: ToastInput) => void;
};

type LogOption = {
  id: string;
  label: string;
  hint?: string;
};

function LogsPage({
  containers,
  dockerRunning,
  initialContainerIDs,
  initialProjectID,
  initialScope,
  inventoryLoading,
  lockedScope = false,
  onToast,
  projects,
  projectsLoading,
}: LogsPageProps) {
  const [scope, setScope] = useState<LogScope>(initialScope ?? "all");
  const [selectedProjectID, setSelectedProjectID] = useState(
    initialProjectID ?? "",
  );
  const [selectedServiceID, setSelectedServiceID] = useState("");
  const [selectedContainerIDs, setSelectedContainerIDs] = useState<string[]>(
    initialContainerIDs ?? [],
  );
  const [lines, setLines] = useState<LogLine[]>([]);
  const [streamID, setStreamID] = useState<string | null>(null);
  const streamIDRef = useRef<string | null>(null);
  const [streamStatus, setStreamStatus] = useState<LoadStatus>("idle");
  const [streamError, setStreamError] = useState<string | null>(null);
  const [streamEnded, setStreamEnded] = useState(false);
  const [restartNonce, setRestartNonce] = useState(0);
  const [paused, setPaused] = useState(false);
  const [pausedAt, setPausedAt] = useState<number | null>(null);
  const [follow, setFollow] = useState(true);
  const [unpinnedAt, setUnpinnedAt] = useState<number | null>(null);
  const [showTimestamps, setShowTimestamps] = useState(true);
  const [wrapLines, setWrapLines] = useState(false);
  const [sourceFilter, setSourceFilter] = useState<string | null>(null);
  const [levelFilters, setLevelFilters] = useState<Set<LogLevelFilter>>(
    () => new Set(logLevelOptions.map((level) => level.id)),
  );
  const [query, setQuery] = useState("");
  const [debouncedQuery, setDebouncedQuery] = useState("");
  const [hideNonMatching, setHideNonMatching] = useState(false);
  const [activeMatch, setActiveMatch] = useState(0);
  const [scrollTop, setScrollTop] = useState(0);
  const [viewportHeight, setViewportHeight] = useState(520);
  const [exportLogs, setExportLogs] =
    useState<ExportLogsState>(emptyExportLogs);
  const viewerRef = useRef<HTMLDivElement>(null);
  const followScrollRAFRef = useRef<number | null>(null);
  const followScrollTimerRef = useRef<number | null>(null);
  const lastFollowScrollAtRef = useRef(0);

  const projectOptions = useMemo<LogOption[]>(
    () =>
      projects.map((project) => ({
        id: project.id,
        label: project.name,
        hint: project.status,
      })),
    [projects],
  );
  const serviceOptions = useMemo<LogOption[]>(() => {
    const seen = new Map<string, LogOption>();
    for (const container of containers) {
      if (!container.projectID || !container.service) {
        continue;
      }
      const id = `${container.projectID}::${container.service}`;
      if (!seen.has(id)) {
        seen.set(id, {
          id,
          label: `${projectName(projects, container.projectID)} / ${container.service}`,
          hint: container.state,
        });
      }
    }
    return Array.from(seen.values()).sort((left, right) =>
      left.label.localeCompare(right.label),
    );
  }, [containers, projects]);
  const containerOptions = useMemo<LogOption[]>(
    () =>
      containers
        .map((container) => ({
          id: container.id,
          label: container.name,
          hint: container.service || container.state,
        }))
        .sort((left, right) => left.label.localeCompare(right.label)),
    [containers],
  );

  const initialContainerIDsKey = (initialContainerIDs ?? []).join("\u0000");

  useEffect(() => {
    if (initialScope) {
      setScope(initialScope);
    }
    if (typeof initialProjectID === "string") {
      setSelectedProjectID(initialProjectID);
    }
    if (typeof initialContainerIDsKey === "string") {
      setSelectedContainerIDs(
        initialContainerIDsKey ? initialContainerIDsKey.split("\u0000") : [],
      );
    }
  }, [initialContainerIDsKey, initialProjectID, initialScope]);

  useEffect(() => {
    if (scope === "project" && !selectedProjectID && projectOptions[0]) {
      setSelectedProjectID(projectOptions[0].id);
    }
    if (scope === "service" && !selectedServiceID && serviceOptions[0]) {
      setSelectedServiceID(serviceOptions[0].id);
    }
  }, [
    projectOptions,
    scope,
    selectedProjectID,
    selectedServiceID,
    serviceOptions,
  ]);

  useEffect(() => {
    streamIDRef.current = streamID;
  }, [streamID]);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setDebouncedQuery(query.trim().toLowerCase());
    }, 200);
    return () => window.clearTimeout(timer);
  }, [query]);

  useEffect(() => {
    setActiveMatch(0);
  }, [debouncedQuery, hideNonMatching, sourceFilter, levelFilters]);

  useEffect(() => {
    const node = viewerRef.current;
    if (!node) {
      return undefined;
    }
    const update = () => setViewportHeight(node.clientHeight || 520);
    update();
    window.addEventListener("resize", update);
    const observer =
      typeof ResizeObserver === "undefined" ? null : new ResizeObserver(update);
    observer?.observe(node);
    return () => {
      window.removeEventListener("resize", update);
      observer?.disconnect();
    };
  }, []);

  useEffect(() => {
    const offLines = Events.On("logs:lines", (event) => {
      const payload = eventPayload<LogLinesPayload>(event);
      if (!payload || payload.streamID !== streamIDRef.current) {
        return;
      }
      const nextLines = (payload.lines ?? []).filter(isLogLine);
      if (nextLines.length === 0) {
        return;
      }
      setLines((current) => {
        const merged = current.concat(nextLines);
        return merged.length > logBufferLimit
          ? merged.slice(merged.length - logBufferLimit)
          : merged;
      });
    });
    const offEOF = Events.On("logs:eof", (event) => {
      const payload = eventPayload<LogErrorPayload>(event);
      if (!payload || payload.streamID !== streamIDRef.current) {
        return;
      }
      setStreamEnded(true);
      setStreamStatus("ready");
    });
    const offError = Events.On("logs:error", (event) => {
      const payload = eventPayload<LogErrorPayload>(event);
      if (!payload || payload.streamID !== streamIDRef.current) {
        return;
      }
      setStreamError(payload.error ?? "Log stream failed");
      setStreamStatus("error");
    });
    return () => {
      offLines();
      offEOF();
      offError();
    };
  }, []);

  const streamIDs = useMemo(() => {
    if (scope === "project") {
      return selectedProjectID ? [selectedProjectID] : [];
    }
    if (scope === "service") {
      return selectedServiceID ? [selectedServiceID] : [];
    }
    if (scope === "container") {
      return selectedContainerIDs;
    }
    return [];
  }, [scope, selectedContainerIDs, selectedProjectID, selectedServiceID]);
  const canStream = dockerRunning && (scope === "all" || streamIDs.length > 0);

  useEffect(() => {
    if (!canStream) {
      setLines([]);
      setStreamID(null);
      setStreamStatus("idle");
      setStreamError(null);
      setStreamEnded(false);
      return undefined;
    }

    let cancelled = false;
    let activeStreamID: string | null = null;
    setLines([]);
    setStreamID(null);
    setStreamStatus("loading");
    setStreamError(null);
    setStreamEnded(false);
    setPaused(false);
    setPausedAt(null);
    setFollow(true);
    setUnpinnedAt(null);

    LogsService.StartLogStream({
      scope,
      ids: streamIDs,
      follow: true,
      tail: 500,
      timestamps: true,
    })
      .then((nextStreamID) => {
        if (cancelled) {
          void LogsService.StopStream(nextStreamID);
          return;
        }
        activeStreamID = nextStreamID;
        setStreamID(nextStreamID);
        setStreamStatus("ready");
      })
      .catch((error: unknown) => {
        if (!cancelled) {
          setStreamError(
            error instanceof Error ? error.message : "Unable to start logs",
          );
          setStreamStatus("error");
        }
      });

    return () => {
      cancelled = true;
      if (activeStreamID) {
        void LogsService.StopStream(activeStreamID);
      }
    };
  }, [canStream, restartNonce, scope, streamIDs]);

  const pausedViewLength =
    paused && pausedAt !== null
      ? Math.min(pausedAt, lines.length)
      : lines.length;
  const pausedNewCount = paused
    ? Math.max(0, lines.length - pausedViewLength)
    : 0;
  const visibleSource = useMemo(
    () => lines.slice(0, pausedViewLength),
    [lines, pausedViewLength],
  );
  const filteredLines = useMemo(
    () =>
      visibleSource.filter((line) => {
        const level = normalizeLogLevel(line.level);
        if (!levelFilters.has(level)) {
          return false;
        }
        if (sourceFilter && logSourceKey(line) !== sourceFilter) {
          return false;
        }
        if (debouncedQuery && hideNonMatching) {
          return line.text.toLowerCase().includes(debouncedQuery);
        }
        return true;
      }),
    [
      debouncedQuery,
      hideNonMatching,
      levelFilters,
      sourceFilter,
      visibleSource,
    ],
  );
  const matchRows = useMemo(() => {
    if (!debouncedQuery) {
      return [];
    }
    const rows: number[] = [];
    filteredLines.forEach((line, index) => {
      if (line.text.toLowerCase().includes(debouncedQuery)) {
        rows.push(index);
      }
    });
    return rows;
  }, [debouncedQuery, filteredLines]);
  const rowHeight = wrapLines ? 44 : 26;
  const totalHeight = filteredLines.length * rowHeight;
  const virtualStart = Math.max(
    0,
    Math.floor(scrollTop / rowHeight) - logRowOverscan,
  );
  const visibleCount =
    Math.ceil(viewportHeight / rowHeight) + logRowOverscan * 2;
  const virtualEnd = Math.min(
    filteredLines.length,
    virtualStart + visibleCount,
  );
  const virtualRows = filteredLines.slice(virtualStart, virtualEnd);
  const newLinesWhileUnpinned =
    !follow && !paused && unpinnedAt !== null
      ? Math.max(0, filteredLines.length - unpinnedAt)
      : 0;

  useEffect(() => {
    if (!follow || paused) {
      return;
    }
    if (
      followScrollRAFRef.current !== null ||
      followScrollTimerRef.current !== null
    ) {
      return;
    }
    const requestScroll = () => {
      followScrollRAFRef.current = window.requestAnimationFrame(() => {
        followScrollRAFRef.current = null;
        lastFollowScrollAtRef.current = Date.now();
        const node = viewerRef.current;
        if (node) {
          node.scrollTop = node.scrollHeight;
        }
      });
    };
    const elapsed = Date.now() - lastFollowScrollAtRef.current;
    const delay = Math.max(0, 50 - elapsed);
    if (delay === 0) {
      requestScroll();
      return;
    }
    followScrollTimerRef.current = window.setTimeout(() => {
      followScrollTimerRef.current = null;
      requestScroll();
    }, delay);
  }, [filteredLines.length, follow, paused]);

  useEffect(
    () => () => {
      if (followScrollTimerRef.current !== null) {
        window.clearTimeout(followScrollTimerRef.current);
        followScrollTimerRef.current = null;
      }
      if (followScrollRAFRef.current !== null) {
        window.cancelAnimationFrame(followScrollRAFRef.current);
        followScrollRAFRef.current = null;
      }
    },
    [],
  );

  const toggleLevel = useCallback((level: LogLevelFilter) => {
    setLevelFilters((current) => {
      const next = new Set(current);
      if (next.has(level) && next.size > 1) {
        next.delete(level);
      } else {
        next.add(level);
      }
      return next;
    });
  }, []);

  const scrollToBottom = useCallback(() => {
    setFollow(true);
    setUnpinnedAt(null);
    window.requestAnimationFrame(() => {
      const node = viewerRef.current;
      if (node) {
        node.scrollTop = node.scrollHeight;
      }
    });
  }, []);

  const jumpMatch = useCallback(
    (direction: -1 | 1) => {
      if (matchRows.length === 0) {
        return;
      }
      setActiveMatch((current) => {
        const next =
          (current + direction + matchRows.length) % matchRows.length;
        const node = viewerRef.current;
        if (node) {
          node.scrollTop = Math.max(0, matchRows[next] * rowHeight - rowHeight);
        }
        return next;
      });
    },
    [matchRows, rowHeight],
  );

  const browseExportPath = useCallback(async () => {
    const format = exportLogs.format;
    const selected = await Dialogs.SaveFile({
      Title: "Export Logs",
      Message: "Choose a log export file",
      ButtonText: "Export",
      Filename: `cairn-${scope}-logs.${format}`,
      Filters: [
        {
          DisplayName: format === "jsonl" ? "JSON Lines" : "Log file",
          Pattern: format === "jsonl" ? "*.jsonl" : "*.log",
        },
      ],
    });
    if (selected) {
      setExportLogs((current) => ({ ...current, path: selected }));
    }
  }, [exportLogs.format, scope]);

  const submitExport = useCallback(async () => {
    setExportLogs((current) => ({ ...current, busy: true, error: undefined }));
    try {
      const result = await LogsService.ExportLogs({
        scope,
        ids: streamIDs,
        path: exportLogs.path,
      });
      if (!result) {
        throw new Error("Log export did not return a result");
      }
      setExportLogs({ ...emptyExportLogs, result });
      onToast({
        action: (
          <Button
            icon={<FolderOpen size={15} />}
            onClick={() => {
              void Clipboard.SetText(result.path);
            }}
            size="sm"
            variant="secondary"
          >
            Open folder
          </Button>
        ),
        body: `${formatCount(result.lineCount)} lines saved`,
        level: "ok",
        title: "Logs exported",
      });
    } catch (error: unknown) {
      setExportLogs((current) => ({
        ...current,
        busy: false,
        error: error instanceof Error ? error.message : "Unable to export logs",
      }));
    }
  }, [exportLogs.path, onToast, scope, streamIDs]);

  const streamLabel =
    lockedScope && scope === "project" && selectedProjectID
      ? projectName(projects, selectedProjectID)
      : lockedScope &&
          scope === "container" &&
          selectedContainerIDs.length === 1
        ? containerName(containers, selectedContainerIDs[0])
        : scope === "all"
          ? "All scopes"
          : streamIDs.length > 0
            ? `${streamIDs.length} selected`
            : "No scope selected";
  const emptyTitle = !canStream
    ? "Pick a project, service, or container"
    : streamStatus === "loading"
      ? "Opening log stream"
      : "No visible logs";

  return (
    <div className="relative min-h-full space-y-4">
      <Card>
        <CardBody className="space-y-3">
          <div className="flex flex-wrap items-center gap-2">
            {!lockedScope ? (
              <div
                aria-label="Log scope"
                className="flex rounded-control border border-border bg-bg-inset p-1"
                role="group"
              >
                {(["all", "project", "service", "container"] as LogScope[]).map(
                  (nextScope) => (
                    <button
                      className={[
                        "h-8 rounded-control px-3 text-xs font-medium capitalize transition",
                        scope === nextScope
                          ? "bg-accent text-bg-app"
                          : "text-text-secondary hover:bg-bg-card hover:text-text-primary",
                      ].join(" ")}
                      key={nextScope}
                      onClick={() => setScope(nextScope)}
                      type="button"
                    >
                      {nextScope}
                    </button>
                  ),
                )}
              </div>
            ) : null}

            {scope === "project" && !lockedScope ? (
              <LogSelect
                ariaLabel="Project scope"
                disabled={projectsLoading}
                onChange={setSelectedProjectID}
                options={projectOptions}
                value={selectedProjectID}
              />
            ) : null}
            {scope === "service" ? (
              <LogSelect
                ariaLabel="Service scope"
                disabled={inventoryLoading}
                onChange={setSelectedServiceID}
                options={serviceOptions}
                value={selectedServiceID}
              />
            ) : null}
            {scope === "container" && !lockedScope ? (
              <LogContainerScopeChecklist
                disabled={inventoryLoading}
                onChange={setSelectedContainerIDs}
                options={containerOptions}
                selectedIDs={selectedContainerIDs}
              />
            ) : null}

            <Badge tone={streamStatus === "error" ? "error" : "info"}>
              {streamLabel}
            </Badge>
            {streamEnded ? <Badge tone="neutral">eof</Badge> : null}
            {streamID ? <Badge>{shortID(streamID)}</Badge> : null}
          </div>

          <div className="flex flex-wrap items-center gap-2">
            <div className="relative min-w-72 flex-1">
              <Search
                className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-text-muted"
                size={16}
              />
              <input
                aria-label="Search logs"
                className="h-9 w-full rounded-control border border-border bg-bg-inset pl-9 pr-3 text-sm text-text-primary placeholder:text-text-muted"
                onChange={(event) => setQuery(event.target.value)}
                placeholder="Search"
                value={query}
              />
            </div>
            <label className="inline-flex h-9 items-center gap-2 rounded-control border border-border bg-bg-inset px-3 text-xs text-text-secondary">
              <input
                checked={hideNonMatching}
                onChange={(event) => setHideNonMatching(event.target.checked)}
                type="checkbox"
              />
              Matches only
            </label>
            <div className="flex items-center gap-1">
              <Button
                disabled={matchRows.length === 0}
                onClick={() => jumpMatch(-1)}
                size="sm"
                variant="secondary"
              >
                Prev
              </Button>
              <Button
                disabled={matchRows.length === 0}
                onClick={() => jumpMatch(1)}
                size="sm"
                variant="secondary"
              >
                Next
              </Button>
              <Badge>
                {matchRows.length > 0 ? activeMatch + 1 : 0}/{matchRows.length}
              </Badge>
            </div>

            <Tooltip label={paused ? "Resume stream display" : "Pause display"}>
              <Button
                icon={paused ? <Play size={16} /> : <Pause size={16} />}
                onClick={() => {
                  if (paused) {
                    setPaused(false);
                    setPausedAt(null);
                    scrollToBottom();
                  } else {
                    setPaused(true);
                    setPausedAt(lines.length);
                  }
                }}
                variant={paused ? "primary" : "secondary"}
              >
                {paused ? "Resume" : "Pause"}
              </Button>
            </Tooltip>
            <Tooltip label="Pin to newest logs">
              <Button
                icon={<ArrowDown size={16} />}
                onClick={scrollToBottom}
                variant={follow ? "primary" : "secondary"}
              >
                Follow
              </Button>
            </Tooltip>
            <Tooltip label="Toggle timestamps">
              <Button
                aria-label="Toggle timestamps"
                icon={<Clock3 size={16} />}
                onClick={() => setShowTimestamps((current) => !current)}
                size="icon"
                variant={showTimestamps ? "primary" : "secondary"}
              />
            </Tooltip>
            <Tooltip label="Toggle line wrap">
              <Button
                aria-label="Toggle line wrap"
                icon={<WrapText size={16} />}
                onClick={() => setWrapLines((current) => !current)}
                size="icon"
                variant={wrapLines ? "primary" : "secondary"}
              />
            </Tooltip>
            <Tooltip label="Export logs">
              <Button
                icon={<Download size={16} />}
                onClick={() =>
                  setExportLogs({
                    ...emptyExportLogs,
                    open: true,
                    path: `cairn-${scope}-logs.jsonl`,
                  })
                }
              >
                Export
              </Button>
            </Tooltip>
          </div>

          <div className="flex flex-wrap items-center gap-2">
            <Filter size={15} className="text-text-muted" />
            {logLevelOptions.map((level) => (
              <button
                className={[
                  "h-7 rounded-control border px-2 text-xs font-medium transition",
                  levelFilters.has(level.id)
                    ? "border-accent bg-accent/10 text-text-primary"
                    : "border-border bg-bg-inset text-text-muted hover:text-text-primary",
                ].join(" ")}
                key={level.id}
                onClick={() => toggleLevel(level.id)}
                type="button"
              >
                {level.label}
              </button>
            ))}
            {sourceFilter ? (
              <Button
                onClick={() => setSourceFilter(null)}
                size="sm"
                variant="ghost"
              >
                Clear source
              </Button>
            ) : null}
          </div>
        </CardBody>
      </Card>

      {paused && pausedNewCount > 0 ? (
        <div className="flex items-center justify-between rounded-control border border-warn/40 bg-warn/10 px-3 py-2 text-sm text-warn">
          <span>Paused - {formatCount(pausedNewCount)} new lines</span>
          <Button
            onClick={() => {
              setPaused(false);
              setPausedAt(null);
              scrollToBottom();
            }}
            size="sm"
            variant="secondary"
          >
            Resume
          </Button>
        </div>
      ) : null}
      {streamError ? (
        <div className="flex items-center justify-between rounded-control border border-error/40 bg-error/10 px-3 py-2 text-sm text-error">
          <span>{streamError}</span>
          <Button
            onClick={() => setRestartNonce((current) => current + 1)}
            size="sm"
            variant="secondary"
          >
            Retry
          </Button>
        </div>
      ) : null}

      <section className="overflow-hidden rounded-card border border-border bg-bg-card">
        <div className="flex h-9 items-center justify-between border-b border-border bg-bg-inset px-3 text-xs text-text-muted">
          <span>{formatCount(filteredLines.length)} visible lines</span>
          <span>{formatCount(lines.length)} buffered</span>
        </div>
        {filteredLines.length === 0 ? (
          <div className="p-10">
            <EmptyState
              body={
                canStream
                  ? "The selected stream has not produced visible lines."
                  : "Select a scope before opening a stream."
              }
              icon={<ScrollText size={28} />}
              title={emptyTitle}
            />
          </div>
        ) : (
          <div
            aria-label="Log lines"
            className="relative h-[520px] overflow-auto font-mono text-xs"
            onScroll={(event) => {
              const node = event.currentTarget;
              const nextTop = node.scrollTop;
              setScrollTop(nextTop);
              const distanceFromBottom =
                node.scrollHeight - node.clientHeight - nextTop;
              if (distanceFromBottom > 48) {
                if (follow) {
                  setUnpinnedAt(filteredLines.length);
                }
                setFollow(false);
              } else if (!paused) {
                setFollow(true);
                setUnpinnedAt(null);
              }
            }}
            ref={viewerRef}
            role="log"
          >
            <div style={{ height: totalHeight, position: "relative" }}>
              {virtualRows.map((line, offset) => {
                const rowIndex = virtualStart + offset;
                return (
                  <LogRow
                    activeSearch={matchRows[activeMatch] === rowIndex}
                    key={`${rowIndex}:${line.ts}:${line.containerID ?? line.containerName ?? ""}:${line.text}`}
                    line={line}
                    onSourceClick={setSourceFilter}
                    query={debouncedQuery}
                    rowHeight={rowHeight}
                    showTimestamp={showTimestamps}
                    style={{ top: rowIndex * rowHeight }}
                    wrap={wrapLines}
                  />
                );
              })}
            </div>
          </div>
        )}
      </section>

      {newLinesWhileUnpinned > 0 ? (
        <button
          className="absolute bottom-5 left-1/2 -translate-x-1/2 rounded-full border border-accent bg-bg-panel px-3 py-1 text-sm text-accent shadow-lg"
          onClick={scrollToBottom}
          type="button"
        >
          {formatCount(newLinesWhileUnpinned)} new lines
        </button>
      ) : null}

      <LogsExportModal
        currentFilters={logFilterSummary(
          scope,
          streamIDs,
          levelFilters,
          sourceFilter,
          debouncedQuery,
        )}
        onBrowse={() => {
          void browseExportPath();
        }}
        onChange={(patch) =>
          setExportLogs((current) => ({ ...current, ...patch }))
        }
        onClose={() => setExportLogs(emptyExportLogs)}
        onSubmit={() => {
          void submitExport();
        }}
        state={exportLogs}
      />
    </div>
  );
}

function LogSelect({
  ariaLabel,
  disabled,
  onChange,
  options,
  value,
}: {
  ariaLabel: string;
  disabled?: boolean;
  onChange: (value: string) => void;
  options: LogOption[];
  value: string;
}) {
  return (
    <select
      aria-label={ariaLabel}
      className="h-9 min-w-60 rounded-control border border-border bg-bg-inset px-3 text-sm text-text-primary"
      disabled={disabled || options.length === 0}
      onChange={(event) => onChange(event.currentTarget.value)}
      value={value}
    >
      {options.length === 0 ? <option value="">No options</option> : null}
      {options.map((option) => (
        <option key={option.id} value={option.id}>
          {option.label}
        </option>
      ))}
    </select>
  );
}

function LogContainerScopeChecklist({
  disabled,
  onChange,
  options,
  selectedIDs,
}: {
  disabled?: boolean;
  onChange: (ids: string[]) => void;
  options: LogOption[];
  selectedIDs: string[];
}) {
  const [open, setOpen] = useState(false);
  const [filter, setFilter] = useState("");
  const rootRef = useRef<HTMLDivElement>(null);
  const selected = useMemo(() => new Set(selectedIDs), [selectedIDs]);
  const selectedOptions = useMemo(
    () => options.filter((option) => selected.has(option.id)),
    [options, selected],
  );
  const filteredOptions = useMemo(() => {
    const needle = filter.trim().toLowerCase();
    if (!needle) {
      return options;
    }
    return options.filter((option) =>
      [option.label, option.hint]
        .filter(Boolean)
        .some((value) => value?.toLowerCase().includes(needle)),
    );
  }, [filter, options]);
  const selectionDisabled = disabled || options.length === 0;

  useEffect(() => {
    if (!open) {
      return undefined;
    }

    const closeOnOutsideClick = (event: MouseEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) {
        setOpen(false);
      }
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setOpen(false);
      }
    };

    window.addEventListener("mousedown", closeOnOutsideClick);
    window.addEventListener("keydown", closeOnEscape);
    return () => {
      window.removeEventListener("mousedown", closeOnOutsideClick);
      window.removeEventListener("keydown", closeOnEscape);
    };
  }, [open]);

  const orderedIDs = (ids: Set<string>) =>
    options.filter((option) => ids.has(option.id)).map((option) => option.id);
  const toggle = (id: string, checked: boolean) => {
    const next = new Set(selected);
    if (checked) {
      next.add(id);
    } else {
      next.delete(id);
    }
    onChange(orderedIDs(next));
  };
  const clearSelection = () => {
    onChange([]);
  };
  const selectVisible = () => {
    const next = new Set(selected);
    for (const option of filteredOptions) {
      next.add(option.id);
    }
    onChange(orderedIDs(next));
  };

  return (
    <div
      aria-label="Container scope"
      className="relative min-w-72 max-w-xl flex-1"
      ref={rootRef}
      role="group"
    >
      <div
        className={[
          "flex min-h-10 w-full items-center gap-2 rounded-control border border-border bg-bg-inset px-2 py-1.5 text-sm",
          selectionDisabled
            ? "opacity-60"
            : "focus-within:border-accent hover:border-accent/70",
        ].join(" ")}
        onClick={() => {
          if (!selectionDisabled) {
            setOpen(true);
          }
        }}
      >
        <div className="flex min-w-0 flex-1 flex-wrap items-center gap-1.5">
          {selectedOptions.length === 0 ? (
            <span className="px-1 text-text-muted">
              {selectionDisabled ? "No containers" : "Select containers"}
            </span>
          ) : null}
          {selectedOptions.slice(0, 3).map((option) => (
            <span
              className="inline-flex max-w-48 items-center gap-1 rounded-full border border-accent/30 bg-accent/10 px-2 py-1 text-xs text-accent"
              key={option.id}
            >
              <span className="truncate">{option.label}</span>
              <button
                aria-label={`Remove ${option.label}`}
                className="rounded-full text-accent hover:bg-accent/15"
                disabled={selectionDisabled}
                onClick={(event) => {
                  event.stopPropagation();
                  toggle(option.id, false);
                }}
                type="button"
              >
                <X size={12} />
              </button>
            </span>
          ))}
          {selectedOptions.length > 3 ? (
            <Badge tone="neutral">+{selectedOptions.length - 3}</Badge>
          ) : null}
          <input
            aria-label="Search containers"
            className="h-7 min-w-24 flex-1 bg-transparent px-1 text-sm text-text-primary outline-none placeholder:text-text-muted"
            disabled={selectionDisabled}
            onChange={(event) => {
              setFilter(event.currentTarget.value);
              setOpen(true);
            }}
            onFocus={() => setOpen(true)}
            placeholder={selectedOptions.length > 0 ? "Search" : ""}
            value={filter}
          />
        </div>
        {selectedOptions.length > 0 ? (
          <button
            aria-label="Clear container selection"
            className="rounded-control p-1 text-text-muted hover:bg-bg-card hover:text-text-primary"
            disabled={selectionDisabled}
            onClick={(event) => {
              event.stopPropagation();
              clearSelection();
            }}
            type="button"
          >
            <X size={15} />
          </button>
        ) : null}
      </div>

      {open && !selectionDisabled ? (
        <div className="absolute left-0 top-[calc(100%+0.25rem)] z-40 w-full overflow-hidden rounded-card border border-border bg-bg-panel shadow-xl">
          <div className="flex items-center justify-between gap-2 border-b border-border px-3 py-2">
            <div className="text-xs text-text-muted">
              {selectedOptions.length} of {options.length} selected
            </div>
            <div className="flex gap-1">
              <Button
                disabled={filteredOptions.length === 0}
                onClick={selectVisible}
                size="sm"
                variant="ghost"
              >
                Select visible
              </Button>
              <Button
                disabled={selectedOptions.length === 0}
                onClick={clearSelection}
                size="sm"
                variant="ghost"
              >
                Clear
              </Button>
            </div>
          </div>
          <div
            aria-label="Container scope options"
            className="max-h-72 overflow-auto p-1"
            role="listbox"
          >
            {filteredOptions.length === 0 ? (
              <div className="px-3 py-6 text-center text-sm text-text-muted">
                No matching containers
              </div>
            ) : null}
            {filteredOptions.map((option) => {
              const checked = selected.has(option.id);
              return (
                <label
                  aria-selected={checked}
                  className={[
                    "flex min-w-0 cursor-pointer items-start gap-2 rounded-control px-2 py-2 text-sm hover:bg-bg-inset",
                    checked ? "bg-accent/10" : "",
                  ].join(" ")}
                  key={option.id}
                  role="option"
                >
                  <input
                    checked={checked}
                    className="mt-0.5"
                    onChange={(event) =>
                      toggle(option.id, event.currentTarget.checked)
                    }
                    type="checkbox"
                  />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-text-primary">
                      {option.label}
                    </span>
                    {option.hint ? (
                      <span className="block truncate text-xs text-text-muted">
                        {option.hint}
                      </span>
                    ) : null}
                  </span>
                </label>
              );
            })}
          </div>
        </div>
      ) : null}
    </div>
  );
}

function LogRow({
  activeSearch,
  line,
  onSourceClick,
  query,
  rowHeight,
  showTimestamp,
  style,
  wrap,
}: {
  activeSearch: boolean;
  line: LogLine;
  onSourceClick: (source: string) => void;
  query: string;
  rowHeight: number;
  showTimestamp: boolean;
  style: { top: number };
  wrap: boolean;
}) {
  const source = logSource(line);
  const isSkipMarker =
    line.stream === "system" && line.text.includes("skipped");
  if (isSkipMarker) {
    return (
      <div
        className="absolute left-0 right-0 flex items-center justify-center border-y border-warn/30 bg-warn/10 text-warn"
        style={{ height: rowHeight, top: style.top }}
      >
        -- {line.text} (UI lagging) --
      </div>
    );
  }

  return (
    <div
      className={[
        "absolute left-0 right-0 grid items-start gap-2 overflow-hidden border-b border-border/60 px-3 py-1",
        showTimestamp
          ? "grid-cols-[96px_128px_64px_1fr]"
          : "grid-cols-[128px_64px_1fr]",
        line.stream === "stderr" ? "border-l-2 border-l-error/70" : "",
        activeSearch ? "bg-accent/10" : "hover:bg-bg-inset",
      ].join(" ")}
      style={{ height: rowHeight, top: style.top }}
    >
      {showTimestamp ? (
        <span className="text-text-muted">{formatLogTimestamp(line.ts)}</span>
      ) : null}
      <button
        className="truncate rounded-control border px-2 py-0.5 text-left"
        onClick={() => onSourceClick(logSourceKey(line))}
        style={{
          borderColor: sourceColor(source),
          color: sourceColor(source),
        }}
        title={source}
        type="button"
      >
        {source}
      </button>
      <Tooltip label={line.level ? "detected" : "undetected"}>
        <span>
          <Badge tone={levelTone(normalizeLogLevel(line.level))}>
            {line.level || "LOG"}
          </Badge>
        </span>
      </Tooltip>
      <span
        className={[
          "min-w-0 overflow-hidden text-text-primary",
          wrap ? "whitespace-pre-wrap break-words" : "truncate whitespace-pre",
        ].join(" ")}
        title={wrap ? line.text : undefined}
      >
        {renderAnsiText(line.text, query)}
      </span>
    </div>
  );
}

function LogsExportModal({
  currentFilters,
  onBrowse,
  onChange,
  onClose,
  onSubmit,
  state,
}: {
  currentFilters: string;
  onBrowse: () => void;
  onChange: (patch: Partial<ExportLogsState>) => void;
  onClose: () => void;
  onSubmit: () => void;
  state: ExportLogsState;
}) {
  return (
    <Modal
      busy={state.busy}
      footer={
        <ModalActions
          busy={state.busy}
          disabled={!state.path.trim()}
          onCancel={onClose}
          onSubmit={onSubmit}
          submitLabel="Export"
        />
      }
      onClose={onClose}
      open={state.open}
      size="md"
      title="Export Logs"
    >
      <div className="space-y-4">
        <div className="grid grid-cols-[120px_1fr] items-center gap-3">
          <label className="text-text-muted" htmlFor="logs-export-format">
            Format
          </label>
          <select
            className="h-9 rounded-control border border-border bg-bg-inset px-3 text-text-primary"
            id="logs-export-format"
            onChange={(event) => {
              const format = event.currentTarget.value as "log" | "jsonl";
              onChange({
                format,
                path: state.path.replace(/\.(log|jsonl)$/i, `.${format}`),
              });
            }}
            value={state.format}
          >
            <option value="jsonl">.jsonl</option>
            <option value="log">.log</option>
          </select>

          <label className="text-text-muted" htmlFor="logs-export-range">
            Range
          </label>
          <select
            className="h-9 rounded-control border border-border bg-bg-inset px-3 text-text-primary"
            id="logs-export-range"
            onChange={(event) =>
              onChange({
                range: event.currentTarget.value as "buffer" | "tail",
              })
            }
            value={state.range}
          >
            <option value="buffer">current buffer</option>
            <option value="tail">tail</option>
          </select>

          <label className="text-text-muted" htmlFor="logs-export-path">
            Path
          </label>
          <div className="flex gap-2">
            <input
              className="h-9 min-w-0 flex-1 rounded-control border border-border bg-bg-inset px-3 text-text-primary"
              id="logs-export-path"
              onChange={(event) =>
                onChange({ path: event.currentTarget.value })
              }
              value={state.path}
            />
            <Button onClick={onBrowse} size="sm" variant="secondary">
              Browse
            </Button>
          </div>
        </div>

        <div className="rounded-control border border-border bg-bg-inset px-3 py-2 text-xs text-text-muted">
          {currentFilters}
        </div>
        {state.error ? (
          <div className="text-sm text-error">{state.error}</div>
        ) : null}
      </div>
    </Modal>
  );
}

type ProjectsPageProps = {
  projects: ProjectSummary[];
  projectSparks: Record<string, SparkPoint[]>;
  actionBusyIDs: Set<string>;
  filter: FilterID;
  search: string;
  sort: ProjectSortID;
  view: ProjectViewMode;
  loading: boolean;
  error: string | null;
  mutationsDisabled: boolean;
  mutationDisabledReason: string;
  onAction: (action: ProjectAction, project: ProjectSummary) => void;
  onFilterChange: (filter: FilterID) => void;
  onSortChange: (sort: ProjectSortID) => void;
  onViewChange: (view: ProjectViewMode) => void;
  onImport: () => void;
  onOpen: (project: ProjectSummary) => void;
  onOpenFolder: (project: ProjectSummary) => void;
  onRefresh: () => void;
};

function ProjectsPage({
  actionBusyIDs,
  error,
  filter,
  loading,
  mutationsDisabled,
  mutationDisabledReason,
  onAction,
  onFilterChange,
  onImport,
  onOpen,
  onOpenFolder,
  onRefresh,
  onSortChange,
  onViewChange,
  projectSparks,
  projects,
  search,
  sort,
  view,
}: ProjectsPageProps) {
  const filtered = useMemo(
    () => sortProjects(filterProjects(projects, search, filter), sort),
    [filter, projects, search, sort],
  );

  if (loading && projects.length === 0) {
    return <TableSkeleton />;
  }
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <FilterChips
          active={filter}
          items={[
            ["all", "All", projects.length],
            [
              "running",
              "Running",
              projects.filter((project) => project.status === "running").length,
            ],
            [
              "stopped",
              "Stopped",
              projects.filter((project) => project.status === "stopped").length,
            ],
            [
              "partial",
              "Partial",
              projects.filter((project) => project.status === "partial").length,
            ],
            [
              "unhealthy",
              "Unhealthy",
              projects.filter((project) => project.health === "unhealthy")
                .length,
            ],
            [
              "updates",
              "Updates available",
              projects.filter(
                (project) => projectActionableUpdateCount(project) > 0,
              ).length,
            ],
            [
              "attention",
              "Needs attention",
              projects.filter(
                (project) => projectManualUpdateCount(project) > 0,
              ).length,
            ],
            [
              "high-cpu",
              "High CPU",
              projects.filter((project) => project.cpuPercent >= 80).length,
            ],
            [
              "recent",
              "Recently changed",
              projects.filter((project) =>
                isRecentlyChanged(project.lastChangedAt),
              ).length,
            ],
          ]}
          onChange={onFilterChange}
        />
        <div className="flex flex-wrap items-center gap-2">
          <select
            aria-label="Sort projects"
            className="h-9 rounded-control border border-border bg-bg-inset px-3 text-sm text-text-primary outline-none"
            onChange={(event) =>
              onSortChange(projectSortID(event.target.value))
            }
            value={sort}
          >
            <option value="name">Name</option>
            <option value="activity">Activity</option>
            <option value="cpu">CPU</option>
          </select>
          <div className="flex rounded-control border border-border bg-bg-inset p-0.5">
            <Tooltip label="Grid view">
              <Button
                aria-label="Grid view"
                icon={<LayoutGrid size={16} />}
                onClick={() => onViewChange("grid")}
                size="icon"
                variant={view === "grid" ? "secondary" : "ghost"}
              />
            </Tooltip>
            <Tooltip label="List view">
              <Button
                aria-label="List view"
                icon={<List size={16} />}
                onClick={() => onViewChange("list")}
                size="icon"
                variant={view === "list" ? "secondary" : "ghost"}
              />
            </Tooltip>
          </div>
          <Button
            icon={<Plus size={16} />}
            onClick={onImport}
            variant="primary"
          >
            Import Project
          </Button>
        </div>
      </div>

      {error ? (
        <div className="rounded-card border border-error/30 bg-error/10 px-4 py-3 text-sm text-error">
          {error}
        </div>
      ) : null}

      {filtered.length === 0 ? (
        <EmptyState
          body="Import a Compose project or refresh after starting one from the Docker CLI."
          icon={<LayoutGrid size={28} />}
          title="No projects found"
        />
      ) : view === "grid" ? (
        <section
          className="grid grid-cols-[repeat(auto-fit,minmax(min(100%,340px),1fr))] gap-4"
          aria-label="Compose projects"
        >
          {filtered.map((project) => (
            <ProjectCard
              actionBusyIDs={actionBusyIDs}
              key={project.id}
              mutationsDisabled={mutationsDisabled}
              mutationDisabledReason={mutationDisabledReason}
              onAction={onAction}
              onOpen={onOpen}
              onOpenFolder={onOpenFolder}
              project={project}
              sparkPoints={projectSparks[project.id]}
            />
          ))}
        </section>
      ) : (
        <ProjectList
          actionBusyIDs={actionBusyIDs}
          mutationsDisabled={mutationsDisabled}
          mutationDisabledReason={mutationDisabledReason}
          onAction={onAction}
          onOpen={onOpen}
          onOpenFolder={onOpenFolder}
          projects={filtered}
        />
      )}

      <div className="flex justify-end">
        <Button
          icon={<RefreshCw size={16} />}
          loading={loading}
          onClick={onRefresh}
        >
          Refresh Projects
        </Button>
      </div>
    </div>
  );
}

function UpdatesPage({
  checkJobID,
  checkProgress,
  error,
  filter,
  history,
  historyError,
  historyLoading,
  ignored,
  ignoredError,
  ignoredLoading,
  lastCheckAt,
  loading,
  mutationsDisabled,
  mutationDisabledReason,
  onCheckNow,
  onFilterChange,
  onIgnore,
  onOpenProject,
  onPlanProject,
  onPlanService,
  onRefresh,
  onRollback,
  onTabChange,
  onUnignore,
  projects,
  search,
  tab,
  updates,
}: {
  updates: ImageUpdate[];
  history: UpdateHistoryItem[];
  ignored: ImageUpdate[];
  projects: ProjectSummary[];
  tab: UpdatesTabID;
  filter: FilterID;
  search: string;
  loading: boolean;
  historyLoading: boolean;
  ignoredLoading: boolean;
  error: string | null;
  historyError: string | null;
  ignoredError: string | null;
  lastCheckAt: number | null;
  checkJobID: string | null;
  checkProgress: UpdateProgressEntry | null;
  mutationsDisabled: boolean;
  mutationDisabledReason: string;
  onCheckNow: () => void;
  onFilterChange: (filter: FilterID) => void;
  onIgnore: (update: ImageUpdate) => void;
  onOpenProject: (projectID: string) => void;
  onPlanProject: (projectID: string) => void;
  onPlanService: (update: ImageUpdate) => void;
  onRefresh: () => void;
  onRollback: (historyID: number) => void;
  onTabChange: (tab: UpdatesTabID) => void;
  onUnignore: (id: number) => void;
}) {
  const filtered = useMemo(
    () => filterUpdateRows(updates, search, filter),
    [filter, search, updates],
  );
  const groups = useMemo(
    () => groupUpdatesByProject(filtered, projects),
    [filtered, projects],
  );
  const counts = useMemo(() => updateFilterCounts(updates), [updates]);
  const checking = Boolean(checkProgress || checkJobID);
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="space-y-1">
          <div className="text-sm text-text-muted">
            {lastCheckAt
              ? `Checked ${relativeTime(lastCheckAt)}`
              : "No update check recorded"}
          </div>
          {checkProgress ? (
            <div className="min-w-[260px]">
              <div className="mb-1 flex items-center justify-between gap-3 text-xs text-text-muted">
                <span>{checkProgress.message ?? "Checking updates"}</span>
                {typeof checkProgress.pct === "number" ? (
                  <span>{checkProgress.pct}%</span>
                ) : null}
              </div>
              <div className="h-1.5 overflow-hidden rounded-full bg-bg-inset">
                <div
                  className="h-full rounded-full bg-accent transition-all"
                  style={{ width: `${checkProgress.pct ?? 20}%` }}
                />
              </div>
            </div>
          ) : null}
        </div>
        <div className="flex flex-wrap gap-2">
          <Button
            disabled={mutationsDisabled}
            disabledReason={mutationDisabledReason}
            icon={<RefreshCw size={15} />}
            loading={checking}
            onClick={onCheckNow}
            variant="primary"
          >
            Check now
          </Button>
          <Button icon={<RotateCw size={15} />} onClick={onRefresh}>
            Refresh
          </Button>
        </div>
      </div>

      <div className="flex flex-wrap gap-2 border-b border-border">
        {(["current", "history", "ignored"] as UpdatesTabID[]).map((id) => (
          <button
            className={[
              "border-b-2 px-3 py-2 text-sm font-medium transition",
              tab === id
                ? "border-accent text-accent"
                : "border-transparent text-text-secondary hover:text-text-primary",
            ].join(" ")}
            key={id}
            onClick={() => onTabChange(id)}
            type="button"
          >
            {id === "current" ? "Current" : titleCase(id)}
          </button>
        ))}
      </div>

      {tab === "current" ? (
        <div className="space-y-4">
          <FilterChips
            active={filter}
            items={[
              ["all", "All", counts.all],
              ["image", "Image updates", counts.image],
              ["base", "Base updates", counts.base],
              ["rebuild", "Rebuild required", counts.rebuild],
              ["pinned", "Pinned", counts.pinned],
              ["unknown", "Unknown base", counts.unknown],
              ["errors", "Errors", counts.errors],
              ["up-to-date", "Up to date", counts.upToDate],
            ]}
            onChange={onFilterChange}
          />
          {error ? (
            <div className="rounded-card border border-error/30 bg-error/10 px-4 py-3 text-sm text-error">
              {error}
            </div>
          ) : null}
          {loading && updates.length === 0 ? <TableSkeleton /> : null}
          {!loading && filtered.length === 0 ? (
            <EmptyState
              body="All images up to date."
              icon={<CheckCircle2 size={28} />}
              title="All images up to date"
            />
          ) : null}
          {groups.map((group) => {
            const actionable = group.rows.filter(isActionableUpdate);
            return (
              <Card key={group.projectID || group.projectName}>
                <CardHeader
                  actions={
                    <div className="flex flex-wrap gap-2">
                      {group.projectID ? (
                        <Button
                          onClick={() => onOpenProject(group.projectID)}
                          size="sm"
                          variant="secondary"
                        >
                          Open project
                        </Button>
                      ) : null}
                      <Button
                        disabled={actionable.length === 0}
                        disabledReason="No actionable rows in this project"
                        icon={<PackagePlus size={15} />}
                        onClick={() => onPlanProject(group.projectID)}
                        size="sm"
                      >
                        Update project
                      </Button>
                    </div>
                  }
                  title={group.projectName}
                />
                <CardBody>
                  <DataTable
                    ariaLabel={`${group.projectName} updates`}
                    columns={[
                      {
                        id: "service",
                        header: "Service",
                        render: (update) => (
                          <span className="font-medium text-text-primary">
                            {update.service || "-"}
                          </span>
                        ),
                      },
                      {
                        id: "container",
                        header: "Container",
                        render: (update) => shortID(update.containerID ?? ""),
                      },
                      {
                        id: "kind",
                        header: "Update type",
                        render: (update) => (
                          <Badge
                            tone={
                              update.kind === UpdateKind.UpdateKindBaseImage
                                ? "accent"
                                : "info"
                            }
                          >
                            {update.kind === UpdateKind.UpdateKindBaseImage
                              ? "Base"
                              : "Image"}
                          </Badge>
                        ),
                      },
                      {
                        id: "image",
                        header: "Current image",
                        render: (update) => (
                          <span title={update.currentImage}>
                            {update.currentImage}
                          </span>
                        ),
                      },
                      {
                        id: "base",
                        header: "Base image",
                        render: (update) => update.baseImage || "-",
                      },
                      {
                        id: "digest",
                        header: "Local -> Remote",
                        render: (update) => (
                          <DigestDelta
                            local={update.localDigest}
                            remote={update.remoteDigest}
                          />
                        ),
                      },
                      {
                        id: "confidence",
                        header: "Confidence",
                        render: (update) => (
                          <ConfidenceChip confidence={update.confidence} />
                        ),
                      },
                      {
                        id: "status",
                        header: "Status/notes",
                        cellClassName:
                          "whitespace-normal break-words leading-5 text-text-secondary",
                        headerClassName: "w-[28%]",
                        render: (update) => (
                          <div className="min-w-0 space-y-1">
                            <Badge tone={updateTone(update.status)}>
                              {updateStatusLabel(update.status)}
                            </Badge>
                            {updateStatusNote(update) ? (
                              <div className="whitespace-normal break-words text-xs text-text-muted">
                                {updateStatusNote(update)}
                              </div>
                            ) : null}
                          </div>
                        ),
                        wrap: true,
                      },
                      {
                        id: "checked",
                        header: "Last checked",
                        render: (update) =>
                          relativeTime(dateMillis(update.checkedAt)),
                      },
                      {
                        id: "actions",
                        header: "",
                        render: (update) => (
                          <div className="flex justify-end gap-2">
                            {isActionableUpdate(update) ? (
                              <Button
                                icon={<PackagePlus size={15} />}
                                onClick={() => onPlanService(update)}
                                size="sm"
                              >
                                Update
                              </Button>
                            ) : null}
                            <Button
                              icon={<Eye size={15} />}
                              onClick={() => onIgnore(update)}
                              size="sm"
                              variant="secondary"
                            >
                              Ignore
                            </Button>
                          </div>
                        ),
                      },
                    ]}
                    empty={
                      <EmptyState
                        body="This project has no matching update rows."
                        icon={<RefreshCw size={28} />}
                        title="No matching rows"
                      />
                    }
                    getRowID={(update) => String(update.id)}
                    rows={group.rows}
                  />
                </CardBody>
              </Card>
            );
          })}
        </div>
      ) : null}

      {tab === "history" ? (
        <UpdateHistoryTable
          error={historyError}
          history={history}
          loading={historyLoading}
          onRollback={onRollback}
          projects={projects}
        />
      ) : null}

      {tab === "ignored" ? (
        <IgnoredUpdatesTable
          error={ignoredError}
          ignored={ignored}
          loading={ignoredLoading}
          onUnignore={onUnignore}
          projects={projects}
        />
      ) : null}
    </div>
  );
}

function UpdateHistoryTable({
  error,
  history,
  loading,
  onRollback,
  projects,
}: {
  history: UpdateHistoryItem[];
  projects: ProjectSummary[];
  loading: boolean;
  error: string | null;
  onRollback: (historyID: number) => void;
}) {
  return (
    <div className="space-y-4">
      {error ? (
        <div className="rounded-card border border-error/30 bg-error/10 px-4 py-3 text-sm text-error">
          {error}
        </div>
      ) : null}
      {loading && history.length === 0 ? <TableSkeleton /> : null}
      <DataTable
        ariaLabel="Update history"
        columns={[
          {
            id: "time",
            header: "Time",
            render: (item) => relativeTime(dateMillis(item.startedAt)),
            sortable: true,
            sortValue: (item) => dateMillis(item.startedAt),
          },
          {
            id: "project",
            header: "Project",
            render: (item) => projectNameForID(projects, item.projectID),
          },
          {
            id: "service",
            header: "Service",
            render: (item) => item.service || "-",
          },
          {
            id: "kind",
            header: "Kind",
            render: (item) => updateKindLabel(item.kind),
          },
          {
            id: "result",
            header: "Result",
            render: (item) => (
              <Badge tone={updateResultTone(item.result)}>
                {item.result || "unknown"}
              </Badge>
            ),
          },
          {
            id: "duration",
            header: "Duration",
            render: (item) => updateDuration(item),
          },
          {
            id: "details",
            header: "Details",
            render: (item) => (
              <details className="max-w-md">
                <summary className="cursor-pointer text-sm text-accent">
                  Details
                </summary>
                <div className="mt-2 space-y-1 rounded-control border border-border bg-bg-inset p-3 text-xs text-text-muted">
                  <div>Rollback: {item.rollbackStatus || "unavailable"}</div>
                  {item.error ? <div>Error: {item.error}</div> : null}
                </div>
              </details>
            ),
          },
          {
            id: "actions",
            header: "",
            render: (item) =>
              item.rollbackStatus === "available" ? (
                <Button
                  icon={<Undo2 size={15} />}
                  onClick={() => onRollback(item.id)}
                  size="sm"
                  variant="secondary"
                >
                  Rollback
                </Button>
              ) : null,
          },
        ]}
        empty={
          <EmptyState
            body="Applied update results land here."
            icon={<HistoryIcon />}
            title="No update history"
          />
        }
        getRowID={(item) => String(item.id)}
        rows={history}
      />
    </div>
  );
}

function HistoryIcon() {
  return <Clock3 size={28} />;
}

function IgnoredUpdatesTable({
  error,
  ignored,
  loading,
  onUnignore,
  projects,
}: {
  ignored: ImageUpdate[];
  projects: ProjectSummary[];
  loading: boolean;
  error: string | null;
  onUnignore: (id: number) => void;
}) {
  return (
    <div className="space-y-4">
      {error ? (
        <div className="rounded-card border border-error/30 bg-error/10 px-4 py-3 text-sm text-error">
          {error}
        </div>
      ) : null}
      {loading && ignored.length === 0 ? <TableSkeleton /> : null}
      <DataTable
        ariaLabel="Ignored updates"
        columns={[
          {
            id: "project",
            header: "Project",
            render: (update) => projectNameForID(projects, update.projectID),
          },
          {
            id: "service",
            header: "Service",
            render: (update) => update.service || "-",
          },
          {
            id: "scope",
            header: "Scope",
            render: (update) => update.currentImage,
          },
          {
            id: "reason",
            header: "Reason",
            render: (update) => update.notes?.join(", ") || "-",
          },
          {
            id: "actions",
            header: "",
            render: (update) => (
              <Button
                icon={<RotateCw size={15} />}
                onClick={() => onUnignore(update.id)}
                size="sm"
                variant="secondary"
              >
                Unignore
              </Button>
            ),
          },
        ]}
        empty={
          <EmptyState
            body="Ignored updates appear here with their reason and scope."
            icon={<Eye size={28} />}
            title="No ignored updates"
          />
        }
        getRowID={(update) => String(update.id)}
        rows={ignored}
      />
    </div>
  );
}

function DigestDelta({ local, remote }: { local?: string; remote?: string }) {
  const differs = Boolean(local && remote && local !== remote);
  return (
    <div
      className={[
        "font-mono text-xs",
        differs ? "text-warn" : "text-text-muted",
      ].join(" ")}
      title={`${local || "-"} -> ${remote || "-"}`}
    >
      {shortDigest(local)} -&gt; {shortDigest(remote)}
    </div>
  );
}

function ConfidenceChip({
  confidence,
  reason,
}: {
  confidence?: string;
  reason?: string;
}) {
  return (
    <Tooltip label={reason || confidenceReason(confidence)}>
      <span>
        <Badge tone={confidenceTone(confidence)}>
          Confidence: {titleCase(confidence || "unknown")}
        </Badge>
      </span>
    </Tooltip>
  );
}

function ProjectCard({
  actionBusyIDs,
  mutationsDisabled,
  mutationDisabledReason,
  onAction,
  onOpen,
  onOpenFolder,
  project,
  sparkPoints,
}: {
  project: ProjectSummary;
  sparkPoints?: SparkPoint[];
  actionBusyIDs: Set<string>;
  mutationsDisabled: boolean;
  mutationDisabledReason: string;
  onAction: (action: ProjectAction, project: ProjectSummary) => void;
  onOpen: (project: ProjectSummary) => void;
  onOpenFolder: (project: ProjectSummary) => void;
}) {
  const workdirMissing = project.status === "error";
  const primaryAction = primaryProjectAction(project);
  const disabledReason = (action: ProjectAction) =>
    projectActionDisabledReason(
      action,
      project,
      mutationsDisabled,
      mutationDisabledReason,
    );
  const disabled = (action: ProjectAction) => Boolean(disabledReason(action));
  const busy = (action: ProjectAction) =>
    actionBusyIDs.has(projectActionBusyKey(action, project.id));
  return (
    <Card>
      <CardBody className="space-y-4">
        <div className="flex items-start gap-3">
          <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-control bg-accent/10 text-accent">
            <LayoutGrid size={19} />
          </div>
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <h2 className="min-w-0 text-base font-semibold">
                <button
                  className="block max-w-full truncate text-left hover:text-accent"
                  onClick={() => onOpen(project)}
                  type="button"
                >
                  {project.name}
                </button>
              </h2>
              <Badge tone={projectStatusTone(project.status)}>
                {project.status || "unknown"}
              </Badge>
            </div>
            <div className="mt-1 truncate text-xs text-text-muted">
              {project.workingDir || "No workdir"}
            </div>
          </div>
        </div>

        <div className="grid grid-cols-2 gap-2 text-sm xl:grid-cols-4">
          <MiniMetric
            label="Services"
            value={`${project.servicesRunning}/${project.servicesTotal}`}
          />
          <MiniMetric label="CPU" value={`${project.cpuPercent.toFixed(1)}%`} />
          <MiniMetric label="RAM" value={formatBytes(project.memoryBytes)} />
          <MiniMetric
            label="GPU"
            value={formatGPUUsage(
              project.gpuMemoryBytes,
              project.gpuUtilizationPercent,
            )}
          />
        </div>

        <div className="h-10 overflow-hidden rounded-control border border-border bg-bg-inset px-2 py-2">
          <Sparkline
            color={chartColors.spark}
            label={`${project.name} project resource trend`}
            points={sparkPoints ?? []}
          />
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <Badge tone={healthTone(project.health)}>
            {project.health || "unknown"}
          </Badge>
          {projectCardUpdateBadges(project)}
          {workdirMissing ? <Badge tone="warn">Workdir missing</Badge> : null}
          <PortList ports={project.ports ?? []} />
        </div>

        <div className="flex flex-wrap items-center gap-x-2 gap-y-2 border-t border-border pt-3">
          <div className="flex min-w-0 flex-1 flex-wrap items-center gap-1">
            <Tooltip label={primaryAction === "stop" ? "Stop" : "Start"}>
              <Button
                aria-label={`${primaryAction === "stop" ? "Stop" : "Start"} ${project.name}`}
                disabled={disabled(primaryAction)}
                disabledReason={disabledReason(primaryAction)}
                icon={
                  primaryAction === "stop" ? (
                    <Square size={15} />
                  ) : (
                    <Play size={15} />
                  )
                }
                loading={busy(primaryAction)}
                onClick={() => onAction(primaryAction, project)}
                size="icon"
                variant="ghost"
              />
            </Tooltip>
            <Tooltip label="Restart">
              <Button
                aria-label={`Restart ${project.name}`}
                disabled={disabled("restart")}
                disabledReason={disabledReason("restart")}
                icon={<RotateCw size={15} />}
                loading={busy("restart")}
                onClick={() => onAction("restart", project)}
                size="icon"
                variant="ghost"
              />
            </Tooltip>
            <Tooltip label="Pull images">
              <Button
                aria-label={`Pull images ${project.name}`}
                disabled={disabled("pull")}
                disabledReason={disabledReason("pull")}
                icon={<Download size={15} />}
                loading={busy("pull")}
                onClick={() => onAction("pull", project)}
                size="icon"
                variant="ghost"
              />
            </Tooltip>
            <Tooltip label="Redeploy">
              <Button
                aria-label={`Redeploy ${project.name}`}
                disabled={disabled("redeploy")}
                disabledReason={disabledReason("redeploy")}
                icon={<PackagePlus size={15} />}
                loading={busy("redeploy")}
                onClick={() => onAction("redeploy", project)}
                size="icon"
                variant="ghost"
              />
            </Tooltip>
            <Tooltip label="Down">
              <Button
                aria-label={`Down ${project.name}`}
                disabled={disabled("down")}
                disabledReason={disabledReason("down")}
                icon={<Square size={15} />}
                loading={busy("down")}
                onClick={() => onAction("down", project)}
                size="icon"
                variant="danger"
              />
            </Tooltip>
            <Tooltip label="Down with volumes">
              <Button
                aria-label={`Down with volumes ${project.name}`}
                disabled={disabled("down-volumes")}
                disabledReason={disabledReason("down-volumes")}
                icon={<Skull size={15} />}
                loading={busy("down-volumes")}
                onClick={() => onAction("down-volumes", project)}
                size="icon"
                variant="danger"
              />
            </Tooltip>
            <Tooltip label="Open folder">
              <Button
                aria-label={`Open folder ${project.name}`}
                disabled={!project.workingDir}
                icon={<FolderOpen size={15} />}
                onClick={() => onOpenFolder(project)}
                size="icon"
                variant="ghost"
              />
            </Tooltip>
            <Tooltip label="Remove from list">
              <Button
                aria-label={`Remove from list ${project.name}`}
                icon={<Trash2 size={15} />}
                loading={busy("remove")}
                onClick={() => onAction("remove", project)}
                size="icon"
                variant="danger"
              />
            </Tooltip>
          </div>
          <span className="ml-auto shrink-0 whitespace-nowrap text-xs text-text-muted">
            {relativeTime(dateMillis(project.lastChangedAt))}
          </span>
        </div>
      </CardBody>
    </Card>
  );
}

function ProjectList({
  actionBusyIDs,
  mutationsDisabled,
  mutationDisabledReason,
  onAction,
  onOpen,
  onOpenFolder,
  projects,
}: {
  projects: ProjectSummary[];
  actionBusyIDs: Set<string>;
  mutationsDisabled: boolean;
  mutationDisabledReason: string;
  onAction: (action: ProjectAction, project: ProjectSummary) => void;
  onOpen: (project: ProjectSummary) => void;
  onOpenFolder: (project: ProjectSummary) => void;
}) {
  return (
    <DataTable
      ariaLabel="Projects list"
      columns={[
        {
          id: "name",
          header: "Name",
          render: (project) => (
            <button
              className="font-medium text-text-primary hover:text-accent"
              onClick={() => onOpen(project)}
              type="button"
            >
              {project.name}
            </button>
          ),
          sortValue: (project) => project.name,
          sortable: true,
        },
        {
          id: "status",
          header: "Status",
          render: (project) => (
            <Badge tone={projectStatusTone(project.status)}>
              {project.status}
            </Badge>
          ),
          sortValue: (project) => project.status,
          sortable: true,
        },
        {
          id: "services",
          header: "Services",
          render: (project) =>
            `${project.servicesRunning}/${project.servicesTotal}`,
          sortValue: (project) => project.servicesTotal,
          sortable: true,
        },
        {
          id: "health",
          header: "Health",
          render: (project) => (
            <Badge tone={healthTone(project.health)}>{project.health}</Badge>
          ),
          sortValue: (project) => project.health,
          sortable: true,
        },
        {
          id: "cpu",
          header: "CPU",
          render: (project) => `${project.cpuPercent.toFixed(1)}%`,
          sortValue: (project) => project.cpuPercent,
          sortable: true,
        },
        {
          id: "ram",
          header: "RAM",
          render: (project) => formatBytes(project.memoryBytes),
          sortValue: (project) => project.memoryBytes,
          sortable: true,
        },
        {
          id: "gpu",
          header: "GPU",
          render: (project) =>
            formatGPUUsage(
              project.gpuMemoryBytes,
              project.gpuUtilizationPercent,
            ),
          sortValue: (project) => project.gpuMemoryBytes ?? 0,
          sortable: true,
        },
        {
          id: "ports",
          header: "Ports",
          render: (project) => <PortList ports={project.ports ?? []} />,
        },
        {
          id: "changed",
          header: "Last changed",
          render: (project) => relativeTime(dateMillis(project.lastChangedAt)),
          sortValue: (project) => dateMillis(project.lastChangedAt),
          sortable: true,
        },
        {
          id: "workdir",
          header: "Workdir",
          render: (project) => project.workingDir || "-",
          sortValue: (project) => project.workingDir || "",
          sortable: true,
        },
        {
          id: "actions",
          header: "",
          render: (project) => (
            <ProjectRowActions
              actionBusyIDs={actionBusyIDs}
              mutationsDisabled={mutationsDisabled}
              mutationDisabledReason={mutationDisabledReason}
              onAction={onAction}
              onOpenFolder={onOpenFolder}
              project={project}
            />
          ),
        },
      ]}
      empty={
        <EmptyState
          body="Import a Compose project to populate this list."
          icon={<LayoutGrid size={28} />}
          title="No projects found"
        />
      }
      getRowID={(project) => project.id}
      rows={projects}
    />
  );
}

function ProjectRowActions({
  actionBusyIDs,
  mutationsDisabled,
  mutationDisabledReason,
  onAction,
  onOpenFolder,
  project,
}: {
  project: ProjectSummary;
  actionBusyIDs: Set<string>;
  mutationsDisabled: boolean;
  mutationDisabledReason: string;
  onAction: (action: ProjectAction, project: ProjectSummary) => void;
  onOpenFolder: (project: ProjectSummary) => void;
}) {
  const primaryAction = primaryProjectAction(project);
  const disabledReason = (action: ProjectAction) =>
    projectActionDisabledReason(
      action,
      project,
      mutationsDisabled,
      mutationDisabledReason,
    );
  const disabled = (action: ProjectAction) => Boolean(disabledReason(action));
  const busy = (action: ProjectAction) =>
    actionBusyIDs.has(projectActionBusyKey(action, project.id));
  return (
    <div className="flex justify-end gap-1">
      <Tooltip label={primaryAction === "stop" ? "Stop" : "Start"}>
        <Button
          aria-label={`${primaryAction === "stop" ? "Stop" : "Start"} ${project.name}`}
          disabled={disabled(primaryAction)}
          disabledReason={disabledReason(primaryAction)}
          icon={
            primaryAction === "stop" ? <Square size={15} /> : <Play size={15} />
          }
          loading={busy(primaryAction)}
          onClick={() => onAction(primaryAction, project)}
          size="icon"
          variant="ghost"
        />
      </Tooltip>
      <Tooltip label="Restart">
        <Button
          aria-label={`Restart ${project.name}`}
          disabled={disabled("restart")}
          disabledReason={disabledReason("restart")}
          icon={<RotateCw size={15} />}
          loading={busy("restart")}
          onClick={() => onAction("restart", project)}
          size="icon"
          variant="ghost"
        />
      </Tooltip>
      <Tooltip label="Pull images">
        <Button
          aria-label={`Pull images ${project.name}`}
          disabled={disabled("pull")}
          disabledReason={disabledReason("pull")}
          icon={<Download size={15} />}
          loading={busy("pull")}
          onClick={() => onAction("pull", project)}
          size="icon"
          variant="ghost"
        />
      </Tooltip>
      <Tooltip label="Redeploy">
        <Button
          aria-label={`Redeploy ${project.name}`}
          disabled={disabled("redeploy")}
          disabledReason={disabledReason("redeploy")}
          icon={<PackagePlus size={15} />}
          loading={busy("redeploy")}
          onClick={() => onAction("redeploy", project)}
          size="icon"
          variant="ghost"
        />
      </Tooltip>
      <Tooltip label="Down">
        <Button
          aria-label={`Down ${project.name}`}
          disabled={disabled("down")}
          disabledReason={disabledReason("down")}
          icon={<Square size={15} />}
          loading={busy("down")}
          onClick={() => onAction("down", project)}
          size="icon"
          variant="danger"
        />
      </Tooltip>
      <Tooltip label="Down with volumes">
        <Button
          aria-label={`Down with volumes ${project.name}`}
          disabled={disabled("down-volumes")}
          disabledReason={disabledReason("down-volumes")}
          icon={<Skull size={15} />}
          loading={busy("down-volumes")}
          onClick={() => onAction("down-volumes", project)}
          size="icon"
          variant="danger"
        />
      </Tooltip>
      <Tooltip label="Remove from list">
        <Button
          aria-label={`Remove from list ${project.name}`}
          icon={<Trash2 size={15} />}
          loading={busy("remove")}
          onClick={() => onAction("remove", project)}
          size="icon"
          variant="danger"
        />
      </Tooltip>
      <Tooltip label="Open folder">
        <Button
          aria-label={`Open folder ${project.name}`}
          disabled={!project.workingDir}
          icon={<FolderOpen size={15} />}
          onClick={() => onOpenFolder(project)}
          size="icon"
          variant="ghost"
        />
      </Tooltip>
    </div>
  );
}

const projectTabs: Array<[ProjectTabID, string]> = [
  ["overview", "Overview"],
  ["services", "Services"],
  ["containers", "Containers"],
  ["logs", "Logs"],
  ["updates", "Updates"],
  ["compose", "Compose"],
  ["backups", "Backups"],
];

function ProjectDetailPage({
  actionBusyIDs,
  backups,
  backupsError,
  backupsLoading,
  commandOutput,
  detail,
  dockerRunning,
  error,
  inventoryLoading,
  loading,
  lineage,
  lineageLoading,
  mutationsDisabled,
  mutationDisabledReason,
  onAction,
  onBack,
  onBackupVolume,
  onCheckUpdates,
  onClearCommandOutput,
  onContainerAction,
  onIgnoreUpdate,
  onOpenContainerDetail,
  onOpenContainerTerminal,
  onRefresh,
  onDeleteBackup,
  onRestoreBackup,
  onToast,
  onTabChange,
  onUpdateProject,
  onUpdateService,
  projectsLoading,
  projectVolumes,
  tab,
  updates,
}: {
  detail: ProjectDetail | null;
  actionBusyIDs: Set<string>;
  backups: BackupSummary[];
  backupsError: string | null;
  backupsLoading: boolean;
  commandOutput: ProjectCommandOutputState | null;
  dockerRunning: boolean;
  inventoryLoading: boolean;
  loading: boolean;
  lineage: ImageLineage[];
  lineageLoading: boolean;
  error: string | null;
  mutationsDisabled: boolean;
  mutationDisabledReason: string;
  projectVolumes: VolumeSummary[];
  tab: ProjectTabID;
  updates: ImageUpdate[];
  onAction: (action: ProjectAction, project: ProjectSummary) => void;
  onBack: () => void;
  onBackupVolume: (volume: VolumeSummary) => void;
  onCheckUpdates: () => void;
  onClearCommandOutput: (projectID: string) => void;
  onContainerAction: (
    action: ContainerAction,
    container: ContainerSummary,
  ) => void;
  onIgnoreUpdate: (update: ImageUpdate) => void;
  onOpenContainerDetail: (
    container: ContainerSummary,
    tab?: ContainerDrilldownTabID,
  ) => void;
  onOpenContainerTerminal: (container: ContainerSummary) => void;
  onRefresh: () => void;
  onDeleteBackup: (backup: BackupSummary) => void;
  onRestoreBackup: (backup: BackupSummary) => void;
  onToast: (toast: ToastInput) => void;
  onTabChange: (tab: ProjectTabID) => void;
  onUpdateProject: () => void;
  onUpdateService: (service: string) => void;
  projectsLoading: boolean;
}) {
  const openContainerDrilldown = useCallback(
    (
      container: ContainerSummary,
      tab: ContainerDrilldownTabID = "overview",
    ) => {
      onOpenContainerDetail(container, tab);
    },
    [onOpenContainerDetail],
  );

  if (loading && !detail) {
    return <TableSkeleton />;
  }
  if (!detail) {
    return (
      <EmptyState
        body={error ?? "Project detail is unavailable."}
        icon={<LayoutGrid size={28} />}
        title="Project not found"
      />
    );
  }

  const project = detail.summary;
  const primaryAction = primaryProjectAction(project);
  const disabledReason = (action: ProjectAction) =>
    projectActionDisabledReason(
      action,
      project,
      mutationsDisabled,
      mutationDisabledReason,
    );
  const disabled = (action: ProjectAction) => Boolean(disabledReason(action));
  const busy = (action: ProjectAction) =>
    actionBusyIDs.has(projectActionBusyKey(action, project.id));

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <Button onClick={onBack} size="sm" variant="ghost">
            Back
          </Button>
          <div className="mt-2 flex flex-wrap items-center gap-2">
            <h2 className="truncate text-2xl font-semibold">{project.name}</h2>
            <Badge tone={projectStatusTone(project.status)}>
              {project.status || "unknown"}
            </Badge>
            <Badge tone="info">{project.providerID}</Badge>
          </div>
          <div className="mt-2 max-w-3xl truncate text-sm text-text-muted">
            {project.workingDir || "No workdir"} - changed{" "}
            {relativeTime(dateMillis(project.lastChangedAt))}
          </div>
        </div>
        <div className="flex flex-wrap justify-end gap-2">
          <Button
            disabled={disabled(primaryAction)}
            disabledReason={disabledReason(primaryAction)}
            icon={
              primaryAction === "stop" ? (
                <Square size={15} />
              ) : (
                <Play size={15} />
              )
            }
            loading={busy(primaryAction)}
            onClick={() => onAction(primaryAction, project)}
          >
            {primaryAction === "stop" ? "Stop" : "Start"}
          </Button>
          <Button
            disabled={disabled("restart")}
            disabledReason={disabledReason("restart")}
            icon={<RotateCw size={15} />}
            loading={busy("restart")}
            onClick={() => onAction("restart", project)}
          >
            Restart
          </Button>
          <Button
            disabled={disabled("redeploy")}
            disabledReason={disabledReason("redeploy")}
            icon={<PackagePlus size={15} />}
            loading={busy("redeploy")}
            onClick={() => onAction("redeploy", project)}
          >
            Redeploy
          </Button>
          <Button
            disabled={disabled("pull")}
            disabledReason={disabledReason("pull")}
            icon={<Download size={15} />}
            loading={busy("pull")}
            onClick={() => onAction("pull", project)}
          >
            Pull
          </Button>
          <Button
            disabled={disabled("down")}
            disabledReason={disabledReason("down")}
            icon={<Square size={15} />}
            loading={busy("down")}
            onClick={() => onAction("down", project)}
            variant="danger"
          >
            Down
          </Button>
          <Button
            disabled={disabled("down-volumes")}
            disabledReason={disabledReason("down-volumes")}
            icon={<Skull size={15} />}
            loading={busy("down-volumes")}
            onClick={() => onAction("down-volumes", project)}
            variant="danger"
          >
            Down + volumes
          </Button>
          <Button
            icon={<Trash2 size={15} />}
            loading={busy("remove")}
            onClick={() => onAction("remove", project)}
            variant="danger"
          >
            Remove from list
          </Button>
          <Button
            icon={<RefreshCw size={15} />}
            loading={loading}
            onClick={onRefresh}
          >
            Refresh
          </Button>
        </div>
      </div>

      {error ? (
        <div className="rounded-card border border-error/30 bg-error/10 px-4 py-3 text-sm text-error">
          {error}
        </div>
      ) : null}

      {commandOutput ? (
        <ProjectCommandOutputPanel
          output={commandOutput}
          onClear={() => onClearCommandOutput(project.id)}
        />
      ) : null}

      <div className="flex flex-wrap gap-2 border-b border-border">
        {projectTabs.map(([id, label]) => (
          <button
            className={[
              "border-b-2 px-3 py-2 text-sm font-medium transition",
              tab === id
                ? "border-accent text-accent"
                : "border-transparent text-text-secondary hover:text-text-primary",
            ].join(" ")}
            key={id}
            onClick={() => onTabChange(id)}
            type="button"
          >
            {label}
          </button>
        ))}
      </div>

      {tab === "overview" ? (
        <ProjectOverviewTab
          actionBusyIDs={actionBusyIDs}
          detail={detail}
          dockerRunning={dockerRunning}
          mutationsDisabled={mutationsDisabled}
          mutationDisabledReason={mutationDisabledReason}
          onAction={onContainerAction}
          onOpenContainerDrilldown={openContainerDrilldown}
        />
      ) : null}
      {tab === "services" ? (
        <ProjectServicesTab
          detail={detail}
          onOpenContainerDrilldown={openContainerDrilldown}
        />
      ) : null}
      {tab === "containers" ? (
        <ProjectContainersTab
          actionBusyIDs={actionBusyIDs}
          detail={detail}
          dockerRunning={dockerRunning}
          mutationsDisabled={mutationsDisabled}
          mutationDisabledReason={mutationDisabledReason}
          onAction={onContainerAction}
          onOpenContainerDrilldown={openContainerDrilldown}
          onOpenContainerTerminal={onOpenContainerTerminal}
        />
      ) : null}
      {tab === "logs" ? (
        <LogsPage
          containers={detail.containers ?? []}
          dockerRunning={dockerRunning}
          initialProjectID={project.id}
          initialScope="project"
          inventoryLoading={inventoryLoading}
          lockedScope
          onToast={onToast}
          projects={[project]}
          projectsLoading={projectsLoading}
        />
      ) : null}
      {tab === "updates" ? (
        <ProjectUpdatesTab
          detail={detail}
          lineage={lineage}
          lineageLoading={lineageLoading}
          onCheckUpdates={onCheckUpdates}
          onIgnoreUpdate={onIgnoreUpdate}
          onUpdateProject={onUpdateProject}
          onUpdateService={onUpdateService}
          updates={updates}
        />
      ) : null}
      {tab === "compose" ? <ProjectComposeTab detail={detail} /> : null}
      {tab === "backups" ? (
        <ProjectBackupsTab
          backups={backups.filter((backup) => backup.projectID === project.id)}
          error={backupsError}
          loading={backupsLoading}
          mutationsDisabled={mutationsDisabled}
          mutationDisabledReason={mutationDisabledReason}
          onBackupVolume={onBackupVolume}
          onDeleteBackup={onDeleteBackup}
          onRestoreBackup={onRestoreBackup}
          volumes={projectVolumes}
        />
      ) : null}
    </div>
  );
}

function ProjectCommandOutputPanel({
  onClear,
  output,
}: {
  output: ProjectCommandOutputState;
  onClear: () => void;
}) {
  const title = projectCommandOutputTitle(output.action);
  const commandText = output.command || title;
  const transcript = [
    commandText ? `$ ${commandText}` : "",
    ...output.lines.map(
      (line) =>
        `[${formatLogTimestamp(new Date(line.ts))}] ${line.phase}: ${line.message}`,
    ),
  ]
    .filter(Boolean)
    .join("\n");
  return (
    <section
      aria-label="Compose command output"
      className="rounded-card border border-border bg-bg-card"
    >
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border px-4 py-3">
        <div className="flex min-w-0 items-center gap-3">
          <Terminal className="shrink-0 text-accent" size={18} />
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <h3 className="font-medium text-text-primary">{title}</h3>
              <Badge tone={projectCommandStatusTone(output.status)}>
                {output.status}
              </Badge>
            </div>
            <div className="mt-1 truncate font-mono text-xs text-text-muted">
              {commandText}
            </div>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Button
            icon={<Copy size={15} />}
            onClick={() => {
              if (transcript) {
                void Clipboard.SetText(transcript);
              }
            }}
            size="sm"
            variant="secondary"
          >
            Copy
          </Button>
          <Tooltip label="Dismiss output">
            <Button
              aria-label="Dismiss command output"
              icon={<X size={15} />}
              onClick={onClear}
              size="icon"
              variant="ghost"
            />
          </Tooltip>
        </div>
      </div>
      <div
        className="max-h-64 overflow-auto bg-bg-inset p-3 font-mono text-xs leading-5"
        role="log"
      >
        {output.lines.length === 0 ? (
          <div className="text-text-muted">Waiting for Compose output...</div>
        ) : (
          <div className="space-y-1">
            {output.lines.map((line) => (
              <div
                className="grid gap-2 rounded-control px-2 py-1 sm:grid-cols-[6rem_4.5rem_minmax(0,1fr)]"
                key={line.id}
              >
                <span className="text-text-muted">
                  {formatLogTimestamp(new Date(line.ts))}
                </span>
                <span className={projectCommandLineClass(line.tone)}>
                  {line.phase}
                </span>
                <span className="min-w-0 whitespace-pre-wrap break-words text-text-primary">
                  {line.message}
                </span>
              </div>
            ))}
          </div>
        )}
      </div>
    </section>
  );
}

function projectCommandOutputTitle(action?: string) {
  switch (action) {
    case "start":
      return "Start output";
    case "stop":
      return "Stop output";
    case "restart":
      return "Restart output";
    case "pull":
      return "Pull output";
    case "redeploy":
      return "Redeploy output";
    case "down":
      return "Down output";
    default:
      return "Compose output";
  }
}

function projectCommandStatusTone(
  status: ProjectCommandOutputState["status"],
): BadgeTone {
  switch (status) {
    case "success":
      return "ok";
    case "failed":
      return "error";
    default:
      return "info";
  }
}

function projectCommandLineClass(
  tone: ProjectCommandOutputLine["tone"],
): string {
  switch (tone) {
    case "error":
      return "font-semibold text-error";
    case "ok":
      return "font-semibold text-ok";
    case "info":
      return "font-semibold text-info";
    default:
      return "text-text-muted";
  }
}

function serviceDrilldownContainer(
  containers: ContainerSummary[],
  service: ComposeServiceRow,
): ContainerSummary | null {
  const matches = containers.filter(
    (container) => container.service === service.name,
  );
  if (matches.length === 0) {
    return null;
  }
  return matches.find(containerIsRunning) ?? matches[0];
}

function containerIsRunning(container: ContainerSummary): boolean {
  return (
    container.state.toLowerCase() === "running" ||
    container.status.toLowerCase().startsWith("up")
  );
}

function ServiceTitle({
  container,
  image,
  name,
  onOpenContainerDrilldown,
}: {
  container: ContainerSummary | null;
  image: string;
  name: string;
  onOpenContainerDrilldown: (
    container: ContainerSummary,
    tab?: ContainerDrilldownTabID,
  ) => void;
}) {
  if (!container) {
    return (
      <div className="min-w-0">
        <h3 className="truncate font-semibold text-text-primary">{name}</h3>
        <div className="mt-1 truncate font-mono text-xs text-text-muted">
          {image}
        </div>
      </div>
    );
  }

  return (
    <button
      aria-label={`Open ${name} container details`}
      className="group min-w-0 rounded-control text-left focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent"
      onClick={() => onOpenContainerDrilldown(container)}
      type="button"
    >
      <h3 className="truncate font-semibold text-accent group-hover:underline">
        {name}
      </h3>
      <div className="mt-1 truncate font-mono text-xs text-text-muted">
        {image}
      </div>
    </button>
  );
}

function ServiceNameButton({
  container,
  name,
  onOpenContainerDrilldown,
}: {
  container: ContainerSummary | null;
  name: string;
  onOpenContainerDrilldown: (
    container: ContainerSummary,
    tab?: ContainerDrilldownTabID,
  ) => void;
}) {
  if (!container) {
    return <span className="font-medium text-text-primary">{name}</span>;
  }

  return (
    <button
      className="min-w-0 truncate text-left font-medium text-accent hover:underline focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent"
      onClick={() => onOpenContainerDrilldown(container)}
      type="button"
    >
      {name}
    </button>
  );
}

function ProjectOverviewTab({
  actionBusyIDs,
  detail,
  dockerRunning,
  mutationsDisabled,
  mutationDisabledReason,
  onAction,
  onOpenContainerDrilldown,
}: {
  actionBusyIDs: Set<string>;
  detail: ProjectDetail;
  dockerRunning: boolean;
  mutationsDisabled: boolean;
  mutationDisabledReason: string;
  onAction: (action: ContainerAction, container: ContainerSummary) => void;
  onOpenContainerDrilldown: (
    container: ContainerSummary,
    tab?: ContainerDrilldownTabID,
  ) => void;
}) {
  const project = detail.summary;
  const services = detail.services ?? [];
  const containers = detail.containers ?? [];
  const actionDisabled = mutationsDisabled || !dockerRunning;
  const actionDisabledReason = mutationsDisabled
    ? mutationDisabledReason
    : "Docker engine is not running";
  return (
    <div className="space-y-4">
      <div className="grid gap-3 md:grid-cols-4">
        <StatusBlock
          label="Services"
          tone="info"
          value={project.servicesTotal}
        />
        <StatusBlock
          label="Running"
          tone={
            project.servicesRunning === project.servicesTotal ? "ok" : "warn"
          }
          value={project.servicesRunning}
        />
        <StatusBlock
          label="Containers"
          tone="neutral"
          value={containers.length}
        />
        <StatusBlock
          label="Updates"
          tone={projectActionableUpdateCount(project) > 0 ? "warn" : "ok"}
          value={projectActionableUpdateCount(project)}
        />
      </div>

      <section className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
        {services.map((service) => {
          const drilldownContainer = serviceDrilldownContainer(
            containers,
            service,
          );
          return (
            <Card key={service.name}>
              <CardBody className="space-y-3">
                <div className="flex items-start justify-between gap-2">
                  <ServiceTitle
                    container={drilldownContainer}
                    image={service.image || "build"}
                    name={service.name}
                    onOpenContainerDrilldown={onOpenContainerDrilldown}
                  />
                  <Badge tone={projectStatusTone(service.status)}>
                    {service.status || "unknown"}
                  </Badge>
                </div>
                <div className="grid grid-cols-2 gap-2 xl:grid-cols-4">
                  <MiniMetric
                    label="Replicas"
                    value={`${service.running}/${service.replicas}`}
                  />
                  <MiniMetric
                    label="CPU"
                    value={`${(service.cpuPercent ?? 0).toFixed(1)}%`}
                  />
                  <MiniMetric
                    label="RAM"
                    value={formatBytes(service.memoryBytes ?? 0)}
                  />
                  <MiniMetric
                    label="GPU"
                    value={formatGPUUsage(
                      service.gpuMemoryBytes,
                      service.gpuUtilizationPercent,
                    )}
                  />
                </div>
                <div className="flex flex-wrap items-center gap-2">
                  <Badge tone={healthTone(service.health)}>
                    {service.health || "unknown"}
                  </Badge>
                  <PortList ports={service.ports ?? []} />
                </div>
                {drilldownContainer ? (
                  <ContainerControlActions
                    actionBusyIDs={actionBusyIDs}
                    container={drilldownContainer}
                    mutationsDisabled={actionDisabled}
                    mutationDisabledReason={actionDisabledReason}
                    onAction={onAction}
                    showKill={false}
                  />
                ) : null}
              </CardBody>
            </Card>
          );
        })}
      </section>
    </div>
  );
}

function ProjectServicesTab({
  detail,
  onOpenContainerDrilldown,
}: {
  detail: ProjectDetail;
  onOpenContainerDrilldown: (
    container: ContainerSummary,
    tab?: ContainerDrilldownTabID,
  ) => void;
}) {
  const containers = detail.containers ?? [];
  return (
    <DataTable
      ariaLabel={`${detail.summary.name} services`}
      columns={[
        {
          id: "name",
          header: "Name",
          render: (service) => (
            <ServiceNameButton
              container={serviceDrilldownContainer(containers, service)}
              name={service.name}
              onOpenContainerDrilldown={onOpenContainerDrilldown}
            />
          ),
          sortValue: (service) => service.name,
          sortable: true,
        },
        {
          id: "image",
          header: "Image",
          render: (service) => service.image || "build",
          sortValue: (service) => service.image || "",
          sortable: true,
        },
        {
          id: "replicas",
          header: "Replicas",
          render: (service) => `${service.running}/${service.replicas}`,
          sortValue: (service) => service.replicas,
          sortable: true,
        },
        {
          id: "status",
          header: "Status",
          render: (service) => (
            <Badge tone={projectStatusTone(service.status)}>
              {service.status}
            </Badge>
          ),
          sortValue: (service) => service.status,
          sortable: true,
        },
        {
          id: "health",
          header: "Health",
          render: (service) => (
            <Badge tone={healthTone(service.health)}>{service.health}</Badge>
          ),
          sortValue: (service) => service.health,
          sortable: true,
        },
        {
          id: "ports",
          header: "Ports",
          render: (service) => <PortList ports={service.ports ?? []} />,
        },
        {
          id: "cpu",
          header: "CPU",
          render: (service) => `${(service.cpuPercent ?? 0).toFixed(1)}%`,
          sortValue: (service) => service.cpuPercent ?? 0,
          sortable: true,
        },
        {
          id: "ram",
          header: "RAM",
          render: (service) => formatBytes(service.memoryBytes ?? 0),
          sortValue: (service) => service.memoryBytes ?? 0,
          sortable: true,
        },
        {
          id: "gpu",
          header: "GPU",
          render: (service) =>
            formatGPUUsage(
              service.gpuMemoryBytes,
              service.gpuUtilizationPercent,
            ),
          sortValue: (service) => service.gpuMemoryBytes ?? 0,
          sortable: true,
        },
      ]}
      empty={
        <EmptyState
          body="No Compose services are recorded for this project."
          icon={<LayoutGrid size={28} />}
          title="No services found"
        />
      }
      getRowID={(service) => service.name}
      rows={detail.services ?? []}
    />
  );
}

function ProjectContainersTab({
  actionBusyIDs,
  detail,
  dockerRunning,
  mutationsDisabled,
  mutationDisabledReason,
  onAction,
  onOpenContainerDrilldown,
  onOpenContainerTerminal,
}: {
  actionBusyIDs: Set<string>;
  detail: ProjectDetail;
  dockerRunning: boolean;
  mutationsDisabled: boolean;
  mutationDisabledReason: string;
  onAction: (action: ContainerAction, container: ContainerSummary) => void;
  onOpenContainerDrilldown: (
    container: ContainerSummary,
    tab?: ContainerDrilldownTabID,
  ) => void;
  onOpenContainerTerminal: (container: ContainerSummary) => void;
}) {
  const containers = detail.containers ?? [];
  const actionDisabled = mutationsDisabled || !dockerRunning;
  const actionDisabledReason = mutationsDisabled
    ? mutationDisabledReason
    : "Docker engine is not running";

  return (
    <div className="space-y-4">
      <DataTable
        ariaLabel={`${detail.summary.name} containers`}
        columns={[
          {
            id: "name",
            header: "Name",
            render: (container) => (
              <button
                className="min-w-0 truncate text-left font-medium text-accent hover:underline"
                onClick={() => onOpenContainerDrilldown(container)}
                type="button"
              >
                {container.name}
              </button>
            ),
            sortValue: (container) => container.name,
            sortable: true,
          },
          {
            id: "service",
            header: "Service",
            render: (container) => container.service || "-",
            sortValue: (container) => container.service || "",
            sortable: true,
          },
          {
            id: "image",
            header: "Image",
            render: (container) => container.image,
            sortValue: (container) => container.image,
            sortable: true,
          },
          {
            id: "state",
            header: "State",
            render: (container) => (
              <Badge tone={containerTone(container)}>
                {container.state || container.status}
              </Badge>
            ),
            sortValue: (container) => container.state,
            sortable: true,
          },
          {
            id: "ports",
            header: "Ports",
            render: (container) => <PortList ports={container.ports ?? []} />,
          },
          {
            id: "cpu",
            header: "CPU",
            render: (container) => `${(container.cpuPercent ?? 0).toFixed(1)}%`,
            sortValue: (container) => container.cpuPercent ?? 0,
            sortable: true,
          },
          {
            id: "ram",
            header: "RAM",
            render: (container) =>
              formatMemory(container.memoryBytes, container.memoryLimit),
            sortValue: (container) => container.memoryBytes ?? 0,
            sortable: true,
          },
          {
            id: "gpu",
            header: "GPU",
            render: (container) =>
              formatGPUUsage(
                container.gpuMemoryBytes,
                container.gpuUtilizationPercent,
              ),
            sortValue: (container) => container.gpuMemoryBytes ?? 0,
            sortable: true,
          },
          {
            id: "actions",
            header: "Actions",
            render: (container) => (
              <div className="flex justify-end gap-1">
                <ContainerControlActions
                  actionBusyIDs={actionBusyIDs}
                  container={container}
                  mutationsDisabled={actionDisabled}
                  mutationDisabledReason={actionDisabledReason}
                  onAction={onAction}
                  showKill={false}
                />
                <Tooltip label="Open logs">
                  <Button
                    aria-label={`Open logs for ${container.name}`}
                    icon={<ScrollText size={15} />}
                    onClick={() => onOpenContainerDrilldown(container, "logs")}
                    size="icon"
                    variant="ghost"
                  />
                </Tooltip>
                <Tooltip label="Browse files">
                  <Button
                    aria-label={`Browse files for ${container.name}`}
                    icon={<FolderOpen size={15} />}
                    onClick={() => onOpenContainerDrilldown(container, "files")}
                    size="icon"
                    variant="ghost"
                  />
                </Tooltip>
                <Tooltip label="Open terminal">
                  <Button
                    aria-label={`Open terminal for ${container.name}`}
                    disabled={!dockerRunning}
                    disabledReason="Docker engine is not running"
                    icon={<Terminal size={15} />}
                    onClick={() => onOpenContainerTerminal(container)}
                    size="icon"
                    variant="ghost"
                  />
                </Tooltip>
                <Tooltip label="Inspect JSON">
                  <Button
                    aria-label={`Inspect ${container.name}`}
                    icon={<FileJson size={15} />}
                    onClick={() =>
                      onOpenContainerDrilldown(container, "inspect")
                    }
                    size="icon"
                    variant="ghost"
                  />
                </Tooltip>
              </div>
            ),
          },
        ]}
        empty={
          <EmptyState
            body="No containers are currently associated with this project."
            icon={<Container size={28} />}
            title="No project containers"
          />
        }
        getRowID={(container) => container.id}
        rows={containers}
      />
    </div>
  );
}

function ContainerDrilldownPanel({
  container,
  dockerRunning,
  inventoryLoading,
  onClose,
  onOpenContainerTerminal,
  onTabChange,
  onToast,
  project,
  projectsLoading,
  tab,
}: {
  container: ContainerSummary;
  dockerRunning: boolean;
  inventoryLoading: boolean;
  projectsLoading: boolean;
  project?: ProjectSummary | null;
  tab: ContainerDrilldownTabID;
  onClose: () => void;
  onOpenContainerTerminal: (container: ContainerSummary) => void;
  onTabChange: (tab: ContainerDrilldownTabID) => void;
  onToast: (toast: ToastInput) => void;
}) {
  const [detailState, setDetailState] = useState<{
    containerID: string;
    detail: ContainerDetail | null;
    error: string | null;
    status: LoadStatus;
  }>(() => ({
    containerID: container.id,
    detail: null,
    error: null,
    status: "loading",
  }));

  useEffect(() => {
    let cancelled = false;
    DockerService.GetContainer(container.id)
      .then((nextDetail) => {
        if (cancelled) {
          return;
        }
        setDetailState({
          containerID: container.id,
          detail: nextDetail,
          error: null,
          status: "ready",
        });
      })
      .catch((error: unknown) => {
        if (cancelled) {
          return;
        }
        setDetailState({
          containerID: container.id,
          detail: null,
          error:
            error instanceof Error ? error.message : "Unable to load container",
          status: "error",
        });
      });
    return () => {
      cancelled = true;
    };
  }, [container.id]);

  const detailCurrent = detailState.containerID === container.id;
  const detail = detailCurrent ? detailState.detail : null;
  const detailError = detailCurrent ? detailState.error : null;
  const detailStatus = detailCurrent ? detailState.status : "loading";
  const tabs: Array<[ContainerDrilldownTabID, string]> = [
    ["overview", "Overview"],
    ["logs", "Logs"],
    ["files", "Files"],
    ["terminal", "Terminal"],
    ["inspect", "Inspect"],
  ];
  const logProjectID = project?.id ?? container.projectID ?? "";
  const logProjects = project ? [project] : [];

  return (
    <Card>
      <CardHeader
        actions={
          <Button
            aria-label="Close container drilldown"
            icon={<X size={15} />}
            onClick={onClose}
            size="icon"
            variant="ghost"
          />
        }
        status={
          <Badge tone={containerTone(container)}>{container.state}</Badge>
        }
        title={
          <span className="flex min-w-0 items-center gap-2">
            <Container size={16} />
            <span className="truncate">{container.name}</span>
            <span className="font-mono text-xs font-normal text-text-muted">
              {shortID(container.id)}
            </span>
          </span>
        }
      />
      <CardBody className="space-y-4">
        <div className="flex flex-wrap gap-2 border-b border-border">
          {tabs.map(([id, label]) => (
            <button
              className={[
                "border-b-2 px-3 py-2 text-sm font-medium transition",
                tab === id
                  ? "border-accent text-accent"
                  : "border-transparent text-text-secondary hover:text-text-primary",
              ].join(" ")}
              key={id}
              onClick={() => onTabChange(id)}
              type="button"
            >
              {label}
            </button>
          ))}
        </div>

        {tab === "overview" ? (
          <ContainerOverviewPanel
            container={container}
            detail={detail}
            error={detailError}
            loading={detailStatus === "loading"}
            onOpenFiles={() => onTabChange("files")}
            onOpenLogs={() => onTabChange("logs")}
            onOpenTerminal={() => onOpenContainerTerminal(container)}
          />
        ) : null}
        {tab === "logs" ? (
          <LogsPage
            containers={[container]}
            dockerRunning={dockerRunning}
            initialContainerIDs={[container.id]}
            initialProjectID={logProjectID}
            initialScope="container"
            inventoryLoading={inventoryLoading}
            lockedScope
            onToast={onToast}
            projects={logProjects}
            projectsLoading={projectsLoading}
          />
        ) : null}
        {tab === "files" ? (
          <ContainerFilesPanel
            container={container}
            key={container.id}
            onToast={onToast}
          />
        ) : null}
        {tab === "terminal" ? (
          <div className="flex flex-wrap items-center justify-between gap-3 rounded-control border border-border bg-bg-inset p-3">
            <div className="min-w-0">
              <div className="font-medium text-text-primary">
                {container.name}
              </div>
              <div className="truncate font-mono text-xs text-text-muted">
                {container.image}
              </div>
            </div>
            <Button
              disabled={!dockerRunning}
              disabledReason="Docker engine is not running"
              icon={<Terminal size={15} />}
              onClick={() => onOpenContainerTerminal(container)}
            >
              Open terminal
            </Button>
          </div>
        ) : null}
        {tab === "inspect" ? (
          <ContainerInspectPanel container={container} />
        ) : null}
      </CardBody>
    </Card>
  );
}

function ContainerOverviewPanel({
  container,
  detail,
  error,
  loading,
  onOpenFiles,
  onOpenLogs,
  onOpenTerminal,
}: {
  container: ContainerSummary;
  detail: ContainerDetail | null;
  error: string | null;
  loading: boolean;
  onOpenFiles: () => void;
  onOpenLogs: () => void;
  onOpenTerminal: () => void;
}) {
  const rows = detail
    ? [
        ...containerRows(detail.summary),
        ["Working dir", detail.workingDir || "-"],
        ["User", detail.user || "-"],
        ["Restart policy", detail.restartPolicy || "-"],
      ]
    : containerRows(container);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap gap-2">
        <Button icon={<ScrollText size={15} />} onClick={onOpenLogs}>
          Logs
        </Button>
        <Button icon={<FolderOpen size={15} />} onClick={onOpenFiles}>
          Files
        </Button>
        <Button icon={<Terminal size={15} />} onClick={onOpenTerminal}>
          Terminal
        </Button>
      </div>
      {loading ? (
        <div className="grid gap-3 md:grid-cols-3">
          <Skeleton className="h-16" />
          <Skeleton className="h-16" />
          <Skeleton className="h-16" />
        </div>
      ) : null}
      {error ? (
        <div className="rounded-control border border-error/30 bg-error/10 px-3 py-2 text-sm text-error">
          {error}
        </div>
      ) : null}
      <div className="grid gap-3 md:grid-cols-3">
        {rows.map(([label, value]) => (
          <div
            className="min-w-0 rounded-control border border-border bg-bg-inset p-3"
            key={label}
          >
            <div className="text-xs text-text-muted">{label}</div>
            <div
              className="mt-1 truncate font-mono text-xs text-text-primary"
              title={value}
            >
              {value || "-"}
            </div>
          </div>
        ))}
      </div>
      {detail?.mounts?.length ? (
        <section className="space-y-2">
          <h3 className="text-sm font-medium text-text-primary">Mounts</h3>
          <div className="grid gap-2">
            {detail.mounts.map((mount) => (
              <div
                className="grid gap-2 rounded-control border border-border bg-bg-inset p-3 text-sm md:grid-cols-[120px_1fr_1fr_auto]"
                key={`${mount.type}:${mount.source}:${mount.target}`}
              >
                <Badge tone="neutral">{mount.type}</Badge>
                <span className="min-w-0 truncate font-mono text-xs text-text-secondary">
                  {mount.source || mount.volumeName || "-"}
                </span>
                <span className="min-w-0 truncate font-mono text-xs text-text-primary">
                  {mount.target}
                </span>
                <Badge tone={mount.readOnly ? "warn" : "ok"}>
                  {mount.readOnly ? "read-only" : "writable"}
                </Badge>
              </div>
            ))}
          </div>
        </section>
      ) : null}
      {detail?.env?.length ? (
        <details className="rounded-control border border-border bg-bg-inset">
          <summary className="cursor-pointer px-3 py-2 text-sm text-text-primary">
            Environment
          </summary>
          <div className="max-h-64 overflow-auto border-t border-border p-3 font-mono text-xs text-text-secondary">
            {detail.env.map((env) => (
              <div className="truncate" key={env.name} title={env.value}>
                {env.name}=<span className="text-text-muted">{env.value}</span>
              </div>
            ))}
          </div>
        </details>
      ) : null}
    </div>
  );
}

function ContainerFilesPanel({
  container,
  onToast,
}: {
  container: ContainerSummary;
  onToast: (toast: ToastInput) => void;
}) {
  const [pathValue, setPathValue] = useState("/");
  const [draftPath, setDraftPath] = useState("/");
  const [listingState, setListingState] = useState<{
    error: string | null;
    listing: ContainerFileListing | null;
    requestKey: string;
    status: LoadStatus;
  }>({
    error: null,
    listing: null,
    requestKey: "",
    status: "loading",
  });
  const [refreshNonce, setRefreshNonce] = useState(0);
  const requestKey = `${container.id}\n${pathValue}\n${refreshNonce}`;

  useEffect(() => {
    let cancelled = false;
    DockerService.ListContainerFiles(container.id, pathValue)
      .then((nextListing) => {
        if (cancelled) {
          return;
        }
        setListingState({
          error: null,
          listing: nextListing,
          requestKey,
          status: "ready",
        });
        setDraftPath(nextListing?.path ?? pathValue);
      })
      .catch((listError: unknown) => {
        if (cancelled) {
          return;
        }
        setListingState({
          error:
            listError instanceof Error
              ? listError.message
              : "Unable to list container files",
          listing: null,
          requestKey,
          status: "error",
        });
      });
    return () => {
      cancelled = true;
    };
  }, [container.id, pathValue, refreshNonce, requestKey]);

  const listingCurrent = listingState.requestKey === requestKey;
  const listing = listingCurrent ? listingState.listing : null;
  const status = listingCurrent ? listingState.status : "loading";
  const error = listingCurrent ? listingState.error : null;
  const entries = listing?.entries ?? [];
  const openPath = (nextPath: string) => {
    setPathValue(nextPath || "/");
    setDraftPath(nextPath || "/");
  };

  return (
    <div className="space-y-3">
      <form
        className="flex flex-wrap items-center gap-2"
        onSubmit={(event) => {
          event.preventDefault();
          openPath(draftPath);
        }}
      >
        <div className="relative min-w-72 flex-1">
          <FolderOpen
            className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-text-muted"
            size={16}
          />
          <input
            aria-label="Container path"
            className="h-9 w-full rounded-control border border-border bg-bg-inset pl-9 pr-3 font-mono text-sm text-text-primary"
            onChange={(event) => setDraftPath(event.currentTarget.value)}
            value={draftPath}
          />
        </div>
        <Button disabled={status === "loading"} type="submit">
          Open
        </Button>
        <Tooltip label="Parent folder">
          <Button
            aria-label="Open parent folder"
            disabled={!listing?.parentPath || status === "loading"}
            icon={<Undo2 size={15} />}
            onClick={() => openPath(listing?.parentPath ?? "/")}
            size="icon"
            variant="secondary"
          />
        </Tooltip>
        <Tooltip label="Refresh files">
          <Button
            aria-label="Refresh container files"
            icon={<RefreshCw size={15} />}
            loading={status === "loading"}
            onClick={() => setRefreshNonce((current) => current + 1)}
            size="icon"
            variant="secondary"
          />
        </Tooltip>
      </form>

      {error ? (
        <div className="rounded-control border border-error/30 bg-error/10 px-3 py-2 text-sm text-error">
          {error}
        </div>
      ) : null}

      <div className="overflow-hidden rounded-control border border-border">
        <table className="w-full text-left text-sm">
          <thead className="bg-bg-inset text-xs uppercase tracking-wide text-text-muted">
            <tr>
              <th className="px-3 py-2">Name</th>
              <th className="px-3 py-2">Type</th>
              <th className="px-3 py-2">Size</th>
              <th className="px-3 py-2">Mode</th>
              <th className="px-3 py-2">Modified</th>
              <th className="px-3 py-2 text-right">Actions</th>
            </tr>
          </thead>
          <tbody>
            {status === "loading" ? (
              <tr>
                <td className="px-3 py-6" colSpan={6}>
                  <Skeleton className="h-6 w-full" />
                </td>
              </tr>
            ) : null}
            {status !== "loading" && entries.length === 0 ? (
              <tr>
                <td className="px-3 py-8" colSpan={6}>
                  <EmptyState
                    body="This path has no visible entries."
                    icon={<FolderOpen size={28} />}
                    title="Empty folder"
                  />
                </td>
              </tr>
            ) : null}
            {status !== "loading"
              ? entries.map((entry) => {
                  const isDirectory = entry.type === "directory";
                  return (
                    <tr
                      className="border-t border-border hover:bg-bg-inset"
                      key={entry.path}
                    >
                      <td className="min-w-0 px-3 py-2">
                        {isDirectory ? (
                          <button
                            className="inline-flex min-w-0 items-center gap-2 text-accent hover:underline"
                            onClick={() => openPath(entry.path)}
                            type="button"
                          >
                            <FolderOpen size={15} />
                            <span className="truncate">{entry.name}</span>
                          </button>
                        ) : (
                          <span className="inline-flex min-w-0 items-center gap-2">
                            <FileJson size={15} className="text-text-muted" />
                            <span className="truncate">{entry.name}</span>
                          </span>
                        )}
                        {entry.linkTarget ? (
                          <div className="truncate pl-6 font-mono text-xs text-text-muted">
                            {entry.linkTarget}
                          </div>
                        ) : null}
                      </td>
                      <td className="px-3 py-2">
                        <Badge tone={isDirectory ? "info" : "neutral"}>
                          {entry.type}
                        </Badge>
                      </td>
                      <td className="px-3 py-2 font-mono text-xs">
                        {isDirectory ? "-" : formatBytes(entry.sizeBytes ?? 0)}
                      </td>
                      <td className="px-3 py-2 font-mono text-xs">
                        {entry.mode || "-"}
                      </td>
                      <td className="px-3 py-2 text-xs text-text-muted">
                        {entry.modifiedAt ? formatDate(entry.modifiedAt) : "-"}
                      </td>
                      <td className="px-3 py-2">
                        <div className="flex justify-end gap-1">
                          <Tooltip label="Copy path">
                            <Button
                              aria-label={`Copy path for ${entry.name}`}
                              icon={<Copy size={15} />}
                              onClick={() => {
                                void Clipboard.SetText(entry.path);
                                onToast({
                                  body: entry.path,
                                  level: "ok",
                                  title: "Path copied",
                                });
                              }}
                              size="icon"
                              variant="ghost"
                            />
                          </Tooltip>
                        </div>
                      </td>
                    </tr>
                  );
                })
              : null}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function ContainerInspectPanel({ container }: { container: ContainerSummary }) {
  const [inspectState, setInspectState] = useState<{
    containerID: string;
    error: string | null;
    raw: string;
    status: LoadStatus;
  }>(() => ({
    containerID: container.id,
    error: null,
    raw: "",
    status: "loading",
  }));

  useEffect(() => {
    let cancelled = false;
    DockerService.InspectContainerRaw(container.id)
      .then((nextRaw) => {
        if (cancelled) {
          return;
        }
        setInspectState({
          containerID: container.id,
          error: null,
          raw: formatJSON(nextRaw),
          status: "ready",
        });
      })
      .catch((inspectError: unknown) => {
        if (cancelled) {
          return;
        }
        setInspectState({
          containerID: container.id,
          error:
            inspectError instanceof Error
              ? inspectError.message
              : "Unable to inspect container",
          raw: "",
          status: "error",
        });
      });
    return () => {
      cancelled = true;
    };
  }, [container.id]);

  const inspectCurrent = inspectState.containerID === container.id;
  const raw = inspectCurrent ? inspectState.raw : "";
  const status = inspectCurrent ? inspectState.status : "loading";
  const error = inspectCurrent ? inspectState.error : null;

  if (status === "loading") {
    return <Skeleton className="h-96 w-full" />;
  }
  if (error) {
    return (
      <div className="rounded-control border border-error/30 bg-error/10 px-3 py-2 text-sm text-error">
        {error}
      </div>
    );
  }
  return (
    <pre className="max-h-[520px] overflow-auto rounded-control border border-border bg-bg-inset p-3 font-mono text-xs text-text-secondary">
      {raw || "{}"}
    </pre>
  );
}

function ProjectUpdatesTab({
  detail,
  lineage,
  lineageLoading,
  onCheckUpdates,
  onIgnoreUpdate,
  onUpdateProject,
  onUpdateService,
  updates,
}: {
  detail: ProjectDetail;
  lineage: ImageLineage[];
  lineageLoading: boolean;
  updates: ImageUpdate[];
  onCheckUpdates: () => void;
  onIgnoreUpdate: (update: ImageUpdate) => void;
  onUpdateProject: () => void;
  onUpdateService: (service: string) => void;
}) {
  const actionable = updates.filter(isActionableUpdate);
  const manualRows = updates.filter((update) => !isActionableUpdate(update));
  const groups = [
    {
      title: "Pull & recreate",
      rows: updates.filter(
        (update) =>
          update.recommendedAction === "pull_recreate" ||
          update.status === "service_image_update_available",
      ),
    },
    {
      title: "Rebuild & redeploy",
      rows: updates.filter(
        (update) =>
          update.recommendedAction === "rebuild_redeploy" ||
          update.status === "base_image_update_available" ||
          update.status === "rebuild_required",
      ),
    },
    {
      title: "Manual attention",
      rows: manualRows,
    },
  ];
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-wrap gap-2">
          {projectUpdateBadges(detail.summary)}
          {lineageLoading ? <Badge tone="info">Lineage loading</Badge> : null}
        </div>
        <div className="flex flex-wrap gap-2">
          <Button icon={<RefreshCw size={15} />} onClick={onCheckUpdates}>
            Check now
          </Button>
          <Button
            disabled={actionable.length === 0}
            disabledReason="No actionable update rows"
            icon={<PackagePlus size={15} />}
            onClick={onUpdateProject}
            variant="primary"
          >
            Update project
          </Button>
        </div>
      </div>

      {updates.length === 0 ? (
        <EmptyState
          body="All images up to date."
          icon={<CheckCircle2 size={28} />}
          title="All images up to date"
        />
      ) : null}

      {updates.length > 0 ? (
        <div className="grid gap-3 md:grid-cols-2">
          <div className="rounded-card border border-border bg-bg-inset px-3 py-2 text-sm">
            <div className="font-medium text-text-primary">
              {actionable.length} actionable update
              {actionable.length === 1 ? "" : "s"}
            </div>
            <div className="mt-1 text-xs text-text-muted">
              Update project applies rows in Pull & recreate or Rebuild &
              redeploy. Use Update service to apply a single row.
            </div>
          </div>
          <div className="rounded-card border border-border bg-bg-inset px-3 py-2 text-sm">
            <div className="font-medium text-text-primary">
              {manualRows.length} manual attention row
              {manualRows.length === 1 ? "" : "s"}
            </div>
            <div className="mt-1 text-xs text-text-muted">
              Manual rows cannot be auto-applied until the registry/base-image
              issue is resolved, or you can ignore them.
            </div>
          </div>
        </div>
      ) : null}

      {groups.map((group) => (
        <Card key={group.title}>
          <CardHeader
            actions={
              <Badge tone={group.rows.length > 0 ? "warn" : "neutral"}>
                {group.rows.length}
              </Badge>
            }
            title={group.title}
          />
          <CardBody className="space-y-2">
            {group.rows.length === 0 ? (
              <div className="text-sm text-text-muted">No services.</div>
            ) : (
              group.rows.map((update) => {
                const rowLineage = lineage.find(
                  (item) => item.service === update.service,
                );
                return (
                  <div
                    className="grid gap-3 rounded-control border border-border bg-bg-inset p-3 text-sm lg:grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)_auto]"
                    key={update.id}
                  >
                    <div className="min-w-0">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="font-medium text-text-primary">
                          {update.service || "-"}
                        </span>
                        <Badge tone={updateTone(update.status)}>
                          {updateStatusLabel(update.status)}
                        </Badge>
                      </div>
                      <div className="mt-1 truncate font-mono text-xs text-text-muted">
                        {update.currentImage}
                      </div>
                    </div>
                    <div className="min-w-0 space-y-1">
                      <div className="truncate text-xs text-text-muted">
                        Base image:{" "}
                        {rowLineage
                          ? lineageBaseText(rowLineage)
                          : update.baseImage || "-"}
                      </div>
                      <DigestDelta
                        local={update.localDigest}
                        remote={update.remoteDigest}
                      />
                      <div className="flex flex-wrap gap-2">
                        <ConfidenceChip
                          confidence={update.confidence}
                          reason={rowLineage?.reason}
                        />
                      </div>
                      {updateStatusNote(update) ? (
                        <div className="whitespace-normal break-words text-xs text-text-muted">
                          {updateStatusNote(update)}
                        </div>
                      ) : null}
                    </div>
                    <div className="flex items-center justify-end gap-2">
                      {isActionableUpdate(update) ? (
                        <Button
                          icon={<PackagePlus size={15} />}
                          onClick={() => onUpdateService(update.service ?? "")}
                          size="sm"
                        >
                          Update service
                        </Button>
                      ) : null}
                      <Button
                        icon={<Eye size={15} />}
                        onClick={() => onIgnoreUpdate(update)}
                        size="sm"
                        variant="secondary"
                      >
                        Ignore
                      </Button>
                    </div>
                  </div>
                );
              })
            )}
          </CardBody>
        </Card>
      ))}
    </div>
  );
}

function ProjectComposeTab({ detail }: { detail: ProjectDetail }) {
  const rawFiles = detail.compose?.rawFiles ?? [];
  const [selection, setSelection] = useState("resolved");
  const activeSelection =
    selection === "resolved" || rawFiles.some((file) => file.path === selection)
      ? selection
      : "resolved";
  const rawFile = rawFiles.find((file) => file.path === activeSelection);
  const value =
    activeSelection === "resolved"
      ? (detail.compose?.resolvedYAML ?? "")
      : (rawFile?.content ?? "");
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <Badge tone={detail.compose?.valid ? "ok" : "error"}>
          {detail.compose?.valid ? "valid" : "invalid"}
        </Badge>
        {(detail.compose?.envFiles ?? []).map((file) => (
          <Badge key={file} tone="neutral">
            {file}
          </Badge>
        ))}
      </div>
      {detail.compose?.errors?.length ? (
        <div className="rounded-card border border-error/30 bg-error/10 p-3 text-sm text-error">
          {detail.compose.errors.join("\n")}
        </div>
      ) : null}
      <div className="flex flex-wrap gap-2">
        <Button
          onClick={() => setSelection("resolved")}
          variant={activeSelection === "resolved" ? "primary" : "secondary"}
        >
          Resolved
        </Button>
        {rawFiles.map((file) => (
          <Button
            key={file.path}
            onClick={() => setSelection(file.path)}
            variant={activeSelection === file.path ? "primary" : "secondary"}
          >
            {shortPath(file.path)}
          </Button>
        ))}
      </div>
      <div className="overflow-hidden rounded-card border border-border">
        <Suspense fallback={<Skeleton className="h-[420px] w-full" />}>
          <MonacoEditor
            height="420px"
            language="yaml"
            options={{
              minimap: { enabled: false },
              readOnly: true,
              scrollBeyondLastLine: false,
              wordWrap: "on",
            }}
            theme="vs-dark"
            value={value || "# No Compose content available"}
          />
        </Suspense>
      </div>
    </div>
  );
}

function ProjectBackupsTab({
  backups,
  error,
  loading,
  mutationsDisabled,
  mutationDisabledReason,
  onBackupVolume,
  onDeleteBackup,
  onRestoreBackup,
  volumes,
}: {
  backups: BackupSummary[];
  error: string | null;
  loading: boolean;
  mutationsDisabled: boolean;
  mutationDisabledReason: string;
  volumes: VolumeSummary[];
  onBackupVolume: (volume: VolumeSummary) => void;
  onDeleteBackup: (backup: BackupSummary) => void;
  onRestoreBackup: (backup: BackupSummary) => void;
}) {
  return (
    <div className="space-y-4">
      {error ? (
        <div className="rounded-card border border-error/30 bg-error/10 px-3 py-2 text-sm text-error">
          {error}
        </div>
      ) : null}

      <Card>
        <CardHeader title="Project Volumes" />
        <CardBody>
          {volumes.length > 0 ? (
            <div className="grid gap-2">
              {volumes.map((volume) => (
                <div
                  className="flex flex-wrap items-center justify-between gap-3 rounded-control border border-border bg-bg-inset px-3 py-2 text-sm"
                  key={volume.name}
                >
                  <div className="min-w-0">
                    <div className="truncate font-medium text-text-primary">
                      {volume.name}
                    </div>
                    <div className="text-xs text-text-muted">
                      {volume.sizeBytes
                        ? formatBytes(volume.sizeBytes)
                        : "size unknown"}
                    </div>
                  </div>
                  <Button
                    disabled={mutationsDisabled}
                    disabledReason={mutationDisabledReason}
                    icon={<Download size={15} />}
                    onClick={() => onBackupVolume(volume)}
                    size="sm"
                  >
                    Backup
                  </Button>
                </div>
              ))}
            </div>
          ) : (
            <EmptyState
              body="Named project volumes appear here after inventory refresh."
              icon={<Database size={28} />}
              title="No project volumes"
            />
          )}
        </CardBody>
      </Card>

      <DataTable
        ariaLabel="Project backups"
        columns={[
          {
            id: "volume",
            header: "Volume",
            render: (backup) => backup.volumeName,
            sortable: true,
            sortValue: (backup) => backup.volumeName,
          },
          {
            id: "file",
            header: "File",
            render: (backup) => (
              <span title={backup.path}>{shortPath(backup.path)}</span>
            ),
          },
          {
            id: "size",
            header: "Size",
            render: (backup) =>
              backup.sizeBytes ? formatBytes(backup.sizeBytes) : "-",
            sortable: true,
            sortValue: (backup) => backup.sizeBytes,
          },
          {
            id: "result",
            header: "Result",
            render: (backup) => (
              <Badge tone={backup.result === "success" ? "ok" : "error"}>
                {backup.result || "unknown"}
              </Badge>
            ),
            sortable: true,
            sortValue: (backup) => backup.result ?? "",
          },
          {
            id: "created",
            header: "Created",
            render: (backup) => relativeTime(dateMillis(backup.createdAt)),
            sortable: true,
            sortValue: (backup) => dateMillis(backup.createdAt),
          },
          {
            id: "actions",
            header: "",
            render: (backup) => (
              <div className="flex justify-end gap-2">
                <Button
                  disabled={mutationsDisabled || backup.result !== "success"}
                  disabledReason={
                    backup.result !== "success"
                      ? "Only successful backups can be restored"
                      : mutationDisabledReason
                  }
                  icon={<Upload size={15} />}
                  onClick={() => onRestoreBackup(backup)}
                  size="sm"
                  variant="secondary"
                >
                  Restore
                </Button>
                <Button
                  disabled={mutationsDisabled}
                  disabledReason={mutationDisabledReason}
                  icon={<Trash2 size={15} />}
                  onClick={() => onDeleteBackup(backup)}
                  size="sm"
                  variant="danger"
                >
                  Delete
                </Button>
              </div>
            ),
          },
        ]}
        empty={
          <EmptyState
            body={
              loading
                ? "Loading backups..."
                : "Backups created for this project appear here."
            }
            icon={<Download size={28} />}
            title={loading ? "Loading backups" : "No backups yet"}
          />
        }
        getRowID={(backup) => backup.id}
        rows={backups}
      />
    </div>
  );
}

function MiniMetric({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-control border border-border bg-bg-inset px-2 py-2">
      <div className="text-[11px] uppercase text-text-muted">{label}</div>
      <div className="mt-1 truncate text-sm font-medium text-text-primary">
        {value}
      </div>
    </div>
  );
}

function ContainerDetailPage({
  actionBusyIDs,
  container,
  dockerRunning,
  inventoryLoading,
  mutationsDisabled,
  mutationDisabledReason,
  onAction,
  onBack,
  onOpenContainerTerminal,
  onTabChange,
  onToast,
  project,
  projectsLoading,
  tab,
}: {
  actionBusyIDs: Set<string>;
  container: ContainerSummary;
  dockerRunning: boolean;
  inventoryLoading: boolean;
  mutationsDisabled: boolean;
  mutationDisabledReason: string;
  project: ProjectSummary | null;
  projectsLoading: boolean;
  tab: ContainerDrilldownTabID;
  onAction: (action: ContainerAction, container: ContainerSummary) => void;
  onBack: () => void;
  onOpenContainerTerminal: (container: ContainerSummary) => void;
  onTabChange: (tab: ContainerDrilldownTabID) => void;
  onToast: (toast: ToastInput) => void;
}) {
  const projectLabel = project?.name ?? container.projectID ?? "Ungrouped";
  const actionDisabled = mutationsDisabled || !dockerRunning;
  const actionDisabledReason = mutationsDisabled
    ? mutationDisabledReason
    : "Docker engine is not running";
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <Button onClick={onBack} size="sm" variant="ghost">
            Back
          </Button>
          <div className="mt-2 flex flex-wrap items-center gap-2">
            <h2 className="truncate text-2xl font-semibold">
              {container.name}
            </h2>
            <Badge tone={containerTone(container)}>
              {container.state || "unknown"}
            </Badge>
            <Badge tone="info">{projectLabel}</Badge>
          </div>
          <div className="mt-2 max-w-3xl truncate font-mono text-sm text-text-muted">
            {container.image || "No image"} - {shortID(container.id)}
          </div>
        </div>
        <div className="flex flex-wrap justify-end gap-2">
          <ContainerControlActions
            actionBusyIDs={actionBusyIDs}
            container={container}
            labels
            mutationsDisabled={actionDisabled}
            mutationDisabledReason={actionDisabledReason}
            onAction={onAction}
          />
          <Button
            disabled={!dockerRunning}
            disabledReason="Docker engine is not running"
            icon={<Terminal size={15} />}
            onClick={() => onOpenContainerTerminal(container)}
          >
            Open terminal
          </Button>
        </div>
      </div>

      <ContainerDrilldownPanel
        container={container}
        dockerRunning={dockerRunning}
        inventoryLoading={inventoryLoading}
        onClose={onBack}
        onOpenContainerTerminal={onOpenContainerTerminal}
        onTabChange={onTabChange}
        onToast={onToast}
        project={project}
        projectsLoading={projectsLoading}
        tab={tab}
      />
    </div>
  );
}

type ContainersPageProps = {
  containers: ContainerSummary[];
  filter: FilterID;
  search: string;
  loading: boolean;
  mutationsDisabled: boolean;
  mutationDisabledReason: string;
  selectedIDs: Set<string>;
  actionBusyIDs: Set<string>;
  onAction: (action: ContainerAction, container: ContainerSummary) => void;
  onBulkAction: (action: Exclude<ContainerAction, "kill" | "remove">) => void;
  onFilterChange: (filter: FilterID) => void;
  onInspect: (container: ContainerSummary) => void;
  onOpen: (container: ContainerSummary) => void;
  onRename: (container: ContainerSummary) => void;
  onToggleAllSelection: (ids: string[], selected: boolean) => void;
  onToggleSelection: (id: string) => void;
};

function ContainersPage({
  actionBusyIDs,
  containers,
  filter,
  loading,
  mutationsDisabled,
  mutationDisabledReason,
  onAction,
  onBulkAction,
  onFilterChange,
  onInspect,
  onOpen,
  onRename,
  onToggleAllSelection,
  onToggleSelection,
  search,
  selectedIDs,
}: ContainersPageProps) {
  const filtered = useMemo(
    () => filterContainers(containers, search, filter),
    [containers, filter, search],
  );
  const counts = useMemo(() => containerFilterCounts(containers), [containers]);
  if (loading && containers.length === 0) {
    return <TableSkeleton />;
  }
  return (
    <div className="space-y-4">
      <FilterChips
        active={filter}
        items={[
          ["all", "All", counts.all],
          ["running", "Running", counts.running],
          ["stopped", "Stopped", counts.stopped],
          ["paused", "Paused", counts.paused],
          ["unhealthy", "Unhealthy", counts.unhealthy],
          ["ungrouped", "Ungrouped", counts.ungrouped],
        ]}
        onChange={onFilterChange}
      />
      <DataTable
        ariaLabel="Containers inventory"
        columns={[
          {
            id: "name",
            header: "Name",
            defaultWidth: 230,
            minWidth: 150,
            render: (container) => (
              <button
                aria-label={`Open ${container.name} container details`}
                className="group block min-w-0 rounded-control text-left focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent"
                onClick={() => onOpen(container)}
                type="button"
              >
                <div className="truncate font-medium text-accent group-hover:underline">
                  {container.name}
                </div>
                <div className="truncate text-xs text-text-muted">
                  {container.service || shortID(container.id)}
                </div>
              </button>
            ),
            sortable: true,
            sortValue: (container) => container.name,
          },
          {
            id: "status",
            header: "Status",
            defaultWidth: 130,
            minWidth: 110,
            render: (container) => (
              <Badge tone={containerTone(container)}>
                {container.state || "unknown"}
              </Badge>
            ),
            sortable: true,
            sortValue: (container) => container.state,
          },
          {
            id: "project",
            header: "Project",
            defaultWidth: 240,
            minWidth: 150,
            render: (container) => container.projectID || "-",
            sortable: true,
            sortValue: (container) => container.projectID || "",
          },
          {
            id: "cpu",
            header: "CPU",
            defaultWidth: 110,
            minWidth: 90,
            render: (container) => `${(container.cpuPercent ?? 0).toFixed(1)}%`,
            sortable: true,
            sortValue: (container) => container.cpuPercent ?? 0,
          },
          {
            id: "image",
            header: "Image",
            defaultWidth: 260,
            minWidth: 160,
            render: (container) => (
              <span title={container.image}>{container.image}</span>
            ),
            sortable: true,
            sortValue: (container) => container.image,
          },
          {
            id: "ports",
            header: "Ports",
            defaultWidth: 180,
            minWidth: 120,
            render: (container) => <PortList ports={container.ports ?? []} />,
          },
          {
            id: "memory",
            header: "Memory",
            defaultWidth: 140,
            minWidth: 110,
            render: (container) =>
              formatMemory(container.memoryBytes, container.memoryLimit),
            sortable: true,
            sortValue: (container) => container.memoryBytes ?? 0,
          },
          {
            id: "gpu",
            header: "GPU",
            defaultWidth: 120,
            minWidth: 100,
            render: (container) =>
              formatGPUUsage(
                container.gpuMemoryBytes,
                container.gpuUtilizationPercent,
              ),
            sortable: true,
            sortValue: (container) => container.gpuMemoryBytes ?? 0,
          },
          {
            id: "health",
            header: "Health",
            defaultWidth: 140,
            minWidth: 110,
            render: (container) => (
              <Badge tone={healthTone(container.health)}>
                {container.health || "unknown"}
              </Badge>
            ),
            sortable: true,
            sortValue: (container) => container.health,
          },
          {
            id: "restarts",
            header: "Restarts",
            defaultWidth: 110,
            minWidth: 90,
            render: (container) => (
              <span
                className={
                  (container.restarts ?? 0) > 3 ? "text-error" : undefined
                }
              >
                {container.restarts ?? 0}
              </span>
            ),
            sortable: true,
            sortValue: (container) => container.restarts ?? 0,
          },
          {
            id: "actions",
            header: "",
            defaultWidth: 190,
            hideable: false,
            minWidth: 160,
            render: (container) => (
              <ContainerRowActions
                busyIDs={actionBusyIDs}
                container={container}
                mutationsDisabled={mutationsDisabled}
                mutationDisabledReason={mutationDisabledReason}
                onAction={onAction}
                onInspect={onInspect}
                onRename={onRename}
              />
            ),
          },
        ]}
        bulkActions={
          <ContainerBulkActions
            busyIDs={actionBusyIDs}
            mutationsDisabled={mutationsDisabled}
            mutationDisabledReason={mutationDisabledReason}
            onAction={onBulkAction}
          />
        }
        empty={
          <EmptyState
            body="Run your first container or import a Compose project."
            icon={<Container size={28} />}
            title="No containers match"
          />
        }
        getRowID={(container) => container.id}
        onToggleAllRows={onToggleAllSelection}
        onToggleRow={onToggleSelection}
        rowLabel={(container) => container.name || shortID(container.id)}
        rows={filtered}
        selectedIDs={selectedIDs}
      />
    </div>
  );
}

type ImagesPageProps = {
  images: ImageSummary[];
  imageUseCounts: Record<string, number>;
  filter: FilterID;
  search: string;
  loading: boolean;
  mutationsDisabled: boolean;
  mutationDisabledReason: string;
  onFilterChange: (filter: FilterID) => void;
  onInspect: (image: ImageSummary) => void;
  onLoad: () => void;
  onPull: () => void;
  onPush: (image: ImageSummary) => void;
  onRemove: (image: ImageSummary) => void;
  onRun: (image?: ImageSummary) => void;
  onSave: (image: ImageSummary) => void;
  onTag: (image: ImageSummary) => void;
};

function ImagesPage({
  filter,
  imageUseCounts,
  images,
  loading,
  mutationsDisabled,
  mutationDisabledReason,
  onFilterChange,
  onInspect,
  onLoad,
  onPull,
  onPush,
  onRemove,
  onRun,
  onSave,
  onTag,
  search,
}: ImagesPageProps) {
  const filtered = useMemo(
    () => filterImages(images, imageUseCounts, search, filter),
    [filter, imageUseCounts, images, search],
  );
  const counts = useMemo(
    () => imageFilterCounts(images, imageUseCounts),
    [imageUseCounts, images],
  );
  if (loading && images.length === 0) {
    return <TableSkeleton />;
  }
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <FilterChips
          active={filter}
          items={[
            ["all", "All", counts.all],
            ["in-use", "In use", counts.inUse],
            ["unused", "Unused", counts.unused],
            ["dangling", "Dangling", counts.dangling],
            ["updates", "Update available", counts.updates],
          ]}
          onChange={onFilterChange}
        />
        <div className="flex items-center gap-2">
          <Button
            disabled={mutationsDisabled}
            disabledReason={mutationDisabledReason}
            icon={<Download size={15} />}
            onClick={onPull}
            variant="secondary"
          >
            Pull image
          </Button>
          <Button
            disabled={mutationsDisabled}
            disabledReason={mutationDisabledReason}
            icon={<Upload size={15} />}
            onClick={onLoad}
            variant="secondary"
          >
            Load tar
          </Button>
          <Button
            disabled={mutationsDisabled}
            disabledReason={mutationDisabledReason}
            icon={<PackagePlus size={15} />}
            onClick={() => onRun()}
            variant="primary"
          >
            Run image
          </Button>
        </div>
      </div>
      <DataTable
        ariaLabel="Images inventory"
        columns={[
          {
            id: "repo",
            header: "Repository",
            render: (image) => imageRepo(image),
            sortable: true,
            sortValue: (image) => imageRepo(image),
          },
          {
            id: "tag",
            header: "Tag",
            render: (image) => imageTag(image),
            sortable: true,
            sortValue: (image) => imageTag(image),
          },
          {
            id: "id",
            header: "Image ID",
            render: (image) => <MonoCopy value={image.id} />,
            sortable: true,
            sortValue: (image) => image.id,
          },
          {
            id: "size",
            header: "Size",
            render: (image) => formatBytes(image.sizeBytes),
            sortable: true,
            sortValue: (image) => image.sizeBytes,
          },
          {
            id: "created",
            header: "Created",
            render: (image) => formatDate(image.createdAt),
            sortable: true,
            sortValue: (image) => dateMillis(image.createdAt),
          },
          {
            id: "used-by",
            header: "Used by",
            render: (image) => (
              <Badge
                tone={
                  (imageUseCounts[image.id] ?? 0) > 0 || image.inUse
                    ? "accent"
                    : "neutral"
                }
              >
                {imageUseCounts[image.id] ?? (image.inUse ? ">=1" : 0)}
              </Badge>
            ),
          },
          {
            id: "update",
            header: "Update",
            render: (image) => (
              <Badge tone={updateTone(image.updateStatus)}>
                {image.updateStatus || "unknown"}
              </Badge>
            ),
          },
          {
            id: "actions",
            header: "",
            render: (image) => (
              <ImageRowActions
                image={image}
                mutationsDisabled={mutationsDisabled}
                mutationDisabledReason={mutationDisabledReason}
                onInspect={() => onInspect(image)}
                onPush={() => onPush(image)}
                onRemove={() => onRemove(image)}
                onRun={() => onRun(image)}
                onSave={() => onSave(image)}
                onTag={() => onTag(image)}
              />
            ),
          },
        ]}
        empty={
          <EmptyState
            body="No images yet - pull one or import a project."
            icon={<Box size={28} />}
            title="No images match"
          />
        }
        getRowID={(image) => image.id}
        rows={filtered}
      />
    </div>
  );
}

type VolumesPageProps = {
  volumes: VolumeSummary[];
  volumeDetails: Record<string, VolumeDetail>;
  filter: FilterID;
  search: string;
  loading: boolean;
  mutationsDisabled: boolean;
  mutationDisabledReason: string;
  onBackup: (volume: VolumeSummary) => void;
  onCreate: () => void;
  onFilterChange: (filter: FilterID) => void;
  onInspect: (volume: VolumeSummary) => void;
  onRemove: (volume: VolumeSummary) => void;
  onRestore: (volume: VolumeSummary) => void;
};

function VolumesPage({
  filter,
  loading,
  mutationsDisabled,
  mutationDisabledReason,
  onBackup,
  onCreate,
  onFilterChange,
  onInspect,
  onRemove,
  onRestore,
  search,
  volumeDetails,
  volumes,
}: VolumesPageProps) {
  const filtered = useMemo(
    () => filterVolumes(volumes, search, filter),
    [filter, search, volumes],
  );
  const counts = useMemo(() => volumeFilterCounts(volumes), [volumes]);
  if (loading && volumes.length === 0) {
    return <TableSkeleton />;
  }
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <FilterChips
          active={filter}
          items={[
            ["all", "All", counts.all],
            ["in-use", "In use", counts.inUse],
            ["unused", "Unused", counts.unused],
          ]}
          onChange={onFilterChange}
        />
        <Button
          disabled={mutationsDisabled}
          disabledReason={mutationDisabledReason}
          icon={<Plus size={15} />}
          onClick={onCreate}
          variant="primary"
        >
          Create volume
        </Button>
      </div>
      <DataTable
        ariaLabel="Volumes inventory"
        columns={[
          {
            id: "name",
            header: "Name",
            render: (volume) => (
              <span className="text-text-primary">{volume.name}</span>
            ),
            sortable: true,
            sortValue: (volume) => volume.name,
          },
          {
            id: "driver",
            header: "Driver",
            render: (volume) => volume.driver,
            sortable: true,
            sortValue: (volume) => volume.driver,
          },
          {
            id: "size",
            header: "Size",
            render: (volume) =>
              volume.sizeBytes ? formatBytes(volume.sizeBytes) : "-",
            sortable: true,
            sortValue: (volume) => volume.sizeBytes ?? 0,
          },
          {
            id: "project",
            header: "Project",
            render: (volume) => volume.labels?.[composeProjectLabel] ?? "-",
            sortable: true,
            sortValue: (volume) => volume.labels?.[composeProjectLabel] ?? "",
          },
          {
            id: "used-by",
            header: "Used by",
            render: (volume) => (
              <Badge tone={volume.inUse ? "accent" : "neutral"}>
                {volumeDetails[volume.name]?.containers?.length ??
                  (volume.inUse ? ">=1" : 0)}
              </Badge>
            ),
          },
          {
            id: "mountpoint",
            header: "Mountpoint",
            render: (volume) => (
              <span title={volume.mountpoint}>{volume.mountpoint || "-"}</span>
            ),
          },
          {
            id: "actions",
            header: "",
            render: (volume) => (
              <div className="flex justify-end gap-1">
                <Tooltip label="Backup">
                  <Button
                    aria-label={`Backup ${volume.name}`}
                    disabled={mutationsDisabled}
                    disabledReason={mutationDisabledReason}
                    icon={<Download size={15} />}
                    onClick={() => onBackup(volume)}
                    size="icon"
                    variant="ghost"
                  />
                </Tooltip>
                <Tooltip label="Restore">
                  <Button
                    aria-label={`Restore ${volume.name}`}
                    disabled={mutationsDisabled}
                    disabledReason={mutationDisabledReason}
                    icon={<Upload size={15} />}
                    onClick={() => onRestore(volume)}
                    size="icon"
                    variant="ghost"
                  />
                </Tooltip>
                <RowActions
                  id={volume.name}
                  label={volume.name}
                  mutationsDisabled={mutationsDisabled}
                  mutationDisabledReason={mutationDisabledReason}
                  onInspect={() => onInspect(volume)}
                  onRemove={() => onRemove(volume)}
                />
              </div>
            ),
          },
        ]}
        empty={
          <EmptyState
            body="No volumes - they appear when containers create them."
            icon={<Database size={28} />}
            title="No volumes match"
          />
        }
        getRowID={(volume) => volume.name}
        rows={filtered}
      />
    </div>
  );
}

type NetworksPageProps = {
  networks: NetworkSummary[];
  networkDetails: Record<string, NetworkDetail>;
  search: string;
  loading: boolean;
  mutationsDisabled: boolean;
  mutationDisabledReason: string;
  onCreate: () => void;
  onInspect: (network: NetworkSummary) => void;
  onRemove: (network: NetworkSummary) => void;
};

function NetworksPage({
  loading,
  mutationsDisabled,
  mutationDisabledReason,
  networkDetails,
  networks,
  onCreate,
  onInspect,
  onRemove,
  search,
}: NetworksPageProps) {
  const filtered = useMemo(
    () => filterNetworks(networks, search),
    [networks, search],
  );
  if (loading && networks.length === 0) {
    return <TableSkeleton />;
  }
  return (
    <div className="space-y-4">
      <div className="flex justify-end">
        <Button
          disabled={mutationsDisabled}
          disabledReason={mutationDisabledReason}
          icon={<Plus size={15} />}
          onClick={onCreate}
          variant="primary"
        >
          Create network
        </Button>
      </div>
      <DataTable
        ariaLabel="Networks inventory"
        columns={[
          {
            id: "name",
            header: "Name",
            render: (network) => (
              <button
                className="text-left font-medium text-text-primary hover:text-accent hover:underline"
                onClick={() => onInspect(network)}
                type="button"
              >
                {network.name}
              </button>
            ),
            sortable: true,
            sortValue: (network) => network.name,
          },
          {
            id: "driver",
            header: "Driver",
            render: (network) => network.driver,
            sortable: true,
            sortValue: (network) => network.driver,
          },
          {
            id: "scope",
            header: "Scope",
            render: (network) => network.scope || "-",
            sortable: true,
            sortValue: (network) => network.scope || "",
          },
          {
            id: "subnet",
            header: "Subnet",
            render: (network) => networkDetails[network.id]?.subnet || "-",
          },
          {
            id: "gateway",
            header: "Gateway",
            render: (network) => networkDetails[network.id]?.gateway || "-",
          },
          {
            id: "containers",
            header: "Containers",
            render: (network) => (
              <Badge tone="neutral">
                {networkDetails[network.id]?.containers?.length ?? 0}
              </Badge>
            ),
          },
          {
            id: "internal",
            header: "Internal",
            render: (network) => (
              <Badge tone={network.internal ? "info" : "neutral"}>
                {network.internal ? "yes" : "no"}
              </Badge>
            ),
            sortable: true,
            sortValue: (network) => (network.internal ? 1 : 0),
          },
          {
            id: "actions",
            header: "",
            render: (network) => (
              <RowActions
                id={network.id}
                label={network.name}
                mutationsDisabled={mutationsDisabled}
                mutationDisabledReason={mutationDisabledReason}
                inspectLabel="Open network"
                onInspect={() => onInspect(network)}
                onRemove={() => onRemove(network)}
              />
            ),
          },
        ]}
        empty={
          <EmptyState
            body="System-only networks are normal on a new daemon."
            icon={<Network size={28} />}
            title="No networks match"
          />
        }
        getRowID={(network) => network.id}
        rows={filtered}
      />
    </div>
  );
}

type NetworkDetailPageProps = {
  containers: ContainerSummary[];
  detail?: NetworkDetail;
  loading: boolean;
  mutationsDisabled: boolean;
  mutationDisabledReason: string;
  network?: NetworkSummary;
  tab: NetworkTabID;
  onBack: () => void;
  onOpenContainerInspect: (container: ContainerSummary) => void;
  onOpenContainerTerminal: (container: ContainerSummary) => void;
  onRefresh: () => Promise<void>;
  onRemove: (network: NetworkSummary) => void;
  onTabChange: (tab: NetworkTabID) => void;
};

function NetworkDetailPage({
  containers,
  detail,
  loading,
  mutationsDisabled,
  mutationDisabledReason,
  network,
  onBack,
  onOpenContainerInspect,
  onOpenContainerTerminal,
  onRefresh,
  onRemove,
  onTabChange,
  tab,
}: NetworkDetailPageProps) {
  const summary = detail?.summary ?? network;
  const attachedContainers = useMemo(
    () => mergeNetworkContainers(detail?.containers ?? [], containers),
    [containers, detail?.containers],
  );
  const labelRows = useMemo(
    () =>
      Object.entries(summary?.labels ?? {})
        .sort(([left], [right]) => left.localeCompare(right))
        .map(([key, value]) => ({ key, value: value ?? "" })),
    [summary?.labels],
  );
  const tabs: Array<[NetworkTabID, string, number | undefined]> = [
    ["overview", "Overview", undefined],
    ["containers", "Containers", attachedContainers.length],
    ["labels", "Labels", labelRows.length],
    ["raw", "Raw", undefined],
  ];

  if (!summary) {
    return (
      <div className="space-y-4">
        <Button onClick={onBack} size="sm" variant="ghost">
          Back
        </Button>
        <EmptyState
          body="The network is no longer present in the active Docker backend."
          icon={<Network size={28} />}
          title="Network not found"
        />
      </div>
    );
  }

  const rawValue =
    detail?.rawJSON && detail.rawJSON.trim().length > 0
      ? detail.rawJSON
      : JSON.stringify(
          detail
            ? { ...detail, containers: attachedContainers }
            : { summary, containers: attachedContainers },
          null,
          2,
        );

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 space-y-2">
          <Button onClick={onBack} size="sm" variant="ghost">
            Back
          </Button>
          <div className="flex min-w-0 flex-wrap items-center gap-2">
            <h2 className="truncate text-2xl font-semibold">{summary.name}</h2>
            <Badge tone="info">{summary.driver || "unknown"}</Badge>
            <Badge tone={summary.internal ? "warn" : "neutral"}>
              {summary.internal ? "internal" : "external"}
            </Badge>
            {summary.attachable ? <Badge tone="ok">attachable</Badge> : null}
          </div>
          <div className="truncate font-mono text-xs text-text-muted">
            {summary.id}
          </div>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button
            icon={<RefreshCw size={15} />}
            loading={loading}
            onClick={() => {
              void onRefresh();
            }}
            variant="secondary"
          >
            Refresh
          </Button>
          <Button
            disabled={mutationsDisabled}
            disabledReason={mutationDisabledReason}
            icon={<Trash2 size={15} />}
            onClick={() => onRemove(summary)}
            variant="danger"
          >
            Remove network
          </Button>
        </div>
      </div>

      <div className="flex flex-wrap gap-2 border-b border-border">
        {tabs.map(([id, label, count]) => (
          <button
            className={[
              "inline-flex items-center gap-2 border-b-2 px-3 py-2 text-sm font-medium transition",
              tab === id
                ? "border-accent text-accent"
                : "border-transparent text-text-secondary hover:text-text-primary",
            ].join(" ")}
            key={id}
            onClick={() => onTabChange(id)}
            type="button"
          >
            <span>{label}</span>
            {typeof count === "number" ? <Badge>{count}</Badge> : null}
          </button>
        ))}
      </div>

      {tab === "overview" ? (
        <NetworkOverviewTab
          attachedContainers={attachedContainers}
          detail={detail}
          labelCount={labelRows.length}
          network={summary}
          onOpenContainers={() => onTabChange("containers")}
        />
      ) : null}

      {tab === "containers" ? (
        <NetworkContainersTab
          containers={attachedContainers}
          onOpenContainerInspect={onOpenContainerInspect}
          onOpenContainerTerminal={onOpenContainerTerminal}
        />
      ) : null}

      {tab === "labels" ? <NetworkLabelsTab labels={labelRows} /> : null}

      {tab === "raw" ? (
        <Card>
          <CardHeader
            status={
              <Badge tone="neutral">{detail ? "loaded" : "summary"}</Badge>
            }
            title="Network detail JSON"
          />
          <CardBody>
            <CodePreview value={rawValue} />
          </CardBody>
        </Card>
      ) : null}
    </div>
  );
}

function NetworkOverviewTab({
  attachedContainers,
  detail,
  labelCount,
  network,
  onOpenContainers,
}: {
  attachedContainers: ContainerSummary[];
  detail?: NetworkDetail;
  labelCount: number;
  network: NetworkSummary;
  onOpenContainers: () => void;
}) {
  const rows = [
    ["Driver", network.driver || "-"],
    ["Scope", network.scope || "-"],
    ["Subnet", detail?.subnet || "-"],
    ["Gateway", detail?.gateway || "-"],
    ["IPAM ranges", String(detail?.ipam?.length ?? 0)],
    ["Options", String(Object.keys(detail?.options ?? {}).length)],
    ["Containers", String(attachedContainers.length)],
    ["Labels", String(labelCount)],
    ["Internal", network.internal ? "yes" : "no"],
    ["Attachable", network.attachable ? "yes" : "no"],
  ];

  return (
    <div className="space-y-4">
      <div className="grid gap-3 md:grid-cols-4">
        {rows.map(([label, value]) => (
          <div
            className="min-w-0 rounded-control border border-border bg-bg-inset p-3"
            key={label}
          >
            <div className="text-xs text-text-muted">{label}</div>
            <div
              className="mt-1 truncate font-mono text-xs text-text-primary"
              title={value}
            >
              {value}
            </div>
          </div>
        ))}
      </div>

      <Card>
        <CardHeader
          actions={
            <Button onClick={onOpenContainers} size="sm" variant="secondary">
              View containers
            </Button>
          }
          status={<Badge tone="neutral">{attachedContainers.length}</Badge>}
          title="Attached containers"
        />
        <CardBody>
          {attachedContainers.length > 0 ? (
            <div className="grid gap-2 lg:grid-cols-2">
              {attachedContainers.slice(0, 6).map((container) => (
                <div
                  className="grid gap-2 rounded-control border border-border bg-bg-inset p-3 text-sm md:grid-cols-[1fr_auto]"
                  key={container.id}
                >
                  <div className="min-w-0">
                    <div className="truncate font-medium text-text-primary">
                      {container.name || shortID(container.id)}
                    </div>
                    <div className="truncate font-mono text-xs text-text-muted">
                      {networkContainerIP(container) || "No IP recorded"}
                    </div>
                  </div>
                  <Badge tone={containerTone(container)}>
                    {container.state}
                  </Badge>
                </div>
              ))}
            </div>
          ) : (
            <EmptyState
              body="No containers are currently attached to this network."
              icon={<Container size={28} />}
              title="No attached containers"
            />
          )}
        </CardBody>
      </Card>
    </div>
  );
}

function NetworkContainersTab({
  containers,
  onOpenContainerInspect,
  onOpenContainerTerminal,
}: {
  containers: ContainerSummary[];
  onOpenContainerInspect: (container: ContainerSummary) => void;
  onOpenContainerTerminal: (container: ContainerSummary) => void;
}) {
  return (
    <DataTable
      ariaLabel="Network containers"
      columns={[
        {
          id: "name",
          header: "Name",
          defaultWidth: 220,
          render: (container) => (
            <div className="min-w-0">
              <button
                className="truncate text-left font-medium text-text-primary hover:text-accent hover:underline"
                onClick={() => onOpenContainerInspect(container)}
                title={container.name}
                type="button"
              >
                {container.name || shortID(container.id)}
              </button>
              <div className="truncate font-mono text-xs text-text-muted">
                {shortID(container.id)}
              </div>
            </div>
          ),
          sortable: true,
          sortValue: (container) => container.name,
        },
        {
          id: "status",
          header: "Status",
          defaultWidth: 120,
          render: (container) => (
            <Badge tone={containerTone(container)}>{container.state}</Badge>
          ),
          sortable: true,
          sortValue: (container) => container.state,
        },
        {
          id: "service",
          header: "Service",
          defaultWidth: 140,
          render: (container) => container.service || "-",
          sortable: true,
          sortValue: (container) => container.service || "",
        },
        {
          id: "image",
          header: "Image",
          defaultWidth: 220,
          render: (container) => container.image || "-",
          wrap: true,
        },
        {
          id: "ipv4",
          header: "IPv4",
          defaultWidth: 150,
          render: (container) => container.ipv4Address || "-",
          sortable: true,
          sortValue: (container) => container.ipv4Address || "",
        },
        {
          id: "ipv6",
          header: "IPv6",
          defaultWidth: 180,
          render: (container) => container.ipv6Address || "-",
          sortable: true,
          sortValue: (container) => container.ipv6Address || "",
        },
        {
          id: "gateway",
          header: "Gateway",
          defaultWidth: 140,
          render: (container) => container.gateway || "-",
        },
        {
          id: "mac",
          header: "MAC",
          defaultWidth: 150,
          render: (container) => container.macAddress || "-",
        },
        {
          id: "ports",
          header: "Ports",
          defaultWidth: 180,
          render: (container) => formatContainerPorts(container.ports),
          wrap: true,
        },
        {
          id: "cpu",
          header: "CPU",
          defaultWidth: 90,
          render: (container) => `${(container.cpuPercent ?? 0).toFixed(1)}%`,
          sortable: true,
          sortValue: (container) => container.cpuPercent ?? 0,
        },
        {
          id: "memory",
          header: "Memory",
          defaultWidth: 130,
          render: (container) =>
            formatMemory(container.memoryBytes, container.memoryLimit),
          sortable: true,
          sortValue: (container) => container.memoryBytes ?? 0,
        },
        {
          id: "gpu",
          header: "GPU",
          defaultWidth: 110,
          render: (container) =>
            formatGPUUsage(
              container.gpuMemoryBytes,
              container.gpuUtilizationPercent,
            ),
          sortable: true,
          sortValue: (container) => container.gpuMemoryBytes ?? 0,
        },
        {
          id: "io",
          header: "Network IO",
          defaultWidth: 180,
          render: (container) =>
            `${formatRate(container.netRxRate ?? 0)} RX / ${formatRate(
              container.netTxRate ?? 0,
            )} TX`,
        },
        {
          id: "aliases",
          header: "Aliases",
          defaultWidth: 180,
          render: (container) => container.aliases?.join(", ") || "-",
          wrap: true,
        },
        {
          id: "actions",
          header: "",
          defaultWidth: 96,
          render: (container) => (
            <div className="flex justify-end gap-1">
              <Tooltip label="Inspect container">
                <Button
                  aria-label={`Inspect container ${container.name}`}
                  icon={<Eye size={15} />}
                  onClick={() => onOpenContainerInspect(container)}
                  size="icon"
                  variant="ghost"
                />
              </Tooltip>
              <Tooltip label="Open terminal">
                <Button
                  aria-label={`Open terminal for ${container.name}`}
                  disabled={container.state !== "running"}
                  disabledReason="Container is not running"
                  icon={<Terminal size={15} />}
                  onClick={() => onOpenContainerTerminal(container)}
                  size="icon"
                  variant="ghost"
                />
              </Tooltip>
            </div>
          ),
        },
      ]}
      empty={
        <EmptyState
          body="No containers are currently attached to this network."
          icon={<Container size={28} />}
          title="No attached containers"
        />
      }
      getRowID={(container) => container.id}
      rows={containers}
    />
  );
}

function NetworkLabelsTab({
  labels,
}: {
  labels: Array<{ key: string; value: string }>;
}) {
  return (
    <DataTable
      ariaLabel="Network labels"
      columns={[
        {
          id: "key",
          header: "Key",
          defaultWidth: 320,
          render: (label) => label.key,
          sortable: true,
          sortValue: (label) => label.key,
        },
        {
          id: "value",
          header: "Value",
          defaultWidth: 420,
          render: (label) => label.value || "-",
          wrap: true,
        },
        {
          id: "actions",
          header: "",
          defaultWidth: 72,
          render: (label) => (
            <div className="flex justify-end">
              <Tooltip label="Copy value">
                <Button
                  aria-label={`Copy ${label.key}`}
                  icon={<Copy size={15} />}
                  onClick={() => {
                    void Clipboard.SetText(label.value);
                  }}
                  size="icon"
                  variant="ghost"
                />
              </Tooltip>
            </div>
          ),
        },
      ]}
      empty={
        <EmptyState
          body="This network does not expose Docker labels."
          icon={<Tag size={28} />}
          title="No labels"
        />
      }
      getRowID={(label) => label.key}
      rows={labels}
    />
  );
}

function SearchBox({
  inputRef,
  onChange,
  value,
}: {
  inputRef?: RefObject<HTMLInputElement>;
  value: string;
  onChange: (value: string) => void;
}) {
  return (
    <label className="flex h-9 w-full items-center gap-2 rounded-control border border-border bg-bg-inset px-3 text-sm text-text-muted sm:w-72">
      <Search size={16} />
      <input
        aria-label="Search inventory"
        className="min-w-0 flex-1 bg-transparent text-text-primary outline-none placeholder:text-text-muted"
        onChange={(event) => onChange(event.target.value)}
        placeholder="Search"
        ref={inputRef}
        value={value}
      />
    </label>
  );
}

function FilterChips({
  active,
  items,
  onChange,
}: {
  active: FilterID;
  items: Array<[FilterID, string, number]>;
  onChange: (id: FilterID) => void;
}) {
  return (
    <div className="flex flex-wrap gap-2">
      {items.map(([id, label, count]) => (
        <button
          className={[
            "inline-flex h-8 items-center gap-2 rounded-full border px-3 text-xs font-medium transition",
            active === id
              ? "border-accent/40 bg-accent/10 text-accent"
              : "border-border bg-bg-inset text-text-secondary hover:text-text-primary",
          ].join(" ")}
          key={id}
          onClick={() => onChange(id)}
          type="button"
        >
          <span>{label}</span>
          <span className="text-text-muted">{count}</span>
        </button>
      ))}
    </div>
  );
}

function RowActions({
  id,
  inspectLabel = "Inspect",
  label,
  mutationsDisabled = false,
  mutationDisabledReason = "",
  onInspect,
  onRemove,
}: {
  id: string;
  inspectLabel?: string;
  label: string;
  mutationsDisabled?: boolean;
  mutationDisabledReason?: string;
  onInspect: () => void;
  onRemove?: () => void;
}) {
  return (
    <div className="flex justify-end gap-1">
      <Tooltip label={inspectLabel}>
        <Button
          aria-label={`${inspectLabel} ${label}`}
          icon={<Eye size={15} />}
          onClick={onInspect}
          size="icon"
          variant="ghost"
        />
      </Tooltip>
      {onRemove ? (
        <Tooltip label="Remove">
          <Button
            aria-label={`Remove ${label}`}
            disabled={mutationsDisabled}
            disabledReason={mutationDisabledReason}
            icon={<Trash2 size={15} />}
            onClick={onRemove}
            size="icon"
            variant="ghost"
          />
        </Tooltip>
      ) : null}
      <Tooltip label="Copy ID">
        <Button
          aria-label={`Copy ${label}`}
          icon={<Copy size={15} />}
          onClick={() => {
            void navigator.clipboard?.writeText(id);
          }}
          size="icon"
          variant="ghost"
        />
      </Tooltip>
    </div>
  );
}

function ImageRowActions({
  image,
  mutationsDisabled,
  mutationDisabledReason,
  onInspect,
  onPush,
  onRemove,
  onRun,
  onSave,
  onTag,
}: {
  image: ImageSummary;
  mutationsDisabled: boolean;
  mutationDisabledReason: string;
  onInspect: () => void;
  onPush: () => void;
  onRemove: () => void;
  onRun: () => void;
  onSave: () => void;
  onTag: () => void;
}) {
  const label = primaryImageRef(image);
  return (
    <div className="flex justify-end gap-1">
      <Tooltip label="Run">
        <Button
          aria-label={`Run ${label}`}
          disabled={mutationsDisabled}
          disabledReason={mutationDisabledReason}
          icon={<Play size={15} />}
          onClick={onRun}
          size="icon"
          variant="ghost"
        />
      </Tooltip>
      <Tooltip label="Tag">
        <Button
          aria-label={`Tag ${label}`}
          disabled={mutationsDisabled}
          disabledReason={mutationDisabledReason}
          icon={<Tag size={15} />}
          onClick={onTag}
          size="icon"
          variant="ghost"
        />
      </Tooltip>
      <Tooltip label="Push">
        <Button
          aria-label={`Push ${label}`}
          disabled={mutationsDisabled}
          disabledReason={mutationDisabledReason}
          icon={<Upload size={15} />}
          onClick={onPush}
          size="icon"
          variant="ghost"
        />
      </Tooltip>
      <Tooltip label="Save to tar">
        <Button
          aria-label={`Save ${label}`}
          disabled={mutationsDisabled}
          disabledReason={mutationDisabledReason}
          icon={<Download size={15} />}
          onClick={onSave}
          size="icon"
          variant="ghost"
        />
      </Tooltip>
      <RowActions
        id={image.id}
        label={label}
        mutationsDisabled={mutationsDisabled}
        mutationDisabledReason={mutationDisabledReason}
        onInspect={onInspect}
        onRemove={onRemove}
      />
    </div>
  );
}

function containerCanStop(container: ContainerSummary) {
  return (
    container.state === "running" ||
    container.state === "paused" ||
    container.state === "restarting"
  );
}

function containerCanStart(container: ContainerSummary) {
  return container.state !== "running" && container.state !== "restarting";
}

function ContainerControlActions({
  actionBusyIDs,
  className = "",
  container,
  labels = false,
  mutationsDisabled,
  mutationDisabledReason,
  onAction,
  showKill = true,
  showRemove = true,
}: {
  actionBusyIDs: Set<string>;
  className?: string;
  container: ContainerSummary;
  labels?: boolean;
  mutationsDisabled: boolean;
  mutationDisabledReason: string;
  onAction: (action: ContainerAction, container: ContainerSummary) => void;
  showKill?: boolean;
  showRemove?: boolean;
}) {
  const canStart = containerCanStart(container);
  const canStop = containerCanStop(container);
  const primaryAction: ContainerAction = canStart ? "start" : "stop";
  const primaryLabel = canStart ? "Start" : "Stop";
  const size = labels ? "sm" : "icon";

  const disabledFor = (
    action: ContainerAction,
    label: string,
    blocked = false,
    blockedReason = "",
  ) => {
    if (mutationsDisabled) {
      return mutationDisabledReason;
    }
    if (blocked) {
      return blockedReason;
    }
    if (actionBusyIDs.has(`${action}:${container.id}`)) {
      return `${label} is already running`;
    }
    return "";
  };
  const renderAction = (
    action: ContainerAction,
    label: string,
    icon: ReactNode,
    options: {
      blocked?: boolean;
      blockedReason?: string;
      variant?: "secondary" | "ghost" | "danger";
    } = {},
  ) => {
    const disabledReason = disabledFor(
      action,
      label,
      options.blocked,
      options.blockedReason,
    );
    const button = (
      <Button
        aria-label={`${label} ${container.name}`}
        disabled={Boolean(disabledReason)}
        disabledReason={disabledReason}
        icon={icon}
        loading={actionBusyIDs.has(`${action}:${container.id}`)}
        onClick={() => onAction(action, container)}
        size={size}
        variant={options.variant ?? "secondary"}
      >
        {labels ? label : null}
      </Button>
    );
    return labels ? (
      button
    ) : (
      <Tooltip label={label} key={action}>
        {button}
      </Tooltip>
    );
  };

  return (
    <div
      className={["flex flex-wrap items-center justify-end gap-1", className]
        .filter(Boolean)
        .join(" ")}
    >
      {renderAction(
        primaryAction,
        primaryLabel,
        primaryAction === "start" ? <Play size={15} /> : <Square size={15} />,
      )}
      {renderAction("restart", "Restart", <RotateCw size={15} />, {
        blocked: !canStop,
        blockedReason: "Container is not running",
      })}
      {showKill
        ? renderAction("kill", "Kill", <Skull size={15} />, {
            blocked: !canStop,
            blockedReason: "Container is not running",
            variant: "danger",
          })
        : null}
      {showRemove
        ? renderAction("remove", "Delete", <Trash2 size={15} />, {
            variant: "danger",
          })
        : null}
    </div>
  );
}

function ContainerRowActions({
  busyIDs,
  container,
  mutationsDisabled,
  mutationDisabledReason,
  onAction,
  onInspect,
  onRename,
}: {
  busyIDs: Set<string>;
  container: ContainerSummary;
  mutationsDisabled: boolean;
  mutationDisabledReason: string;
  onAction: (action: ContainerAction, container: ContainerSummary) => void;
  onInspect: (container: ContainerSummary) => void;
  onRename: (container: ContainerSummary) => void;
}) {
  return (
    <div className="flex justify-end gap-1">
      <ContainerControlActions
        actionBusyIDs={busyIDs}
        container={container}
        mutationsDisabled={mutationsDisabled}
        mutationDisabledReason={mutationDisabledReason}
        onAction={onAction}
      />
      <Tooltip label="Rename">
        <Button
          aria-label={`Rename ${container.name}`}
          disabled={mutationsDisabled}
          disabledReason={mutationDisabledReason}
          icon={<Pencil size={15} />}
          onClick={() => onRename(container)}
          size="icon"
          variant="ghost"
        />
      </Tooltip>
      <RowActions
        id={container.id}
        label={container.name}
        onInspect={() => onInspect(container)}
      />
    </div>
  );
}

function ContainerBulkActions({
  busyIDs,
  mutationsDisabled,
  mutationDisabledReason,
  onAction,
}: {
  busyIDs: Set<string>;
  mutationsDisabled: boolean;
  mutationDisabledReason: string;
  onAction: (action: Exclude<ContainerAction, "kill" | "remove">) => void;
}) {
  return (
    <div className="flex items-center gap-1">
      <Button
        icon={<Play size={15} />}
        disabled={mutationsDisabled}
        disabledReason={mutationDisabledReason}
        loading={busyIDs.has("bulk:start")}
        onClick={() => onAction("start")}
        size="sm"
        variant="secondary"
      >
        Start
      </Button>
      <Button
        icon={<Square size={15} />}
        disabled={mutationsDisabled}
        disabledReason={mutationDisabledReason}
        loading={busyIDs.has("bulk:stop")}
        onClick={() => onAction("stop")}
        size="sm"
        variant="secondary"
      >
        Stop
      </Button>
      <Button
        icon={<RotateCw size={15} />}
        loading={busyIDs.has("bulk:restart")}
        onClick={() => onAction("restart")}
        size="sm"
        variant="secondary"
      >
        Restart
      </Button>
    </div>
  );
}

function appendProjectCommandProgress(
  current: Record<string, ProjectCommandOutputState>,
  payload: ProjectJobEvent,
): Record<string, ProjectCommandOutputState> {
  if (!payload.projectID || !payload.jobID) {
    return current;
  }
  const now = Date.now();
  const existing = current[payload.projectID];
  const isSameJob = existing?.jobID === payload.jobID;
  const base: ProjectCommandOutputState = isSameJob
    ? existing
    : {
        projectID: payload.projectID,
        jobID: payload.jobID,
        action: payload.action,
        command: payload.command,
        status: "running",
        startedAt: now,
        updatedAt: now,
        lines: [],
      };
  const message = payload.message?.trim() ?? "";
  const nextLines = message
    ? base.lines.concat({
        id: `${payload.jobID}:${base.lines.length}:${now}`,
        ts: now,
        phase: payload.phase || "output",
        message,
        tone: projectCommandLineTone(payload.phase),
      })
    : base.lines;
  return {
    ...current,
    [payload.projectID]: {
      ...base,
      action: payload.action || base.action,
      command: payload.command || base.command,
      status: "running",
      updatedAt: now,
      lines: nextLines.slice(-maxProjectCommandOutputLines),
    },
  };
}

function appendProjectCommandDone(
  current: Record<string, ProjectCommandOutputState>,
  payload: ProjectJobEvent,
): Record<string, ProjectCommandOutputState> {
  if (!payload.projectID || !payload.jobID) {
    return current;
  }
  const now = Date.now();
  const existing = current[payload.projectID];
  const base: ProjectCommandOutputState =
    existing?.jobID === payload.jobID
      ? existing
      : {
          projectID: payload.projectID,
          jobID: payload.jobID,
          action: payload.action,
          command: payload.command,
          status: "running",
          startedAt: now,
          updatedAt: now,
          lines: [],
        };
  const failed = Boolean(payload.error);
  const message = failed
    ? payload.error || "Command failed"
    : payload.result
      ? `Result: ${payload.result}`
      : "Command finished";
  const lines = base.lines.concat({
    id: `${payload.jobID}:done:${now}`,
    ts: now,
    phase: failed ? "failed" : "done",
    message,
    tone: failed ? "error" : "ok",
  });
  return {
    ...current,
    [payload.projectID]: {
      ...base,
      action: payload.action || base.action,
      command: payload.command || base.command,
      status: failed ? "failed" : "success",
      updatedAt: now,
      lines: lines.slice(-maxProjectCommandOutputLines),
      result: payload.result,
      error: payload.error,
    },
  };
}

function projectCommandLineTone(
  phase: string | undefined,
): ProjectCommandOutputLine["tone"] {
  switch (phase) {
    case "stderr":
    case "failed":
      return "error";
    case "stdout":
      return "muted";
    case "done":
      return "ok";
    default:
      return "info";
  }
}

function eventPayload<T>(event: unknown): T | null {
  if (!isRecord(event) || !("data" in event)) {
    return null;
  }
  return event.data == null ? null : (event.data as T);
}

function isLogLine(value: unknown): value is LogLine {
  if (!isRecord(value)) {
    return false;
  }
  return typeof value.text === "string" && typeof value.stream === "string";
}

function normalizeLogLevel(level?: string): LogLevelFilter {
  const value = level?.trim().toLowerCase();
  if (!value) {
    return "log";
  }
  if (
    value === "error" ||
    value === "warn" ||
    value === "info" ||
    value === "debug" ||
    value === "log"
  ) {
    return value;
  }
  if (value === "fatal") {
    return "error";
  }
  return "unknown";
}

function levelTone(level: LogLevelFilter): BadgeTone {
  if (level === "error") {
    return "error";
  }
  if (level === "warn") {
    return "warn";
  }
  if (level === "info") {
    return "info";
  }
  return "neutral";
}

function logSource(line: LogLine) {
  return (
    line.containerName ||
    line.service ||
    shortID(line.containerID ?? "") ||
    "system"
  );
}

function logSourceKey(line: LogLine) {
  return line.containerID || line.containerName || line.service || "system";
}

function projectName(projects: ProjectSummary[], id: string) {
  return projects.find((project) => project.id === id)?.name ?? id;
}

function containerName(containers: ContainerSummary[], id: string) {
  return (
    containers.find((container) => container.id === id)?.name ?? shortID(id)
  );
}

function mergeNetworkContainers(
  attachedContainers: ContainerSummary[],
  inventoryContainers: ContainerSummary[],
) {
  const inventoryByID = new Map(
    inventoryContainers.map((container) => [container.id, container]),
  );

  return attachedContainers.map((container) => {
    const current = inventoryByID.get(container.id);
    if (!current) {
      return container;
    }
    return {
      ...current,
      ...container,
      cpuPercent: current.cpuPercent ?? container.cpuPercent,
      memoryBytes: current.memoryBytes ?? container.memoryBytes,
      memoryLimit: current.memoryLimit ?? container.memoryLimit,
      netRxRate: current.netRxRate ?? container.netRxRate,
      netTxRate: current.netTxRate ?? container.netTxRate,
      ports: current.ports?.length ? current.ports : container.ports,
    };
  });
}

function networkContainerIP(container: ContainerSummary) {
  return container.ipv4Address || container.ipv6Address || "";
}

function formatContainerPorts(ports?: PortBinding[]) {
  if (!ports?.length) {
    return "-";
  }
  return ports.map(formatPortBinding).join(", ");
}

function formatPortBinding(port: PortBinding) {
  const containerPort = [
    port.containerPort || "-",
    port.protocol ? `/${port.protocol}` : "",
  ].join("");
  if (!port.hostPort) {
    return containerPort;
  }
  const host = [port.hostIP, port.hostPort].filter(Boolean).join(":");
  return `${host}->${containerPort}`;
}

function hostPathFileURL(path: string) {
  const value = path.trim();
  if (value.startsWith("\\\\")) {
    return `file://${encodeURI(value.slice(2).replace(/\\/g, "/"))}`;
  }
  const normalized = value.replace(/\\/g, "/");
  if (/^[a-zA-Z]:\//.test(normalized)) {
    return `file:///${encodeURI(normalized)}`;
  }
  if (normalized.startsWith("/")) {
    return `file://${encodeURI(normalized)}`;
  }
  return encodeURI(normalized);
}

function projectIDForVolume(volume: VolumeSummary, projects: ProjectSummary[]) {
  const composeName = volume.labels?.[composeProjectLabel] ?? "";
  if (!composeName) {
    return "";
  }
  return (
    projects.find((project) => project.name === composeName)?.id ?? composeName
  );
}

function sourceColor(source: string) {
  const hue = hashString(source) % 360;
  return `hsl(${hue} 78% 68%)`;
}

function hashString(value: string) {
  let hash = 0;
  for (let index = 0; index < value.length; index += 1) {
    hash = (hash * 31 + value.charCodeAt(index)) >>> 0;
  }
  return hash;
}

function formatCount(value: number) {
  return new Intl.NumberFormat().format(value);
}

function formatLogTimestamp(value: unknown) {
  const date = value instanceof Date ? value : new Date(String(value ?? ""));
  if (Number.isNaN(date.getTime())) {
    return "--:--:--.---";
  }
  const hh = String(date.getHours()).padStart(2, "0");
  const mm = String(date.getMinutes()).padStart(2, "0");
  const ss = String(date.getSeconds()).padStart(2, "0");
  const ms = String(date.getMilliseconds()).padStart(3, "0");
  return `${hh}:${mm}:${ss}.${ms}`;
}

function renderAnsiText(text: string, query: string) {
  const ansiPattern = new RegExp(
    `${String.fromCharCode(27)}\\[([0-9;]*)m`,
    "g",
  );
  const nodes: JSX.Element[] = [];
  let className = "";
  let cursor = 0;
  let match: RegExpExecArray | null;
  let key = 0;

  while ((match = ansiPattern.exec(text)) !== null) {
    if (match.index > cursor) {
      nodes.push(
        <span className={className} key={`ansi-${key}`}>
          {renderHighlightedText(text.slice(cursor, match.index), query, key)}
        </span>,
      );
      key += 1;
    }
    className = ansiClass(match[1], className);
    cursor = ansiPattern.lastIndex;
  }
  if (cursor < text.length) {
    nodes.push(
      <span className={className} key={`ansi-${key}`}>
        {renderHighlightedText(text.slice(cursor), query, key)}
      </span>,
    );
  }
  if (nodes.length === 0) {
    return renderHighlightedText(text, query, 0);
  }
  return nodes;
}

function ansiClass(codesText: string, current: string) {
  const codes = codesText
    .split(";")
    .filter(Boolean)
    .map((code) => Number.parseInt(code, 10));
  if (codes.length === 0 || codes.includes(0)) {
    return "";
  }
  let next = current;
  for (const code of codes) {
    if (code === 1) {
      next = `${next} font-semibold`.trim();
    } else if (code === 31) {
      next = "text-error";
    } else if (code === 32) {
      next = "text-ok";
    } else if (code === 33) {
      next = "text-warn";
    } else if (code === 34) {
      next = "text-info";
    } else if (code === 36) {
      next = "text-accent";
    } else if (code === 90) {
      next = "text-text-muted";
    }
  }
  return next;
}

function renderHighlightedText(text: string, query: string, keyPrefix: number) {
  if (!query) {
    return text;
  }
  const lower = text.toLowerCase();
  const parts: JSX.Element[] = [];
  let cursor = 0;
  let index = lower.indexOf(query);
  let key = 0;
  while (index >= 0) {
    if (index > cursor) {
      parts.push(
        <span key={`${keyPrefix}-text-${key}`}>
          {text.slice(cursor, index)}
        </span>,
      );
      key += 1;
    }
    parts.push(
      <mark
        className="rounded bg-accent/30 px-0.5 text-text-primary"
        key={`${keyPrefix}-mark-${key}`}
      >
        {text.slice(index, index + query.length)}
      </mark>,
    );
    key += 1;
    cursor = index + query.length;
    index = lower.indexOf(query, cursor);
  }
  if (cursor < text.length) {
    parts.push(
      <span key={`${keyPrefix}-text-${key}`}>{text.slice(cursor)}</span>,
    );
  }
  return parts;
}

function logFilterSummary(
  scope: LogScope,
  ids: string[],
  levels: Set<LogLevelFilter>,
  source: string | null,
  query: string,
) {
  const selectedLevels = logLevelOptions
    .filter((level) => levels.has(level.id))
    .map((level) => level.label)
    .join(", ");
  const parts = [
    `scope ${scope}`,
    `selected ${ids.length || "all"}`,
    `levels ${selectedLevels}`,
  ];
  if (source) {
    parts.push(`source ${source}`);
  }
  if (query) {
    parts.push(`search ${query}`);
  }
  return parts.join(" | ");
}

function ImageLineageCard({ lineage }: { lineage: ImageLineage | null }) {
  return (
    <div className="mt-4 rounded-control border border-border bg-bg-inset p-3">
      <div className="mb-3 flex items-center justify-between gap-3">
        <div className="flex items-center gap-2 font-medium text-text-primary">
          <Layers size={16} />
          Image Lineage
        </div>
        <ConfidenceChip
          confidence={lineage?.confidence ?? "unknown"}
          reason={lineage?.reason}
        />
      </div>
      {lineage ? (
        <div className="grid gap-3 text-sm md:grid-cols-2">
          <LineageField label="Running image" value={lineage.imageRef} />
          <LineageField
            label="Image ID"
            value={shortID(lineage.imageID ?? "")}
          />
          <LineageField
            label="Built from"
            value={lineage.baseImage || "Unknown"}
          />
          <LineageField
            copyable
            label="Base @ build"
            value={lineage.baseDigest || "-"}
          />
          <LineageField label="Source" value={lineage.source || "unknown"} />
          <LineageField
            label="Status"
            value={
              lineage.baseImage
                ? "Base tracking available"
                : "Base image: Unknown - this is a third-party registry image and no base metadata was found."
            }
          />
        </div>
      ) : (
        <div className="text-sm text-text-muted">
          Base image: Unknown - this is a third-party registry image and no base
          metadata was found.
        </div>
      )}
    </div>
  );
}

function LineageField({
  copyable,
  label,
  value,
}: {
  label: string;
  value: string;
  copyable?: boolean;
}) {
  return (
    <div className="min-w-0">
      <div className="text-xs text-text-muted">{label}</div>
      <div className="mt-1 flex items-center gap-2">
        <span
          className="truncate font-mono text-xs text-text-primary"
          title={value}
        >
          {value || "-"}
        </span>
        {copyable && value && value !== "-" ? (
          <Button
            aria-label={`Copy ${label}`}
            icon={<Copy size={14} />}
            onClick={() => {
              void Clipboard.SetText(value);
            }}
            size="icon"
            variant="ghost"
          />
        ) : null}
      </div>
    </div>
  );
}

function InspectModal({
  inspect,
  onClose,
}: {
  inspect: InspectState;
  onClose: () => void;
}) {
  return (
    <Modal
      open={inspect.open}
      onClose={onClose}
      size="lg"
      title={inspect.title || "Inspect"}
    >
      {inspect.subtitle ? (
        <div className="mb-3 font-mono text-xs text-text-muted">
          {inspect.subtitle}
        </div>
      ) : null}
      <div className="grid grid-cols-2 gap-3">
        {inspect.rows.map(([label, value]) => (
          <div
            className="rounded-control border border-border bg-bg-inset p-3"
            key={label}
          >
            <div className="text-xs text-text-muted">{label}</div>
            <div
              className="mt-1 truncate font-mono text-xs text-text-primary"
              title={value}
            >
              {value || "-"}
            </div>
          </div>
        ))}
      </div>
      {typeof inspect.lineage !== "undefined" ? (
        <ImageLineageCard lineage={inspect.lineage} />
      ) : null}
      {inspect.loading ? (
        <div className="mt-4 space-y-2">
          <Skeleton className="h-4 w-40" />
          <Skeleton className="h-28 w-full" />
        </div>
      ) : null}
      {inspect.error ? (
        <div className="mt-4 rounded-control border border-error/30 bg-error/10 p-3 text-error">
          {inspect.error}
        </div>
      ) : null}
      {inspect.raw ? (
        <details
          className="mt-4 rounded-control border border-border bg-bg-inset"
          open
        >
          <summary className="cursor-pointer px-3 py-2 text-sm text-text-primary">
            <span className="inline-flex items-center gap-2">
              <FileJson size={15} />
              Inspect JSON
            </span>
          </summary>
          <pre className="max-h-96 overflow-auto border-t border-border p-3 font-mono text-xs text-text-secondary">
            {inspect.raw}
          </pre>
        </details>
      ) : null}
    </Modal>
  );
}

function ConfirmPlanModal({
  confirm,
  onApply,
  onChangeTypedName,
  onClose,
}: {
  confirm: ConfirmState;
  onApply: () => void;
  onChangeTypedName: (value: string) => void;
  onClose: () => void;
}) {
  const plan = confirm.plan;
  const typedName = plan?.requiresTypedName ?? "";
  const typedReady = !typedName || confirm.typedName === typedName;
  return (
    <Modal
      busy={confirm.busy}
      danger={plan?.risk === "destructive" || plan?.risk === "dangerous"}
      footer={
        <div className="flex justify-end gap-2">
          <Button disabled={confirm.busy} onClick={onClose} variant="secondary">
            Cancel
          </Button>
          <Button
            disabled={!typedReady}
            loading={confirm.busy}
            onClick={onApply}
            variant="danger"
          >
            Confirm
          </Button>
        </div>
      }
      onClose={onClose}
      open={confirm.open}
      size="lg"
      title={plan?.title ?? "Confirm action"}
    >
      {plan ? (
        <div className="space-y-4">
          <div className="flex items-center gap-2">
            <Badge tone={riskTone(plan.risk)}>{plan.risk}</Badge>
            <span className="text-text-muted">
              Plan expires {formatDate(plan.expiresAt)}
            </span>
          </div>
          <div>
            <div className="mb-2 text-sm font-medium text-text-primary">
              Effects
            </div>
            <ul className="space-y-2">
              {plan.effects?.map((effect) => (
                <li
                  className="rounded-control border border-border bg-bg-inset p-3"
                  key={effect}
                >
                  {effect}
                </li>
              ))}
            </ul>
          </div>
          <div>
            <div className="mb-2 text-sm font-medium text-text-primary">
              Commands
            </div>
            <div className="space-y-2">
              {plan.commands?.map((command) => (
                <div
                  className="rounded-control border border-border bg-bg-inset p-3"
                  key={`${command.order}-${command.command}`}
                >
                  <div className="font-mono text-xs text-text-primary">
                    {command.command}
                  </div>
                  <div className="mt-2 text-xs text-text-muted">
                    {command.explanation}
                  </div>
                </div>
              ))}
            </div>
          </div>
          {typedName ? (
            <label className="block">
              <span className="text-sm font-medium text-text-primary">
                Type {typedName} to confirm
              </span>
              <input
                className="mt-2 h-9 w-full rounded-control border border-border bg-bg-inset px-3 font-mono text-sm text-text-primary outline-none focus:border-accent"
                onChange={(event) => onChangeTypedName(event.target.value)}
                value={confirm.typedName}
              />
            </label>
          ) : null}
          {confirm.error ? (
            <div className="rounded-control border border-error/30 bg-error/10 p-3 text-error">
              {confirm.error}
            </div>
          ) : null}
        </div>
      ) : null}
    </Modal>
  );
}

function RemoveProjectModal({
  onClose,
  onConfirm,
  state,
}: {
  onClose: () => void;
  onConfirm: () => void;
  state: RemoveProjectState;
}) {
  const project = state.project;
  return (
    <Modal
      busy={state.busy}
      danger
      footer={
        <div className="flex justify-end gap-2">
          <Button disabled={state.busy} onClick={onClose} variant="secondary">
            Cancel
          </Button>
          <Button loading={state.busy} onClick={onConfirm} variant="danger">
            Remove
          </Button>
        </div>
      }
      onClose={onClose}
      open={state.open}
      title="Remove project from list?"
    >
      <div className="space-y-4">
        <p className="text-sm text-text-secondary">
          Remove{" "}
          <span className="font-semibold text-text-primary">
            {project?.name ?? "this project"}
          </span>{" "}
          from Cairn's project list.
        </p>
        <div className="rounded-control border border-warn/30 bg-warn/10 p-3 text-sm text-warn">
          This does not stop containers, remove volumes, delete images, or
          delete files. You can import the project again later.
        </div>
        {project?.workingDir ? (
          <div className="truncate rounded-control border border-border bg-bg-inset p-3 font-mono text-xs text-text-muted">
            {project.workingDir}
          </div>
        ) : null}
        {state.error ? (
          <div className="rounded-control border border-error/30 bg-error/10 p-3 text-sm text-error">
            {state.error}
          </div>
        ) : null}
      </div>
    </Modal>
  );
}

function UpdatePlanModal({
  onApply,
  onChange,
  onClose,
  state,
}: {
  state: UpdatePlanState;
  onApply: () => void;
  onChange: (patch: Partial<UpdatePlanState>) => void;
  onClose: () => void;
}) {
  const plan = state.plan;
  const targetLabel =
    state.target?.kind === "project"
      ? `project "${state.target.projectName ?? state.target.projectID}"`
      : `service "${state.target?.service ?? ""}" in project "${
          state.target?.projectName ?? state.target?.projectID ?? ""
        }"`;
  const title =
    state.mode === "rollback"
      ? plan
        ? `Rollback ${targetLabel}`
        : state.target
          ? `Plan rollback for ${targetLabel}`
          : "Rollback"
      : plan
        ? `Update ${targetLabel}`
        : state.target
          ? `Plan update for ${targetLabel}`
          : "Update";
  const commandText = plan?.commands
    .map((command) => command.command)
    .join("\n");
  const applying = state.applying || Boolean(state.jobID);
  return (
    <Modal
      busy={state.busy}
      footer={
        <div className="flex justify-end gap-2">
          <Button disabled={state.busy} onClick={onClose} variant="secondary">
            {state.result ? "Close" : "Cancel"}
          </Button>
          {plan && !applying && !state.result ? (
            <Button loading={state.busy} onClick={onApply} variant="primary">
              {state.mode === "rollback"
                ? "Roll back"
                : state.target?.kind === "project"
                  ? "Update project"
                  : "Update service"}
            </Button>
          ) : null}
        </div>
      }
      onClose={onClose}
      open={state.open}
      size="lg"
      title={title}
    >
      <div className="max-h-[72vh] space-y-4 overflow-auto pr-1">
        {state.busy && !plan ? (
          <div className="space-y-2">
            <Skeleton className="h-4 w-48" />
            <Skeleton className="h-32 w-full" />
          </div>
        ) : null}

        {plan ? (
          <>
            <div className="flex flex-wrap gap-2">
              {plan.items.map((item) => (
                <Badge key={`${item.service}-${item.kind}`} tone="warn">
                  {item.service} - {updateActionLabel(item.action)}
                </Badge>
              ))}
            </div>
            <div className="grid gap-3 md:grid-cols-2">
              {plan.items.map((item) => (
                <div
                  className="rounded-control border border-border bg-bg-inset p-3"
                  key={`${item.service}-${item.kind}-digest`}
                >
                  <div className="flex items-center justify-between gap-2">
                    <div className="font-medium text-text-primary">
                      {item.service}
                    </div>
                    <ConfidenceChip confidence={item.confidence} />
                  </div>
                  <dl className="mt-3 space-y-2 text-sm">
                    <div>
                      <dt className="text-xs text-text-muted">Service image</dt>
                      <dd className="truncate font-mono text-xs">
                        {item.currentImage}
                      </dd>
                    </div>
                    <div>
                      <dt className="text-xs text-text-muted">Base image</dt>
                      <dd className="truncate font-mono text-xs">
                        {item.baseImage || "-"}
                      </dd>
                    </div>
                    <div>
                      <dt className="text-xs text-text-muted">Digest</dt>
                      <dd>
                        <DigestDelta
                          local={item.localDigest}
                          remote={item.remoteDigest}
                        />
                      </dd>
                    </div>
                  </dl>
                </div>
              ))}
            </div>

            <div>
              <div className="mb-2 flex items-center justify-between gap-2">
                <div className="text-sm font-medium text-text-primary">
                  Commands
                </div>
                <Button
                  icon={<Copy size={15} />}
                  onClick={() => {
                    if (commandText) {
                      void Clipboard.SetText(commandText);
                    }
                  }}
                  size="sm"
                  variant="secondary"
                >
                  Copy
                </Button>
              </div>
              <div className="space-y-2">
                {plan.commands.map((command) => (
                  <div
                    className="rounded-control border border-border bg-bg-inset p-3"
                    key={`${command.order}-${command.command}`}
                  >
                    <div className="font-mono text-xs text-text-primary">
                      $ {command.command}
                    </div>
                    <div className="mt-2 flex flex-wrap gap-2 text-xs text-text-muted">
                      <Badge tone={riskTone(command.risk)}>
                        {command.risk}
                      </Badge>
                      <span>{command.explanation}</span>
                      {command.workingDir ? (
                        <span>workdir: {command.workingDir}</span>
                      ) : null}
                    </div>
                  </div>
                ))}
              </div>
            </div>

            {plan.warnings?.length ? (
              <div className="rounded-control border border-warn/30 bg-warn/10 p-3 text-sm text-warn">
                {plan.warnings.map((warning) => (
                  <div key={warning}>{warning}</div>
                ))}
              </div>
            ) : null}

            {state.mode === "update" ? (
              <div className="space-y-2 rounded-control border border-border bg-bg-inset p-3 text-sm">
                <label className="flex items-center gap-2">
                  <input
                    checked={state.backupVolumesFirst}
                    disabled={applying}
                    onChange={(event) =>
                      onChange({ backupVolumesFirst: event.target.checked })
                    }
                    type="checkbox"
                  />
                  Back up named volumes first
                </label>
                <label className="flex items-center gap-2">
                  <input
                    checked={state.watchHealth}
                    disabled={applying}
                    onChange={(event) =>
                      onChange({ watchHealth: event.target.checked })
                    }
                    type="checkbox"
                  />
                  Watch health after update (60 s)
                </label>
                <label className="flex items-center gap-2">
                  <input
                    checked={state.rollbackOnFailure}
                    disabled={applying}
                    onChange={(event) =>
                      onChange({ rollbackOnFailure: event.target.checked })
                    }
                    type="checkbox"
                  />
                  Roll back automatically if health check fails
                </label>
                <div className="text-xs text-text-muted">
                  Rollback possible when the previous image is kept locally. If
                  it is unavailable, Cairn records manual-needed guidance in
                  History.
                </div>
              </div>
            ) : null}
          </>
        ) : null}

        {applying ? (
          <div className="space-y-2 rounded-control border border-border bg-bg-inset p-3">
            <div className="text-sm font-medium text-text-primary">
              Apply progress
            </div>
            {state.progress.length === 0 ? (
              <div className="text-sm text-text-muted">Waiting for job...</div>
            ) : (
              <ol className="space-y-2">
                {state.progress.map((entry, index) => (
                  <li
                    className="rounded-control border border-border bg-bg-card p-2 text-sm"
                    key={`${entry.jobID}-${index}`}
                  >
                    <div className="flex items-center justify-between gap-3">
                      <span className="text-text-primary">
                        {entry.phase || "update"}
                      </span>
                      {typeof entry.pct === "number" ? (
                        <span className="text-xs text-text-muted">
                          {entry.pct}%
                        </span>
                      ) : null}
                    </div>
                    <div className="mt-1 text-xs text-text-muted">
                      {entry.message || "Working"}
                    </div>
                  </li>
                ))}
              </ol>
            )}
          </div>
        ) : null}

        {state.result ? (
          <div className="rounded-control border border-ok/30 bg-ok/10 p-3 text-sm text-ok">
            Result: {state.result}
          </div>
        ) : null}
        {state.error ? (
          <div className="rounded-control border border-error/30 bg-error/10 p-3 text-sm text-error">
            {state.error}
          </div>
        ) : null}
      </div>
    </Modal>
  );
}

function IgnoreUpdateModal({
  onChange,
  onClose,
  onSubmit,
  state,
}: {
  state: IgnoreUpdateState;
  onChange: (patch: Partial<IgnoreUpdateState>) => void;
  onClose: () => void;
  onSubmit: () => void;
}) {
  const update = state.update;
  return (
    <Modal
      busy={state.busy}
      footer={
        <ModalActions
          busy={state.busy}
          disabled={false}
          onCancel={onClose}
          onSubmit={onSubmit}
          submitLabel="Ignore"
        />
      }
      onClose={onClose}
      open={state.open}
      title={`Ignore ${update?.service ?? "update"}`}
    >
      <div className="space-y-4">
        <div className="rounded-control border border-border bg-bg-inset p-3 text-sm">
          <div className="font-medium text-text-primary">
            {update?.currentImage ?? "-"}
          </div>
          <div className="mt-1 text-text-muted">
            Scope: this service update. It will move to the Ignored tab and stop
            contributing to project badges.
          </div>
        </div>
        <TextField
          label="Reason"
          onChange={(reason) => onChange({ reason })}
          value={state.reason}
        />
        <FormError error={state.error} />
      </div>
    </Modal>
  );
}

function RenameContainerModal({
  onChange,
  onClose,
  onSubmit,
  state,
}: {
  state: RenameState;
  onChange: (name: string) => void;
  onClose: () => void;
  onSubmit: () => void;
}) {
  const composeManaged = Boolean(state.container?.projectID);
  return (
    <Modal
      busy={state.busy}
      footer={
        <ModalActions
          busy={state.busy}
          disabled={!state.name.trim()}
          onCancel={onClose}
          onSubmit={onSubmit}
          submitLabel="Rename"
        />
      }
      onClose={onClose}
      open={state.open}
      title={`Rename ${state.container?.name ?? "container"}`}
    >
      <div className="space-y-4">
        {composeManaged ? (
          <div className="rounded-control border border-warn/30 bg-warn/10 p-3 text-warn">
            Compose may recreate this container with its original service name.
          </div>
        ) : null}
        <TextField
          autoFocus
          label="New name"
          onChange={onChange}
          value={state.name}
        />
        <CodePreview
          value={joinPreview([
            "docker",
            "rename",
            state.container?.name ?? "",
            state.name,
          ])}
        />
        <FormError error={state.error} />
      </div>
    </Modal>
  );
}

function RunImageModal({
  networks,
  onAddAutoPort,
  onBack,
  onChange,
  onClose,
  onSelectHubResult,
  onSubmit,
  state,
}: {
  state: RunImageState;
  networks: NetworkSummary[];
  onAddAutoPort: () => void;
  onBack: () => void;
  onChange: (patch: Partial<RunImageState>) => void;
  onClose: () => void;
  onSelectHubResult: (result: HubSearchResult) => void;
  onSubmit: () => void;
}) {
  const validation = runImageValidation(state);
  const command = safeDockerRunPreview(state);
  return (
    <Modal
      busy={state.busy}
      footer={
        <div className="flex justify-between gap-2">
          {state.step === 2 ? (
            <Button disabled={state.busy} onClick={onBack} variant="secondary">
              Back
            </Button>
          ) : (
            <span />
          )}
          <div className="flex gap-2">
            <Button disabled={state.busy} onClick={onClose} variant="secondary">
              Cancel
            </Button>
            <Button
              disabled={Boolean(validation)}
              loading={state.busy}
              onClick={onSubmit}
              variant="primary"
            >
              {state.step === 1 ? "Next" : "Run"}
            </Button>
          </div>
        </div>
      }
      onClose={onClose}
      open={state.open}
      size="lg"
      title="Run Image"
    >
      <div className="max-h-[68vh] space-y-4 overflow-auto pr-1">
        {state.step === 1 ? (
          <>
            <TextField
              label="Image ref"
              onChange={(imageRef) => onChange({ imageRef })}
              readOnly={state.imageLocked}
              value={state.imageRef}
            />
            {!state.imageLocked ? (
              <>
                <TextField
                  label="Docker Hub search"
                  onChange={(hubQuery) => onChange({ hubQuery })}
                  value={state.hubQuery}
                />
                <HubResultList
                  loading={state.hubLoading}
                  onSelect={onSelectHubResult}
                  results={state.hubResults}
                />
                {state.hubError ? (
                  <div className="rounded-control border border-warn/30 bg-warn/10 p-3 text-warn">
                    {state.hubError}
                  </div>
                ) : null}
              </>
            ) : null}
            <TextField
              label="Container name"
              onChange={(name) => onChange({ name })}
              value={state.name}
            />
            <label className="flex items-center gap-2 text-sm">
              <input
                checked={state.pullIfMissing}
                onChange={(event) =>
                  onChange({ pullIfMissing: event.target.checked })
                }
                type="checkbox"
              />
              <span>Pull if missing</span>
            </label>
          </>
        ) : (
          <>
            <div className="grid grid-cols-2 gap-4">
              <TextAreaField
                label="Ports"
                onChange={(portsText) => onChange({ portsText })}
                rows={4}
                value={state.portsText}
              />
              <div className="flex items-end">
                <Button
                  icon={<Plus size={15} />}
                  onClick={onAddAutoPort}
                  variant="secondary"
                >
                  Auto port
                </Button>
              </div>
              <TextAreaField
                label="Environment"
                onChange={(envText) => onChange({ envText })}
                rows={4}
                value={state.envText}
              />
              <TextAreaField
                label="Volumes"
                onChange={(volumesText) => onChange({ volumesText })}
                rows={4}
                value={state.volumesText}
              />
              <SelectField
                label="Network"
                onChange={(networkID) => onChange({ networkID })}
                options={[
                  ["", "bridge"],
                  ...networks.map(
                    (network) =>
                      [network.name, network.name] as [string, string],
                  ),
                ]}
                value={state.networkID}
              />
              <SelectField
                label="Restart"
                onChange={(restartPolicy) => onChange({ restartPolicy })}
                options={[
                  ["no", "no"],
                  ["on-failure", "on-failure"],
                  ["unless-stopped", "unless-stopped"],
                  ["always", "always"],
                ]}
                value={state.restartPolicy}
              />
              <TextField
                label="Command"
                onChange={(commandText) => onChange({ commandText })}
                value={state.commandText}
              />
              <TextField
                label="User"
                onChange={(user) => onChange({ user })}
                value={state.user}
              />
            </div>
            {secretKeys(state.envText).length > 0 ? (
              <div className="rounded-control border border-border bg-bg-inset p-3">
                <Badge tone="warn">masked</Badge>
                <span className="ml-2 text-text-muted">
                  {secretKeys(state.envText).join(", ")}
                </span>
              </div>
            ) : null}
            <CodePreview value={command} />
          </>
        )}
        {validation ? (
          <div className="rounded-control border border-error/30 bg-error/10 p-3 text-error">
            {validation}
          </div>
        ) : null}
        <FormError error={state.error} />
      </div>
    </Modal>
  );
}

function PullImageModal({
  onChange,
  onClose,
  onSelectResult,
  onSubmit,
  state,
}: {
  state: PullImageState;
  onChange: (patch: Partial<PullImageState>) => void;
  onClose: () => void;
  onSelectResult: (result: HubSearchResult) => void;
  onSubmit: () => void;
}) {
  const ref = imageRefWithTag(state.ref, state.tag);
  return (
    <Modal
      busy={state.busy}
      footer={
        <ModalActions
          busy={state.busy}
          disabled={!state.ref.trim()}
          onCancel={onClose}
          onSubmit={onSubmit}
          submitLabel="Pull"
        />
      }
      onClose={onClose}
      open={state.open}
      title="Pull Image"
    >
      <div className="space-y-4">
        <TextField
          label="Image ref"
          onChange={(nextRef) => onChange({ ref: nextRef })}
          value={state.ref}
        />
        <TextField
          label="Tag"
          onChange={(tag) => onChange({ tag })}
          value={state.tag}
        />
        <TextField
          label="Docker Hub search"
          onChange={(query) => onChange({ query })}
          value={state.query}
        />
        <HubResultList
          loading={state.loadingResults}
          onSelect={onSelectResult}
          results={state.results}
        />
        {state.searchError ? (
          <div className="rounded-control border border-warn/30 bg-warn/10 p-3 text-warn">
            {state.searchError}
          </div>
        ) : null}
        <CodePreview value={joinPreview(["docker", "pull", ref])} />
        <FormError error={state.error} />
      </div>
    </Modal>
  );
}

function TagImageModal({
  onChange,
  onClose,
  onSubmit,
  state,
}: {
  state: TagImageState;
  onChange: (patch: Partial<TagImageState>) => void;
  onClose: () => void;
  onSubmit: () => void;
}) {
  const preview = imageRefPreview(state.newRef);
  return (
    <Modal
      busy={state.busy}
      footer={
        <ModalActions
          busy={state.busy}
          disabled={!state.image || Boolean(preview.error)}
          onCancel={onClose}
          onSubmit={onSubmit}
          submitLabel="Create tag"
        />
      }
      onClose={onClose}
      open={state.open}
      title="Tag Image"
    >
      <div className="space-y-4">
        <TextField
          label="New ref"
          onChange={(newRef) => onChange({ newRef })}
          value={state.newRef}
        />
        <ImageRefPreview preview={preview} />
        <CodePreview
          value={joinPreview([
            "docker",
            "tag",
            state.image?.id ?? "<image>",
            state.newRef,
          ])}
        />
        <FormError error={state.error ?? preview.error} />
      </div>
    </Modal>
  );
}

function PushImageModal({
  accounts,
  accountsLoading,
  onChange,
  onClose,
  onCopyPull,
  onLogin,
  onRefreshAccounts,
  onSubmit,
  state,
}: {
  state: PushImageState;
  accounts: RegistryAccount[];
  accountsLoading: boolean;
  onChange: (patch: Partial<PushImageState>) => void;
  onClose: () => void;
  onCopyPull: (ref: string) => void;
  onLogin: (registry?: string) => void;
  onRefreshAccounts: () => void;
  onSubmit: () => void;
}) {
  const preview = imageRefPreview(state.ref);
  const registry = preview.registry || registryFromImageRef(state.ref);
  const account = registryAccountFor(accounts, registry);
  const disabled =
    Boolean(preview.error) || !state.confirmed || state.busy || state.success;
  return (
    <Modal
      busy={state.busy}
      footer={
        <div className="flex flex-wrap justify-end gap-2">
          <Button disabled={state.busy} onClick={onClose} variant="secondary">
            Close
          </Button>
          <Button
            disabled={disabled}
            loading={state.busy}
            onClick={onSubmit}
            variant="primary"
          >
            Push
          </Button>
        </div>
      }
      onClose={onClose}
      open={state.open}
      title="Push Image"
    >
      <div className="space-y-4">
        <TextField
          label="Image ref"
          onChange={(ref) =>
            onChange({
              ref,
              confirmed: false,
              error: undefined,
              success: false,
            })
          }
          value={state.ref}
        />
        <ImageRefPreview preview={preview} />
        <div className="rounded-control border border-border bg-bg-inset p-3 text-sm">
          <div className="flex flex-wrap items-center gap-2">
            {account ? (
              <>
                <Badge tone="ok">{account.username || registry}</Badge>
                <span className="text-text-muted">
                  {registryStorageLabel(account)}
                </span>
              </>
            ) : (
              <>
                <Badge tone="error">Not logged in</Badge>
                <Button
                  icon={<LogIn size={15} />}
                  onClick={() => onLogin(registry)}
                  size="sm"
                  variant="secondary"
                >
                  Log in
                </Button>
              </>
            )}
            <Button
              icon={<RefreshCw size={15} />}
              loading={accountsLoading}
              onClick={onRefreshAccounts}
              size="sm"
              variant="ghost"
            >
              Refresh
            </Button>
          </div>
        </div>
        <CodePreview value={joinPreview(["docker", "push", state.ref])} />
        <label className="flex items-start gap-3 rounded-control border border-border bg-bg-inset p-3 text-sm">
          <input
            checked={state.confirmed}
            disabled={state.busy || state.success}
            onChange={(event) => onChange({ confirmed: event.target.checked })}
            type="checkbox"
          />
          <span>
            Confirm publishing{" "}
            <span className="font-mono text-xs text-text-primary">
              {state.ref || "<ref>"}
            </span>
          </span>
        </label>
        <PushProgressList progress={state.progress} />
        {state.success ? (
          <div className="flex flex-wrap items-center gap-2 rounded-control border border-ok/30 bg-ok/10 p-3 text-sm text-ok">
            <span className="font-medium">Push complete</span>
            <Button
              icon={<Copy size={15} />}
              onClick={() => onCopyPull(state.ref)}
              size="sm"
              variant="secondary"
            >
              docker pull
            </Button>
          </div>
        ) : null}
        <FormError error={state.error ?? preview.error} />
      </div>
    </Modal>
  );
}

function SaveImageModal({
  onChange,
  onClose,
  onSubmit,
  state,
}: {
  state: SaveImageState;
  onChange: (patch: Partial<SaveImageState>) => void;
  onClose: () => void;
  onSubmit: () => void;
}) {
  const refs = splitRefs(state.refsText);
  return (
    <Modal
      busy={state.busy}
      footer={
        <ModalActions
          busy={state.busy}
          disabled={refs.length === 0 || !state.destPath.trim()}
          onCancel={onClose}
          onSubmit={onSubmit}
          submitLabel="Save"
        />
      }
      onClose={onClose}
      open={state.open}
      title="Save Image"
    >
      <div className="space-y-4">
        <TextAreaField
          label="Image refs"
          onChange={(refsText) => onChange({ refsText })}
          rows={3}
          value={state.refsText}
        />
        <TextField
          label="Destination tar"
          onChange={(destPath) => onChange({ destPath })}
          value={state.destPath}
        />
        <CodePreview
          value={joinPreview(["docker", "save", "-o", state.destPath, ...refs])}
        />
        <FormError error={state.error} />
      </div>
    </Modal>
  );
}

function LoadImageModal({
  onChange,
  onClose,
  onSubmit,
  state,
}: {
  state: LoadImageState;
  onChange: (patch: Partial<LoadImageState>) => void;
  onClose: () => void;
  onSubmit: () => void;
}) {
  return (
    <Modal
      busy={state.busy}
      footer={
        <ModalActions
          busy={state.busy}
          disabled={!state.srcPath.trim()}
          onCancel={onClose}
          onSubmit={onSubmit}
          submitLabel="Load"
        />
      }
      onClose={onClose}
      open={state.open}
      title="Load Image"
    >
      <div className="space-y-4">
        <TextField
          label="Source tar"
          onChange={(srcPath) => onChange({ srcPath })}
          value={state.srcPath}
        />
        <CodePreview
          value={joinPreview(["docker", "load", "-i", state.srcPath])}
        />
        <FormError error={state.error} />
      </div>
    </Modal>
  );
}

function RegistryLoginModal({
  onChange,
  onClose,
  onSubmit,
  presets,
  state,
}: {
  state: RegistryLoginState;
  presets: RegistryPreset[];
  onChange: (patch: Partial<RegistryLoginState>) => void;
  onClose: () => void;
  onSubmit: () => void;
}) {
  const options: Array<[string, string]> = [
    ...presets.map(
      (preset) => [preset.registry, preset.name] as [string, string],
    ),
    ["custom", "Custom"],
  ];
  const presetValue = presets.some(
    (preset) => preset.registry === state.registry,
  )
    ? state.registry
    : "custom";
  return (
    <Modal
      busy={state.busy}
      footer={
        <ModalActions
          busy={state.busy}
          disabled={
            !state.registry.trim() ||
            !state.username.trim() ||
            !state.secret.trim()
          }
          onCancel={onClose}
          onSubmit={onSubmit}
          submitLabel="Log in"
        />
      }
      onClose={onClose}
      open={state.open}
      title="Registry Login"
    >
      <div className="space-y-4">
        <SelectField
          label="Registry"
          onChange={(value) => {
            if (value !== "custom") {
              onChange({ registry: value });
            }
          }}
          options={options}
          value={presetValue}
        />
        <TextField
          label="Registry URL"
          onChange={(registry) => onChange({ registry })}
          value={state.registry}
        />
        <TextField
          label="Username"
          onChange={(username) => onChange({ username })}
          value={state.username}
        />
        <SelectField
          label="Secret kind"
          onChange={(secretKind) =>
            onChange({
              secretKind: secretKind === "token" ? "token" : "password",
            })
          }
          options={[
            ["password", "Password"],
            ["token", "Access token"],
          ]}
          value={state.secretKind}
        />
        <label className="block">
          <span className="text-sm font-medium text-text-primary">Secret</span>
          <input
            className="mt-2 h-9 w-full rounded-control border border-border bg-bg-inset px-3 text-sm text-text-primary outline-none focus:border-accent"
            onChange={(event) => onChange({ secret: event.target.value })}
            type="password"
            value={state.secret}
          />
        </label>
        {normalizeRegistryHostForUI(state.registry) === "docker.io" ? (
          <div className="rounded-control border border-info/30 bg-info/10 p-3 text-sm text-info">
            Docker Hub accounts with 2FA require an access token.
          </div>
        ) : null}
        <FormError error={state.error} />
      </div>
    </Modal>
  );
}

function CreateVolumeModal({
  onChange,
  onClose,
  onSubmit,
  state,
}: {
  state: CreateVolumeState;
  onChange: (patch: Partial<CreateVolumeState>) => void;
  onClose: () => void;
  onSubmit: () => void;
}) {
  return (
    <Modal
      busy={state.busy}
      footer={
        <ModalActions
          busy={state.busy}
          disabled={!state.name.trim()}
          onCancel={onClose}
          onSubmit={onSubmit}
          submitLabel="Create"
        />
      }
      onClose={onClose}
      open={state.open}
      title="Create Volume"
    >
      <div className="space-y-4">
        <TextField
          autoFocus
          label="Name"
          onChange={(name) => onChange({ name })}
          value={state.name}
        />
        <TextField
          label="Driver"
          onChange={(driver) => onChange({ driver })}
          value={state.driver}
        />
        <details className="rounded-control border border-border bg-bg-inset">
          <summary className="cursor-pointer px-3 py-2">Driver options</summary>
          <div className="border-t border-border p-3">
            <TextAreaField
              label="Options"
              onChange={(driverOptsText) => onChange({ driverOptsText })}
              rows={3}
              value={state.driverOptsText}
            />
          </div>
        </details>
        <details className="rounded-control border border-border bg-bg-inset">
          <summary className="cursor-pointer px-3 py-2">Labels</summary>
          <div className="border-t border-border p-3">
            <TextAreaField
              label="Labels"
              onChange={(labelsText) => onChange({ labelsText })}
              rows={3}
              value={state.labelsText}
            />
          </div>
        </details>
        <CodePreview value={dockerVolumePreview(state)} />
        <FormError error={state.error} />
      </div>
    </Modal>
  );
}

function BackupVolumeModal({
  onChange,
  onClose,
  onSubmit,
  state,
}: {
  state: BackupVolumeState;
  onChange: (patch: Partial<BackupVolumeState>) => void;
  onClose: () => void;
  onSubmit: () => void;
}) {
  const volumeName = state.volume?.name ?? "";
  return (
    <Modal
      busy={state.busy}
      footer={
        <ModalActions
          busy={state.busy}
          disabled={!volumeName}
          onCancel={onClose}
          onSubmit={onSubmit}
          submitLabel="Preview backup"
        />
      }
      onClose={onClose}
      open={state.open}
      title="Back Up Volume"
    >
      <div className="space-y-4">
        <div className="rounded-control border border-border bg-bg-inset p-3 text-sm">
          <div className="text-xs uppercase text-text-muted">Volume</div>
          <div className="mt-1 font-medium text-text-primary">{volumeName}</div>
        </div>
        <TextField
          label="Destination directory"
          onChange={(destPath) => onChange({ destPath })}
          value={state.destPath}
        />
        <div className="rounded-control border border-warn/30 bg-warn/10 p-3 text-sm text-warn">
          Stop projects that write to this volume before backing up databases.
        </div>
        <CodePreview
          value={joinPreview([
            "docker",
            "run",
            "--rm",
            "-v",
            `${volumeName}:/source:ro`,
            "-v",
            `${state.destPath || "<backup-dir>"}:/backup`,
            "alpine:3",
            "tar",
            "czf",
            `/backup/${volumeName || "volume"}-<timestamp>.tar.gz`,
            "-C",
            "/source",
            ".",
          ])}
        />
        <FormError error={state.error} />
      </div>
    </Modal>
  );
}

function RestoreVolumeModal({
  onChange,
  onClose,
  onSubmit,
  state,
}: {
  state: RestoreVolumeState;
  onChange: (patch: Partial<RestoreVolumeState>) => void;
  onClose: () => void;
  onSubmit: () => void;
}) {
  const selectedBackup = state.backups.find(
    (backup) => backup.id === state.backupID,
  );
  const archivePath = selectedBackup?.path ?? state.sourcePath;
  const disabled =
    !state.targetName.trim() || (!state.backupID && !state.sourcePath.trim());
  return (
    <Modal
      busy={state.busy}
      footer={
        <ModalActions
          busy={state.busy}
          disabled={disabled}
          onCancel={onClose}
          onSubmit={onSubmit}
          submitLabel="Preview restore"
        />
      }
      onClose={onClose}
      open={state.open}
      title="Restore Volume"
    >
      <div className="space-y-4">
        <SelectField
          label="Backup"
          onChange={(backupID) => {
            const backup = state.backups.find((item) => item.id === backupID);
            onChange({
              backupID,
              sourcePath: backup?.path ?? state.sourcePath,
            });
          }}
          options={[
            ["", state.loading ? "Loading backups..." : "Manual archive path"],
            ...state.backups.map(
              (backup) =>
                [
                  backup.id,
                  `${backup.volumeName} - ${formatDate(backup.createdAt)}`,
                ] as [string, string],
            ),
          ]}
          value={state.backupID}
        />
        {!state.backupID ? (
          <TextField
            label="Source archive"
            onChange={(sourcePath) => onChange({ sourcePath })}
            value={state.sourcePath}
          />
        ) : null}
        <TextField
          label="Target volume"
          onChange={(targetName) => onChange({ targetName })}
          value={state.targetName}
        />
        <label className="flex items-center gap-2 rounded-control border border-border bg-bg-inset px-3 py-2 text-sm">
          <input
            checked={state.overwrite}
            onChange={(event) => onChange({ overwrite: event.target.checked })}
            type="checkbox"
          />
          <span>Overwrite existing target volume</span>
        </label>
        <CodePreview
          value={joinPreview([
            "docker",
            "run",
            "--rm",
            "-v",
            `${state.targetName || "<target>"}:/restore`,
            "-v",
            `${archivePath ? shortPath(archivePath) : "<backup-dir>"}:/backup:ro`,
            "alpine:3",
            "sh",
            "-c",
            'set -eu; archive=$1; stash=/restore/.cairn-restore-old-$$; mkdir "$stash"; mv existing contents to stash; tar xzf "$archive" -C /restore || rollback',
            "cairn-restore",
            "/backup/<file>",
          ])}
        />
        <FormError error={state.error} />
      </div>
    </Modal>
  );
}

function CreateNetworkModal({
  onChange,
  onClose,
  onSubmit,
  state,
}: {
  state: CreateNetworkState;
  onChange: (patch: Partial<CreateNetworkState>) => void;
  onClose: () => void;
  onSubmit: () => void;
}) {
  const customDriver = state.driver === "custom";
  return (
    <Modal
      busy={state.busy}
      footer={
        <ModalActions
          busy={state.busy}
          disabled={!state.name.trim()}
          onCancel={onClose}
          onSubmit={onSubmit}
          submitLabel="Create"
        />
      }
      onClose={onClose}
      open={state.open}
      title="Create Network"
    >
      <div className="space-y-4">
        <TextField
          autoFocus
          label="Name"
          onChange={(name) => onChange({ name })}
          value={state.name}
        />
        <SelectField
          label="Driver"
          onChange={(nextDriver) => onChange({ driver: nextDriver })}
          options={[
            ["bridge", "bridge"],
            ["overlay", "overlay"],
            ["custom", "custom"],
          ]}
          value={state.driver}
        />
        {customDriver ? (
          <TextField
            label="Custom driver"
            onChange={(customDriver) => onChange({ customDriver })}
            value={state.customDriver}
          />
        ) : null}
        <div className="grid grid-cols-2 gap-3">
          <TextField
            label="Subnet CIDR"
            onChange={(subnet) => onChange({ subnet })}
            value={state.subnet}
          />
          <TextField
            label="Gateway"
            onChange={(gateway) => onChange({ gateway })}
            value={state.gateway}
          />
        </div>
        <div className="flex gap-4">
          <label className="flex items-center gap-2">
            <input
              checked={state.internal}
              onChange={(event) => onChange({ internal: event.target.checked })}
              type="checkbox"
            />
            <span>Internal</span>
          </label>
          <label className="flex items-center gap-2">
            <input
              checked={state.attachable}
              onChange={(event) =>
                onChange({ attachable: event.target.checked })
              }
              type="checkbox"
            />
            <span>Attachable</span>
          </label>
        </div>
        <TextAreaField
          label="Labels"
          onChange={(labelsText) => onChange({ labelsText })}
          rows={3}
          value={state.labelsText}
        />
        <CodePreview value={dockerNetworkPreview(state)} />
        <FormError error={state.error} />
      </div>
    </Modal>
  );
}

function ImportProjectModal({
  onBrowse,
  onChange,
  onClose,
  onSubmit,
  state,
}: {
  state: ImportProjectState;
  onBrowse: () => void;
  onChange: (folderPath: string) => void;
  onClose: () => void;
  onSubmit: () => void;
}) {
  const previewName = projectNameFromPath(state.folderPath);
  const candidates = composeFileCandidates(state.folderPath);
  const wslMount = state.folderPath.replace(/\\/g, "/").startsWith("/mnt/");
  return (
    <Modal
      busy={state.busy}
      footer={
        <ModalActions
          busy={state.busy}
          disabled={!state.folderPath.trim()}
          onCancel={onClose}
          onSubmit={onSubmit}
          submitLabel="Import"
        />
      }
      onClose={onClose}
      open={state.open}
      title="Import Project"
      size="lg"
    >
      <div className="space-y-4">
        <label className="block">
          <span className="text-xs font-medium uppercase text-text-muted">
            Folder
          </span>
          <div className="mt-1 flex flex-col gap-2 sm:flex-row">
            <input
              className="h-9 min-w-0 flex-1 rounded-control border border-border bg-bg-inset px-3 text-sm text-text-primary outline-none"
              onChange={(event) => onChange(event.target.value)}
              placeholder="/home/me/project"
              value={state.folderPath}
            />
            <Button icon={<FolderOpen size={16} />} onClick={onBrowse}>
              Browse
            </Button>
          </div>
        </label>

        {state.folderPath ? (
          <div className="grid gap-3 md:grid-cols-2">
            <div className="rounded-card border border-border bg-bg-inset p-3">
              <div className="text-xs font-medium uppercase text-text-muted">
                Compose files
              </div>
              <div className="mt-2 space-y-1">
                {candidates.map((file) => (
                  <label className="flex items-center gap-2 text-sm" key={file}>
                    <input checked readOnly type="checkbox" />
                    <span className="truncate font-mono text-xs">{file}</span>
                  </label>
                ))}
              </div>
            </div>
            <div className="rounded-card border border-border bg-bg-inset p-3">
              <div className="text-xs font-medium uppercase text-text-muted">
                Project name
              </div>
              <div className="mt-2 truncate text-base font-semibold text-text-primary">
                {previewName || "-"}
              </div>
              <div className="mt-3 text-xs text-text-muted">
                {state.imported?.summary.id ?? "Pending validation"}
              </div>
            </div>
          </div>
        ) : null}

        {wslMount ? (
          <div className="rounded-card border border-warn/30 bg-warn/10 px-3 py-2 text-sm text-warn">
            WSL mount paths may be slower than files stored inside the distro.
          </div>
        ) : null}

        {state.error ? (
          <div className="rounded-card border border-error/30 bg-error/10 px-3 py-2 text-sm text-error">
            {state.error}
          </div>
        ) : null}
        {state.imported ? (
          <div className="rounded-card border border-ok/30 bg-ok/10 px-3 py-2 text-sm text-ok">
            Imported {state.imported.summary.name}
          </div>
        ) : null}
      </div>
    </Modal>
  );
}

function ModalActions({
  busy,
  disabled,
  onCancel,
  onSubmit,
  submitLabel,
}: {
  busy: boolean;
  disabled: boolean;
  onCancel: () => void;
  onSubmit: () => void;
  submitLabel: string;
}) {
  return (
    <div className="flex justify-end gap-2">
      <Button disabled={busy} onClick={onCancel} variant="secondary">
        Cancel
      </Button>
      <Button
        disabled={disabled}
        loading={busy}
        onClick={onSubmit}
        variant="primary"
      >
        {submitLabel}
      </Button>
    </div>
  );
}

function TextField({
  autoFocus,
  label,
  onChange,
  readOnly,
  value,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  autoFocus?: boolean;
  readOnly?: boolean;
}) {
  return (
    <label className="block">
      <span className="text-sm font-medium text-text-primary">{label}</span>
      <input
        autoFocus={autoFocus}
        className="mt-2 h-9 w-full rounded-control border border-border bg-bg-inset px-3 text-sm text-text-primary outline-none focus:border-accent disabled:opacity-70"
        onChange={(event) => onChange(event.target.value)}
        readOnly={readOnly}
        value={value}
      />
    </label>
  );
}

function TextAreaField({
  label,
  onChange,
  rows,
  value,
}: {
  label: string;
  value: string;
  rows: number;
  onChange: (value: string) => void;
}) {
  return (
    <label className="block">
      <span className="text-sm font-medium text-text-primary">{label}</span>
      <textarea
        className="mt-2 w-full resize-y rounded-control border border-border bg-bg-inset px-3 py-2 font-mono text-sm text-text-primary outline-none focus:border-accent"
        onChange={(event) => onChange(event.target.value)}
        rows={rows}
        value={value}
      />
    </label>
  );
}

function SelectField({
  label,
  onChange,
  options,
  value,
}: {
  label: string;
  value: string;
  options: Array<[string, string]>;
  onChange: (value: string) => void;
}) {
  return (
    <label className="block">
      <span className="text-sm font-medium text-text-primary">{label}</span>
      <select
        className="mt-2 h-9 w-full rounded-control border border-border bg-bg-inset px-3 text-sm text-text-primary outline-none focus:border-accent"
        onChange={(event) => onChange(event.target.value)}
        value={value}
      >
        {options.map(([optionValue, label]) => (
          <option key={optionValue || label} value={optionValue}>
            {label}
          </option>
        ))}
      </select>
    </label>
  );
}

function HubResultList({
  loading,
  onSelect,
  results,
}: {
  loading: boolean;
  results: HubSearchResult[];
  onSelect: (result: HubSearchResult) => void;
}) {
  if (loading) {
    return <Skeleton className="h-20 w-full" />;
  }
  if (results.length === 0) {
    return null;
  }
  return (
    <div className="max-h-48 overflow-auto rounded-control border border-border bg-bg-inset">
      {results.map((result) => (
        <button
          className="flex w-full items-start gap-3 border-b border-border px-3 py-2 text-left last:border-b-0 hover:bg-bg-card"
          key={result.name}
          onClick={() => onSelect(result)}
          type="button"
        >
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <span className="truncate font-mono text-xs text-text-primary">
                {result.name}
              </span>
              {result.official ? <Badge tone="ok">Official</Badge> : null}
            </div>
            <div className="mt-1 line-clamp-2 text-xs text-text-muted">
              {result.description || "-"}
            </div>
          </div>
          <Badge tone="neutral">{result.stars}</Badge>
        </button>
      ))}
    </div>
  );
}

function CodePreview({ value }: { value: string }) {
  return (
    <pre className="overflow-auto rounded-control border border-border bg-bg-inset p-3 font-mono text-xs text-text-secondary">
      {value || "-"}
    </pre>
  );
}

function ImageRefPreview({
  preview,
}: {
  preview: ReturnType<typeof imageRefPreview>;
}) {
  if (preview.error && !preview.registry && !preview.repository) {
    return null;
  }
  return (
    <div className="grid gap-2 rounded-control border border-border bg-bg-inset p-3 text-sm sm:grid-cols-3">
      <StatusPill
        label="Registry"
        ok={Boolean(preview.registry)}
        value={preview.registry || "-"}
      />
      <StatusPill
        label="Repository"
        ok={Boolean(preview.repository)}
        value={preview.repository || "-"}
      />
      <StatusPill
        label="Tag"
        ok={Boolean(preview.tag)}
        value={preview.tag || "-"}
      />
    </div>
  );
}

function PushProgressList({ progress }: { progress: ImageProgressPayload[] }) {
  if (progress.length === 0) {
    return null;
  }
  return (
    <div className="max-h-52 overflow-auto rounded-control border border-border bg-bg-inset">
      {progress.map((item, index) => {
        const pct =
          item.total && item.current
            ? Math.min(100, Math.round((item.current / item.total) * 100))
            : null;
        return (
          <div
            className="grid grid-cols-[minmax(0,1fr)_auto] gap-3 border-b border-border px-3 py-2 text-sm last:border-b-0"
            key={`${item.layerID || "stream"}-${index}`}
          >
            <div className="min-w-0">
              <div className="truncate font-mono text-xs text-text-primary">
                {item.layerID || "push"}
              </div>
              <div className="mt-1 truncate text-xs text-text-muted">
                {item.status}
              </div>
            </div>
            {pct === null ? (
              <Badge tone={item.status === "done" ? "ok" : "neutral"}>
                {item.status}
              </Badge>
            ) : (
              <Badge tone={pct >= 100 ? "ok" : "info"}>{pct}%</Badge>
            )}
          </div>
        );
      })}
    </div>
  );
}

function FormError({ error }: { error?: string }) {
  return error ? (
    <div className="rounded-control border border-error/30 bg-error/10 p-3 text-error">
      {error}
    </div>
  ) : null;
}

function MetricButton({
  hint,
  label,
  onClick,
  value,
}: {
  label: string;
  value: number;
  hint: string;
  onClick: () => void;
}) {
  return (
    <button
      className="rounded-card border border-border bg-bg-card p-3 text-left transition hover:border-border-strong hover:bg-bg-panel"
      onClick={onClick}
      type="button"
    >
      <div className="text-sm text-text-secondary">{label}</div>
      <div className="mt-1 text-xl font-semibold">{value}</div>
      <div className="mt-1 text-xs text-text-muted">{hint}</div>
    </button>
  );
}

function StatusBlock({
  label,
  tone,
  value,
}: {
  label: string;
  value: number;
  tone: BadgeTone;
}) {
  return (
    <div className="rounded-control border border-border bg-bg-inset p-3">
      <Badge tone={tone}>{label}</Badge>
      <div className="mt-3 text-2xl font-semibold">{value}</div>
    </div>
  );
}

function PortList({ ports }: { ports: PortBinding[] }) {
  if (ports.length === 0) {
    return <span>-</span>;
  }
  return (
    <div className="flex flex-wrap gap-1">
      {ports.slice(0, 3).map((port) => (
        <Badge
          key={`${port.hostIP}-${port.hostPort}-${port.containerPort}-${port.protocol}`}
        >
          {port.hostPort ? `${port.hostPort}->` : ""}
          {port.containerPort}/{port.protocol}
        </Badge>
      ))}
      {ports.length > 3 ? <Badge>+{ports.length - 3}</Badge> : null}
    </div>
  );
}

function MonoCopy({ value }: { value: string }) {
  return (
    <button
      className="font-mono text-xs text-text-secondary hover:text-accent"
      onClick={() => {
        void navigator.clipboard?.writeText(value);
      }}
      title={value}
      type="button"
    >
      {shortID(value)}
    </button>
  );
}

function buildRunImageRequest(state: RunImageState): RunImageRequest {
  const validation = runImageValidation(state);
  if (validation) {
    throw new Error(validation);
  }
  return {
    imageRef: state.imageRef.trim(),
    name: state.name.trim(),
    ports: parsePorts(state.portsText),
    env: parseEnv(state.envText),
    volumes: parseMounts(state.volumesText),
    networkID: state.networkID,
    restartPolicy: state.restartPolicy,
    command: splitCommand(state.commandText),
    user: state.user.trim(),
    detach: true,
    pullIfMissing: state.pullIfMissing,
  };
}

function runImageValidation(state: RunImageState) {
  if (!state.imageRef.trim()) {
    return "Image ref is required";
  }
  try {
    parsePorts(state.portsText);
    parseEnv(state.envText);
    parseMounts(state.volumesText);
  } catch (error) {
    return error instanceof Error ? error.message : "Invalid run configuration";
  }
  return "";
}

function parsePorts(value: string): PortMapping[] {
  return splitLines(value).map((line) => {
    const [portPart, protocol = "tcp"] = line.split("/");
    const parts = portPart.split(":");
    const containerPort = parts.pop()?.trim() ?? "";
    const hostPort = parts.pop()?.trim() ?? "";
    const hostIP = parts.join(":").trim();
    if (!containerPort) {
      throw new Error("Container port is required");
    }
    return {
      hostIP,
      hostPort,
      containerPort,
      protocol: protocol.trim() || "tcp",
    };
  });
}

function parseEnv(value: string) {
  return splitLines(value).map((line) => {
    const [name, envValue = ""] = line.split(/=(.*)/s);
    if (!name.trim()) {
      throw new Error("Environment key is required");
    }
    return { name: name.trim(), value: envValue };
  });
}

export function parseMounts(value: string): MountSpec[] {
  return splitLines(value).map((line) => {
    if (line.includes("type=") || line.includes("target=")) {
      const values = parseCommaKeyValue(line);
      const mountType = values.type || "volume";
      const target = values.target || values.destination || "";
      const source = values.source || values.src || "";
      if (!target || !source) {
        throw new Error("Mount source and target are required");
      }
      return {
        type: mountType,
        source,
        target,
        volumeName: mountType === "volume" ? source : "",
        readOnly:
          values.ro === "true" ||
          values.readonly === "true" ||
          values.mode === "ro",
      };
    }
    const parts = line.split(":");
    const maybeMode = parts[parts.length - 1]?.trim().toLowerCase();
    const mode =
      maybeMode === "ro" || maybeMode === "rw" || maybeMode === "readonly"
        ? parts.pop()?.trim().toLowerCase()
        : "rw";
    const target = parts.pop()?.trim() ?? "";
    const first = parts[0]?.trim() ?? "";
    const hasExplicitType = first === "volume" || first === "bind";
    const sourceParts = hasExplicitType ? parts.slice(1) : parts;
    const source = sourceParts.join(":").trim();
    const mountType = hasExplicitType
      ? first
      : looksLikeBindMountSource(source)
        ? "bind"
        : "volume";
    if (!target || !source) {
      throw new Error("Mount source and target are required");
    }
    return {
      type: mountType,
      source,
      target,
      volumeName: mountType === "volume" ? source : "",
      readOnly: mode === "ro" || mode === "readonly",
    };
  });
}

function looksLikeBindMountSource(source: string) {
  return (
    source.startsWith("/") ||
    source.startsWith("./") ||
    source.startsWith("../") ||
    source.startsWith("~") ||
    /^[A-Za-z]:[\\/]/.test(source) ||
    source.startsWith("\\\\")
  );
}

function parseKeyValueLines(value: string) {
  const pairs = splitLines(value);
  if (pairs.length === 0) {
    return undefined;
  }
  const out: Record<string, string> = {};
  for (const line of pairs) {
    const [key, nextValue = ""] = line.split(/=(.*)/s);
    if (key.trim()) {
      out[key.trim()] = nextValue;
    }
  }
  return out;
}

function parseCommaKeyValue(value: string) {
  const out: Record<string, string> = {};
  for (const raw of value.split(",")) {
    const [key, nextValue = ""] = raw.split(/=(.*)/s);
    if (key.trim()) {
      out[key.trim().toLowerCase()] = nextValue.trim();
    }
  }
  return out;
}

function splitLines(value: string) {
  return value
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean);
}

function splitRefs(value: string) {
  return value
    .split(/\r?\n|,/)
    .map((line) => line.trim())
    .filter(Boolean);
}

function mergeImageProgress(
  current: ImageProgressPayload[],
  next: ImageProgressPayload,
) {
  if (!next.layerID) {
    return current.concat(next).slice(-30);
  }
  const index = current.findIndex((item) => item.layerID === next.layerID);
  if (index === -1) {
    return current.concat(next).slice(-30);
  }
  const merged = current.slice();
  merged[index] = next;
  return merged;
}

export function imageRefPreview(raw: string) {
  const value = raw.trim();
  if (!value) {
    return {
      registry: "",
      repository: "",
      tag: "",
      error: "Image ref is required",
    };
  }
  if (/\s/.test(value)) {
    return {
      registry: "",
      repository: "",
      tag: "",
      error: "Image ref cannot contain whitespace",
    };
  }
  if (value.includes("@")) {
    return {
      registry: "",
      repository: "",
      tag: "",
      error: "Use a tag ref before pushing",
    };
  }
  const first = value.split("/")[0] ?? "";
  const hasRegistry =
    value.includes("/") &&
    (first === "localhost" || first.includes(".") || first.includes(":"));
  const registry = hasRegistry
    ? normalizeRegistryHostForUI(first)
    : "docker.io";
  const rest = hasRegistry ? value.slice(first.length + 1) : value;
  const slash = rest.lastIndexOf("/");
  const colon = rest.lastIndexOf(":");
  const repository = colon > slash ? rest.slice(0, colon) : rest;
  const tag = colon > slash ? rest.slice(colon + 1) : "latest";
  if (!repository || !tag) {
    return {
      registry,
      repository,
      tag,
      error: "Image ref needs repository and tag",
    };
  }
  return { registry, repository, tag, error: undefined };
}

function registryFromImageRef(ref: string) {
  return imageRefPreview(ref).registry || "docker.io";
}

function registryAccountFor(accounts: RegistryAccount[], registry: string) {
  const normalized = normalizeRegistryHostForUI(registry);
  return accounts.find(
    (account) => normalizeRegistryHostForUI(account.registry) === normalized,
  );
}

function pushableImageRef(image: ImageSummary) {
  return image.repoTags?.find((tag) => tag && tag !== "<none>:<none>") ?? "";
}

function taggableImageRef(image: ImageSummary) {
  const tagged = pushableImageRef(image);
  if (tagged) {
    return tagged;
  }
  return `localhost:5000/cairn/${shortID(image.id).replace(/^sha256:/, "")}:latest`;
}

function splitCommand(value: string) {
  const trimmed = value.trim();
  if (!trimmed) {
    return [];
  }
  const matches = trimmed.match(/"([^"]*)"|'([^']*)'|[^\s]+/g) ?? [];
  return matches.map((part) => part.replace(/^["']|["']$/g, ""));
}

function appendLine(current: string, line: string) {
  return current.trim() ? `${current.trimEnd()}\n${line}` : line;
}

function imageRefWithTag(ref: string, tag: string) {
  const cleanRef = ref.trim();
  const cleanTag = tag.trim();
  if (!cleanRef || !cleanTag) {
    return cleanRef;
  }
  const slash = cleanRef.lastIndexOf("/");
  const colon = cleanRef.lastIndexOf(":");
  if (colon > slash || cleanRef.includes("@")) {
    return cleanRef;
  }
  return `${cleanRef}:${cleanTag}`;
}

function suggestContainerName(ref: string) {
  const withoutDigest = ref.split("@")[0] ?? ref;
  const withoutTag = withoutDigest.replace(/:[^/:]+$/, "");
  const name = withoutTag.split("/").pop() || "container";
  return name.replace(/[^a-zA-Z0-9_.-]/g, "-");
}

function dockerRunPreview(state: RunImageState) {
  const args = ["docker", "run", "-d"];
  if (state.name.trim()) {
    args.push("--name", state.name.trim());
  }
  for (const port of parsePorts(state.portsText)) {
    const host = [port.hostIP, port.hostPort].filter(Boolean).join(":");
    args.push(
      "-p",
      `${host ? `${host}:` : ""}${port.containerPort}/${port.protocol || "tcp"}`,
    );
  }
  for (const env of parseEnv(state.envText)) {
    args.push(
      "-e",
      `${env.name}=${secretLikeKey(env.name) ? "********" : env.value}`,
    );
  }
  for (const mount of parseMounts(state.volumesText)) {
    args.push(
      "--mount",
      `type=${mount.type},source=${mount.source || mount.volumeName},target=${mount.target},${mount.readOnly ? "ro" : "rw"}`,
    );
  }
  if (state.networkID) {
    args.push("--network", state.networkID);
  }
  if (state.restartPolicy && state.restartPolicy !== "no") {
    args.push("--restart", state.restartPolicy);
  }
  if (state.user.trim()) {
    args.push("--user", state.user.trim());
  }
  args.push(state.imageRef.trim() || "<image>");
  args.push(...splitCommand(state.commandText));
  return joinPreview(args);
}

function safeDockerRunPreview(state: RunImageState) {
  try {
    return dockerRunPreview(state);
  } catch {
    return "docker run -d";
  }
}

function dockerVolumePreview(state: CreateVolumeState) {
  const args = ["docker", "volume", "create"];
  if (state.driver.trim()) {
    args.push("--driver", state.driver.trim());
  }
  for (const [key, value] of Object.entries(
    parseKeyValueLines(state.driverOptsText) ?? {},
  )) {
    args.push("--opt", `${key}=${value}`);
  }
  for (const [key, value] of Object.entries(
    parseKeyValueLines(state.labelsText) ?? {},
  )) {
    args.push("--label", `${key}=${value}`);
  }
  args.push(state.name.trim() || "<name>");
  return joinPreview(args);
}

function dockerNetworkPreview(state: CreateNetworkState) {
  const args = ["docker", "network", "create"];
  const driver = state.driver === "custom" ? state.customDriver : state.driver;
  if (driver.trim()) {
    args.push("--driver", driver.trim());
  }
  if (state.subnet.trim()) {
    args.push("--subnet", state.subnet.trim());
  }
  if (state.gateway.trim()) {
    args.push("--gateway", state.gateway.trim());
  }
  if (state.internal) {
    args.push("--internal");
  }
  if (state.attachable) {
    args.push("--attachable");
  }
  for (const [key, value] of Object.entries(
    parseKeyValueLines(state.labelsText) ?? {},
  )) {
    args.push("--label", `${key}=${value}`);
  }
  args.push(state.name.trim() || "<name>");
  return joinPreview(args);
}

function joinPreview(args: string[]) {
  return args.filter(Boolean).map(quotePreviewArg).join(" ");
}

function quotePreviewArg(value: string) {
  if (!value) {
    return '""';
  }
  return /\s|["']/.test(value) ? `"${value.replace(/"/g, '\\"')}"` : value;
}

function secretKeys(value: string) {
  try {
    return parseEnv(value)
      .map((env) => env.name)
      .filter(secretLikeKey);
  } catch {
    return [];
  }
}

function secretLikeKey(name: string) {
  const lower = name.toLowerCase();
  return ["pass", "password", "token", "secret", "key", "auth"].some((marker) =>
    lower.includes(marker),
  );
}

function activeProviderSummary(
  providers: ProviderSummary[],
): ProviderSummary | null {
  return providers.find((provider) => provider.active) ?? providers[0] ?? null;
}

export function filterContainers(
  containers: ContainerSummary[],
  search: string,
  filter: FilterID,
) {
  const needle = normalize(search);
  return containers.filter((container) => {
    const matchesFilter =
      filter === "all" ||
      (filter === "stopped" && container.state === "exited") ||
      (filter === "ungrouped" && !container.projectID) ||
      container.state === filter ||
      (filter === "unhealthy" && container.health === "unhealthy");
    return matchesFilter && matchesContainerSearch(container, needle);
  });
}

export function filterImages(
  images: ImageSummary[],
  counts: Record<string, number>,
  search: string,
  filter: FilterID,
) {
  const needle = normalize(search);
  return images.filter((image) => {
    const inUse = (counts[image.id] ?? 0) > 0 || image.inUse;
    const matchesFilter =
      filter === "all" ||
      (filter === "in-use" && inUse) ||
      (filter === "unused" && !inUse) ||
      (filter === "dangling" && imageDangling(image)) ||
      (filter === "updates" &&
        Boolean(image.updateStatus && image.updateStatus !== "unknown"));
    return matchesFilter && matchesImageSearch(image, needle);
  });
}

export function filterVolumes(
  volumes: VolumeSummary[],
  search: string,
  filter: FilterID,
) {
  const needle = normalize(search);
  return volumes.filter((volume) => {
    const matchesFilter =
      filter === "all" ||
      (filter === "in-use" && volume.inUse) ||
      (filter === "unused" && !volume.inUse);
    return matchesFilter && matchesVolumeSearch(volume, needle);
  });
}

export function filterNetworks(networks: NetworkSummary[], search: string) {
  const needle = normalize(search);
  return networks.filter((network) => matchesNetworkSearch(network, needle));
}

function normalize(value: unknown) {
  return String(value ?? "").toLowerCase();
}

function normalizedIncludes(value: unknown, needle: string) {
  return needle === "" || normalize(value).includes(needle);
}

function matchesContainerSearch(container: ContainerSummary, needle: string) {
  if (needle === "") {
    return true;
  }
  return (
    normalizedIncludes(container.name, needle) ||
    normalizedIncludes(container.image, needle) ||
    normalizedIncludes(container.id, needle) ||
    normalizedIncludes(container.projectID, needle) ||
    normalizedIncludes(container.service, needle)
  );
}

function matchesImageSearch(image: ImageSummary, needle: string) {
  if (needle === "") {
    return true;
  }
  if (normalizedIncludes(image.id, needle)) {
    return true;
  }
  for (const ref of image.repoTags ?? []) {
    if (ref && normalizedIncludes(ref, needle)) {
      return true;
    }
  }
  for (const digest of image.repoDigests ?? []) {
    if (digest && normalizedIncludes(digest, needle)) {
      return true;
    }
  }
  return false;
}

function matchesVolumeSearch(volume: VolumeSummary, needle: string) {
  if (needle === "") {
    return true;
  }
  return (
    normalizedIncludes(volume.name, needle) ||
    normalizedIncludes(volume.driver, needle) ||
    normalizedIncludes(volume.mountpoint, needle) ||
    normalizedIncludes(volume.labels?.[composeProjectLabel], needle)
  );
}

function matchesNetworkSearch(network: NetworkSummary, needle: string) {
  if (needle === "") {
    return true;
  }
  return (
    normalizedIncludes(network.name, needle) ||
    normalizedIncludes(network.id, needle) ||
    normalizedIncludes(network.driver, needle) ||
    normalizedIncludes(network.scope, needle)
  );
}

function containerFilterCounts(containers: ContainerSummary[]) {
  const counts = {
    all: containers.length,
    running: 0,
    stopped: 0,
    paused: 0,
    unhealthy: 0,
    ungrouped: 0,
  };
  for (const container of containers) {
    if (container.state === "running") {
      counts.running++;
    }
    if (container.state === "exited") {
      counts.stopped++;
    }
    if (container.state === "paused") {
      counts.paused++;
    }
    if (container.health === "unhealthy") {
      counts.unhealthy++;
    }
    if (!container.projectID) {
      counts.ungrouped++;
    }
  }
  return counts;
}

function imageFilterCounts(
  images: ImageSummary[],
  countsByID: Record<string, number>,
) {
  const counts = {
    all: images.length,
    inUse: 0,
    unused: 0,
    dangling: 0,
    updates: 0,
  };
  for (const image of images) {
    const inUse = (countsByID[image.id] ?? 0) > 0 || image.inUse;
    if (inUse) {
      counts.inUse++;
    } else {
      counts.unused++;
    }
    if (imageDangling(image)) {
      counts.dangling++;
    }
    if (image.updateStatus && image.updateStatus !== "unknown") {
      counts.updates++;
    }
  }
  return counts;
}

function volumeFilterCounts(volumes: VolumeSummary[]) {
  const counts = { all: volumes.length, inUse: 0, unused: 0 };
  for (const volume of volumes) {
    if (volume.inUse) {
      counts.inUse++;
    } else {
      counts.unused++;
    }
  }
  return counts;
}

function macOSSetupCheckRows(status: ProviderStatus | null) {
  const problem = (code: string) =>
    status?.problems?.find((entry) => entry.code === code) ?? null;
  const warning = (code: string) =>
    status?.warnings?.find((entry) => entry.code === code) ?? null;
  const brewWarning = warning("BREW_MISSING");
  return [
    brewWarning && status
      ? {
          label: "Homebrew available",
          state: "warn" as StatusToneID,
          detail: brewWarning.message,
        }
      : setupCheckRow("Homebrew available", status, null),
    setupCheckRow(
      "Docker CLI installed",
      status,
      problem("DOCKER_MISSING"),
      status?.dockerInstalled,
    ),
    setupCheckRow(
      "Compose installed",
      status,
      problem("COMPOSE_MISSING"),
      status?.composeInstalled,
    ),
    setupCheckRow(
      "Buildx installed",
      status,
      problem("BUILDX_MISSING"),
      status?.buildxInstalled,
    ),
    setupWarningCheckRow(
      "Docker tools current",
      status,
      warning("DOCKER_PACKAGES_OUTDATED"),
      Boolean(
        status?.dockerInstalled &&
        status?.composeInstalled &&
        status?.buildxInstalled,
      ),
    ),
    setupCheckRow("Colima installed", status, problem("COLIMA_MISSING")),
    setupCheckRow("Colima running", status, problem("COLIMA_STOPPED")),
    setupCheckRow("Colima context ready", status, problem("CONTEXT_MISSING")),
    setupCheckRow(
      "Docker daemon reachable",
      status,
      problem("DOCKERD_DOWN"),
      status?.dockerRunning,
    ),
  ];
}

function linuxSetupCheckRows(status: ProviderStatus | null) {
  const problem = (code: string) =>
    status?.problems?.find((entry) => entry.code === code) ?? null;
  const warning = (code: string) =>
    status?.warnings?.find((entry) => entry.code === code) ?? null;
  const systemdWarning = warning("SYSTEMD_MISSING");
  return [
    systemdWarning && status
      ? {
          label: "systemd available",
          state: "warn" as StatusToneID,
          detail: systemdWarning.message,
        }
      : setupCheckRow("systemd available", status, null),
    setupCheckRow(
      "Docker CLI installed",
      status,
      problem("DOCKER_MISSING"),
      status?.dockerInstalled,
    ),
    setupCheckRow(
      "Compose plugin installed",
      status,
      problem("COMPOSE_MISSING"),
      status?.composeInstalled,
    ),
    setupCheckRow(
      "Buildx plugin installed",
      status,
      problem("BUILDX_MISSING"),
      status?.buildxInstalled,
    ),
    setupWarningCheckRow(
      "Docker packages current",
      status,
      warning("DOCKER_PACKAGES_OUTDATED"),
      Boolean(
        status?.dockerInstalled &&
        status?.composeInstalled &&
        status?.buildxInstalled,
      ),
    ),
    setupCheckRow(
      "Docker socket accessible",
      status,
      problem("PERM_SOCKET"),
      Boolean(status?.dockerHost),
    ),
    setupCheckRow(
      "Docker daemon reachable",
      status,
      problem("DOCKERD_DOWN"),
      status?.dockerRunning,
    ),
  ];
}

function windowsSetupCheckRows(status: ProviderStatus | null) {
  const problem = (code: string) =>
    status?.problems?.find((entry) => entry.code === code) ?? null;
  const warning = (code: string) =>
    status?.warnings?.find((entry) => entry.code === code) ?? null;
  return [
    setupCheckRow("WSL installed", status, problem("WSL_MISSING")),
    setupCheckRow("Ubuntu distro present", status, problem("UBUNTU_MISSING")),
    setupCheckRow("WSL2 enabled", status, problem("WSL2_REQUIRED")),
    setupCheckRow("systemd enabled", status, problem("SYSTEMD_OFF")),
    setupCheckRow(
      "Docker CLI installed",
      status,
      problem("DOCKER_MISSING"),
      status?.dockerInstalled,
    ),
    setupCheckRow(
      "Compose plugin installed",
      status,
      problem("COMPOSE_MISSING"),
      status?.composeInstalled,
    ),
    setupCheckRow(
      "Buildx plugin installed",
      status,
      problem("BUILDX_MISSING"),
      status?.buildxInstalled,
    ),
    setupWarningCheckRow(
      "Docker packages current",
      status,
      warning("DOCKER_PACKAGES_OUTDATED"),
      Boolean(
        status?.dockerInstalled &&
        status?.composeInstalled &&
        status?.buildxInstalled,
      ),
    ),
    setupCheckRow(
      "Docker daemon reachable",
      status,
      problem("DOCKERD_DOWN"),
      status?.dockerRunning,
    ),
    setupNVIDIACheckRow(status, warning("NVIDIA_RUNTIME_MISSING")),
  ];
}

function providerHasWarning(status: ProviderStatus | null, code: string) {
  return Boolean(status?.warnings?.some((warning) => warning.code === code));
}

function isEditableElement(target: EventTarget | null) {
  if (!(target instanceof HTMLElement)) {
    return false;
  }
  const tag = target.tagName.toLowerCase();
  return (
    target.isContentEditable ||
    tag === "input" ||
    tag === "textarea" ||
    tag === "select"
  );
}

function setupCheckRow(
  label: string,
  status: ProviderStatus | null,
  problem: ProviderProblem | null,
  okFlag?: boolean,
) {
  if (!status) {
    return {
      label,
      state: "neutral" as StatusToneID,
      detail: "Not checked yet",
    };
  }
  if (problem) {
    return {
      label,
      state: "error" as StatusToneID,
      detail: problem.repairHint || problem.message,
    };
  }
  if (okFlag === false) {
    return {
      label,
      state: "neutral" as StatusToneID,
      detail: "Not detected yet",
    };
  }
  return { label, state: "ok" as StatusToneID, detail: "Ready" };
}

function setupWarningCheckRow(
  label: string,
  status: ProviderStatus | null,
  warning: ProviderWarning | null,
  okFlag?: boolean,
) {
  if (!status) {
    return {
      label,
      state: "neutral" as StatusToneID,
      detail: "Not checked yet",
    };
  }
  if (warning) {
    return {
      label,
      state: "warn" as StatusToneID,
      detail: warning.message,
    };
  }
  if (okFlag === false) {
    return {
      label,
      state: "neutral" as StatusToneID,
      detail: "Not detected yet",
    };
  }
  return { label, state: "ok" as StatusToneID, detail: "Current" };
}

function setupNVIDIACheckRow(
  status: ProviderStatus | null,
  warning: ProviderWarning | null,
) {
  if (!status) {
    return {
      label: "NVIDIA GPU runtime",
      state: "neutral" as StatusToneID,
      detail: "Not checked yet",
    };
  }
  if (!status.nvidiaGPUDetected) {
    return {
      label: "NVIDIA GPU runtime",
      state: "neutral" as StatusToneID,
      detail: "No NVIDIA GPU exposed to this backend",
    };
  }
  if (warning) {
    return {
      label: "NVIDIA GPU runtime",
      state: "warn" as StatusToneID,
      detail: warning.message,
    };
  }
  if (status.nvidiaContainerRuntime) {
    return {
      label: "NVIDIA GPU runtime",
      state: "ok" as StatusToneID,
      detail: "Ready",
    };
  }
  return {
    label: "NVIDIA GPU runtime",
    state: "neutral" as StatusToneID,
    detail: "Not detected yet",
  };
}

function filterAuditEntries(entries: AuditEntry[], filter: AuditFilterState) {
  const action = filter.action.trim().toLowerCase();
  const projectID = filter.projectID.trim().toLowerCase();
  const cutoff = auditRangeCutoff(filter.range);
  return entries.filter((entry) => {
    if (action && !entry.action.toLowerCase().startsWith(action)) {
      return false;
    }
    if (filter.status && entry.result !== filter.status) {
      return false;
    }
    if (projectID) {
      const metadataProject = auditMetadataString(entry, "projectID");
      if (
        !metadataProject.toLowerCase().includes(projectID) &&
        !(entry.target || "").toLowerCase().includes(projectID)
      ) {
        return false;
      }
    }
    if (cutoff !== null) {
      const timestamp = dateMillis(entry.ts);
      if (!timestamp || timestamp < cutoff) {
        return false;
      }
    }
    return true;
  });
}

function auditRangeCutoff(range: AuditRangeID) {
  const now = Date.now();
  switch (range) {
    case "24h":
      return now - 24 * 60 * 60 * 1000;
    case "7d":
      return now - 7 * 24 * 60 * 60 * 1000;
    case "30d":
      return now - 30 * 24 * 60 * 60 * 1000;
    case "90d":
      return now - 90 * 24 * 60 * 60 * 1000;
    case "all":
      return null;
  }
}

function imageUsageCounts(containers: ContainerSummary[]) {
  return containers.reduce<Record<string, number>>((counts, container) => {
    if (container.imageID) {
      counts[container.imageID] = (counts[container.imageID] ?? 0) + 1;
    }
    return counts;
  }, {});
}

export function filterProjects(
  projects: ProjectSummary[],
  search: string,
  filter: FilterID,
) {
  const query = search.trim().toLowerCase();
  return projects.filter((project) => {
    if (!matchesProjectSearch(project, query)) {
      return false;
    }
    switch (filter) {
      case "running":
        return project.status === "running";
      case "stopped":
        return project.status === "stopped";
      case "partial":
        return project.status === "partial";
      case "unhealthy":
        return project.health === "unhealthy";
      case "updates":
        return projectActionableUpdateCount(project) > 0;
      case "attention":
        return projectManualUpdateCount(project) > 0;
      case "high-cpu":
        return project.cpuPercent >= 80;
      case "recent":
        return isRecentlyChanged(project.lastChangedAt);
      default:
        return true;
    }
  });
}

function matchesProjectSearch(project: ProjectSummary, query: string) {
  if (query === "") {
    return true;
  }
  return (
    normalizedIncludes(project.name, query) ||
    normalizedIncludes(project.id, query) ||
    normalizedIncludes(project.providerID, query) ||
    normalizedIncludes(project.workingDir, query)
  );
}

function sortProjects(projects: ProjectSummary[], sort: ProjectSortID) {
  return [...projects].sort((left, right) => {
    if (sort === "activity") {
      return dateMillis(right.lastChangedAt) - dateMillis(left.lastChangedAt);
    }
    if (sort === "cpu") {
      return right.cpuPercent - left.cpuPercent;
    }
    return left.name.localeCompare(right.name, undefined, {
      numeric: true,
      sensitivity: "base",
    });
  });
}

function projectSortID(value: string): ProjectSortID {
  return value === "activity" || value === "cpu" ? value : "name";
}

function projectUpdateCount(project: ProjectSummary) {
  const badges = project.updateBadges;
  if (!badges) {
    return 0;
  }
  return (
    badges.imageUpdates +
    badges.baseUpdates +
    badges.rebuildNeeded +
    badges.pinned +
    badges.unknownBase
  );
}

function projectActionableUpdateCount(project: ProjectSummary) {
  const badges = project.updateBadges;
  if (!badges) {
    return 0;
  }
  return badges.imageUpdates + badges.baseUpdates + badges.rebuildNeeded;
}

function projectManualUpdateCount(project: ProjectSummary) {
  const badges = project.updateBadges;
  if (!badges) {
    return 0;
  }
  return badges.pinned + badges.unknownBase;
}

function projectCardUpdateBadges(project: ProjectSummary) {
  const actionable = projectActionableUpdateCount(project);
  const manual = projectManualUpdateCount(project);
  if (actionable === 0 && manual === 0) {
    return <Badge tone="neutral">0 updates</Badge>;
  }
  return (
    <>
      {actionable > 0 ? <Badge tone="warn">{actionable} updates</Badge> : null}
      {manual > 0 ? <Badge tone="warn">{manual} needs attention</Badge> : null}
    </>
  );
}

function summarizeProjectUpdates(projects: ProjectSummary[]) {
  return projects.reduce(
    (summary, project) => {
      const badges = project.updateBadges;
      summary.image += badges?.imageUpdates ?? 0;
      summary.base += badges?.baseUpdates ?? 0;
      summary.rebuild += badges?.rebuildNeeded ?? 0;
      return summary;
    },
    { image: 0, base: 0, rebuild: 0 },
  );
}

function projectUpdateBadges(project: ProjectSummary) {
  const badges = project.updateBadges;
  if (!badges || projectUpdateCount(project) === 0) {
    return [
      <Badge key="up-to-date" tone="ok">
        Up to date
      </Badge>,
    ];
  }
  const out = [];
  if (badges.imageUpdates > 0) {
    out.push(
      <Badge key="image" tone="warn">
        {badges.imageUpdates} image
      </Badge>,
    );
  }
  if (badges.baseUpdates > 0) {
    out.push(
      <Badge key="base" tone="warn">
        {badges.baseUpdates} base
      </Badge>,
    );
  }
  if (badges.rebuildNeeded > 0) {
    out.push(
      <Badge key="rebuild" tone="warn">
        {badges.rebuildNeeded} rebuild
      </Badge>,
    );
  }
  if (badges.pinned > 0) {
    out.push(
      <Badge key="pinned" tone="neutral">
        {badges.pinned} pinned
      </Badge>,
    );
  }
  if (badges.unknownBase > 0) {
    out.push(
      <Badge key="unknown" tone="neutral">
        {badges.unknownBase} unknown
      </Badge>,
    );
  }
  return out;
}

function isStatsSample(value: unknown): value is StatsSample {
  if (!isRecord(value)) {
    return false;
  }
  return (
    typeof value.containerID === "string" &&
    typeof value.cpuPercent === "number" &&
    typeof value.memoryBytes === "number"
  );
}

function sampleLabel(sample: StatsSample) {
  const date = toDate(sample.sampledAt) ?? new Date();
  return date.toLocaleTimeString(undefined, {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

function aggregateChartPoint(
  samples: StatsSample[],
  label: string,
): DashboardChartPoint {
  return {
    ts: Date.now(),
    label,
    cpu: samples.reduce((sum, sample) => sum + sample.cpuPercent, 0),
    gpu: samples.reduce((sum, sample) => sum + (sample.gpuMemoryBytes ?? 0), 0),
    memory: samples.reduce((sum, sample) => sum + sample.memoryBytes, 0),
    netRx: samples.reduce((sum, sample) => sum + sample.networkRxRate, 0),
    netTx: samples.reduce((sum, sample) => sum + sample.networkTxRate, 0),
  };
}

function trimChartPoints(points: DashboardChartPoint[]) {
  return points.length > 300 ? points.slice(points.length - 300) : points;
}

function appendSparkEntries(
  current: Record<string, SparkPoint[]>,
  entries: Array<{ id: string; label: string; value: number }>,
) {
  if (entries.length === 0) {
    return current;
  }
  const next = { ...current };
  for (const entry of entries) {
    if (!entry.id) {
      continue;
    }
    const existing = next[entry.id] ?? [];
    next[entry.id] = existing
      .concat({ label: entry.label, value: entry.value })
      .slice(-60);
  }
  return next;
}

type SampleAggregate = {
  cpuPercent: number;
  gpuDeviceIDs: Set<string>;
  gpuMemoryBytes: number;
  gpuUtilizationPercent: number;
  memoryBytes: number;
  netRxRate: number;
  netTxRate: number;
};

function newSampleAggregate(): SampleAggregate {
  return {
    cpuPercent: 0,
    gpuDeviceIDs: new Set<string>(),
    gpuMemoryBytes: 0,
    gpuUtilizationPercent: 0,
    memoryBytes: 0,
    netRxRate: 0,
    netTxRate: 0,
  };
}

function addSampleToAggregate(aggregate: SampleAggregate, sample: StatsSample) {
  aggregate.cpuPercent += sample.cpuPercent;
  aggregate.gpuMemoryBytes += sample.gpuMemoryBytes ?? 0;
  aggregate.gpuUtilizationPercent += sample.gpuUtilizationPercent ?? 0;
  aggregate.memoryBytes += sample.memoryBytes;
  aggregate.netRxRate += sample.networkRxRate;
  aggregate.netTxRate += sample.networkTxRate;
  for (const deviceID of sample.gpuDeviceIDs ?? []) {
    if (deviceID) {
      aggregate.gpuDeviceIDs.add(deviceID);
    }
  }
}

function aggregateDeviceIDs(aggregate: SampleAggregate) {
  return Array.from(aggregate.gpuDeviceIDs).sort();
}

function mergeStatsSamples(
  current: Record<string, StatsSample>,
  samples: StatsSample[],
) {
  const next = { ...current };
  for (const sample of samples) {
    next[sample.containerID] = sample;
  }
  return next;
}

function applyStatsSamplesToContainers(
  containers: ContainerSummary[],
  samples: StatsSample[],
) {
  const byID = new Map(samples.map((sample) => [sample.containerID, sample]));
  let changed = false;
  const next = containers.map((container) => {
    const sample = byID.get(container.id);
    if (!sample) {
      return container;
    }
    changed = true;
    return {
      ...container,
      cpuPercent: sample.cpuPercent,
      gpuDeviceIDs: sample.gpuDeviceIDs ?? [],
      gpuMemoryBytes: sample.gpuMemoryBytes ?? 0,
      gpuUtilizationPercent: sample.gpuUtilizationPercent ?? 0,
      memoryBytes: sample.memoryBytes,
      memoryLimit: sample.memoryLimitBytes ?? container.memoryLimit,
      netRxRate: sample.networkRxRate,
      netTxRate: sample.networkTxRate,
      restarts: sample.restartCount ?? container.restarts,
    };
  });
  return changed ? next : containers;
}

function projectAggregates(samples: StatsSample[]) {
  const byProject = new Map<string, SampleAggregate>();
  for (const sample of samples) {
    if (!sample.projectID) {
      continue;
    }
    const aggregate = byProject.get(sample.projectID) ?? newSampleAggregate();
    addSampleToAggregate(aggregate, sample);
    byProject.set(sample.projectID, aggregate);
  }
  return byProject;
}

function applyProjectAggregate(
  project: ProjectSummary,
  aggregate: SampleAggregate,
): ProjectSummary {
  return {
    ...project,
    cpuPercent: aggregate.cpuPercent,
    gpuDeviceIDs: aggregateDeviceIDs(aggregate),
    gpuMemoryBytes: aggregate.gpuMemoryBytes,
    gpuUtilizationPercent: aggregate.gpuUtilizationPercent,
    memoryBytes: aggregate.memoryBytes,
    netRxRate: aggregate.netRxRate,
    netTxRate: aggregate.netTxRate,
  };
}

function applyStatsSamplesToProjects(
  projects: ProjectSummary[],
  samples: StatsSample[],
) {
  const byProject = projectAggregates(samples);
  if (byProject.size === 0) {
    return projects;
  }
  let changed = false;
  const next = projects.map((project) => {
    const aggregate = byProject.get(project.id);
    if (!aggregate) {
      return project;
    }
    changed = true;
    return applyProjectAggregate(project, aggregate);
  });
  return changed ? next : projects;
}

function applyStatsSamplesToProjectDetail(
  detail: ProjectDetail,
  samples: StatsSample[],
): ProjectDetail {
  const projectID = detail.summary.id;
  const projectSamples = samples.filter(
    (sample) => sample.projectID === projectID,
  );
  if (projectSamples.length === 0) {
    return detail;
  }

  const projectAggregate = projectAggregates(projectSamples).get(projectID);
  const serviceAggregates = new Map<string, SampleAggregate>();
  for (const sample of projectSamples) {
    const serviceName = sampleServiceName(sample);
    if (!serviceName) {
      continue;
    }
    const aggregate =
      serviceAggregates.get(serviceName) ?? newSampleAggregate();
    addSampleToAggregate(aggregate, sample);
    serviceAggregates.set(serviceName, aggregate);
  }

  return {
    ...detail,
    containers: applyStatsSamplesToContainers(
      detail.containers ?? [],
      projectSamples,
    ),
    services: (detail.services ?? []).map((service) => {
      const aggregate = serviceAggregates.get(service.name);
      if (!aggregate) {
        return service;
      }
      return {
        ...service,
        cpuPercent: aggregate.cpuPercent,
        gpuDeviceIDs: aggregateDeviceIDs(aggregate),
        gpuMemoryBytes: aggregate.gpuMemoryBytes,
        gpuUtilizationPercent: aggregate.gpuUtilizationPercent,
        memoryBytes: aggregate.memoryBytes,
      };
    }),
    summary: projectAggregate
      ? applyProjectAggregate(detail.summary, projectAggregate)
      : detail.summary,
  };
}

function sampleServiceName(sample: StatsSample) {
  if (!sample.serviceID) {
    return "";
  }
  const marker = "::";
  const markerIndex = sample.serviceID.lastIndexOf(marker);
  return markerIndex >= 0
    ? sample.serviceID.slice(markerIndex + marker.length)
    : sample.serviceID;
}

function emptyProjectMetricSparks(): ProjectMetricSparks {
  return { cpu: {}, gpu: {}, memory: {}, network: {} };
}

function appendProjectMetricSparkEntries(
  current: ProjectMetricSparks,
  samples: StatsSample[],
  label: string,
): ProjectMetricSparks {
  return {
    cpu: appendSparkEntries(
      current.cpu,
      projectSparkEntries(samples, label, "cpu"),
    ),
    gpu: appendSparkEntries(
      current.gpu,
      projectSparkEntries(samples, label, "gpu"),
    ),
    memory: appendSparkEntries(
      current.memory,
      projectSparkEntries(samples, label, "memory"),
    ),
    network: appendSparkEntries(
      current.network,
      projectSparkEntries(samples, label, "network"),
    ),
  };
}

function projectSparkEntries(
  samples: StatsSample[],
  label: string,
  metric: DashboardMetricID = "cpu",
) {
  const grouped = new Map<string, number>();
  for (const sample of samples) {
    if (!sample.projectID) {
      continue;
    }
    const value = statsSampleMetricValue(sample, metric);
    grouped.set(sample.projectID, (grouped.get(sample.projectID) ?? 0) + value);
  }
  return Array.from(grouped.entries()).map(([id, value]) => ({
    id,
    label,
    value,
  }));
}

function statsSampleMetricValue(
  sample: StatsSample,
  metric: DashboardMetricID,
) {
  switch (metric) {
    case "gpu":
      return sample.gpuMemoryBytes ?? 0;
    case "memory":
      return sample.memoryBytes;
    case "network":
      return sample.networkRxRate + sample.networkTxRate;
    default:
      return sample.cpuPercent;
  }
}

function formatGPUUsage(memoryBytes?: number, utilizationPercent?: number) {
  const memory = memoryBytes ?? 0;
  const utilization = utilizationPercent ?? 0;
  if (utilization > 0 && memory > 0) {
    return `${utilization.toFixed(0)}% / ${formatBytes(memory)}`;
  }
  if (utilization > 0) {
    return `${utilization.toFixed(0)}%`;
  }
  return formatBytes(memory);
}

function projectActivityScore(project: ProjectSummary, points?: SparkPoint[]) {
  const latest = points?.[points.length - 1]?.value ?? project.cpuPercent;
  return latest + project.memoryBytes / 1024 / 1024 / 1024;
}

function dashboardMetricColor(metric: DashboardMetricID) {
  switch (metric) {
    case "gpu":
      return chartColors.gpu;
    case "memory":
      return chartColors.memory;
    case "network":
      return chartColors.networkRx;
    default:
      return chartColors.cpu;
  }
}

function dashboardMetricLabel(metric: DashboardMetricID) {
  switch (metric) {
    case "gpu":
      return "GPU memory";
    case "memory":
      return "memory";
    case "network":
      return "network";
    default:
      return "CPU";
  }
}

function containerSparkPoints(container: ContainerSummary): SparkPoint[] {
  const baseline = Math.max(0.2, container.cpuPercent ?? 0);
  return sparkBars(container.id).map((height, index) => ({
    label: String(index),
    value: Math.max(0, (height / 100) * baseline),
  }));
}

function formatRate(value: number) {
  return `${formatBytes(value)}/s`;
}

function formatMetricValue(
  metric: DashboardMetricID,
  value: number,
  key?: string,
) {
  if (metric === "memory" || metric === "gpu") {
    return formatBytes(value);
  }
  if (metric === "network" || key === "netRx" || key === "netTx") {
    return formatRate(value);
  }
  return `${value.toFixed(1)}%`;
}

function formatDuration(seconds: number) {
  if (seconds < 60) {
    return `${seconds}s`;
  }
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) {
    return `${minutes}m`;
  }
  const hours = Math.floor(minutes / 60);
  if (hours < 24) {
    return `${hours}h ${minutes % 60}m`;
  }
  return `${Math.floor(hours / 24)}d ${hours % 24}h`;
}

function logLevelClass(level: LogLevelFilter) {
  switch (level) {
    case "error":
      return "text-error";
    case "warn":
      return "text-warn";
    case "info":
      return "text-info";
    default:
      return "text-text-muted";
  }
}

function projectActionBusyKey(action: ProjectAction, projectID: string) {
  return `project:${action}:${projectID}`;
}

function primaryProjectAction(project: ProjectSummary): ProjectAction {
  return project.status === "running" || project.servicesRunning > 0
    ? "stop"
    : "start";
}

function projectActionDisabledReason(
  action: ProjectAction,
  project: ProjectSummary,
  mutationsDisabled: boolean,
  mutationDisabledReason: string,
) {
  if (action === "remove") {
    return "";
  }
  if (mutationsDisabled) {
    return mutationDisabledReason;
  }
  const canUseStaleContainers =
    (action === "stop" || action === "down" || action === "down-volumes") &&
    project.servicesTotal > 0;
  if (canUseStaleContainers) {
    return "";
  }
  if (project.status === "error") {
    return "Re-link folder before running this Compose action";
  }
  if (!project.workingDir) {
    return "No workdir";
  }
  return "";
}

function isRecentlyChanged(value: unknown) {
  const changedAt = dateMillis(value);
  return changedAt > 0 && Date.now() - changedAt < 24 * 60 * 60 * 1000;
}

function projectStatusTone(status: string): BadgeTone {
  switch (status) {
    case "running":
      return "ok";
    case "partial":
      return "warn";
    case "error":
      return "error";
    case "stopped":
      return "neutral";
    default:
      return "info";
  }
}

function dotTone(tone: BadgeTone): StatusToneID {
  return tone === "accent" ? "info" : tone;
}

function sparkBars(seed: string) {
  const source = seed || "project";
  return Array.from({ length: 24 }, (_, index) => {
    const code = source.charCodeAt(index % source.length) || 17;
    return 18 + ((code + index * 13) % 70);
  });
}

function projectNameFromPath(path: string) {
  const normalized = path.trim().replace(/\\/g, "/").replace(/\/+$/, "");
  return (
    normalized
      .split("/")
      .pop()
      ?.toLowerCase()
      .replace(/[^a-z0-9_-]/g, "-") ?? ""
  );
}

function composeFileCandidates(folderPath: string) {
  if (!folderPath.trim()) {
    return [];
  }
  const separator = folderPath.includes("\\") ? "\\" : "/";
  const base = folderPath.replace(/[\\/]+$/, "");
  return [
    "compose.yaml",
    "compose.yml",
    "docker-compose.yml",
    "docker-compose.yaml",
  ].map((name) => `${base}${separator}${name}`);
}

function containerTone(container: ContainerSummary): BadgeTone {
  if (container.health === "unhealthy") {
    return "error";
  }
  switch (container.state) {
    case "running":
      return "ok";
    case "paused":
    case "restarting":
      return "warn";
    case "dead":
      return "error";
    default:
      return "neutral";
  }
}

function healthTone(health: string): BadgeTone {
  switch (health) {
    case "healthy":
      return "ok";
    case "starting":
      return "warn";
    case "unhealthy":
      return "error";
    default:
      return "neutral";
  }
}

function updateTone(status?: string): BadgeTone {
  if (!status || status === "unknown") {
    return "neutral";
  }
  if (status === "up_to_date") {
    return "ok";
  }
  if (
    status === "error" ||
    status === "auth_required" ||
    status === "rate_limited"
  ) {
    return "error";
  }
  return "warn";
}

function isActionableUpdate(update: ImageUpdate) {
  return (
    update.status === UpdateStatus.UpdateStatusServiceImageUpdateAvailable ||
    update.status === UpdateStatus.UpdateStatusBaseImageUpdateAvailable ||
    update.status === UpdateStatus.UpdateStatusRebuildRequired ||
    update.recommendedAction === "pull_recreate" ||
    update.recommendedAction === "rebuild_redeploy"
  );
}

function updateFilterCounts(updates: ImageUpdate[]) {
  return updates.reduce(
    (counts, update) => {
      counts.all++;
      if (update.kind === UpdateKind.UpdateKindServiceImage) {
        counts.image++;
      }
      if (update.status === UpdateStatus.UpdateStatusBaseImageUpdateAvailable) {
        counts.base++;
      }
      if (update.status === UpdateStatus.UpdateStatusRebuildRequired) {
        counts.rebuild++;
      }
      if (update.status === UpdateStatus.UpdateStatusPinnedDigest) {
        counts.pinned++;
      }
      if (update.status === UpdateStatus.UpdateStatusUnknownBaseImage) {
        counts.unknown++;
      }
      if (
        update.status === UpdateStatus.UpdateStatusAuthRequired ||
        update.status === UpdateStatus.UpdateStatusRateLimited ||
        update.status === UpdateStatus.UpdateStatusError
      ) {
        counts.errors++;
      }
      if (update.status === UpdateStatus.UpdateStatusUpToDate) {
        counts.upToDate++;
      }
      return counts;
    },
    {
      all: 0,
      image: 0,
      base: 0,
      rebuild: 0,
      pinned: 0,
      unknown: 0,
      errors: 0,
      upToDate: 0,
    },
  );
}

function filterUpdateRows(
  updates: ImageUpdate[],
  search: string,
  filter: FilterID,
) {
  const query = search.trim().toLowerCase();
  return updates.filter((update) => {
    if (
      query &&
      ![
        update.projectID,
        update.service,
        update.containerID,
        update.currentImage,
        update.baseImage,
        update.localDigest,
        update.remoteDigest,
      ].some((value) => normalizedIncludes(value, query))
    ) {
      return false;
    }
    switch (filter) {
      case "image":
        return update.kind === UpdateKind.UpdateKindServiceImage;
      case "base":
        return (
          update.status === UpdateStatus.UpdateStatusBaseImageUpdateAvailable
        );
      case "rebuild":
        return update.status === UpdateStatus.UpdateStatusRebuildRequired;
      case "pinned":
        return update.status === UpdateStatus.UpdateStatusPinnedDigest;
      case "unknown":
        return update.status === UpdateStatus.UpdateStatusUnknownBaseImage;
      case "errors":
        return (
          update.status === UpdateStatus.UpdateStatusAuthRequired ||
          update.status === UpdateStatus.UpdateStatusRateLimited ||
          update.status === UpdateStatus.UpdateStatusError
        );
      case "up-to-date":
        return update.status === UpdateStatus.UpdateStatusUpToDate;
      default:
        return true;
    }
  });
}

function groupUpdatesByProject(
  updates: ImageUpdate[],
  projects: ProjectSummary[],
) {
  const map = new Map<
    string,
    { projectID: string; projectName: string; rows: ImageUpdate[] }
  >();
  for (const update of updates) {
    const projectID = update.projectID ?? "";
    const key = projectID || "unscoped";
    const current = map.get(key) ?? {
      projectID,
      projectName: projectNameForID(projects, projectID),
      rows: [],
    };
    current.rows.push(update);
    map.set(key, current);
  }
  return Array.from(map.values()).map((group) => ({
    ...group,
    rows: sortUpdateRows(group.rows),
  }));
}

function sortUpdateRows(rows: ImageUpdate[]) {
  const rank = (update: ImageUpdate) => {
    switch (update.status) {
      case UpdateStatus.UpdateStatusRebuildRequired:
        return 0;
      case UpdateStatus.UpdateStatusBaseImageUpdateAvailable:
        return 1;
      case UpdateStatus.UpdateStatusServiceImageUpdateAvailable:
        return 2;
      case UpdateStatus.UpdateStatusAuthRequired:
      case UpdateStatus.UpdateStatusRateLimited:
      case UpdateStatus.UpdateStatusError:
        return 3;
      case UpdateStatus.UpdateStatusPinnedDigest:
      case UpdateStatus.UpdateStatusUnknownBaseImage:
        return 4;
      default:
        return 5;
    }
  };
  return [...rows].sort(
    (left, right) =>
      rank(left) - rank(right) ||
      (left.service ?? "").localeCompare(right.service ?? ""),
  );
}

function projectNameForID(projects: ProjectSummary[], projectID?: string) {
  if (!projectID) {
    return "Unscoped";
  }
  return (
    projects.find((project) => project.id === projectID)?.name ?? projectID
  );
}

function updateStatusLabel(status?: string) {
  switch (status) {
    case UpdateStatus.UpdateStatusServiceImageUpdateAvailable:
      return "Image update available";
    case UpdateStatus.UpdateStatusBaseImageUpdateAvailable:
      return "Base image update available";
    case UpdateStatus.UpdateStatusRebuildRequired:
      return "Rebuild required";
    case UpdateStatus.UpdateStatusPinnedDigest:
      return "Pinned digest";
    case UpdateStatus.UpdateStatusBuiltLocally:
      return "Built locally";
    case UpdateStatus.UpdateStatusUnknownBaseImage:
      return "Unknown base image";
    case UpdateStatus.UpdateStatusLocalOnlyImage:
      return "Local only image";
    case UpdateStatus.UpdateStatusAuthRequired:
      return "Auth required";
    case UpdateStatus.UpdateStatusRateLimited:
      return "Rate limited";
    case UpdateStatus.UpdateStatusUpToDate:
      return "Up to date";
    case UpdateStatus.UpdateStatusIgnored:
      return "Ignored";
    case UpdateStatus.UpdateStatusError:
      return "Error";
    default:
      return "Unknown";
  }
}

function updateStatusNote(update: ImageUpdate) {
  if (
    update.status === UpdateStatus.UpdateStatusServiceImageUpdateAvailable &&
    /:latest($|@)/.test(update.currentImage)
  ) {
    return "Mutable tag warning";
  }
  if (update.status === UpdateStatus.UpdateStatusAuthRequired) {
    return "Registry login required";
  }
  if (update.status === UpdateStatus.UpdateStatusRateLimited) {
    return "Rate limited; retry after the registry cooldown";
  }
  if (update.status === UpdateStatus.UpdateStatusUnknownBaseImage) {
    return "Base image: Unknown - this is a third-party registry image and no base metadata was found.";
  }
  return update.notes?.[0] ?? "";
}

function updateActionLabel(action?: string) {
  switch (action) {
    case "pull_recreate":
      return "Pull & recreate";
    case "rebuild_redeploy":
      return "Rebuild & redeploy";
    case "manual":
      return "Manual attention";
    default:
      return "No action";
  }
}

function updateKindLabel(kind?: string) {
  return kind === UpdateKind.UpdateKindBaseImage
    ? "Base image"
    : "Service image";
}

function updateResultTone(result?: string): BadgeTone {
  switch (result) {
    case "success":
      return "ok";
    case "success_warn":
      return "warn";
    case "rolled_back":
      return "info";
    case "failed":
    case "manual_needed":
      return "error";
    default:
      return "neutral";
  }
}

function updateDuration(item: UpdateHistoryItem) {
  const started = dateMillis(item.startedAt);
  const finished = dateMillis(item.finishedAt);
  if (!started || !finished || finished < started) {
    return "-";
  }
  const seconds = Math.round((finished - started) / 1000);
  return seconds < 60 ? `${seconds}s` : `${Math.round(seconds / 60)}m`;
}

function confidenceTone(confidence?: string): BadgeTone {
  switch (confidence) {
    case "high":
      return "ok";
    case "medium":
      return "info";
    case "low":
      return "warn";
    default:
      return "neutral";
  }
}

function confidenceReason(confidence?: string) {
  switch (confidence) {
    case "high":
      return "from Compose build config and Dockerfile";
    case "medium":
      return "from Compose build config and Dockerfile; build-time digest unknown";
    case "low":
      return "from image metadata labels";
    default:
      return "no reliable base information";
  }
}

function lineageBaseText(lineage: ImageLineage) {
  if (!lineage.baseImage) {
    return "Unknown - this is a third-party registry image and no base metadata was found.";
  }
  return `${lineage.baseImage} - Confidence: ${titleCase(lineage.confidence)} - Reason: ${
    lineage.reason || confidenceReason(lineage.confidence)
  }`;
}

function shortDigest(value?: string) {
  if (!value) {
    return "-";
  }
  const clean = value.replace(/^sha256:/, "");
  return clean.length > 12 ? `sha256:${clean.slice(0, 12)}` : value;
}

function titleCase(value: string) {
  return value
    .split(/[_\s-]+/)
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}

function formatBytes(value?: number) {
  if (!value || value <= 0) {
    return "0 B";
  }
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let next = value;
  let unit = 0;
  while (next >= 1024 && unit < units.length - 1) {
    next /= 1024;
    unit += 1;
  }
  return `${next >= 10 || unit === 0 ? next.toFixed(0) : next.toFixed(1)} ${units[unit]}`;
}

function formatMemory(used?: number, limit?: number) {
  if (!used) {
    return "-";
  }
  return limit
    ? `${formatBytes(used)} / ${formatBytes(limit)}`
    : formatBytes(used);
}

function formatGPUStatus(gpu: GPUMetrics) {
  if (!gpu.available) {
    return "unavailable";
  }
  const utilization = `${(gpu.utilizationPercent ?? 0).toFixed(0)}%`;
  const memoryTotal = gpu.memoryTotalBytes ?? 0;
  const memoryUsed = gpu.memoryUsedBytes ?? 0;
  if (memoryTotal > 0) {
    return `${utilization} / ${formatBytes(memoryUsed)}`;
  }
  return utilization;
}

function formatGPUTitle(gpu: GPUMetrics) {
  if (!gpu.available) {
    return gpu.message || "GPU metrics unavailable";
  }
  const devices = gpu.devices ?? [];
  const deviceCount = gpu.deviceCount || devices.length || 1;
  const header = `${deviceCount} GPU${deviceCount === 1 ? "" : "s"} via ${
    gpu.source || "GPU probe"
  }`;
  const lines = devices.map((device) => {
    const memory =
      (device.memoryTotalBytes ?? 0) > 0
        ? `, ${formatBytes(device.memoryUsedBytes ?? 0)} / ${formatBytes(
            device.memoryTotalBytes,
          )}`
        : "";
    const temperatureCelsius = device.temperatureCelsius ?? 0;
    const temperature =
      temperatureCelsius > 0 ? `, ${temperatureCelsius.toFixed(0)}C` : "";
    return `${device.name || `GPU ${device.index}`}: ${(
      device.utilizationPercent ?? 0
    ).toFixed(0)}%${memory}${temperature}`;
  });
  return [header, ...lines].join("\n");
}

function shortID(value: string) {
  if (!value) {
    return "-";
  }
  const clean = value.replace(/^sha256:/, "");
  return clean.length > 12 ? `${clean.slice(0, 12)}` : clean;
}

function shortPath(value: string) {
  const normalized = value.replace(/\\/g, "/");
  return normalized.split("/").filter(Boolean).slice(-2).join("/") || value;
}

function imageRefs(image: ImageSummary) {
  const tags =
    image.repoTags?.filter((tag) => tag && tag !== "<none>:<none>") ?? [];
  return tags.length > 0 ? tags : (image.repoDigests ?? []);
}

function primaryImageRef(image: ImageSummary) {
  return imageRefs(image)[0] ?? shortID(image.id);
}

function imageRepo(image: ImageSummary) {
  const ref = primaryImageRef(image);
  const slash = ref.includes("/") ? ref.lastIndexOf("/") : -1;
  const colon = ref.lastIndexOf(":");
  if (colon > slash) {
    return ref.slice(0, colon);
  }
  return ref;
}

function imageTag(image: ImageSummary) {
  const ref = primaryImageRef(image);
  const slash = ref.includes("/") ? ref.lastIndexOf("/") : -1;
  const colon = ref.lastIndexOf(":");
  if (colon > slash) {
    return ref.slice(colon + 1);
  }
  return "<none>";
}

function imageDangling(image: ImageSummary) {
  return imageRefs(image).length === 0;
}

function containerRows(container: ContainerSummary): Array<[string, string]> {
  return [
    ["ID", container.id],
    ["Image", container.image],
    ["Image ID", container.imageID ?? "-"],
    ["Status", container.state],
    ["Health", container.health],
    ["Project", container.projectID ?? "-"],
    ["Service", container.service ?? "-"],
    ["CPU", `${(container.cpuPercent ?? 0).toFixed(1)}%`],
    ["Memory", formatMemory(container.memoryBytes, container.memoryLimit)],
    [
      "GPU",
      formatGPUUsage(container.gpuMemoryBytes, container.gpuUtilizationPercent),
    ],
    ["Created", formatDate(container.createdAt)],
    ["Restarts", String(container.restarts ?? 0)],
  ];
}

function imageRows(
  image: ImageSummary,
  usedBy: number,
): Array<[string, string]> {
  return [
    ["Image ID", image.id],
    ["Reference", primaryImageRef(image)],
    ["Size", formatBytes(image.sizeBytes)],
    ["Created", formatDate(image.createdAt)],
    ["Used by", String(usedBy || (image.inUse ? ">=1" : 0))],
    ["Update", image.updateStatus ?? "unknown"],
  ];
}

function imageDetailRows(
  detail: ImageDetail,
  usedBy: number,
): Array<[string, string]> {
  return [
    ...imageRows(detail.summary, usedBy),
    ["Architecture", detail.architecture || "-"],
    ["OS", detail.os || "-"],
    ["Author", detail.author || "-"],
    ["Layers", String(detail.layers?.length ?? 0)],
  ];
}

function volumeRows(
  volume: VolumeSummary,
  detail?: VolumeDetail,
): Array<[string, string]> {
  return [
    ["Name", volume.name],
    ["Driver", volume.driver],
    ["Size", volume.sizeBytes ? formatBytes(volume.sizeBytes) : "-"],
    ["In use", volume.inUse ? "yes" : "no"],
    ["Containers", String(detail?.containers?.length ?? 0)],
    ["Mountpoint", volume.mountpoint ?? "-"],
    ["Project", volume.labels?.[composeProjectLabel] ?? "-"],
  ];
}

function formatJSON(raw: string) {
  try {
    return JSON.stringify(JSON.parse(raw), null, 2);
  } catch {
    return raw;
  }
}

const composeProjectLabel = "com.docker.compose.project";

export default App;
