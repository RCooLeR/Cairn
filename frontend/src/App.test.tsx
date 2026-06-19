import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { InventorySnapshot } from "./api/inventory";
import type {
  BackupSummary,
  ContainerSummary,
  CommandPlan,
  DashboardMetrics,
  DiskUsageCategory,
  GPUMetrics,
  CheatsheetEntry,
  ContainerDetail,
  ContainerFileListing,
  ImageLineage,
  ImageSummary,
  ImageUpdate,
  LogLine,
  NetworkSummary,
  Notification,
  ProjectDetail,
  ProjectSummary,
  ProviderStatus,
  TerminalSessionInfo,
  UpdateHistoryItem,
  UpdatePlan,
  VolumeSummary,
} from "../bindings/github.com/RCooLeR/Cairn/internal/models/models.js";
import {
  Confidence,
  HealthStatus,
  ProjectStatus,
  RecommendedAction,
  UpdateKind,
  Risk,
  UpdateStatus,
} from "../bindings/github.com/RCooLeR/Cairn/internal/models/models.js";

import App, {
  filterContainers,
  filterImages,
  filterNetworks,
  filterProjects,
  filterVolumes,
  imageRefPreview,
  parseMounts,
} from "./App";
import { csvCell } from "./settings/SettingsPage";
import {
  decodeBase64Bytes,
  encodeTerminalInput,
} from "./components/terminal/terminalEncoding";
import { useAppStore } from "./state/appStore";
import { useInventoryStore } from "./state/inventoryStore";
import { resetAgentSessionForTest } from "./agent/AgentPage";

const inventoryMock = vi.hoisted(() => ({
  getInventorySnapshot: vi.fn<() => Promise<InventorySnapshot>>(),
}));

const runtimeMock = vi.hoisted(() => ({
  on: vi.fn<
    (eventName: string, callback: (event?: unknown) => void) => () => void
  >(() => vi.fn()),
  openFile: vi.fn(),
  openURL: vi.fn(),
  question: vi.fn(),
  saveFile: vi.fn(),
  setClipboardText: vi.fn(),
}));

const dockerServiceMock = vi.hoisted(() => ({
  ListContainers: vi.fn(),
  ListImages: vi.fn(),
  ListNetworks: vi.fn(),
  ListVolumes: vi.fn(),
  GetContainer: vi.fn(),
  InspectContainerRaw: vi.fn(),
  ListContainerFiles: vi.fn(),
  GetImage: vi.fn(),
  GetNetwork: vi.fn(),
  GetVolume: vi.fn(),
  StartContainer: vi.fn(),
  StopContainer: vi.fn(),
  RestartContainer: vi.fn(),
  BulkContainerAction: vi.fn(),
  PlanKillContainer: vi.fn(),
  ApplyContainerPlan: vi.fn(),
  RenameContainer: vi.fn(),
  RunImage: vi.fn(),
  PlanRunImage: vi.fn(),
  ApplyRunImagePlan: vi.fn(),
  PullImage: vi.fn(),
  TagImage: vi.fn(),
  PlanPushImage: vi.fn(),
  ApplyPushImagePlan: vi.fn(),
  PushImage: vi.fn(),
  SaveImage: vi.fn(),
  LoadImage: vi.fn(),
  SearchHub: vi.fn(),
  CreateVolume: vi.fn(),
  PlanRemoveImage: vi.fn(),
  PlanPrune: vi.fn(),
  PlanRemoveVolume: vi.fn(),
  CreateNetwork: vi.fn(),
  PlanRemoveNetwork: vi.fn(),
}));

const projectServiceMock = vi.hoisted(() => ({
  RefreshProjects: vi.fn(),
  ListProjects: vi.fn(),
  GetProject: vi.fn(),
  ImportProject: vi.fn(),
  StartProject: vi.fn(),
  StopProject: vi.fn(),
  RestartProject: vi.fn(),
  PullProject: vi.fn(),
  PlanRedeployProject: vi.fn(),
  PlanDownProject: vi.fn(),
  ApplyProjectPlan: vi.fn(),
  RemoveProjectFromList: vi.fn(),
}));

const backupServiceMock = vi.hoisted(() => ({
  PlanBackupVolume: vi.fn(),
  ApplyBackup: vi.fn(),
  PlanRestoreVolume: vi.fn(),
  ApplyRestore: vi.fn(),
  ListBackups: vi.fn(),
  PlanDeleteBackup: vi.fn(),
  ApplyDeleteBackup: vi.fn(),
  DeleteBackup: vi.fn(),
}));

const providerServiceMock = vi.hoisted(() => ({
  Detect: vi.fn(),
  ListDockerContexts: vi.fn(),
  ListWSLDistros: vi.fn(),
  PlanInstall: vi.fn(),
  ApplyInstall: vi.fn(),
  SetDockerContext: vi.fn(),
  SetActiveProvider: vi.fn(),
  PlanRestart: vi.fn(),
  ApplyProviderPlan: vi.fn(),
  Start: vi.fn(),
}));

const logsServiceMock = vi.hoisted(() => ({
  StartLogStream: vi.fn(),
  StopStream: vi.fn(),
  FetchLogPage: vi.fn(),
  ExportLogs: vi.fn(),
}));

const metricsServiceMock = vi.hoisted(() => ({
  GetDashboardMetrics: vi.fn(),
  GetProjectMetrics: vi.fn(),
  GetContainerMetrics: vi.fn(),
  StartStatsStream: vi.fn(),
  StopStream: vi.fn(),
}));

const terminalServiceMock = vi.hoisted(() => ({
  ListTerminalSessions: vi.fn(),
  OpenHostTerminal: vi.fn(),
  OpenBackendTerminal: vi.fn(),
  OpenProjectTerminal: vi.fn(),
  OpenContainerTerminal: vi.fn(),
  DetectContainerShells: vi.fn(),
  WriteTerminal: vi.fn(),
  ResizeTerminal: vi.fn(),
  CloseTerminal: vi.fn(),
}));

const settingsServiceMock = vi.hoisted(() => ({
  GetSettings: vi.fn(),
  SetSetting: vi.fn(),
  GetAuditLog: vi.fn(),
  GetNotifications: vi.fn(),
  MarkNotificationsRead: vi.fn(),
  GetCheatsheet: vi.fn(),
  CheckAppUpdate: vi.fn(),
}));

const agentServiceMock = vi.hoisted(() => ({
  AnalyzeProject: vi.fn(),
  ApplyFileEdit: vi.fn(),
  Chat: vi.fn(),
  DraftProjectFile: vi.fn(),
  ExecuteTool: vi.fn(),
  PlanFileEdit: vi.fn(),
  Status: vi.fn(),
  ToolCatalog: vi.fn(),
}));

const registryServiceMock = vi.hoisted(() => ({
  KnownRegistries: vi.fn(),
  ListRegistryAccounts: vi.fn(),
  Login: vi.fn(),
  Logout: vi.fn(),
  TestAuth: vi.fn(),
}));

const updateServiceMock = vi.hoisted(() => ({
  CheckAllUpdates: vi.fn(),
  CheckProjectUpdates: vi.fn(),
  CheckServiceUpdate: vi.fn(),
  ListCurrentUpdates: vi.fn(),
  PlanServiceUpdate: vi.fn(),
  PlanProjectUpdate: vi.fn(),
  ApplyUpdate: vi.fn(),
  PlanRollback: vi.fn(),
  ApplyRollback: vi.fn(),
  IgnoreUpdate: vi.fn(),
  UnignoreUpdate: vi.fn(),
  ListUpdateHistory: vi.fn(),
  Rollback: vi.fn(),
}));

const imageLineageServiceMock = vi.hoisted(() => ({
  DiscoverProjectLineage: vi.fn(),
  GetContainerLineage: vi.fn(),
  GetProjectLineage: vi.fn(),
  GetServiceLineage: vi.fn(),
  RefreshServiceLineage: vi.fn(),
}));

const appApiMock = vi.hoisted(() => ({
  getAppVersion: vi.fn(),
}));

vi.mock("./api/app", () => ({
  getAppVersion: appApiMock.getAppVersion,
}));

vi.mock("./api/inventory", () => ({
  getInventorySnapshot: inventoryMock.getInventorySnapshot,
}));

vi.mock("./api/services", () => ({
  AgentService: agentServiceMock,
  BackupService: backupServiceMock,
  DockerService: dockerServiceMock,
  LogsService: logsServiceMock,
  MetricsService: metricsServiceMock,
  ProviderService: providerServiceMock,
  ProjectService: projectServiceMock,
  RegistryService: registryServiceMock,
  SettingsService: settingsServiceMock,
  TerminalService: terminalServiceMock,
  UpdateService: updateServiceMock,
  ImageLineageService: imageLineageServiceMock,
}));

vi.mock("@monaco-editor/react", () => ({
  default: ({ value }: { value?: string }) => (
    <pre data-testid="monaco-viewer">{value}</pre>
  ),
}));

vi.mock("@xterm/xterm", () => ({
  Terminal: class {
    open = vi.fn();
    resize = vi.fn();
    write = vi.fn();
    dispose = vi.fn();
    onData = vi.fn(() => ({ dispose: vi.fn() }));
  },
}));

vi.mock("@wailsio/runtime", () => ({
  Create: {
    Any: (source: unknown) => source,
    Array:
      <T,>(element: (source: unknown) => T) =>
      (source: unknown[] | null) =>
        source?.map(element) ?? [],
    Map:
      <T,>(_key: (source: unknown) => string, value: (source: unknown) => T) =>
      (source: Record<string, unknown> | null) =>
        Object.fromEntries(
          Object.entries(source ?? {}).map(([key, entry]) => [
            key,
            value(entry),
          ]),
        ),
    Nullable:
      <T,>(element: (source: unknown) => T) =>
      (source: unknown | null) =>
        source === null ? null : element(source),
  },
  Events: {
    On: runtimeMock.on,
  },
  Dialogs: {
    OpenFile: runtimeMock.openFile,
    Question: runtimeMock.question,
    SaveFile: runtimeMock.saveFile,
  },
  Clipboard: {
    SetText: runtimeMock.setClipboardText,
  },
  Browser: {
    OpenURL: runtimeMock.openURL,
  },
}));

describe("App inventory shell", () => {
  beforeEach(() => {
    vi.useRealTimers();
    vi.clearAllMocks();
    window.localStorage.clear();
    Object.defineProperty(window.navigator, "platform", {
      configurable: true,
      value: "Win32",
    });
    inventoryMock.getInventorySnapshot.mockReset();
    appApiMock.getAppVersion.mockResolvedValue({
      version: "1.0.0",
      goVersion: "go1.26.4",
    });
    dockerServiceMock.ListContainers.mockResolvedValue(
      seededSnapshot().containers,
    );
    dockerServiceMock.ListImages.mockResolvedValue(seededSnapshot().images);
    dockerServiceMock.ListNetworks.mockResolvedValue(seededSnapshot().networks);
    dockerServiceMock.ListVolumes.mockResolvedValue(seededSnapshot().volumes);
    dockerServiceMock.GetContainer.mockResolvedValue(seededContainerDetail());
    dockerServiceMock.InspectContainerRaw.mockResolvedValue(
      '{"Id":"container-1"}',
    );
    dockerServiceMock.ListContainerFiles.mockResolvedValue(
      seededContainerFiles(),
    );
    dockerServiceMock.GetImage.mockResolvedValue(null);
    dockerServiceMock.GetNetwork.mockResolvedValue(null);
    dockerServiceMock.GetVolume.mockResolvedValue(null);
    dockerServiceMock.StartContainer.mockResolvedValue(undefined);
    dockerServiceMock.StopContainer.mockResolvedValue(undefined);
    dockerServiceMock.RestartContainer.mockResolvedValue(undefined);
    dockerServiceMock.BulkContainerAction.mockResolvedValue({
      total: 1,
      succeeded: 1,
      failed: 0,
      items: [],
    });
    dockerServiceMock.PlanKillContainer.mockResolvedValue(killPlan());
    dockerServiceMock.ApplyContainerPlan.mockResolvedValue(undefined);
    dockerServiceMock.RenameContainer.mockResolvedValue(undefined);
    dockerServiceMock.RunImage.mockResolvedValue("container-new");
    dockerServiceMock.PlanRunImage.mockResolvedValue(runImagePlan());
    dockerServiceMock.ApplyRunImagePlan.mockResolvedValue("container-new");
    dockerServiceMock.PullImage.mockResolvedValue("pull-stream");
    dockerServiceMock.TagImage.mockResolvedValue(undefined);
    dockerServiceMock.PlanPushImage.mockResolvedValue(pushImagePlan());
    dockerServiceMock.ApplyPushImagePlan.mockResolvedValue("push-stream");
    dockerServiceMock.PushImage.mockResolvedValue("push-stream");
    dockerServiceMock.SaveImage.mockResolvedValue("save-job");
    dockerServiceMock.LoadImage.mockResolvedValue("load-job");
    dockerServiceMock.SearchHub.mockResolvedValue([]);
    dockerServiceMock.CreateVolume.mockResolvedValue({
      name: "created_volume",
      driver: "local",
      inUse: false,
    });
    dockerServiceMock.PlanRemoveImage.mockResolvedValue(removeImagePlan());
    dockerServiceMock.PlanPrune.mockImplementation((kind: string) =>
      Promise.resolve(prunePlan(kind)),
    );
    dockerServiceMock.PlanRemoveVolume.mockResolvedValue(removeVolumePlan());
    dockerServiceMock.CreateNetwork.mockResolvedValue({
      id: "network-new",
      name: "created_network",
      driver: "bridge",
      internal: false,
      attachable: false,
    });
    dockerServiceMock.PlanRemoveNetwork.mockResolvedValue(removeNetworkPlan());
    projectServiceMock.RefreshProjects.mockResolvedValue([]);
    projectServiceMock.ListProjects.mockResolvedValue([]);
    projectServiceMock.GetProject.mockResolvedValue(null);
    projectServiceMock.ImportProject.mockResolvedValue(seededProjectDetail());
    projectServiceMock.StartProject.mockResolvedValue(undefined);
    projectServiceMock.StopProject.mockResolvedValue(undefined);
    projectServiceMock.RestartProject.mockResolvedValue(undefined);
    projectServiceMock.PullProject.mockResolvedValue(undefined);
    projectServiceMock.PlanRedeployProject.mockResolvedValue(
      projectRedeployPlan(),
    );
    projectServiceMock.PlanDownProject.mockResolvedValue(
      projectDownVolumesPlan(),
    );
    projectServiceMock.ApplyProjectPlan.mockResolvedValue(undefined);
    projectServiceMock.RemoveProjectFromList.mockResolvedValue(undefined);
    runtimeMock.question.mockResolvedValue("Remove");
    backupServiceMock.ListBackups.mockResolvedValue([]);
    backupServiceMock.PlanBackupVolume.mockResolvedValue(backupPlan());
    backupServiceMock.ApplyBackup.mockResolvedValue("backup-job");
    backupServiceMock.PlanRestoreVolume.mockResolvedValue(restorePlan());
    backupServiceMock.ApplyRestore.mockResolvedValue("restore-job");
    backupServiceMock.PlanDeleteBackup.mockResolvedValue(deleteBackupPlan());
    backupServiceMock.ApplyDeleteBackup.mockResolvedValue(undefined);
    backupServiceMock.DeleteBackup.mockResolvedValue(undefined);
    providerServiceMock.Detect.mockResolvedValue(healthyProviderStatus());
    providerServiceMock.ListDockerContexts.mockResolvedValue([
      {
        name: "desktop-linux",
        description: "Docker Desktop",
        current: true,
        dockerHost: "unix:///var/run/docker.sock",
      },
    ]);
    providerServiceMock.ListWSLDistros.mockResolvedValue([
      {
        name: "Ubuntu",
        state: "Running",
        version: 2,
        default: true,
      },
      {
        name: "cairn-dev",
        state: "Stopped",
        version: 2,
        default: false,
      },
    ]);
    providerServiceMock.PlanInstall.mockResolvedValue(wslInstallPlan());
    providerServiceMock.ApplyInstall.mockResolvedValue({
      planID: "plan-wsl-install",
      streamID: "setup-stream",
    });
    providerServiceMock.SetDockerContext.mockResolvedValue(undefined);
    providerServiceMock.SetActiveProvider.mockResolvedValue(undefined);
    providerServiceMock.PlanRestart.mockResolvedValue(providerRestartPlan());
    providerServiceMock.ApplyProviderPlan.mockResolvedValue(undefined);
    providerServiceMock.Start.mockResolvedValue(undefined);
    logsServiceMock.StartLogStream.mockResolvedValue("stream-1");
    logsServiceMock.StopStream.mockResolvedValue(undefined);
    logsServiceMock.FetchLogPage.mockResolvedValue({ lines: [] });
    logsServiceMock.ExportLogs.mockResolvedValue({
      path: "/tmp/cairn-logs.jsonl",
      bytes: 42,
      lineCount: 2,
    });
    metricsServiceMock.GetDashboardMetrics.mockReset();
    metricsServiceMock.GetProjectMetrics.mockReset();
    metricsServiceMock.GetContainerMetrics.mockReset();
    metricsServiceMock.StartStatsStream.mockReset();
    metricsServiceMock.StopStream.mockReset();
    metricsServiceMock.GetDashboardMetrics.mockResolvedValue(
      seededDashboardMetrics(),
    );
    metricsServiceMock.GetProjectMetrics.mockResolvedValue({ series: [] });
    metricsServiceMock.GetContainerMetrics.mockResolvedValue({ series: [] });
    metricsServiceMock.StartStatsStream.mockResolvedValue("stats-stream-1");
    metricsServiceMock.StopStream.mockResolvedValue(undefined);
    terminalServiceMock.ListTerminalSessions.mockResolvedValue([]);
    terminalServiceMock.OpenHostTerminal.mockResolvedValue(
      seededTerminalSession({ id: "host-1", title: "Host", kind: "host" }),
    );
    terminalServiceMock.OpenBackendTerminal.mockResolvedValue(
      seededTerminalSession({
        id: "backend-1",
        title: "Linux native",
        kind: "backend",
      }),
    );
    terminalServiceMock.OpenProjectTerminal.mockResolvedValue(
      seededTerminalSession({
        id: "project-1",
        title: "demo",
        kind: "project",
      }),
    );
    terminalServiceMock.OpenContainerTerminal.mockResolvedValue(
      seededTerminalSession({
        id: "container-1-term",
        title: "web-1",
        kind: "container",
        containerID: "container-1",
      }),
    );
    terminalServiceMock.DetectContainerShells.mockResolvedValue(["/bin/sh"]);
    terminalServiceMock.WriteTerminal.mockResolvedValue(undefined);
    terminalServiceMock.ResizeTerminal.mockResolvedValue(undefined);
    terminalServiceMock.CloseTerminal.mockResolvedValue(undefined);
    settingsServiceMock.GetSettings.mockResolvedValue({
      "general.theme": "dark",
      "general.autostart_app": false,
      "general.language": "en",
      "linux.sudo_mode": "ask",
      "linux.socket_path": "/var/run/docker.sock",
      "provider.autostart_backend": true,
      "provider.active_id": "windows_wsl_ubuntu",
      "windows.wsl_distro": "Ubuntu",
      "macos.colima_profile": "default",
      "macos.colima_cpu": 2,
      "macos.colima_memory_gb": 4,
      "macos.colima_disk_gb": 60,
      "updates.check_interval_hours": 24,
      "updates.notify": true,
      "metrics.retention_raw_minutes": 60,
      "metrics.sample_interval_seconds": 2,
      "terminal.default_shell": "",
      "security.confirm_destructive": true,
      "backups.directory": "",
      "registry.credentials_mode": "docker_helper",
    });
    settingsServiceMock.SetSetting.mockResolvedValue(undefined);
    settingsServiceMock.GetAuditLog.mockResolvedValue([]);
    settingsServiceMock.GetNotifications.mockResolvedValue([]);
    settingsServiceMock.MarkNotificationsRead.mockResolvedValue(undefined);
    settingsServiceMock.GetCheatsheet.mockResolvedValue(seededCheatsheet());
    settingsServiceMock.CheckAppUpdate.mockResolvedValue(null);
    agentServiceMock.Status.mockResolvedValue({
      enabled: true,
      provider: "ollama",
      endpoint: "http://127.0.0.1:11434",
      model: "gemma4:12b-it-q8_0",
      reachable: true,
      availableModels: ["gemma4:12b-it-q8_0", "gemma4:12b", "qwen2.5-coder:7b"],
      candidateModels: [
        "gemma4:12b-it-q8_0",
        "gemma4:12b",
        "qwen2.5-coder:7b",
        "llama3.1:8b",
      ],
    });
    agentServiceMock.Chat.mockResolvedValue({
      message: "Agent response.",
      toolResults: [],
      model: "gemma4:12b-it-q8_0",
    });
    agentServiceMock.AnalyzeProject.mockResolvedValue({
      projectID: "linux_native/app-db",
      projectName: "app-db",
      workingDir: "/home/cairn/projects/app-db",
      stacks: ["Node.js"],
      runtimeHints: ["npm install"],
      configFiles: ["package.json"],
      envVars: [],
      ports: [],
      recommendations: [],
    });
    agentServiceMock.DraftProjectFile.mockResolvedValue({
      projectID: "linux_native/app-db",
      path: ".env",
      content: "APP_PORT=8080\n",
      summary: "Drafted .env",
      model: "gemma4:12b-it-q8_0",
    });
    agentServiceMock.PlanFileEdit.mockResolvedValue(agentFileEditPlan());
    agentServiceMock.ApplyFileEdit.mockResolvedValue({
      projectID: "linux_native/app-db",
      path: ".env",
      bytesWritten: 14,
      appliedAt: "2026-06-13T09:00:00Z",
    });
    agentServiceMock.ToolCatalog.mockResolvedValue([
      {
        id: "updates.check_all",
        name: "Check all updates",
        description: "Run Cairn's update detector for all known projects.",
        readOnly: false,
        requiresApproval: true,
        argumentSchema: "{}",
      },
      {
        id: "docker.containers",
        name: "Containers",
        description: "All containers and status.",
        readOnly: true,
        requiresApproval: false,
        argumentSchema: "{}",
      },
    ]);
    agentServiceMock.ExecuteTool.mockResolvedValue({
      toolID: "updates.check_all",
      title: "Check all updates",
      summary: "Update check started",
      data: '{\n  "jobID": "updates-check-job"\n}',
    });
    registryServiceMock.KnownRegistries.mockResolvedValue([
      { name: "Docker Hub", registry: "docker.io" },
    ]);
    registryServiceMock.ListRegistryAccounts.mockResolvedValue([]);
    registryServiceMock.Login.mockResolvedValue(undefined);
    registryServiceMock.Logout.mockResolvedValue(undefined);
    registryServiceMock.TestAuth.mockResolvedValue({
      registry: "docker.io",
      loggedIn: true,
      username: "ada",
    });
    updateServiceMock.CheckAllUpdates.mockResolvedValue("updates-check-job");
    updateServiceMock.CheckProjectUpdates.mockResolvedValue([]);
    updateServiceMock.CheckServiceUpdate.mockResolvedValue(null);
    updateServiceMock.ListCurrentUpdates.mockResolvedValue([]);
    updateServiceMock.PlanServiceUpdate.mockResolvedValue(updatePlan());
    updateServiceMock.PlanProjectUpdate.mockResolvedValue(updateProjectPlan());
    updateServiceMock.ApplyUpdate.mockResolvedValue("updates-apply-job");
    updateServiceMock.PlanRollback.mockResolvedValue(rollbackPlan());
    updateServiceMock.ApplyRollback.mockResolvedValue("updates-rollback-job");
    updateServiceMock.IgnoreUpdate.mockResolvedValue(undefined);
    updateServiceMock.UnignoreUpdate.mockResolvedValue(undefined);
    updateServiceMock.ListUpdateHistory.mockResolvedValue([]);
    updateServiceMock.Rollback.mockResolvedValue("updates-rollback-job");
    imageLineageServiceMock.DiscoverProjectLineage.mockResolvedValue([]);
    imageLineageServiceMock.GetContainerLineage.mockResolvedValue(null);
    imageLineageServiceMock.GetProjectLineage.mockResolvedValue([]);
    imageLineageServiceMock.GetServiceLineage.mockResolvedValue(null);
    imageLineageServiceMock.RefreshServiceLineage.mockResolvedValue(null);
    runtimeMock.openFile.mockResolvedValue("");
    runtimeMock.saveFile.mockResolvedValue("/tmp/cairn-logs.jsonl");
    runtimeMock.setClipboardText.mockResolvedValue(undefined);
    useAppStore.setState({
      version: null,
      versionLoading: false,
      versionError: null,
    });
    useInventoryStore.setState({
      status: "idle",
      error: null,
      lastLoadedAt: null,
      providers: [],
      dockerInfo: null,
      dockerVersion: null,
      diskUsage: null,
      containers: [],
      images: [],
      volumes: [],
      networks: [],
      volumeDetails: {},
      networkDetails: {},
    });
    resetAgentSessionForTest();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    delete document.documentElement.dataset.theme;
    document.documentElement.style.colorScheme = "";
  });

  it("deduplicates inventory refreshes and re-enters loading on retry", async () => {
    let resolveSnapshot!: (snapshot: InventorySnapshot) => void;
    inventoryMock.getInventorySnapshot.mockReturnValueOnce(
      new Promise<InventorySnapshot>((resolve) => {
        resolveSnapshot = resolve;
      }),
    );

    const first = useInventoryStore.getState().refresh();
    const second = useInventoryStore.getState().refresh();

    expect(inventoryMock.getInventorySnapshot).toHaveBeenCalledTimes(1);
    expect(useInventoryStore.getState().status).toBe("loading");

    resolveSnapshot(seededSnapshot());
    await Promise.all([first, second]);

    expect(useInventoryStore.getState().status).toBe("ready");
    expect(useInventoryStore.getState().containers).toHaveLength(1);

    inventoryMock.getInventorySnapshot.mockRejectedValueOnce(new Error("boom"));
    await useInventoryStore.getState().refresh();
    expect(useInventoryStore.getState().status).toBe("error");

    inventoryMock.getInventorySnapshot.mockResolvedValueOnce(seededSnapshot());
    const retry = useInventoryStore.getState().refresh();
    expect(useInventoryStore.getState().status).toBe("loading");
    await retry;
    expect(useInventoryStore.getState().status).toBe("ready");
  });

  it("renders seeded Docker inventory and subscribes to object refresh events", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());

    render(<App />);

    expect(screen.getByRole("img", { name: "Cairn" })).toBeInTheDocument();
    expect(
      screen.getByRole("navigation", { name: "Main navigation" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "Overview" }),
    ).toBeInTheDocument();
    expect(await screen.findByText("v1.0.0")).toBeInTheDocument();
    expect(
      await screen.findByText("Docker Engine - Running"),
    ).toBeInTheDocument();
    expect(screen.getAllByText("cairn-dev").length).toBeGreaterThan(0);
    await waitFor(() =>
      expect(metricsServiceMock.GetDashboardMetrics).toHaveBeenCalled(),
    );
    expect(metricsServiceMock.StartStatsStream).toHaveBeenCalledWith({
      kind: "all",
      ids: [],
    });
    expect(runtimeMock.on).toHaveBeenCalledWith(
      "objects:changed",
      expect.any(Function),
    );
    const searchInput = screen.getByLabelText("Search inventory");
    fireEvent.keyDown(window, { key: "/" });
    expect(searchInput).toHaveFocus();
  });

  it("renders agent markdown, plan items, and Enter chat shortcuts", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());
    agentServiceMock.Chat.mockResolvedValueOnce({
      message: [
        "Plan",
        "[x] Inspect app files",
        "[-] Draft Compose changes",
        "[ ] Verify with tests",
        "",
        "## Answer",
        "Use `docker compose up` after checking [Docker docs](https://docs.docker.com).",
        "",
        "| Feature | Dedicated Vector DB | PostgreSQL + pgvector |",
        "| :--- | :--- | :--- |",
        "| Complexity | More infrastructure | One database |",
        "| Performance | Better at huge scale | Good for small/medium |",
        "",
        "```yaml",
        "services:",
        "  app:",
        "    image: nginx",
        "```",
      ].join("\n"),
      toolResults: [
        {
          toolID: "project.files",
          title: "Project files",
          summary: "compose.yaml",
        },
      ],
      model: "gemma4:12b-it-q8_0",
    });

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", { name: /Agent/ }),
    );

    await screen.findByText("Local Agent");
    const input = screen.getByPlaceholderText("Ask a Docker question...");
    expect(screen.getByTestId("agent-transcript")).toHaveClass("overflow-auto");

    fireEvent.change(input, {
      target: { value: "Review this Compose project and make a plan" },
    });
    fireEvent.keyDown(input, { key: "Enter", shiftKey: true });
    expect(agentServiceMock.Chat).not.toHaveBeenCalled();

    fireEvent.keyDown(input, { key: "Enter" });
    await waitFor(() => expect(agentServiceMock.Chat).toHaveBeenCalledTimes(1));
    expect(agentServiceMock.Chat.mock.calls[0][0].prompt).toContain(
      'Markdown section named "Plan"',
    );

    const transcript = screen.getByTestId("agent-transcript");
    const plan = await screen.findByTestId("agent-plan-content");
    expect(within(plan).getByText("Inspect app files")).toBeInTheDocument();
    expect(within(plan).getByText("Draft Compose changes")).toBeInTheDocument();
    expect(within(plan).getByText("Verify with tests")).toBeInTheDocument();
    expect(within(plan).getByLabelText("Done")).toBeInTheDocument();
    expect(within(plan).getByLabelText("In progress")).toBeInTheDocument();
    expect(within(plan).getByLabelText("Todo")).toBeInTheDocument();
    expect(within(transcript).queryByText("Plan")).not.toBeInTheDocument();
    expect(
      within(transcript).queryByText("Inspect app files"),
    ).not.toBeInTheDocument();
    expect(within(transcript).getByText("Answer")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Docker docs" })).toHaveAttribute(
      "href",
      "https://docs.docker.com",
    );
    const markdownTable = screen.getByTestId("agent-markdown-table");
    expect(within(markdownTable).getByText("Feature")).toBeInTheDocument();
    expect(
      within(markdownTable).getByText("Dedicated Vector DB"),
    ).toBeInTheDocument();
    expect(
      within(markdownTable).getByText("PostgreSQL + pgvector"),
    ).toBeInTheDocument();
    expect(
      within(markdownTable).getByText("More infrastructure"),
    ).toBeInTheDocument();
    expect(screen.getByText(/services:\s+app:/)).toBeInTheDocument();
    expect(
      screen.getByText(
        "Understanding request: Review this Compose project and make a plan",
      ),
    ).toBeInTheDocument();
    expect(screen.getByText("Created plan with 3 tasks")).toBeInTheDocument();
    expect(screen.getByText("Used tool: Project files")).toBeInTheDocument();
    expect(
      screen.getByText("Provided final answer with gemma4:12b-it-q8_0"),
    ).toBeInTheDocument();
  });

  it("keeps the agent conversation when navigating away and back", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());
    agentServiceMock.Chat.mockResolvedValueOnce({
      message: "Persistent agent answer.",
      toolResults: [],
      model: "gemma4:12b-it-q8_0",
    });

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    const nav = screen.getByRole("navigation", { name: "Main navigation" });
    fireEvent.click(within(nav).getByRole("button", { name: /Agent/ }));

    const input = await screen.findByPlaceholderText(
      "Ask a Docker question...",
    );
    fireEvent.change(input, {
      target: { value: "Remember this conversation" },
    });
    fireEvent.keyDown(input, { key: "Enter" });

    await screen.findByText("Persistent agent answer.");
    fireEvent.click(within(nav).getByRole("button", { name: /Projects/ }));
    await screen.findByRole("heading", { name: "Projects" });
    fireEvent.click(within(nav).getByRole("button", { name: /Agent/ }));

    const transcript = await screen.findByTestId("agent-transcript");
    expect(
      within(transcript).getByText("Remember this conversation"),
    ).toBeInTheDocument();
    expect(
      within(transcript).getByText("Persistent agent answer."),
    ).toBeInTheDocument();
    expect(agentServiceMock.Chat).toHaveBeenCalledTimes(1);
  });

  it("does not convert ordinary agent bullets into plan items", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());
    agentServiceMock.Chat.mockResolvedValueOnce({
      message: [
        "A RAG stack usually has these layers:",
        "",
        "- Vector database such as Qdrant",
        "- Embedding model",
        "- LLM runtime",
        "- Orchestration app",
      ].join("\n"),
      toolResults: [],
      model: "gemma4:12b-it-q8_0",
    });

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", { name: /Agent/ }),
    );

    const input = await screen.findByPlaceholderText(
      "Ask a Docker question...",
    );
    fireEvent.change(input, {
      target: { value: "What belongs in a RAG stack?" },
    });
    fireEvent.keyDown(input, { key: "Enter" });

    await screen.findByText("Vector database such as Qdrant");
    const plan = await screen.findByTestId("agent-plan-content");
    expect(
      within(plan).getByText("No plan in the latest answer."),
    ).toBeInTheDocument();
    expect(
      within(plan).queryByText("Vector database such as Qdrant"),
    ).not.toBeInTheDocument();
  });

  it("asks approval for model-requested Cairn tools and continues with the result", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());
    projectServiceMock.RefreshProjects.mockResolvedValue([seededProject()]);
    agentServiceMock.Chat.mockResolvedValueOnce({
      message: [
        "I need to check updates first.",
        "",
        "```cairn-tool",
        '{"toolID":"updates.check_all","reason":"Find available image updates before planning changes.","arguments":{}}',
        "```",
      ].join("\n"),
      toolResults: [],
      model: "gemma4:12b-it-q8_0",
    });
    agentServiceMock.Chat.mockResolvedValueOnce({
      message:
        "The update check has started. Review update results when it finishes.",
      toolResults: [],
      model: "gemma4:12b-it-q8_0",
    });

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", { name: /Agent/ }),
    );

    const input = await screen.findByPlaceholderText(
      "Ask a Docker question...",
    );
    fireEvent.change(input, {
      target: {
        value: "Can you upgrade all images if any upgrades available?",
      },
    });
    fireEvent.keyDown(input, { key: "Enter" });

    const dialog = await screen.findByRole("dialog", {
      name: "Allow Agent Tool?",
    });
    expect(within(dialog).getByText("Check all updates")).toBeInTheDocument();
    expect(
      within(dialog).getByText(
        "Find available image updates before planning changes.",
      ),
    ).toBeInTheDocument();

    fireEvent.click(within(dialog).getByRole("button", { name: "Allow" }));

    await waitFor(() =>
      expect(agentServiceMock.ExecuteTool).toHaveBeenCalledWith({
        arguments: "{}",
        reason: "Find available image updates before planning changes.",
        scope: { projectID: undefined },
        toolID: "updates.check_all",
      }),
    );
    expect(
      await screen.findByText(
        "The update check has started. Review update results when it finishes.",
      ),
    ).toBeInTheDocument();
    expect(agentServiceMock.Chat).toHaveBeenCalledTimes(2);
    expect(agentServiceMock.Chat.mock.calls[1][0].prompt).toContain(
      "Cairn tool result: Check all updates [updates.check_all]",
    );
  });

  it("lets users decline a requested Cairn tool", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());
    agentServiceMock.Chat.mockResolvedValueOnce({
      message: [
        "I can inspect containers.",
        "",
        "```cairn-tool",
        '{"toolID":"docker.containers","reason":"List containers before diagnosing runtime state.","arguments":{"all":true}}',
        "```",
      ].join("\n"),
      toolResults: [],
      model: "gemma4:12b-it-q8_0",
    });

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", { name: /Agent/ }),
    );

    const input = await screen.findByPlaceholderText(
      "Ask a Docker question...",
    );
    fireEvent.change(input, {
      target: { value: "Clean up dangling images" },
    });
    fireEvent.keyDown(input, { key: "Enter" });

    const dialog = await screen.findByRole("dialog", {
      name: "Allow Agent Tool?",
    });
    fireEvent.click(within(dialog).getByRole("button", { name: "Decline" }));

    expect(
      await screen.findByText("Tool request declined: docker.containers."),
    ).toBeInTheDocument();
    expect(agentServiceMock.ExecuteTool).not.toHaveBeenCalled();
  });

  it("opens the notification center and marks notifications read", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());
    settingsServiceMock.GetNotifications.mockResolvedValue(
      seededNotifications(),
    );

    render(<App />);

    const bell = await screen.findByRole("button", {
      name: "Notifications 1 unread",
    });
    fireEvent.click(bell);

    const dialog = await screen.findByRole("dialog", {
      name: "Notification center",
    });
    expect(within(dialog).getByText("Provider degraded")).toBeInTheDocument();
    fireEvent.click(within(dialog).getByText("Provider degraded"));
    expect(
      await screen.findByRole("heading", { name: "Overview" }),
    ).toBeInTheDocument();

    fireEvent.click(
      await screen.findByRole("button", { name: "Notifications 1 unread" }),
    );
    const updateDialog = screen.getByRole("dialog", {
      name: "Notification center",
    });
    fireEvent.click(
      within(updateDialog).getByRole("button", {
        name: /Update check complete/,
      }),
    );
    expect(
      await screen.findByRole("heading", { name: "Updates" }),
    ).toBeInTheDocument();

    fireEvent.click(
      await screen.findByRole("button", { name: "Notifications 1 unread" }),
    );
    fireEvent.click(
      await screen.findByRole("button", { name: "Mark all read" }),
    );
    await waitFor(() =>
      expect(settingsServiceMock.MarkNotificationsRead).toHaveBeenCalledWith(
        [],
      ),
    );
  });

  it("shows an in-app app-update notice from the backend app update check", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());
    settingsServiceMock.CheckAppUpdate.mockResolvedValueOnce({
      version: "9.9.9",
      name: "Cairn v9.9.9",
      url: "https://github.com/RCooLeR/Cairn/releases/tag/v9.9.9",
      publishedAt: "2026-06-13T10:00:00Z",
    });

    render(<App />);

    await waitFor(() =>
      expect(settingsServiceMock.CheckAppUpdate).toHaveBeenCalledWith("1.0.0"),
    );
    expect(
      await screen.findByText("Cairn 9.9.9 is available"),
    ).toBeInTheDocument();
    expect(screen.getByText("Cairn v9.9.9")).toBeInTheDocument();

    fireEvent.click(
      await screen.findByRole("button", { name: "Notifications 1 unread" }),
    );
    const dialog = await screen.findByRole("dialog", {
      name: "Notification center",
    });
    fireEvent.click(
      within(dialog).getByRole("button", {
        name: /Cairn 9.9.9 is available/,
      }),
    );
    expect(
      await screen.findByRole("heading", { name: "Settings" }),
    ).toBeInTheDocument();
  });

  it("updates dashboard charts from stats samples and deep-links container filters", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());
    projectServiceMock.RefreshProjects.mockResolvedValue([seededProject()]);

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    expect(await screen.findByText("18% / 2.0 GiB")).toBeInTheDocument();
    await waitFor(() =>
      expect(metricsServiceMock.StartStatsStream).toHaveBeenCalled(),
    );

    emitRuntimeEvent("stats:sample", {
      streamID: "stats-stream-1",
      gpu: {
        ...seededGPUMetrics(),
        utilizationPercent: 27,
        memoryUsedBytes: 3 * 1024 * 1024 * 1024,
      },
      samples: [
        statsSample({
          cpuPercent: 31.2,
          memoryBytes: 96 * 1024 * 1024,
          networkRxRate: 4096,
          networkTxRate: 2048,
        }),
      ],
    });

    expect(await screen.findByText("31.2% CPU")).toBeInTheDocument();
    expect(await screen.findByText("27% / 3.0 GiB")).toBeInTheDocument();
    expect(screen.getByText("Resource Usage")).toBeInTheDocument();
    expect(screen.queryByText("Top Containers")).toBeNull();
    expect(screen.queryByText("Recent Events")).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: /Running1/ }));

    expect(
      await screen.findByRole("heading", { name: "Containers" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Running1/ })).toHaveClass(
      "border-accent/40",
    );
  });

  it("meets Phase 4 seed-scale dashboard and filter budgets", async () => {
    const projects = seedScaleProjects();
    const snapshot = seedScaleSnapshot(projects);
    inventoryMock.getInventorySnapshot.mockResolvedValue(snapshot);
    projectServiceMock.RefreshProjects.mockResolvedValue(projects);
    projectServiceMock.ListProjects.mockResolvedValue(projects);
    metricsServiceMock.GetDashboardMetrics.mockResolvedValue(
      seedScaleDashboardMetrics(snapshot, projects),
    );

    const renderStart = performance.now();
    render(<App />);

    expect(
      await screen.findByLabelText("Docker object counts"),
    ).toBeInTheDocument();
    expect(performance.now() - renderStart).toBeLessThan(1500);

    const counts = seedScaleImageUseCounts(snapshot.containers);
    const filterStart = performance.now();
    expect(
      filterContainers(snapshot.containers, "service-42", "all"),
    ).toHaveLength(1);
    expect(filterContainers(snapshot.containers, "", "running")).toHaveLength(
      50,
    );
    expect(
      filterImages(snapshot.images, counts, "repo-499", "all"),
    ).toHaveLength(1);
    expect(
      filterImages(snapshot.images, counts, "", "unused").length,
    ).toBeGreaterThan(300);
    expect(filterVolumes(snapshot.volumes, "volume-199", "all")).toHaveLength(
      1,
    );
    expect(filterNetworks(snapshot.networks, "network-19")).toHaveLength(1);
    expect(filterProjects(projects, "project-9", "all")).toHaveLength(1);
    expect(performance.now() - filterStart).toBeLessThan(100);
  });

  it("hardens audit CSV and run-image parsing helpers", () => {
    expect(csvCell("=cmd|' /c calc'!A1")).toBe(`"'=cmd|' /c calc'!A1"`);
    expect(csvCell("plain")).toBe('"plain"');
    expect(csvCell('say "hello"')).toBe('"say ""hello"""');

    expect(imageRefPreview("redis:7")).toEqual({
      registry: "docker.io",
      repository: "redis",
      tag: "7",
      error: undefined,
    });
    expect(imageRefPreview("localhost:5000/team/app:dev")).toEqual({
      registry: "localhost:5000",
      repository: "team/app",
      tag: "dev",
      error: undefined,
    });

    expect(parseMounts("myvol:/data")).toEqual([
      {
        type: "volume",
        source: "myvol",
        target: "/data",
        volumeName: "myvol",
        readOnly: false,
      },
    ]);
    expect(parseMounts("C:\\Users\\Ada\\data:/data:ro")).toEqual([
      {
        type: "bind",
        source: "C:\\Users\\Ada\\data",
        target: "/data",
        volumeName: "",
        readOnly: true,
      },
    ]);

    const bytes = decodeBase64Bytes(encodeTerminalInput("caf\u00e9 \u26a1"));
    expect(bytes).toBeInstanceOf(Uint8Array);
    expect(new TextDecoder().decode(bytes)).toBe("caf\u00e9 \u26a1");
  });

  it("opens terminal sessions from the Terminal page", async () => {
    const snapshot = seededSnapshot();
    const projectContainer = {
      ...snapshot.containers[0],
      id: "app-running",
      name: "app-db-app-1",
      projectID: "linux_native/app-db",
      service: "app",
      state: "running",
      status: "Up 2 minutes",
    };
    snapshot.containers = [
      projectContainer,
      {
        ...projectContainer,
        id: "app-stopped",
        name: "app-db-stopped-1",
        state: "exited",
        status: "Exited 1 minute ago",
      },
      {
        ...projectContainer,
        id: "other-running",
        name: "other-api-1",
        projectID: "linux_native/other",
      },
    ];
    inventoryMock.getInventorySnapshot.mockResolvedValue(snapshot);
    projectServiceMock.ListProjects.mockResolvedValue([
      seededProject(),
      { ...seededProject(), id: "linux_native/other", name: "other" },
    ]);
    projectServiceMock.RefreshProjects.mockResolvedValue([seededProject()]);

    render(<App />);

    const nav = await screen.findByRole("navigation", {
      name: "Main navigation",
    });
    fireEvent.click(within(nav).getByRole("button", { name: /Terminal/i }));
    expect(
      await screen.findByRole("heading", { level: 1, name: "Terminal" }),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Host" }));

    await waitFor(() =>
      expect(terminalServiceMock.OpenHostTerminal).toHaveBeenCalledWith({
        cols: 120,
        rows: 30,
      }),
    );
    await waitFor(() =>
      expect(screen.getAllByText("Host").length).toBeGreaterThan(1),
    );

    const projectSelect = screen.getByLabelText(
      "Project terminal",
    ) as HTMLSelectElement;
    fireEvent.change(projectSelect, {
      target: { value: "linux_native/app-db" },
    });
    const containerSelect = screen.getByLabelText(
      "Container terminal",
    ) as HTMLSelectElement;
    expect(
      within(containerSelect).getByRole("option", { name: "app-db-app-1" }),
    ).toBeInTheDocument();
    expect(
      within(containerSelect).queryByRole("option", {
        name: "app-db-stopped-1",
      }),
    ).not.toBeInTheDocument();
    expect(
      within(containerSelect).queryByRole("option", { name: "other-api-1" }),
    ).not.toBeInTheDocument();

    fireEvent.change(containerSelect, { target: { value: "app-running" } });
    await waitFor(() =>
      expect(terminalServiceMock.DetectContainerShells).toHaveBeenCalledWith(
        "app-running",
      ),
    );
    await waitFor(() =>
      expect(
        (screen.getByLabelText("Container shell path") as HTMLSelectElement)
          .value,
      ).toBe("/bin/sh"),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Open container terminal" }),
    );
    await waitFor(() =>
      expect(terminalServiceMock.OpenContainerTerminal).toHaveBeenCalledWith(
        "app-running",
        {
          shell: "/bin/sh",
          user: "",
          workingDir: "",
          cols: 120,
          rows: 30,
        },
      ),
    );
  });

  it("command palette navigates and schedules safe terminal commands only", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.keyDown(window, { key: "k", ctrlKey: true });
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    fireEvent.click(await screen.findByText("docker ps"));

    await waitFor(() =>
      expect(terminalServiceMock.OpenBackendTerminal).toHaveBeenCalled(),
    );
    await waitFor(
      () =>
        expect(terminalServiceMock.WriteTerminal).toHaveBeenCalledWith(
          "backend-1",
          "ZG9ja2VyIHBzDQ==",
        ),
      { timeout: 1800 },
    );
  });

  it("command palette copies dangerous terminal commands without running them", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.keyDown(window, { key: "k", ctrlKey: true });
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    fireEvent.click(await screen.findByText("docker system prune"));

    await waitFor(() =>
      expect(runtimeMock.setClipboardText).toHaveBeenCalledWith(
        "docker system prune",
      ),
    );
    expect(terminalServiceMock.OpenBackendTerminal).not.toHaveBeenCalled();
    expect(terminalServiceMock.WriteTerminal).not.toHaveBeenCalled();
  });

  it("requires typed confirmation when cleanup includes volumes", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(screen.getByRole("button", { name: "Prune" }));

    const dialog = await screen.findByRole("dialog", {
      name: "Clean Up Docker Space",
    });
    fireEvent.click(within(dialog).getByLabelText("Unused volumes"));

    expect(
      within(dialog).getByRole("button", { name: "Clean up" }),
    ).toBeDisabled();
    fireEvent.change(within(dialog).getByLabelText("Type prune to confirm"), {
      target: { value: "prune" },
    });

    const cleanUpButton = within(dialog).getByRole("button", {
      name: "Clean up",
    });
    expect(cleanUpButton).toBeEnabled();
    fireEvent.click(cleanUpButton);

    await waitFor(() =>
      expect(dockerServiceMock.PlanPrune).toHaveBeenCalledWith("volumes"),
    );
    expect(dockerServiceMock.ApplyContainerPlan).toHaveBeenCalledWith(
      "plan-prune-volumes",
      "prune",
    );
  });

  it("refreshes overview data when cleanup fails after a partial prune", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());
    projectServiceMock.RefreshProjects.mockResolvedValue([seededProject()]);
    dockerServiceMock.ApplyContainerPlan.mockImplementation((planID: string) =>
      planID === "plan-prune-containers"
        ? Promise.reject(new Error("containers failed"))
        : Promise.resolve(undefined),
    );

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    const inventoryCallsBefore =
      inventoryMock.getInventorySnapshot.mock.calls.length;
    const projectRefreshesBefore =
      projectServiceMock.RefreshProjects.mock.calls.length;
    const dashboardCallsBefore =
      metricsServiceMock.GetDashboardMetrics.mock.calls.length;

    fireEvent.click(screen.getByRole("button", { name: "Prune" }));
    const dialog = await screen.findByRole("dialog", {
      name: "Clean Up Docker Space",
    });
    fireEvent.change(within(dialog).getByLabelText("Type prune to confirm"), {
      target: { value: "prune" },
    });
    fireEvent.click(within(dialog).getByRole("button", { name: "Clean up" }));

    expect(
      await within(dialog).findAllByText("containers failed"),
    ).toHaveLength(2);
    await waitFor(() =>
      expect(
        inventoryMock.getInventorySnapshot.mock.calls.length,
      ).toBeGreaterThan(inventoryCallsBefore),
    );
    expect(
      projectServiceMock.RefreshProjects.mock.calls.length,
    ).toBeGreaterThan(projectRefreshesBefore);
    expect(
      metricsServiceMock.GetDashboardMetrics.mock.calls.length,
    ).toBeGreaterThan(dashboardCallsBefore);
  });

  it("lists containers and applies search without leaving the table view", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", {
        name: /Containers/,
      }),
    );

    expect(
      screen.getByRole("heading", { name: "Containers" }),
    ).toBeInTheDocument();
    expect(screen.getAllByText("web").length).toBeGreaterThan(0);
    expect(screen.getByText("cairn/web:latest")).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("Search inventory"), {
      target: { value: "does-not-exist" },
    });

    expect(screen.getByText("No containers match")).toBeInTheDocument();
  });

  it("opens a full container detail screen from the containers table", async () => {
    const snapshot = seededSnapshot();
    const container = snapshot.containers[0];
    inventoryMock.getInventorySnapshot.mockResolvedValue(snapshot);
    dockerServiceMock.GetContainer.mockResolvedValue({
      ...seededContainerDetail(),
      summary: container,
      workingDir: "/usr/share/nginx/html",
    });

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", {
        name: /Containers/,
      }),
    );

    const table = screen.getByRole("table", { name: "Containers inventory" });
    fireEvent.click(
      within(table).getByRole("button", {
        name: `Open ${container.name} container details`,
      }),
    );

    await waitFor(() =>
      expect(dockerServiceMock.GetContainer).toHaveBeenCalledWith(
        "container-1",
      ),
    );
    expect(
      screen.getByRole("heading", { name: container.name }),
    ).toBeInTheDocument();
    expect(screen.getByText("Working dir")).toBeInTheDocument();
    expect(screen.getByText("/usr/share/nginx/html")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Back" }));
    expect(
      screen.getByRole("table", { name: "Containers inventory" }),
    ).toBeInTheDocument();
  });

  it("renders Compose projects and applies project filters", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());
    projectServiceMock.RefreshProjects.mockResolvedValue([seededProject()]);

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", {
        name: /Projects/,
      }),
    );

    expect(
      await screen.findByRole("heading", { name: "Projects" }),
    ).toBeInTheDocument();
    expect(await screen.findByText("app-db")).toBeInTheDocument();
    expect(screen.getByText("1/2")).toBeInTheDocument();
    expect(screen.getByText("2 updates")).toBeInTheDocument();
    expect(screen.getByText("8080->80/tcp")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /Stopped/ }));

    expect(screen.getByText("No projects found")).toBeInTheDocument();
  });

  it("runs safe project lifecycle actions from project cards", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());
    projectServiceMock.RefreshProjects.mockResolvedValue([seededProject()]);

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", {
        name: /Projects/,
      }),
    );
    await screen.findByText("app-db");
    fireEvent.click(screen.getByRole("button", { name: "Stop app-db" }));

    await waitFor(() =>
      expect(projectServiceMock.StopProject).toHaveBeenCalledWith(
        "linux_native/app-db",
      ),
    );
    await waitFor(() =>
      expect(projectServiceMock.RefreshProjects).toHaveBeenCalledTimes(2),
    );
  });

  it("lets users dismiss project action errors", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());
    projectServiceMock.RefreshProjects.mockResolvedValue([seededProject()]);
    projectServiceMock.StopProject.mockRejectedValueOnce(
      new Error("Compose project action failed"),
    );

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", {
        name: /Projects/,
      }),
    );
    await screen.findByText("app-db");
    fireEvent.click(screen.getByRole("button", { name: "Stop app-db" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Compose project action failed",
    );
    fireEvent.click(screen.getByRole("button", { name: "Dismiss error" }));

    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("removes error projects from the list without requiring Docker actions", async () => {
    const brokenProject = seededBrokenProject();
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());
    projectServiceMock.RefreshProjects.mockResolvedValueOnce([
      brokenProject,
    ]).mockResolvedValueOnce([]);

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", {
        name: /Projects/,
      }),
    );
    await screen.findByText("Workdir missing");
    fireEvent.click(
      screen.getByRole("button", {
        name: `Remove from list ${brokenProject.name}`,
      }),
    );

    expect(
      await screen.findByRole("dialog", {
        name: "Remove project from list?",
      }),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/This does not stop containers/),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Remove" }));

    await waitFor(() =>
      expect(projectServiceMock.RemoveProjectFromList).toHaveBeenCalledWith(
        brokenProject.id,
      ),
    );
    expect(projectServiceMock.StartProject).not.toHaveBeenCalledWith(
      brokenProject.id,
    );
  });

  it("opens project detail tabs with services, containers, and Compose config", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());
    projectServiceMock.RefreshProjects.mockResolvedValue([seededProject()]);
    projectServiceMock.GetProject.mockResolvedValue(seededProjectDetail());
    backupServiceMock.ListBackups.mockResolvedValue([seededBackup()]);

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", {
        name: /Projects/,
      }),
    );
    fireEvent.click(await screen.findByRole("button", { name: "app-db" }));

    await waitFor(() =>
      expect(projectServiceMock.GetProject).toHaveBeenCalledWith(
        "linux_native/app-db",
      ),
    );
    expect(await screen.findByText("linux_native")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Containers" }),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Services" }));
    expect(screen.getByText("postgres:16")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Containers" }));
    expect(screen.getByText("container-app")).toBeInTheDocument();

    const logsButtons = screen.getAllByRole("button", { name: "Logs" });
    fireEvent.click(logsButtons[logsButtons.length - 1]);
    await waitFor(() =>
      expect(logsServiceMock.StartLogStream).toHaveBeenCalledWith({
        scope: "project",
        ids: ["linux_native/app-db"],
        follow: true,
        tail: 500,
        timestamps: true,
      }),
    );
    expect(screen.queryByRole("group", { name: "Log scope" })).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Compose" }));
    expect(screen.getByText("valid")).toBeInTheDocument();
    expect(await screen.findByTestId("monaco-viewer")).toHaveTextContent(
      "services:",
    );

    fireEvent.click(screen.getByRole("button", { name: "Backups" }));
    expect(screen.getByText("Project Volumes")).toBeInTheDocument();
    expect(screen.getByText("cairn_data")).toBeInTheDocument();
    expect(screen.getByText("success")).toBeInTheDocument();
  });

  it("drills into project containers with logs, files, inspect, and terminal actions", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());
    projectServiceMock.RefreshProjects.mockResolvedValue([seededProject()]);
    projectServiceMock.GetProject.mockResolvedValue(seededProjectDetail());
    dockerServiceMock.GetContainer.mockResolvedValue(seededContainerDetail());
    dockerServiceMock.InspectContainerRaw.mockResolvedValue(
      '{"Id":"container-app","Config":{"Image":"cairn/app:latest"}}',
    );
    dockerServiceMock.ListContainerFiles.mockResolvedValue(
      seededContainerFiles(),
    );

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", {
        name: /Projects/,
      }),
    );
    fireEvent.click(await screen.findByRole("button", { name: "app-db" }));
    await screen.findByText("linux_native");

    fireEvent.click(
      screen.getByRole("button", { name: "Open app container details" }),
    );
    await waitFor(() =>
      expect(dockerServiceMock.GetContainer).toHaveBeenCalledWith(
        "container-app",
      ),
    );
    expect(
      screen.getByRole("heading", { name: "container-app" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Working dir")).toBeInTheDocument();

    const drilldownLogsButtons = screen.getAllByRole("button", {
      name: "Logs",
    });
    fireEvent.click(drilldownLogsButtons[drilldownLogsButtons.length - 1]);
    await waitFor(() =>
      expect(logsServiceMock.StartLogStream).toHaveBeenLastCalledWith({
        scope: "container",
        ids: ["container-app"],
        follow: true,
        tail: 500,
        timestamps: true,
      }),
    );

    fireEvent.click(screen.getByRole("button", { name: "Files" }));
    await waitFor(() =>
      expect(dockerServiceMock.ListContainerFiles).toHaveBeenCalledWith(
        "container-app",
        "/",
      ),
    );
    expect(await screen.findByText("app.log")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Inspect" }));
    await waitFor(() =>
      expect(dockerServiceMock.InspectContainerRaw).toHaveBeenCalledWith(
        "container-app",
      ),
    );
    await waitFor(() =>
      expect(screen.getAllByText(/cairn\/app:latest/).length).toBeGreaterThan(
        1,
      ),
    );

    const terminalButtons = screen.getAllByRole("button", {
      name: "Terminal",
    });
    fireEvent.click(terminalButtons[terminalButtons.length - 1]);
    const openTerminalButtons = screen.getAllByRole("button", {
      name: "Open terminal",
    });
    fireEvent.click(openTerminalButtons[openTerminalButtons.length - 1]);
    await waitFor(() =>
      expect(terminalServiceMock.OpenContainerTerminal).toHaveBeenCalledWith(
        "container-app",
        { cols: 120, rows: 30 },
      ),
    );
  });

  it("plans and applies updates from the global Updates page", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());
    projectServiceMock.RefreshProjects.mockResolvedValue([seededProject()]);
    updateServiceMock.ListCurrentUpdates.mockImplementation((filter) =>
      Promise.resolve(
        filter?.status?.includes(UpdateStatus.UpdateStatusIgnored)
          ? [ignoredUpdate()]
          : seededUpdates(),
      ),
    );
    updateServiceMock.ListUpdateHistory.mockResolvedValue([updateHistoryRow()]);

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", {
        name: /Updates/,
      }),
    );

    expect(
      await screen.findByText("Image update available"),
    ).toBeInTheDocument();
    expect(screen.getAllByText("Rebuild required").length).toBeGreaterThan(0);
    expect(
      screen.getByText(
        "Base image: Unknown - this is a third-party registry image and no base metadata was found.",
      ),
    ).toBeInTheDocument();

    fireEvent.click(screen.getAllByRole("button", { name: "Update" })[0]);
    expect(
      await screen.findByText("$ docker compose pull app"),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByLabelText(/Back up named volumes first/));
    fireEvent.click(screen.getByRole("button", { name: "Update service" }));

    await waitFor(() =>
      expect(updateServiceMock.ApplyUpdate).toHaveBeenCalledWith({
        planID: "plan-update-app",
        backupVolumesFirst: true,
        watchHealth: true,
        rollbackOnFailure: true,
      }),
    );

    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    fireEvent.click(screen.getByRole("button", { name: "History" }));
    fireEvent.click(await screen.findByRole("button", { name: "Rollback" }));
    await waitFor(() =>
      expect(updateServiceMock.PlanRollback).toHaveBeenCalledWith(301),
    );
    fireEvent.click(screen.getByRole("button", { name: "Roll back" }));
    await waitFor(() =>
      expect(updateServiceMock.ApplyRollback).toHaveBeenCalledWith(
        "rollback-plan-app",
      ),
    );

    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    fireEvent.click(screen.getByRole("button", { name: "Ignored" }));
    expect(
      await screen.findByText("Waiting for maintenance window"),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Unignore" }));
    expect(updateServiceMock.UnignoreUpdate).toHaveBeenCalledWith(201);
  });

  it("shows project Updates tab grouping and lineage wording", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());
    projectServiceMock.RefreshProjects.mockResolvedValue([seededProject()]);
    projectServiceMock.GetProject.mockResolvedValue(seededProjectDetail());
    updateServiceMock.ListCurrentUpdates.mockResolvedValue(seededUpdates());
    imageLineageServiceMock.GetProjectLineage.mockResolvedValue(
      seededLineage(),
    );

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", {
        name: /Projects/,
      }),
    );
    fireEvent.click(await screen.findByRole("button", { name: "app-db" }));
    await screen.findByText("linux_native");
    const updatesButtons = await waitFor(() => {
      const buttons = screen.getAllByRole("button", { name: "Updates" });
      expect(buttons.length).toBeGreaterThan(1);
      return buttons;
    });
    fireEvent.click(updatesButtons[updatesButtons.length - 1]);

    expect(await screen.findByText("Pull & recreate")).toBeInTheDocument();
    expect(screen.getByText("Rebuild & redeploy")).toBeInTheDocument();
    expect(screen.getByText("Manual attention")).toBeInTheDocument();
    expect(
      screen.getByText(/node:20-alpine - Confidence: High/),
    ).toBeInTheDocument();
    expect(
      screen.getByText(
        "Base image: Unknown - this is a third-party registry image and no base metadata was found.",
      ),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Update project" }));
    expect(updateServiceMock.PlanProjectUpdate).toHaveBeenCalledWith(
      "linux_native/app-db",
    );
  });

  it("streams logs, filters search matches, and keeps nonmatching rows hidden", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());
    projectServiceMock.RefreshProjects.mockResolvedValue([seededProject()]);

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", {
        name: /Logs/,
      }),
    );

    await waitFor(() =>
      expect(logsServiceMock.StartLogStream).toHaveBeenCalledWith(
        expect.objectContaining({ follow: true, scope: "all" }),
      ),
    );
    emitRuntimeEvent("logs:lines", {
      streamID: "stream-1",
      lines: [
        logLine({ level: "info", text: "INFO server started" }),
        logLine({ level: "", text: "plain startup message" }),
        logLine({
          level: "error",
          stream: "stderr",
          text: "ERROR failed request",
        }),
      ],
    });

    expect(await screen.findByText(/server started/)).toBeInTheDocument();
    expect(screen.getByText(/plain startup/)).toBeInTheDocument();
    expect(screen.getByText(/failed request/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "LOG" }));
    expect(screen.queryByText(/plain startup/)).not.toBeInTheDocument();
    expect(screen.getByText(/server started/)).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("Search logs"), {
      target: { value: "failed" },
    });
    await waitFor(() => expect(screen.getByText("1/1")).toBeInTheDocument());
    fireEvent.click(screen.getByLabelText("Matches only"));

    expect(screen.queryByText(/server started/)).not.toBeInTheDocument();
    expect(screen.getByText(/request/)).toBeInTheDocument();
  });

  it("stops a metrics stream that resolves after app cleanup", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());
    let resolveStatsStream: (streamID: string) => void = () => undefined;
    metricsServiceMock.StartStatsStream.mockImplementationOnce(
      () =>
        new Promise<string>((resolve) => {
          resolveStatsStream = resolve;
        }),
    );

    const { unmount } = render(<App />);

    await waitFor(() =>
      expect(metricsServiceMock.StartStatsStream).toHaveBeenCalled(),
    );
    unmount();

    await act(async () => {
      resolveStatsStream("late-stats-stream");
    });

    expect(metricsServiceMock.StopStream).toHaveBeenCalledWith(
      "late-stats-stream",
    );
  });

  it("stops a log stream that resolves after logs page cleanup", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());
    let resolveLogStream: (streamID: string) => void = () => undefined;
    logsServiceMock.StartLogStream.mockImplementationOnce(
      () =>
        new Promise<string>((resolve) => {
          resolveLogStream = resolve;
        }),
    );

    const { unmount } = render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", {
        name: /Logs/,
      }),
    );
    await waitFor(() =>
      expect(logsServiceMock.StartLogStream).toHaveBeenCalled(),
    );
    unmount();

    await act(async () => {
      resolveLogStream("late-log-stream");
    });

    expect(logsServiceMock.StopStream).toHaveBeenCalledWith("late-log-stream");
  });

  it("uses checkboxes for container log scope selection", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", {
        name: /Logs/,
      }),
    );
    await waitFor(() =>
      expect(logsServiceMock.StartLogStream).toHaveBeenCalledWith(
        expect.objectContaining({ follow: true, scope: "all" }),
      ),
    );

    fireEvent.click(screen.getByRole("button", { name: "container" }));
    const scope = await screen.findByRole("group", {
      name: "Container scope",
    });
    fireEvent.click(within(scope).getByLabelText("Search containers"));
    const checkbox = within(scope).getByRole("checkbox", { name: /web/ });
    expect(checkbox).not.toBeChecked();

    fireEvent.click(checkbox);

    await waitFor(() =>
      expect(logsServiceMock.StartLogStream).toHaveBeenLastCalledWith(
        expect.objectContaining({
          ids: ["container-1"],
          scope: "container",
        }),
      ),
    );
  });

  it("pauses visible logs while buffering new stream events", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", {
        name: /Logs/,
      }),
    );
    await waitFor(() =>
      expect(logsServiceMock.StartLogStream).toHaveBeenCalled(),
    );
    emitRuntimeEvent("logs:lines", {
      streamID: "stream-1",
      lines: [logLine({ text: "INFO first line" })],
    });
    expect(await screen.findByText(/first line/)).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Pause" }));
    emitRuntimeEvent("logs:lines", {
      streamID: "stream-1",
      lines: [logLine({ text: "INFO buffered line" })],
    });

    expect(await screen.findByText("Paused - 1 new lines")).toBeInTheDocument();
    expect(screen.queryByText(/buffered line/)).not.toBeInTheDocument();

    fireEvent.click(screen.getAllByRole("button", { name: "Resume" })[0]);
    expect(await screen.findByText(/buffered line/)).toBeInTheDocument();
  });

  it("exports logs through the current stream scope", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", {
        name: /Logs/,
      }),
    );
    await waitFor(() =>
      expect(logsServiceMock.StartLogStream).toHaveBeenCalled(),
    );
    fireEvent.click(screen.getByRole("button", { name: "Export" }));

    const dialog = await screen.findByRole("dialog", { name: "Export Logs" });
    fireEvent.click(within(dialog).getByRole("button", { name: "Browse" }));
    await waitFor(() =>
      expect(runtimeMock.saveFile).toHaveBeenCalledWith(
        expect.objectContaining({ ButtonText: "Export" }),
      ),
    );
    fireEvent.click(within(dialog).getByRole("button", { name: "Export" }));

    await waitFor(() =>
      expect(logsServiceMock.ExportLogs).toHaveBeenCalledWith({
        scope: "all",
        ids: [],
        path: "/tmp/cairn-logs.jsonl",
      }),
    );
    expect(await screen.findByText("Logs exported")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Open folder" }));
    expect(runtimeMock.setClipboardText).toHaveBeenCalledWith(
      "/tmp/cairn-logs.jsonl",
    );
  });

  it("confirms dangerous project plans through the project apply endpoint", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());
    projectServiceMock.RefreshProjects.mockResolvedValue([seededProject()]);

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", {
        name: /Projects/,
      }),
    );
    await screen.findByText("app-db");
    fireEvent.click(
      screen.getByRole("button", { name: "Down with volumes app-db" }),
    );

    await waitFor(() =>
      expect(projectServiceMock.PlanDownProject).toHaveBeenCalledWith(
        "linux_native/app-db",
        true,
      ),
    );
    expect(
      await screen.findByRole("dialog", { name: "Down app-db with volumes" }),
    ).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Type app-db to confirm"), {
      target: { value: "app-db" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Confirm" }));

    await waitFor(() =>
      expect(projectServiceMock.ApplyProjectPlan).toHaveBeenCalledWith(
        "plan-down-volumes",
        "app-db",
      ),
    );
  });

  it("imports a Compose project through the folder picker", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());
    projectServiceMock.RefreshProjects.mockResolvedValueOnce(
      [],
    ).mockResolvedValueOnce([seededProject()]);
    runtimeMock.openFile.mockResolvedValue(
      "E:\\Development\\projects\\apps\\rcooler\\Cairn\\testdata\\projects\\app-db",
    );

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", {
        name: /Projects/,
      }),
    );
    fireEvent.click(
      await screen.findByRole("button", { name: "Import Project" }),
    );
    expect(
      await screen.findByRole("dialog", { name: "Import Project" }),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Browse" }));
    await waitFor(() =>
      expect(runtimeMock.openFile).toHaveBeenCalledWith(
        expect.objectContaining({ CanChooseDirectories: true }),
      ),
    );
    expect(screen.getByLabelText("Folder")).toHaveValue(
      "E:\\Development\\projects\\apps\\rcooler\\Cairn\\testdata\\projects\\app-db",
    );

    fireEvent.click(screen.getByRole("button", { name: "Import" }));

    await waitFor(() =>
      expect(projectServiceMock.ImportProject).toHaveBeenCalledWith({
        folderPath:
          "E:\\Development\\projects\\apps\\rcooler\\Cairn\\testdata\\projects\\app-db",
        composeFilePaths: [],
      }),
    );
    expect(await screen.findByText("Imported app-db")).toBeInTheDocument();
    await waitFor(() =>
      expect(projectServiceMock.RefreshProjects).toHaveBeenCalledTimes(2),
    );
  });

  it("warns when importing a project from a WSL mount path", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());
    projectServiceMock.RefreshProjects.mockResolvedValue([]);

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", {
        name: /Projects/,
      }),
    );
    fireEvent.click(
      await screen.findByRole("button", { name: "Import Project" }),
    );
    fireEvent.change(await screen.findByLabelText("Folder"), {
      target: { value: "/mnt/c/Users/Ada/path-heavy-project" },
    });

    expect(
      screen.getByText(
        "WSL mount paths may be slower than files stored inside the distro.",
      ),
    ).toBeInTheDocument();
  });

  it("runs safe container actions directly and refreshes inventory", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", {
        name: /Containers/,
      }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Stop web" }));

    expect(dockerServiceMock.StopContainer).toHaveBeenCalledWith(
      "container-1",
      10,
    );
    await waitFor(() =>
      expect(inventoryMock.getInventorySnapshot).toHaveBeenCalledTimes(2),
    );
  });

  it("previews and confirms kill through the command-plan pipeline", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", {
        name: /Containers/,
      }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Kill web" }));

    expect(dockerServiceMock.PlanKillContainer).toHaveBeenCalledWith(
      "container-1",
    );
    expect(
      await screen.findByRole("dialog", { name: "Kill web" }),
    ).toBeInTheDocument();
    expect(screen.getByText("docker kill web")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Confirm" }));

    await waitFor(() =>
      expect(dockerServiceMock.ApplyContainerPlan).toHaveBeenCalledWith(
        "plan-kill-web",
        "",
      ),
    );
  });

  it("runs an image from the row wizard and refreshes inventory", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", {
        name: /Images/,
      }),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Run cairn/web:latest" }),
    );

    expect(
      await screen.findByRole("dialog", { name: "Run Image" }),
    ).toBeInTheDocument();
    expect(screen.getByLabelText("Image ref")).toHaveValue("cairn/web:latest");
    fireEvent.click(screen.getByRole("button", { name: "Next" }));
    fireEvent.change(screen.getByLabelText("Volumes"), {
      target: { value: "myvol:/data" },
    });
    expect(screen.getByText(/docker run -d/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Run" }));

    await waitFor(() =>
      expect(dockerServiceMock.PlanRunImage).toHaveBeenCalledWith(
        expect.objectContaining({
          imageRef: "cairn/web:latest",
          name: "web",
          detach: true,
          pullIfMissing: true,
          volumes: [
            expect.objectContaining({
              type: "volume",
              source: "myvol",
              target: "/data",
              volumeName: "myvol",
            }),
          ],
        }),
      ),
    );
    await waitFor(() =>
      expect(dockerServiceMock.ApplyRunImagePlan).toHaveBeenCalledWith(
        "run-image-plan",
        "",
      ),
    );
    expect(dockerServiceMock.RunImage).not.toHaveBeenCalled();
    await waitFor(() =>
      expect(inventoryMock.getInventorySnapshot).toHaveBeenCalledTimes(2),
    );
  });

  it("renames a container through the modal", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", {
        name: /Containers/,
      }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Rename web" }));
    fireEvent.change(screen.getByLabelText("New name"), {
      target: { value: "web-renamed" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Rename" }));

    await waitFor(() =>
      expect(dockerServiceMock.RenameContainer).toHaveBeenCalledWith(
        "container-1",
        "web-renamed",
      ),
    );
  });

  it("pulls, tags, pushes, saves, and loads images from image modals", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", {
        name: /Images/,
      }),
    );

    fireEvent.click(screen.getByRole("button", { name: "Pull image" }));
    fireEvent.change(screen.getByLabelText("Image ref"), {
      target: { value: "redis" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Pull" }));
    await waitFor(() =>
      expect(dockerServiceMock.PullImage).toHaveBeenCalledWith("redis:latest"),
    );

    fireEvent.click(
      screen.getByRole("button", { name: "Tag cairn/web:latest" }),
    );
    fireEvent.change(screen.getByLabelText("New ref"), {
      target: { value: "redis:7" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Create tag" }));
    await waitFor(() =>
      expect(dockerServiceMock.TagImage).toHaveBeenCalledWith(
        "sha256:image-1",
        "redis:7",
      ),
    );

    fireEvent.click(
      screen.getByRole("button", { name: "Push cairn/web:latest" }),
    );
    fireEvent.change(screen.getByLabelText("Image ref"), {
      target: { value: "redis:7" },
    });
    fireEvent.click(screen.getByLabelText(/Confirm publishing/));
    fireEvent.click(screen.getByRole("button", { name: "Push" }));
    await waitFor(() =>
      expect(dockerServiceMock.PlanPushImage).toHaveBeenCalledWith("redis:7"),
    );
    expect(dockerServiceMock.ApplyPushImagePlan).toHaveBeenCalledWith(
      "push-image-plan",
    );

    fireEvent.click(
      screen.getByRole("button", { name: "Save cairn/web:latest" }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(dockerServiceMock.SaveImage).toHaveBeenCalledWith(
        ["cairn/web:latest"],
        "cairn_web_latest.tar",
      ),
    );

    fireEvent.click(screen.getByRole("button", { name: "Load tar" }));
    fireEvent.change(screen.getByLabelText("Source tar"), {
      target: { value: "/tmp/image.tar" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Load" }));
    await waitFor(() =>
      expect(dockerServiceMock.LoadImage).toHaveBeenCalledWith(
        "/tmp/image.tar",
      ),
    );
  });

  it("creates volumes and networks from page actions", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", {
        name: /Volumes/,
      }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Create volume" }));
    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "demo_data" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Create" }));
    await waitFor(() =>
      expect(dockerServiceMock.CreateVolume).toHaveBeenCalledWith(
        expect.objectContaining({ name: "demo_data", driver: "local" }),
      ),
    );

    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", {
        name: /Networks/,
      }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Create network" }));
    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "demo_net" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Create" }));
    await waitFor(() =>
      expect(dockerServiceMock.CreateNetwork).toHaveBeenCalledWith(
        expect.objectContaining({ name: "demo_net", driver: "bridge" }),
      ),
    );
  });

  it("opens a network detail view with attached container endpoint data", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", {
        name: /Networks/,
      }),
    );
    fireEvent.click(screen.getByRole("button", { name: "cairn_default" }));

    expect(
      screen.getByRole("heading", { name: "cairn_default" }),
    ).toBeInTheDocument();
    expect(screen.getByText("172.19.0.0/16")).toBeInTheDocument();

    fireEvent.click(screen.getAllByRole("button", { name: /Containers/ })[1]);

    expect(
      screen.getByRole("table", { name: "Network containers" }),
    ).toBeInTheDocument();
    expect(screen.getByText("172.19.0.2/16")).toBeInTheDocument();
    expect(screen.getByText("02:42:ac:13:00:02")).toBeInTheDocument();
  });

  it("plans volume backup and restore actions behind confirmation", async () => {
    const backup = seededBackup();
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());
    backupServiceMock.ListBackups.mockResolvedValue([backup]);

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", {
        name: /Volumes/,
      }),
    );

    fireEvent.click(screen.getByRole("button", { name: "Backup cairn_data" }));
    const backupDialog = await screen.findByRole("dialog", {
      name: "Back Up Volume",
    });
    expect(within(backupDialog).getByText("cairn_data")).toBeInTheDocument();
    fireEvent.click(
      within(backupDialog).getByRole("button", { name: "Preview backup" }),
    );

    await waitFor(() =>
      expect(backupServiceMock.PlanBackupVolume).toHaveBeenCalledWith(
        expect.objectContaining({
          volumeName: "cairn_data",
          projectID: "cairn",
        }),
      ),
    );
    fireEvent.click(await screen.findByRole("button", { name: "Confirm" }));
    await waitFor(() =>
      expect(backupServiceMock.ApplyBackup).toHaveBeenCalledWith(
        "plan-backup-volume",
      ),
    );

    fireEvent.click(screen.getByRole("button", { name: "Restore cairn_data" }));
    const restoreDialog = await screen.findByRole("dialog", {
      name: "Restore Volume",
    });
    await waitFor(() =>
      expect(within(restoreDialog).getByLabelText("Backup")).toHaveValue(""),
    );
    fireEvent.change(within(restoreDialog).getByLabelText("Backup"), {
      target: { value: backup.id },
    });
    fireEvent.change(within(restoreDialog).getByLabelText("Target volume"), {
      target: { value: "cairn_data" },
    });
    fireEvent.click(
      within(restoreDialog).getByRole("button", { name: "Preview restore" }),
    );

    await waitFor(() =>
      expect(backupServiceMock.PlanRestoreVolume).toHaveBeenCalledWith(
        expect.objectContaining({
          backupID: backup.id,
          sourcePath: backup.path,
          volumeName: "cairn_data",
        }),
      ),
    );
    fireEvent.change(
      await screen.findByLabelText("Type cairn_data to confirm"),
      { target: { value: "cairn_data" } },
    );
    fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
    await waitFor(() =>
      expect(backupServiceMock.ApplyRestore).toHaveBeenCalledWith(
        "plan-restore-volume",
        "cairn_data",
      ),
    );
  });

  it("renders empty states when the daemon has no objects", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(emptySnapshot());

    render(<App />);

    expect(await screen.findByText("No containers yet")).toBeInTheDocument();
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", {
        name: /Images/,
      }),
    );

    expect(screen.getByText("No images match")).toBeInTheDocument();
  });

  it("renders stale cached data and disables Docker mutations when the daemon is stopped", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(
      stoppedDaemonSnapshot(),
    );

    render(<App />);

    expect(await screen.findByText("Stale cached data")).toBeInTheDocument();
    expect(screen.getByText("Docker is not reachable")).toBeInTheDocument();
    expect(logsServiceMock.StartLogStream).not.toHaveBeenCalled();
    expect(metricsServiceMock.StartStatsStream).not.toHaveBeenCalled();

    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", {
        name: /Containers/,
      }),
    );

    const stopButton = screen.getByRole("button", { name: "Stop web" });
    expect(stopButton).toBeDisabled();
    fireEvent.click(stopButton);
    expect(dockerServiceMock.StopContainer).not.toHaveBeenCalled();

    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", {
        name: /Logs/,
      }),
    );
    expect(logsServiceMock.StartLogStream).not.toHaveBeenCalled();
  });

  it("persists Linux socket permission choices from the provider repair dialog", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(
      permissionDeniedSnapshot(),
    );

    render(<App />);

    expect(
      await screen.findByText("Cairn cannot access the Docker socket."),
    ).toBeInTheDocument();

    fireEvent.click(screen.getAllByRole("button", { name: "Repair" })[0]);
    const dialog = await screen.findByRole("dialog", {
      name: "Repair Docker Provider",
    });
    fireEvent.click(within(dialog).getByLabelText(/Add user to docker group/));
    fireEvent.click(
      within(dialog).getByRole("button", {
        name: "Save permission mode",
      }),
    );

    await waitFor(() =>
      expect(settingsServiceMock.SetSetting).toHaveBeenCalledWith(
        "linux.sudo_mode",
        "group",
      ),
    );
    expect(providerServiceMock.Detect).toHaveBeenCalledWith("linux_native");
  });

  it("runs the Windows WSL setup checks and install plan from onboarding", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(noProviderSnapshot());
    providerServiceMock.Detect.mockResolvedValueOnce({
      ...healthyProviderStatus(),
      installed: false,
      running: false,
      healthy: false,
      dockerInstalled: false,
      dockerRunning: false,
      composeInstalled: false,
      buildxInstalled: false,
      problems: [
        {
          code: "WSL_MISSING",
          message: "WSL is not installed.",
          repairHint: "Install WSL 2 with an Ubuntu distribution.",
          recoverable: true,
        },
      ],
    });

    render(<App />);

    expect(
      await screen.findByText("No Docker provider configured"),
    ).toBeInTheDocument();
    fireEvent.click(screen.getAllByRole("button", { name: "Set up" })[0]);
    const dialog = await screen.findByRole("dialog", {
      name: "Set Up Docker Backend",
    });

    fireEvent.click(
      within(dialog).getByRole("button", { name: "Get started" }),
    );
    fireEvent.click(
      within(dialog).getByRole("button", { name: /Ubuntu on WSL2/ }),
    );
    fireEvent.click(within(dialog).getByRole("button", { name: "Run checks" }));

    await waitFor(() =>
      expect(providerServiceMock.Detect).toHaveBeenCalledWith(
        "windows_wsl_ubuntu",
      ),
    );
    expect(
      await within(dialog).findByText(
        "Install WSL 2 with an Ubuntu distribution.",
      ),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByText(/inside the WSL distro/),
    ).toBeInTheDocument();

    fireEvent.click(
      within(dialog).getByRole("button", { name: "Review auto repair" }),
    );
    await waitFor(() =>
      expect(providerServiceMock.PlanInstall).toHaveBeenCalledWith(
        "windows_wsl_ubuntu",
        expect.objectContaining({
          backend: "windows_wsl_ubuntu",
          extra: { distro: "Ubuntu" },
        }),
      ),
    );
    expect(
      await within(dialog).findByText(
        "wsl.exe --install Ubuntu --name cairn-dev",
      ),
    ).toBeInTheDocument();

    fireEvent.click(
      within(dialog).getByRole("button", { name: "Run auto repair" }),
    );
    await waitFor(() =>
      expect(providerServiceMock.ApplyInstall).toHaveBeenCalledWith(
        "plan-wsl-install",
      ),
    );
    const welcomeStep = within(dialog).getByRole("button", { name: /Welcome/ });
    expect(welcomeStep).toHaveAttribute("aria-disabled", "true");
    expect(welcomeStep).not.toBeDisabled();
    emitRuntimeEvent("provider:install:progress", {
      planID: "plan-wsl-install",
      streamID: "setup-stream",
      step: 1,
      totalSteps: 1,
      message: "Install complete",
      done: true,
    });

    expect(
      await within(dialog).findByText("Windows WSL backend is ready"),
    ).toBeInTheDocument();
  });

  it("opens existing Docker context setup from the onboarding shortcut", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(noProviderSnapshot());

    render(<App />);

    expect(
      await screen.findByText("No Docker provider configured"),
    ).toBeInTheDocument();
    fireEvent.click(screen.getAllByRole("button", { name: "Set up" })[0]);
    const dialog = await screen.findByRole("dialog", {
      name: "Set Up Docker Backend",
    });

    fireEvent.click(
      within(dialog).getByRole("button", {
        name: "I already have Docker running",
      }),
    );

    expect(
      await within(dialog).findByText("Use an existing Docker context"),
    ).toBeInTheDocument();
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Open Settings" }),
    );

    expect(await screen.findByText("Docker Contexts")).toBeInTheDocument();
    expect(providerServiceMock.ListDockerContexts).toHaveBeenCalled();
  });

  it("detects projects before completing onboarding", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(noProviderSnapshot());
    projectServiceMock.RefreshProjects.mockResolvedValue([seededProject()]);

    render(<App />);

    expect(
      await screen.findByText("No Docker provider configured"),
    ).toBeInTheDocument();
    fireEvent.click(screen.getAllByRole("button", { name: "Set up" })[0]);
    const dialog = await screen.findByRole("dialog", {
      name: "Set Up Docker Backend",
    });

    fireEvent.click(
      within(dialog).getByRole("button", { name: "Get started" }),
    );
    fireEvent.click(
      within(dialog).getByRole("button", { name: /Ubuntu on WSL2/ }),
    );
    fireEvent.click(within(dialog).getByRole("button", { name: "Run checks" }));

    expect(
      await within(dialog).findByText("Windows WSL backend is ready"),
    ).toBeInTheDocument();
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Detect projects" }),
    );

    expect(
      await within(dialog).findByText("Found 1 Compose project"),
    ).toBeInTheDocument();
    expect(within(dialog).getByLabelText(/app-db/)).toBeChecked();
    fireEvent.click(within(dialog).getByRole("button", { name: "Finish" }));
    expect(
      await within(dialog).findByText("Cairn is ready"),
    ).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("button", { name: "Continue" }));

    await waitFor(() =>
      expect(
        screen.queryByRole("dialog", { name: "Set Up Docker Backend" }),
      ).not.toBeInTheDocument(),
    );
  });

  it("runs the Linux native onboarding branch with permission persistence", async () => {
    Object.defineProperty(window.navigator, "platform", {
      configurable: true,
      value: "Linux x86_64",
    });
    inventoryMock.getInventorySnapshot.mockResolvedValue(noProviderSnapshot());
    providerServiceMock.Detect.mockResolvedValueOnce({
      ...healthyProviderStatus(),
      installed: true,
      running: false,
      healthy: false,
      dockerHost: "unix:///var/run/docker.sock",
      dockerRunning: false,
      problems: [
        {
          code: "PERM_SOCKET",
          message: "Cairn cannot access the Docker socket.",
          repairHint:
            "Choose sudo-per-action in Settings or add your Linux user to the docker group, then sign out and back in.",
          recoverable: true,
        },
      ],
    });
    providerServiceMock.PlanInstall.mockResolvedValue(linuxInstallPlan());
    providerServiceMock.ApplyInstall.mockResolvedValue({
      planID: "plan-linux-install",
      streamID: "setup-stream",
    });

    render(<App />);

    expect(
      await screen.findByText("No Docker provider configured"),
    ).toBeInTheDocument();
    fireEvent.click(screen.getAllByRole("button", { name: "Set up" })[0]);
    const dialog = await screen.findByRole("dialog", {
      name: "Set Up Docker Backend",
    });

    fireEvent.click(
      within(dialog).getByRole("button", { name: "Get started" }),
    );
    fireEvent.click(
      within(dialog).getByRole("button", { name: /Native Docker Engine/ }),
    );
    fireEvent.click(within(dialog).getByRole("button", { name: "Run checks" }));

    await waitFor(() =>
      expect(providerServiceMock.Detect).toHaveBeenCalledWith("linux_native"),
    );
    expect(
      await within(dialog).findByText(
        /add your Linux user to the docker group/,
      ),
    ).toBeInTheDocument();
    fireEvent.click(within(dialog).getByLabelText(/Add user to docker group/));
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Save permission mode" }),
    );
    await waitFor(() =>
      expect(settingsServiceMock.SetSetting).toHaveBeenCalledWith(
        "linux.sudo_mode",
        "group",
      ),
    );

    fireEvent.click(
      within(dialog).getByRole("button", { name: "Review auto repair" }),
    );
    await waitFor(() =>
      expect(providerServiceMock.PlanInstall).toHaveBeenCalledWith(
        "linux_native",
        expect.objectContaining({
          backend: "linux_native",
          extra: { socketPath: "/var/run/docker.sock" },
        }),
      ),
    );
    expect(
      await within(dialog).findByText("sudo apt-get update"),
    ).toBeInTheDocument();

    fireEvent.click(
      within(dialog).getByRole("button", { name: "Run auto repair" }),
    );
    await waitFor(() =>
      expect(providerServiceMock.ApplyInstall).toHaveBeenCalledWith(
        "plan-linux-install",
      ),
    );
    emitRuntimeEvent("provider:install:progress", {
      planID: "plan-linux-install",
      streamID: "setup-stream",
      step: 1,
      totalSteps: 1,
      message: "Install complete",
      done: true,
    });

    expect(
      await within(dialog).findByText("Linux native backend is ready"),
    ).toBeInTheDocument();
  });

  it("resumes onboarding from a stored install plan", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());
    window.localStorage.setItem(
      "cairn.providerSetup.v1",
      JSON.stringify({
        open: true,
        step: "install",
        platform: "windows",
        backend: "windows_wsl_ubuntu",
        distro: "Ubuntu",
        colimaProfile: "default",
        colimaCPU: 2,
        colimaMemoryGB: 4,
        colimaDiskGB: 60,
        detecting: false,
        detection: null,
        plan: wslInstallPlan(),
        planning: false,
        installing: false,
        progress: [],
        detectedProjects: [],
        selectedProjectIDs: [],
        detectingProjects: false,
      }),
    );

    render(<App />);

    const dialog = await screen.findByRole("dialog", {
      name: "Set Up Docker Backend",
    });
    expect(
      within(dialog).getByText(
        "Install or update Docker Engine in Ubuntu on WSL",
      ),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByText("wsl.exe --install Ubuntu --name cairn-dev"),
    ).toBeInTheDocument();
  });

  it("handles provider install progress before ApplyInstall returns a stream handle", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());
    let resolveApplyInstall:
      | ((handle: { planID: string; streamID: string }) => void)
      | undefined;
    providerServiceMock.ApplyInstall.mockImplementation(
      () =>
        new Promise<{ planID: string; streamID: string }>((resolve) => {
          resolveApplyInstall = resolve;
        }),
    );
    window.localStorage.setItem(
      "cairn.providerSetup.v1",
      JSON.stringify({
        open: true,
        step: "install",
        platform: "windows",
        backend: "windows_wsl_ubuntu",
        distro: "Ubuntu",
        colimaProfile: "default",
        colimaCPU: 2,
        colimaMemoryGB: 4,
        colimaDiskGB: 60,
        detecting: false,
        detection: null,
        plan: wslInstallPlan(),
        planning: false,
        installing: false,
        progress: [],
        detectedProjects: [],
        selectedProjectIDs: [],
        detectingProjects: false,
      }),
    );

    render(<App />);

    const dialog = await screen.findByRole("dialog", {
      name: "Set Up Docker Backend",
    });
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Run auto repair" }),
    );

    expect(
      await within(dialog).findByText("Starting auto repair"),
    ).toBeInTheDocument();
    emitRuntimeEvent("provider:install:progress", {
      planID: "plan-wsl-install",
      streamID: "early-stream",
      step: 1,
      totalSteps: 1,
      message: "Install complete",
      done: true,
    });

    expect(
      await within(dialog).findByText("Windows WSL backend is ready"),
    ).toBeInTheDocument();
    await act(async () => {
      resolveApplyInstall?.({
        planID: "plan-wsl-install",
        streamID: "early-stream",
      });
    });
  });

  it("shows provider install failure details once in the progress list", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());
    window.localStorage.setItem(
      "cairn.providerSetup.v1",
      JSON.stringify({
        open: true,
        step: "install",
        platform: "windows",
        backend: "windows_wsl_ubuntu",
        distro: "Ubuntu",
        colimaProfile: "default",
        colimaCPU: 2,
        colimaMemoryGB: 4,
        colimaDiskGB: 60,
        detecting: false,
        detection: null,
        plan: wslInstallPlan(),
        planning: false,
        installing: false,
        progress: [],
        detectedProjects: [],
        selectedProjectIDs: [],
        detectingProjects: false,
      }),
    );

    render(<App />);

    const dialog = await screen.findByRole("dialog", {
      name: "Set Up Docker Backend",
    });
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Run auto repair" }),
    );
    await waitFor(() =>
      expect(providerServiceMock.ApplyInstall).toHaveBeenCalledWith(
        "plan-wsl-install",
      ),
    );
    emitRuntimeEvent("provider:install:progress", {
      planID: "plan-wsl-install",
      streamID: "setup-stream",
      step: 3,
      totalSteps: 10,
      message: "Install failed",
      done: true,
      error: "E_PROVIDER_NOT_READY: WSL install step failed\nWSL stderr detail",
    });

    expect(
      await within(dialog).findByText(/WSL stderr detail/),
    ).toBeInTheDocument();
    expect(within(dialog).getAllByText(/E_PROVIDER_NOT_READY/)).toHaveLength(1);
  });

  it("saves Windows WSL settings and shows the path mapping panel", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", {
        name: /Settings/,
      }),
    );

    expect(await screen.findByText("Windows WSL")).toBeInTheDocument();
    expect(screen.getByText("Path mapping")).toBeInTheDocument();
    expect(
      await screen.findByText("Installed WSL distros"),
    ).toBeInTheDocument();
    expect(screen.getAllByText("cairn-dev").length).toBeGreaterThan(0);
    expect(
      screen.getByText(/Docker contexts are separate Docker CLI endpoints/),
    ).toBeInTheDocument();
    fireEvent.click(screen.getAllByRole("button", { name: "Use distro" })[1]);
    await waitFor(() =>
      expect(settingsServiceMock.SetSetting).toHaveBeenCalledWith(
        "windows.wsl_distro",
        "cairn-dev",
      ),
    );
    settingsServiceMock.SetSetting.mockClear();
    const distroInput = screen.getByLabelText("WSL distro");
    fireEvent.change(distroInput, { target: { value: "custom-dev" } });
    fireEvent.blur(distroInput);

    await waitFor(() =>
      expect(settingsServiceMock.SetSetting).toHaveBeenCalledWith(
        "windows.wsl_distro",
        "custom-dev",
      ),
    );
    fireEvent.click(
      screen.getByRole("checkbox", {
        name: /Start Docker backend on app launch/,
      }),
    );
    await waitFor(() =>
      expect(settingsServiceMock.SetSetting).toHaveBeenCalledWith(
        "provider.autostart_backend",
        false,
      ),
    );
  });

  it("round-trips settings from every Settings section", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", {
        name: /Settings/,
      }),
    );

    const socketInput = await screen.findByLabelText("Socket path");
    fireEvent.change(socketInput, {
      target: { value: "/run/user/1000/docker.sock" },
    });
    fireEvent.blur(socketInput);
    await waitFor(() =>
      expect(settingsServiceMock.SetSetting).toHaveBeenCalledWith(
        "linux.socket_path",
        "/run/user/1000/docker.sock",
      ),
    );

    fireEvent.change(screen.getByLabelText("Permission mode"), {
      target: { value: "group" },
    });
    await waitFor(() =>
      expect(settingsServiceMock.SetSetting).toHaveBeenCalledWith(
        "linux.sudo_mode",
        "group",
      ),
    );

    clickSettingsSection("General");
    fireEvent.change(await screen.findByLabelText("Theme"), {
      target: { value: "light" },
    });
    await waitFor(() =>
      expect(settingsServiceMock.SetSetting).toHaveBeenCalledWith(
        "general.theme",
        "light",
      ),
    );
    await waitFor(() =>
      expect(document.documentElement.dataset.theme).toBe("light"),
    );
    expect(document.documentElement.style.colorScheme).toBe("light");
    fireEvent.click(
      screen.getByRole("checkbox", { name: "Launch Cairn at login" }),
    );
    await waitFor(() =>
      expect(settingsServiceMock.SetSetting).toHaveBeenCalledWith(
        "general.autostart_app",
        true,
      ),
    );

    clickSettingsSection("Updates");
    fireEvent.change(await screen.findByLabelText("Check interval"), {
      target: { value: "6" },
    });
    await waitFor(() =>
      expect(settingsServiceMock.SetSetting).toHaveBeenCalledWith(
        "updates.check_interval_hours",
        6,
      ),
    );
    fireEvent.click(
      screen.getByRole("checkbox", { name: "Notify on available updates" }),
    );
    await waitFor(() =>
      expect(settingsServiceMock.SetSetting).toHaveBeenCalledWith(
        "updates.notify",
        false,
      ),
    );

    clickSettingsSection("Metrics");
    const sampleInput = await screen.findByLabelText("Sample interval seconds");
    fireEvent.change(sampleInput, { target: { value: "5" } });
    fireEvent.blur(sampleInput);
    await waitFor(() =>
      expect(settingsServiceMock.SetSetting).toHaveBeenCalledWith(
        "metrics.sample_interval_seconds",
        5,
      ),
    );

    clickSettingsSection("Terminal");
    const shellInput = await screen.findByLabelText("Default shell");
    fireEvent.change(shellInput, { target: { value: "/bin/zsh" } });
    fireEvent.blur(shellInput);
    await waitFor(() =>
      expect(settingsServiceMock.SetSetting).toHaveBeenCalledWith(
        "terminal.default_shell",
        "/bin/zsh",
      ),
    );

    clickSettingsSection("Appearance");
    fireEvent.change(await screen.findByLabelText("Theme"), {
      target: { value: "system" },
    });
    await waitFor(() =>
      expect(settingsServiceMock.SetSetting).toHaveBeenCalledWith(
        "general.theme",
        "system",
      ),
    );
    await waitFor(() =>
      expect(document.documentElement.dataset.theme).toBe("system"),
    );

    clickSettingsSection("Backups");
    const backupInput = await screen.findByLabelText("Backup directory");
    fireEvent.change(backupInput, { target: { value: "/data/backups" } });
    fireEvent.blur(backupInput);
    await waitFor(() =>
      expect(settingsServiceMock.SetSetting).toHaveBeenCalledWith(
        "backups.directory",
        "/data/backups",
      ),
    );

    clickSettingsSection("Registries");
    fireEvent.change(await screen.findByLabelText("Credential mode"), {
      target: { value: "none" },
    });
    await waitFor(() =>
      expect(settingsServiceMock.SetSetting).toHaveBeenCalledWith(
        "registry.credentials_mode",
        "none",
      ),
    );

    clickSettingsSection("Security & Audit");
    expect(
      await screen.findByRole("checkbox", {
        name: "Destructive-action confirmation",
      }),
    ).toBeDisabled();

    clickSettingsSection("About");
    expect(await screen.findByText("1.0.0")).toBeInTheDocument();
    expect(screen.getByText("go1.26.4")).toBeInTheDocument();
  });

  it("uses the stamped frontend version if backend version loading fails", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());
    appApiMock.getAppVersion.mockRejectedValue(new Error("backend offline"));

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", {
        name: /Settings/,
      }),
    );
    clickSettingsSection("About");

    expect(await screen.findByText("1.0.0")).toBeInTheDocument();
    expect(screen.getByText("Unavailable")).toBeInTheDocument();
  });

  it("filters audit rows and opens audit details from Settings", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());
    settingsServiceMock.GetAuditLog.mockResolvedValue(seededAuditEntries());

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", {
        name: /Settings/,
      }),
    );
    clickSettingsSection("Security & Audit");

    expect(await screen.findByText("update.apply")).toBeInTheDocument();
    expect(screen.getByText("container.start")).toBeInTheDocument();
    await waitFor(() =>
      expect(settingsServiceMock.GetAuditLog).toHaveBeenCalledWith({
        topic: "",
        limit: 500,
      }),
    );

    fireEvent.change(screen.getByLabelText("Status"), {
      target: { value: "success" },
    });
    expect(screen.getByText("update.apply")).toBeInTheDocument();
    expect(screen.queryByText("container.start")).not.toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("Project"), {
      target: { value: "linux_native/app" },
    });
    fireEvent.change(screen.getByLabelText("Action"), {
      target: { value: "update." },
    });
    await waitFor(() =>
      expect(settingsServiceMock.GetAuditLog).toHaveBeenCalledWith({
        topic: "update.",
        limit: 500,
      }),
    );

    expect(screen.getByText("2.0 s")).toBeInTheDocument();
    fireEvent.click(
      screen.getByRole("button", { name: "View audit update.apply" }),
    );
    const dialog = await screen.findByRole("dialog", { name: "Audit row" });
    expect(
      within(dialog).getByText("docker compose up -d"),
    ).toBeInTheDocument();
    expect(within(dialog).getByText("linux_native")).toBeInTheDocument();
  });

  it("runs the macOS Colima setup branch through checks and install planning", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(macOSColimaSnapshot());
    providerServiceMock.Detect.mockResolvedValueOnce({
      ...healthyProviderStatus(),
      healthy: false,
      installed: false,
      running: false,
      dockerInstalled: true,
      dockerRunning: false,
      problems: [
        {
          code: "COLIMA_MISSING",
          message: "Colima is not installed.",
          repairHint: "Install Colima with Homebrew.",
          recoverable: true,
        },
      ],
    });
    providerServiceMock.PlanInstall.mockResolvedValue(colimaInstallPlan());
    providerServiceMock.ApplyInstall.mockResolvedValue({
      planID: "plan-colima-install",
      streamID: "setup-stream",
    });

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", {
        name: /Settings/,
      }),
    );
    fireEvent.click(
      await screen.findByRole("button", { name: "Set up new backend" }),
    );
    const dialog = await screen.findByRole("dialog", {
      name: "Set Up Docker Backend",
    });
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Get started" }),
    );
    fireEvent.click(within(dialog).getByRole("button", { name: /Colima/ }));
    fireEvent.change(within(dialog).getByLabelText("Profile"), {
      target: { value: "dev" },
    });
    fireEvent.change(within(dialog).getByLabelText("CPU"), {
      target: { value: "4" },
    });
    fireEvent.click(within(dialog).getByRole("button", { name: "Run checks" }));

    await waitFor(() =>
      expect(providerServiceMock.Detect).toHaveBeenCalledWith("macos_colima"),
    );
    expect(
      await within(dialog).findByText("Install Colima with Homebrew."),
    ).toBeInTheDocument();

    fireEvent.click(
      within(dialog).getByRole("button", { name: "Review auto repair" }),
    );
    await waitFor(() =>
      expect(providerServiceMock.PlanInstall).toHaveBeenCalledWith(
        "macos_colima",
        expect.objectContaining({
          backend: "macos_colima",
          extra: expect.objectContaining({ profile: "dev", cpu: "4" }),
        }),
      ),
    );
    expect(
      await within(dialog).findByText("brew install colima"),
    ).toBeInTheDocument();

    fireEvent.click(
      within(dialog).getByRole("button", { name: "Run auto repair" }),
    );
    await waitFor(() =>
      expect(providerServiceMock.ApplyInstall).toHaveBeenCalledWith(
        "plan-colima-install",
      ),
    );
    emitRuntimeEvent("provider:install:progress", {
      planID: "plan-colima-install",
      streamID: "setup-stream",
      step: 1,
      totalSteps: 1,
      message: "Install complete",
      done: true,
    });

    expect(
      await within(dialog).findByText("macOS Colima backend is ready"),
    ).toBeInTheDocument();
  });

  it("saves macOS Colima settings and switches existing Docker contexts", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(macOSColimaSnapshot());
    providerServiceMock.ListDockerContexts.mockResolvedValue([
      {
        name: "colima",
        description: "Colima",
        current: true,
        dockerHost: "unix:///Users/ada/.colima/default/docker.sock",
      },
      {
        name: "remote-prod",
        description: "Remote",
        current: false,
        dockerHost: "tcp://192.0.2.10:2375",
      },
    ]);

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    fireEvent.click(
      within(
        screen.getByRole("navigation", { name: "Main navigation" }),
      ).getByRole("button", {
        name: /Settings/,
      }),
    );

    expect((await screen.findAllByText("macOS Colima")).length).toBeGreaterThan(
      0,
    );
    fireEvent.change(screen.getByLabelText("Profile"), {
      target: { value: "dev" },
    });
    fireEvent.blur(screen.getByLabelText("Profile"));
    await waitFor(() =>
      expect(settingsServiceMock.SetSetting).toHaveBeenCalledWith(
        "macos.colima_profile",
        "dev",
      ),
    );

    fireEvent.change(screen.getByLabelText("CPU"), { target: { value: "4" } });
    fireEvent.blur(screen.getByLabelText("CPU"));
    await waitFor(() =>
      expect(settingsServiceMock.SetSetting).toHaveBeenCalledWith(
        "macos.colima_cpu",
        4,
      ),
    );

    clickSettingsSection("Docker contexts");
    expect(await screen.findByText("remote-prod")).toBeInTheDocument();
    expect(screen.getByText("unencrypted tcp://")).toBeInTheDocument();
    fireEvent.click(
      screen.getAllByRole("button", { name: "Use this context" })[1],
    );
    await waitFor(() =>
      expect(providerServiceMock.SetDockerContext).toHaveBeenCalledWith(
        "remote-prod",
      ),
    );
  });

  it("refreshes inventory when Docker object events arrive", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    expect(inventoryMock.getInventorySnapshot).toHaveBeenCalledTimes(1);
    vi.useFakeTimers();

    const callbacks = runtimeMock.on.mock.calls
      .filter(([name]) => name === "objects:changed")
      .map(([, callback]) => callback as (event?: unknown) => void);
    expect(callbacks).toHaveLength(1);
    act(() => {
      callbacks.forEach((callback) =>
        callback({ name: "objects:changed", data: undefined }),
      );
    });

    await act(async () => {
      await vi.advanceTimersByTimeAsync(500);
    });

    expect(inventoryMock.getInventorySnapshot).toHaveBeenCalledTimes(2);
  });

  it("uses object event kinds to refresh only the changed inventory slice", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    expect(inventoryMock.getInventorySnapshot).toHaveBeenCalledTimes(1);
    vi.useFakeTimers();

    const callback = runtimeMock.on.mock.calls.find(
      ([name]) => name === "objects:changed",
    )?.[1] as (event?: unknown) => void;

    act(() => {
      callback({
        name: "objects:changed",
        data: { kind: "image", ids: ["image-nginx"] },
      });
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(500);
    });

    expect(dockerServiceMock.ListImages).toHaveBeenCalled();
    expect(inventoryMock.getInventorySnapshot).toHaveBeenCalledTimes(1);
  });
});

function clickSettingsSection(name: string) {
  const buttons = screen.getAllByRole("button", { name });
  fireEvent.click(buttons[buttons.length - 1]);
}

function emitRuntimeEvent(eventName: string, data: unknown) {
  const callback = [...runtimeMock.on.mock.calls]
    .reverse()
    .find(([name]) => name === eventName)?.[1] as
    | ((event?: unknown) => void)
    | undefined;
  expect(callback).toEqual(expect.any(Function));
  act(() => {
    callback?.({ name: eventName, data });
  });
}

function logLine(patch: Partial<LogLine>): LogLine {
  return {
    ts: "2026-06-13T09:00:00Z",
    containerID: "container-1",
    containerName: "web",
    service: "web",
    stream: "stdout",
    level: "info",
    text: "INFO log line",
    ...patch,
  } as LogLine;
}

function statsSample(patch: Record<string, unknown> = {}) {
  return {
    providerID: "linux_native",
    projectID: "cairn",
    serviceID: "web",
    containerID: "container-1",
    containerName: "web",
    health: HealthStatus.HealthStatusHealthy,
    restartCount: 0,
    uptimeSeconds: 120,
    cpuPercent: 2.4,
    memoryBytes: 64 * 1024 * 1024,
    memoryLimitBytes: 512 * 1024 * 1024,
    networkRxBytes: 1024,
    networkTxBytes: 512,
    networkRxRate: 0,
    networkTxRate: 0,
    blockReadBytes: 0,
    blockWriteBytes: 0,
    blockReadRate: 0,
    blockWriteRate: 0,
    pids: 4,
    sampledAt: "2026-06-13T09:00:01Z",
    ...patch,
  };
}

function seededDashboardMetrics(): DashboardMetrics {
  return {
    projects: 1,
    containers: 1,
    images: 1,
    volumes: 1,
    diskUsage: seededSnapshot().diskUsage,
    gpu: seededGPUMetrics(),
    top: [
      {
        id: "container-1",
        name: "web",
        kind: "container",
        cpuPercent: 2.4,
        memoryBytes: 64 * 1024 * 1024,
      },
    ],
    recentEvents: [
      {
        id: 1,
        ts: "2026-06-13T09:00:00Z",
        actor: "cairn",
        action: "container.start",
        target: "web",
        result: "success",
      },
    ],
  } as DashboardMetrics;
}

function seededGPUMetrics(): GPUMetrics {
  return {
    available: true,
    source: "nvidia-smi",
    deviceCount: 1,
    utilizationPercent: 18,
    memoryUsedBytes: 2 * 1024 * 1024 * 1024,
    memoryTotalBytes: 8 * 1024 * 1024 * 1024,
    temperatureCelsius: 54,
    driverVersion: "555.85",
    devices: [
      {
        id: "0",
        index: 0,
        name: "NVIDIA GeForce RTX 4090",
        driverVersion: "555.85",
        utilizationPercent: 18,
        memoryUsedBytes: 2 * 1024 * 1024 * 1024,
        memoryTotalBytes: 8 * 1024 * 1024 * 1024,
        temperatureCelsius: 54,
      },
    ],
    checkedAt: "2026-06-13T09:00:00Z",
  } as GPUMetrics;
}

function seededSnapshot(): InventorySnapshot {
  const container = {
    id: "container-1",
    name: "web",
    image: "cairn/web:latest",
    imageID: "sha256:image-1",
    status: "Up 2 minutes",
    state: "running",
    health: HealthStatus.HealthStatusHealthy,
    projectID: "cairn",
    service: "web",
    ports: [
      {
        hostIP: "127.0.0.1",
        hostPort: "8080",
        containerPort: "80",
        protocol: "tcp",
      },
    ],
    cpuPercent: 2.4,
    memoryBytes: 64 * 1024 * 1024,
    memoryLimit: 512 * 1024 * 1024,
    restarts: 0,
    createdAt: "2026-06-13T08:00:00Z",
  };
  const volume = {
    name: "cairn_data",
    driver: "local",
    mountpoint: "/var/lib/docker/volumes/cairn_data/_data",
    labels: { "com.docker.compose.project": "cairn" },
    sizeBytes: 2048,
    inUse: true,
  };
  const network = {
    id: "network-1",
    name: "cairn_default",
    driver: "bridge",
    scope: "local",
    internal: false,
    attachable: true,
    labels: { "com.docker.compose.project": "cairn" },
  };

  return {
    providers: [
      {
        id: "wsl-cairn-dev",
        name: "cairn-dev",
        kind: "wsl",
        active: true,
        status: healthyProviderStatus(),
        healthy: true,
      },
    ],
    dockerInfo: {
      id: "engine-1",
      name: "cairn-dev",
      serverVersion: "26.1.0",
      storageDriver: "overlay2",
      operatingSystem: "Ubuntu 24.04",
      architecture: "x86_64",
      cpus: 8,
      memoryBytes: 8 * 1024 * 1024 * 1024,
    },
    dockerVersion: {
      clientVersion: "26.1.0",
      serverVersion: "26.1.0",
      apiVersion: "1.45",
    },
    diskUsage: {
      images: diskCategory(1, 1, 128 * 1024 * 1024),
      containers: diskCategory(1, 1, 8 * 1024 * 1024),
      volumes: diskCategory(1, 1, 2048),
      buildCache: diskCategory(0, 0, 0),
      totalBytes: 136 * 1024 * 1024,
      reclaimable: 4 * 1024 * 1024,
    },
    containers: [container],
    images: [
      {
        id: "sha256:image-1",
        repoTags: ["cairn/web:latest"],
        repoDigests: ["cairn/web@sha256:digest"],
        sizeBytes: 128 * 1024 * 1024,
        createdAt: "2026-06-12T08:00:00Z",
        inUse: true,
        updateStatus: UpdateStatus.UpdateStatusUpToDate,
      },
    ],
    volumes: [volume],
    networks: [network],
    volumeDetails: {
      [volume.name]: {
        summary: volume,
        containers: [container],
      },
    },
    networkDetails: {
      [network.id]: {
        summary: network,
        subnet: "172.19.0.0/16",
        gateway: "172.19.0.1",
        containers: [
          {
            ...container,
            networkName: network.name,
            endpointID: "endpoint-1",
            ipv4Address: "172.19.0.2/16",
            gateway: "172.19.0.1",
            macAddress: "02:42:ac:13:00:02",
            aliases: ["web", "cairn-web"],
          },
        ],
      },
    },
    degradedReason: null,
  };
}

function seedScaleSnapshot(projects: ProjectSummary[]): InventorySnapshot {
  const base = seededSnapshot();
  const containers = Array.from(
    { length: 100 },
    (_, index): ContainerSummary => {
      const project = projects[index % projects.length];
      const state =
        index % 10 === 0 ? "paused" : index % 5 < 3 ? "running" : "exited";
      return {
        id: `container-${index}`,
        name: `service-${index}`,
        image: `cairn/repo-${index % 500}:latest`,
        imageID: `sha256:image-${index % 150}`,
        status: state === "running" ? "Up 5 minutes" : state,
        state,
        health:
          index % 17 === 0
            ? HealthStatus.HealthStatusUnhealthy
            : HealthStatus.HealthStatusHealthy,
        projectID: project.id,
        service: `svc-${index % 12}`,
        ports: [],
        cpuPercent: index % 100,
        memoryBytes: (32 + index) * 1024 * 1024,
        memoryLimit: 512 * 1024 * 1024,
        restarts: index % 4,
        createdAt: `2026-06-13T08:${String(index % 60).padStart(2, "0")}:00Z`,
      };
    },
  );
  const images = Array.from(
    { length: 500 },
    (_, index): ImageSummary => ({
      id: `sha256:image-${index}`,
      repoTags: [`cairn/repo-${index}:latest`],
      repoDigests: [`cairn/repo-${index}@sha256:digest-${index}`],
      sizeBytes: (16 + index) * 1024 * 1024,
      createdAt: `2026-06-12T${String(index % 24).padStart(2, "0")}:00:00Z`,
      inUse: index < 150,
      updateStatus:
        index % 25 === 0
          ? UpdateStatus.UpdateStatusServiceImageUpdateAvailable
          : UpdateStatus.UpdateStatusUnknown,
    }),
  );
  const volumes = Array.from(
    { length: 200 },
    (_, index): VolumeSummary => ({
      name: `volume-${index}`,
      driver: "local",
      mountpoint: `/var/lib/docker/volumes/volume-${index}/_data`,
      labels: {
        "com.docker.compose.project": projects[index % projects.length].name,
      },
      sizeBytes: index * 4096,
      inUse: index % 2 === 0,
    }),
  );
  const networks = Array.from(
    { length: 20 },
    (_, index): NetworkSummary => ({
      id: `network-${index}`,
      name: `network-${index}`,
      driver: "bridge",
      scope: "local",
      internal: index % 5 === 0,
      attachable: true,
      labels: {
        "com.docker.compose.project": projects[index % projects.length].name,
      },
    }),
  );

  return {
    ...base,
    diskUsage: {
      images: diskCategory(images.length, 150, 12 * 1024 * 1024 * 1024),
      containers: diskCategory(containers.length, 60, 2 * 1024 * 1024 * 1024),
      volumes: diskCategory(volumes.length, 100, 6 * 1024 * 1024 * 1024),
      buildCache: diskCategory(12, 0, 512 * 1024 * 1024),
      totalBytes: 20 * 1024 * 1024 * 1024,
      reclaimable: 4 * 1024 * 1024 * 1024,
    },
    containers,
    images,
    volumes,
    networks,
    volumeDetails: {},
    networkDetails: {},
  };
}

function seedScaleProjects(): ProjectSummary[] {
  return Array.from(
    { length: 10 },
    (_, index): ProjectSummary => ({
      ...seededProject(),
      id: `linux_native/project-${index}`,
      name: `project-${index}`,
      status:
        index % 3 === 0
          ? ProjectStatus.ProjectStatusPartial
          : ProjectStatus.ProjectStatusRunning,
      health:
        index % 4 === 0
          ? HealthStatus.HealthStatusUnhealthy
          : HealthStatus.HealthStatusHealthy,
      servicesRunning: index % 3 === 0 ? 8 : 12,
      servicesTotal: 12,
      cpuPercent: index === 9 ? 92 : 10 + index,
      memoryBytes: (256 + index * 32) * 1024 * 1024,
      workingDir: `/home/cairn/projects/project-${index}`,
      lastChangedAt: `2026-06-13T09:${String(index).padStart(2, "0")}:00Z`,
    }),
  );
}

function seedScaleDashboardMetrics(
  snapshot: InventorySnapshot,
  projects: ProjectSummary[],
): DashboardMetrics {
  return {
    projects: projects.length,
    containers: snapshot.containers.length,
    images: snapshot.images.length,
    volumes: snapshot.volumes.length,
    diskUsage: snapshot.diskUsage ?? seededSnapshot().diskUsage,
    gpu: seededGPUMetrics(),
    top: snapshot.containers.slice(0, 10).map((container) => ({
      id: container.id,
      name: container.name,
      kind: "container",
      cpuPercent: container.cpuPercent ?? 0,
      memoryBytes: container.memoryBytes ?? 0,
    })),
    recentEvents: [],
  } as DashboardMetrics;
}

function seedScaleImageUseCounts(containers: ContainerSummary[]) {
  return containers.reduce<Record<string, number>>((counts, container) => {
    if (container.imageID) {
      counts[container.imageID] = (counts[container.imageID] ?? 0) + 1;
    }
    return counts;
  }, {});
}

function seededProject(): ProjectSummary {
  return {
    id: "linux_native/app-db",
    name: "app-db",
    providerID: "linux_native",
    status: ProjectStatus.ProjectStatusRunning,
    health: HealthStatus.HealthStatusHealthy,
    servicesRunning: 1,
    servicesTotal: 2,
    cpuPercent: 12.5,
    memoryBytes: 256 * 1024 * 1024,
    netRxRate: 0,
    netTxRate: 0,
    updateBadges: {
      imageUpdates: 2,
      baseUpdates: 0,
      rebuildNeeded: 0,
      pinned: 0,
      unknownBase: 0,
    },
    ports: [
      {
        hostIP: "127.0.0.1",
        hostPort: "8080",
        containerPort: "80",
        protocol: "tcp",
      },
    ],
    workingDir:
      "E:\\Development\\projects\\apps\\rcooler\\Cairn\\testdata\\projects\\app-db",
    lastChangedAt: "2026-06-13T08:00:00Z",
  };
}

function seededBrokenProject(): ProjectSummary {
  return {
    ...seededProject(),
    id: "linux_native/missing-workdir",
    name: "missing-workdir",
    status: ProjectStatus.ProjectStatusError,
    health: HealthStatus.HealthStatusUnknown,
    servicesRunning: 0,
    servicesTotal: 1,
    cpuPercent: 0,
    memoryBytes: 0,
    ports: [],
    workingDir:
      "E:\\Development\\projects\\apps\\rcooler\\Cairn\\.scratch\\missing-workdir",
  };
}

function seededProjectDetail(): ProjectDetail {
  return {
    summary: seededProject(),
    services: [
      {
        name: "app",
        image: "cairn/app:latest",
        replicas: 1,
        running: 1,
        status: ProjectStatus.ProjectStatusRunning,
        health: HealthStatus.HealthStatusHealthy,
        ports: [
          {
            hostIP: "127.0.0.1",
            hostPort: "8080",
            containerPort: "80",
            protocol: "tcp",
          },
        ],
        cpuPercent: 10,
        memoryBytes: 128 * 1024 * 1024,
      },
      {
        name: "db",
        image: "postgres:16",
        replicas: 1,
        running: 0,
        status: ProjectStatus.ProjectStatusStopped,
        health: HealthStatus.HealthStatusUnknown,
      },
    ],
    containers: [
      {
        id: "container-app",
        name: "container-app",
        image: "cairn/app:latest",
        imageID: "sha256:image-app",
        status: "running",
        state: "running",
        health: HealthStatus.HealthStatusHealthy,
        projectID: "linux_native/app-db",
        service: "app",
        ports: [
          {
            hostIP: "127.0.0.1",
            hostPort: "8080",
            containerPort: "80",
            protocol: "tcp",
          },
        ],
        cpuPercent: 5,
        memoryBytes: 64 * 1024 * 1024,
        restarts: 0,
        createdAt: "2026-06-13T08:00:00Z",
      },
    ],
    compose: {
      rawFiles: [
        {
          path: "E:\\Development\\projects\\apps\\rcooler\\Cairn\\testdata\\projects\\app-db\\compose.yaml",
          content: "services:\n  app:\n    image: cairn/app:latest\n",
        },
      ],
      resolvedYAML:
        "services:\n  app:\n    image: cairn/app:latest\n  db:\n    image: postgres:16\n",
      envFiles: [
        "E:\\Development\\projects\\apps\\rcooler\\Cairn\\testdata\\projects\\app-db\\.env",
      ],
      valid: true,
      errors: [],
    },
  };
}

function seededContainerDetail(): ContainerDetail {
  const container = seededProjectDetail().containers?.[0] as ContainerSummary;
  return {
    summary: container,
    command: ["node", "server.js"],
    entrypoint: ["docker-entrypoint.sh"],
    env: [
      { name: "NODE_ENV", value: "production" },
      { name: "API_TOKEN", value: "[redacted]" },
    ],
    mounts: [
      {
        type: "bind",
        source: "/srv/app",
        target: "/app",
        readOnly: false,
      },
    ],
    networks: ["app-db_default"],
    labels: { "com.docker.compose.service": "app" },
    workingDir: "/app",
    user: "node",
    restartPolicy: "unless-stopped",
  };
}

function seededContainerFiles(): ContainerFileListing {
  return {
    containerID: "container-app",
    path: "/",
    entries: [
      {
        name: "app",
        path: "/app",
        type: "directory",
        mode: "drwxr-xr-x",
        modifiedAt: "2026-06-13T08:00:00Z",
      },
      {
        name: "app.log",
        path: "/app.log",
        type: "file",
        sizeBytes: 42,
        mode: "-rw-r--r--",
        modifiedAt: "2026-06-13T08:05:00Z",
      },
    ],
  };
}

function seededTerminalSession(
  patch: Partial<TerminalSessionInfo> = {},
): TerminalSessionInfo {
  return {
    id: "terminal-1",
    kind: "host",
    title: "Host",
    shell: "sh",
    isRoot: false,
    createdAt: "2026-06-13T08:00:00Z",
    ...patch,
  };
}

function seededCheatsheet(): CheatsheetEntry[] {
  return [
    {
      category: "containers",
      command: "docker ps",
      description: "List running containers",
      risk: Risk.RiskSafe,
      runnable: true,
    },
    {
      category: "cleanup",
      command: "docker system prune",
      description: "Remove unused Docker data",
      risk: Risk.RiskDangerous,
      runnable: false,
    },
  ];
}

function seededNotifications(): Notification[] {
  return [
    {
      id: 1,
      level: "warn",
      title: "Provider degraded",
      body: "Docker daemon stopped",
      topic: "provider",
      read: false,
      createdAt: "2026-06-13T09:00:00Z",
    },
    {
      id: 2,
      level: "info",
      title: "Update check complete",
      body: "No updates found",
      topic: "update",
      read: true,
      createdAt: "2026-06-13T08:55:00Z",
    },
  ];
}

function seededAuditEntries() {
  return [
    {
      id: 10,
      ts: "2026-06-13T09:00:00Z",
      action: "update.apply",
      target: "linux_native/app",
      result: "success",
      metadata: {
        command: "docker compose up -d",
        durationMS: 2000,
        projectID: "linux_native/app",
        providerID: "linux_native",
        risk: Risk.RiskNeedsConfirmation,
        targetType: "project",
      },
    },
    {
      id: 9,
      ts: "2026-06-13T08:55:00Z",
      action: "container.start",
      target: "web-1",
      result: "started",
      metadata: {
        command: "docker start web-1",
        projectID: "linux_native/app",
        risk: Risk.RiskSafe,
        targetType: "container",
      },
    },
  ];
}

function seededUpdates(): ImageUpdate[] {
  return [
    {
      id: 101,
      projectID: "linux_native/app-db",
      service: "app",
      containerID: "container-app",
      kind: UpdateKind.UpdateKindServiceImage,
      status: UpdateStatus.UpdateStatusServiceImageUpdateAvailable,
      currentImage: "cairn/app:latest",
      localDigest: "sha256:aaa111",
      remoteDigest: "sha256:bbb222",
      confidence: Confidence.ConfidenceHigh,
      recommendedAction: RecommendedAction.RecommendedActionPullRecreate,
      checkedAt: "2026-06-13T09:00:00Z",
      notes: ["Mutable tag warning"],
    },
    {
      id: 102,
      projectID: "linux_native/app-db",
      service: "worker",
      containerID: "container-worker",
      kind: UpdateKind.UpdateKindBaseImage,
      status: UpdateStatus.UpdateStatusRebuildRequired,
      currentImage: "cairn/worker:local",
      baseImage: "node:20-alpine",
      localDigest: "sha256:ccc333",
      remoteDigest: "sha256:ddd444",
      confidence: Confidence.ConfidenceHigh,
      recommendedAction: RecommendedAction.RecommendedActionRebuildRedeploy,
      checkedAt: "2026-06-13T09:01:00Z",
    },
    {
      id: 103,
      projectID: "linux_native/app-db",
      service: "third-party",
      kind: UpdateKind.UpdateKindBaseImage,
      status: UpdateStatus.UpdateStatusUnknownBaseImage,
      currentImage: "postgres:16",
      confidence: Confidence.ConfidenceUnknown,
      recommendedAction: RecommendedAction.RecommendedActionManual,
      checkedAt: "2026-06-13T09:02:00Z",
    },
  ] as ImageUpdate[];
}

function ignoredUpdate(): ImageUpdate {
  return {
    ...seededUpdates()[0],
    id: 201,
    status: UpdateStatus.UpdateStatusIgnored,
    notes: ["Waiting for maintenance window"],
  };
}

function seededLineage(): ImageLineage[] {
  return [
    {
      projectID: "linux_native/app-db",
      service: "worker",
      containerID: "container-worker",
      imageRef: "cairn/worker:local",
      imageID: "sha256:image-worker",
      baseImage: "node:20-alpine",
      baseDigest: "sha256:ccc333",
      source: "compose_dockerfile",
      confidence: Confidence.ConfidenceHigh,
      reason: "from Compose build config and Dockerfile",
    },
  ] as ImageLineage[];
}

function updateHistoryRow(): UpdateHistoryItem {
  return {
    id: 301,
    projectID: "linux_native/app-db",
    service: "app",
    kind: UpdateKind.UpdateKindServiceImage,
    result: "success",
    startedAt: "2026-06-13T09:05:00Z",
    finishedAt: "2026-06-13T09:06:00Z",
    rollbackStatus: "available",
  } as UpdateHistoryItem;
}

function seededBackup(): BackupSummary {
  return {
    id: "backup-1",
    providerID: "linux_native",
    volumeName: "cairn_data",
    projectID: "linux_native/app-db",
    path: "/tmp/cairn-backups/cairn_data-20260613T080000Z.tar.gz",
    metadataPath: "/tmp/cairn-backups/cairn_data-20260613T080000Z.json",
    sizeBytes: 4096,
    result: "success",
    createdAt: "2026-06-13T08:00:00Z",
  } as BackupSummary;
}

function updatePlan(): UpdatePlan {
  return {
    planID: "plan-update-app",
    projectID: "linux_native/app-db",
    items: [
      {
        service: "app",
        kind: UpdateKind.UpdateKindServiceImage,
        currentImage: "cairn/app:latest",
        localDigest: "sha256:aaa111",
        remoteDigest: "sha256:bbb222",
        confidence: Confidence.ConfidenceHigh,
        action: RecommendedAction.RecommendedActionPullRecreate,
      },
    ],
    commands: [
      {
        order: 1,
        command: "docker compose pull app",
        risk: Risk.RiskNeedsConfirmation,
        explanation: "Pull updated service image.",
      },
      {
        order: 2,
        command: "docker compose up -d app",
        risk: Risk.RiskNeedsConfirmation,
        explanation: "Recreate service with the pulled image.",
      },
    ],
    warnings: [],
  };
}

function rollbackPlan(): UpdatePlan {
  return {
    planID: "rollback-plan-app",
    projectID: "linux_native/app-db",
    items: [
      {
        service: "app",
        kind: UpdateKind.UpdateKindServiceImage,
        currentImage: "cairn/app:latest",
        localDigest: "sha256:new",
        remoteDigest: "sha256:old",
        confidence: Confidence.ConfidenceMedium,
        action: RecommendedAction.RecommendedActionManual,
      },
    ],
    commands: [
      {
        order: 1,
        command: "docker tag sha256:old cairn/app:latest",
        risk: Risk.RiskNeedsConfirmation,
        explanation:
          "Retags the previous local image back to the service image reference.",
      },
      {
        order: 2,
        command: "docker compose up -d app",
        risk: Risk.RiskNeedsConfirmation,
        explanation: "Recreates the service with the restored image reference.",
      },
    ],
    warnings: [
      "Rollback will retag the previous local image and recreate the service.",
    ],
  };
}

function updateProjectPlan(): UpdatePlan {
  return {
    planID: "plan-update-project",
    projectID: "linux_native/app-db",
    items: updatePlan().items.concat([
      {
        service: "worker",
        kind: UpdateKind.UpdateKindBaseImage,
        currentImage: "cairn/worker:local",
        baseImage: "node:20-alpine",
        localDigest: "sha256:ccc333",
        remoteDigest: "sha256:ddd444",
        confidence: Confidence.ConfidenceHigh,
        action: RecommendedAction.RecommendedActionRebuildRedeploy,
      },
    ]),
    commands: [
      {
        order: 1,
        command: "docker compose pull app",
        risk: Risk.RiskNeedsConfirmation,
        explanation: "Pull updated service images.",
      },
      {
        order: 2,
        command: "docker compose build --pull worker",
        risk: Risk.RiskNeedsConfirmation,
        explanation: "Rebuild services from newer base images.",
      },
      {
        order: 3,
        command: "docker compose up -d app worker",
        risk: Risk.RiskNeedsConfirmation,
        explanation: "Recreate changed services.",
      },
    ],
    warnings: ["third-party: base image unknown"],
  };
}

function backupPlan(): CommandPlan {
  return {
    planID: "plan-backup-volume",
    title: "Back up cairn_data",
    risk: Risk.RiskNeedsConfirmation,
    commands: [
      {
        order: 1,
        command:
          "docker run --rm -v cairn_data:/source:ro -v /tmp/cairn-backups:/backup alpine:3 tar czf /backup/cairn_data-20260613T080000Z.tar.gz -C /source .",
        risk: Risk.RiskNeedsConfirmation,
        explanation:
          "Runs a helper container to archive the selected named volume.",
      },
    ],
    effects: [
      "cairn_data: Creates a compressed tar.gz backup and JSON sidecar.",
    ],
    expiresAt: "2026-06-13T08:10:00Z",
  };
}

function agentFileEditPlan(): CommandPlan {
  return {
    planID: "plan-agent-file-env",
    title: "Update .env",
    risk: Risk.RiskNeedsConfirmation,
    commands: [
      {
        order: 1,
        command: "write .env",
        risk: Risk.RiskNeedsConfirmation,
        explanation: "Apply an agent-drafted project configuration edit.",
      },
    ],
    effects: ["Update .env", "Write 14 bytes"],
    expiresAt: "2026-06-13T08:10:00Z",
  };
}

function deleteBackupPlan(): CommandPlan {
  return {
    planID: "plan-delete-backup",
    title: "Delete backup backup-1",
    risk: Risk.RiskNeedsConfirmation,
    commands: [
      {
        order: 1,
        command:
          "delete backup /tmp/cairn-backups/cairn_data-20260613T080000Z.tar.gz",
        risk: Risk.RiskNeedsConfirmation,
        explanation:
          "Deletes the selected backup archive and metadata from disk.",
      },
    ],
    effects: [
      "Backup backup-1 will be removed from Cairn and deleted from disk.",
    ],
    expiresAt: "2026-06-13T08:10:00Z",
  };
}

function restorePlan(): CommandPlan {
  return {
    planID: "plan-restore-volume",
    title: "Restore cairn_data",
    risk: Risk.RiskDangerous,
    commands: [
      {
        order: 1,
        command:
          'docker run --rm -v cairn_data:/restore -v /tmp/cairn-backups:/backup:ro alpine:3 sh -c \'set -eu; archive=$1; stash=/restore/.cairn-restore-old-$$; mkdir "$stash"; move existing contents to stash; tar xzf "$archive" -C /restore || rollback stash\' cairn-restore /backup/cairn_data-20260613T080000Z.tar.gz',
        risk: Risk.RiskDangerous,
        explanation:
          "Moves existing contents aside, restores files from the selected archive, and rolls back the original contents if extraction fails.",
      },
    ],
    effects: ["cairn_data: Replaces volume contents with the selected backup."],
    requiresTypedName: "cairn_data",
    expiresAt: "2026-06-13T08:10:00Z",
  };
}

function pushImagePlan(): CommandPlan {
  return {
    planID: "push-image-plan",
    title: "Push image redis:7",
    risk: Risk.RiskNeedsConfirmation,
    commands: [
      {
        order: 1,
        command: "docker push redis:7",
        risk: Risk.RiskNeedsConfirmation,
        explanation: "Publishes the selected image reference to its registry.",
      },
    ],
    effects: [
      "Image redis:7 will be uploaded to the configured registry if credentials allow it.",
    ],
    expiresAt: "2026-06-13T08:10:00Z",
  };
}

function runImagePlan(risk: Risk = Risk.RiskSafe): CommandPlan {
  return {
    planID: "run-image-plan",
    title: "Run image cairn/web:latest",
    risk,
    requiresTypedName: risk === Risk.RiskDangerous ? "web" : undefined,
    commands: [
      {
        order: 1,
        command:
          "docker run -d --name web --mount type=volume,source=myvol,target=/data,rw cairn/web:latest",
        risk,
        explanation: "Creates a container from the selected image.",
      },
    ],
    effects: ["Container web will be created from cairn/web:latest."],
    expiresAt: "2026-06-13T08:10:00Z",
  };
}

function killPlan(): CommandPlan {
  return {
    planID: "plan-kill-web",
    title: "Kill web",
    risk: Risk.RiskNeedsConfirmation,
    commands: [
      {
        order: 1,
        command: "docker kill web",
        risk: Risk.RiskNeedsConfirmation,
        explanation: "Immediately sends SIGKILL to the selected container.",
      },
    ],
    effects: ["web: Immediately sends SIGKILL to the selected container."],
    expiresAt: "2026-06-13T08:10:00Z",
  };
}

function removeImagePlan(): CommandPlan {
  return {
    planID: "plan-remove-image",
    title: "Remove image cairn/web:latest",
    risk: Risk.RiskDestructive,
    commands: [
      {
        order: 1,
        command: "docker image rm --force cairn/web:latest",
        risk: Risk.RiskDestructive,
        explanation: "Removes the selected image from the Docker backend.",
      },
    ],
    effects: [
      "Image cairn/web:latest will be removed from the active Docker backend.",
    ],
    expiresAt: "2026-06-13T08:10:00Z",
  };
}

function prunePlan(kind: string): CommandPlan {
  const dangerous = kind === "volumes";
  return {
    planID: `plan-prune-${kind}`,
    title: `Prune ${kind}`,
    risk: dangerous ? Risk.RiskDangerous : Risk.RiskDestructive,
    commands: [
      {
        order: 1,
        command:
          kind === "build-cache"
            ? "docker builder prune"
            : `docker ${kind.slice(0, -1)} prune`,
        risk: dangerous ? Risk.RiskDangerous : Risk.RiskDestructive,
        explanation: `Prune ${kind}`,
      },
    ],
    effects: [`Unused ${kind} will be removed.`],
    requiresTypedName: dangerous ? "prune" : undefined,
    expiresAt: "2026-06-13T08:10:00Z",
  };
}

function removeVolumePlan(): CommandPlan {
  return {
    planID: "plan-remove-volume",
    title: "Delete volume cairn_data",
    risk: Risk.RiskDangerous,
    commands: [
      {
        order: 1,
        command: "docker volume rm cairn_data",
        risk: Risk.RiskDangerous,
        explanation: "Deletes the selected Docker volume.",
      },
    ],
    effects: [
      "Volume cairn_data and its data will be deleted from the active Docker backend.",
    ],
    requiresTypedName: "cairn_data",
    expiresAt: "2026-06-13T08:10:00Z",
  };
}

function removeNetworkPlan(): CommandPlan {
  return {
    planID: "plan-remove-network",
    title: "Remove network cairn",
    risk: Risk.RiskNeedsConfirmation,
    commands: [
      {
        order: 1,
        command: "docker network rm cairn",
        risk: Risk.RiskNeedsConfirmation,
        explanation: "Removes the selected Docker network.",
      },
    ],
    effects: ["Network cairn will be removed from the active Docker backend."],
    expiresAt: "2026-06-13T08:10:00Z",
  };
}

function providerRestartPlan(): CommandPlan {
  return {
    planID: "plan-provider-restart",
    title: "Restart Docker backend",
    risk: Risk.RiskDestructive,
    commands: [
      {
        order: 1,
        command: "systemctl --user restart docker",
        risk: Risk.RiskDestructive,
        explanation: "Restart the active Docker backend.",
      },
    ],
    effects: ["Docker backend will be restarted."],
    expiresAt: "2026-06-13T08:10:00Z",
  };
}

function projectRedeployPlan(): CommandPlan {
  return {
    planID: "plan-redeploy",
    title: "Redeploy app-db",
    risk: Risk.RiskDestructive,
    commands: [
      {
        order: 1,
        command: "docker compose -f compose.yaml up -d --force-recreate",
        workingDir:
          "E:\\Development\\projects\\apps\\rcooler\\Cairn\\testdata\\projects\\app-db",
        risk: Risk.RiskDestructive,
        explanation:
          "Runs docker compose up -d --force-recreate for the project.",
      },
    ],
    effects: [
      "app-db: Runs docker compose up -d --force-recreate for the project.",
    ],
    expiresAt: "2026-06-13T08:10:00Z",
  };
}

function projectDownVolumesPlan(): CommandPlan {
  return {
    planID: "plan-down-volumes",
    title: "Down app-db with volumes",
    risk: Risk.RiskDangerous,
    commands: [
      {
        order: 1,
        command: "docker compose -f compose.yaml down --volumes",
        workingDir:
          "E:\\Development\\projects\\apps\\rcooler\\Cairn\\testdata\\projects\\app-db",
        risk: Risk.RiskDangerous,
        explanation:
          "Runs docker compose down --volumes and removes named volumes declared by the project.",
      },
    ],
    effects: [
      "app-db: Runs docker compose down --volumes and removes named volumes declared by the project.",
    ],
    requiresTypedName: "app-db",
    expiresAt: "2026-06-13T08:10:00Z",
  };
}

function wslInstallPlan(): CommandPlan {
  return {
    planID: "plan-wsl-install",
    title: "Install or update Docker Engine in Ubuntu on WSL",
    risk: Risk.RiskNeedsConfirmation,
    commands: [
      {
        order: 1,
        command: "wsl.exe --install Ubuntu --name cairn-dev",
        risk: Risk.RiskNeedsConfirmation,
        explanation: "Install the selected Ubuntu WSL distribution",
      },
    ],
    effects: ["Install Ubuntu and Docker Engine inside WSL."],
    expiresAt: "2026-06-13T08:10:00Z",
  };
}

function colimaInstallPlan(): CommandPlan {
  return {
    planID: "plan-colima-install",
    title: "Install or update Colima backend",
    risk: Risk.RiskNeedsConfirmation,
    commands: [
      {
        order: 1,
        command: "brew install colima",
        risk: Risk.RiskNeedsConfirmation,
        explanation: "Install Colima with Homebrew",
      },
    ],
    effects: ["Install Colima and verify hello-world."],
    expiresAt: "2026-06-13T08:10:00Z",
  };
}

function linuxInstallPlan(): CommandPlan {
  return {
    planID: "plan-linux-install",
    title: "Install or update Docker Engine on Linux",
    risk: Risk.RiskNeedsConfirmation,
    commands: [
      {
        order: 1,
        command: "sudo apt-get update",
        risk: Risk.RiskNeedsConfirmation,
        explanation: "Refresh apt package indexes",
      },
    ],
    effects: ["Install Docker Engine and verify hello-world."],
    expiresAt: "2026-06-13T08:10:00Z",
  };
}

function emptySnapshot(): InventorySnapshot {
  return {
    ...seededSnapshot(),
    diskUsage: {
      images: diskCategory(0, 0, 0),
      containers: diskCategory(0, 0, 0),
      volumes: diskCategory(0, 0, 0),
      buildCache: diskCategory(0, 0, 0),
      totalBytes: 0,
      reclaimable: 0,
    },
    containers: [],
    images: [],
    volumes: [],
    networks: [],
    volumeDetails: {},
    networkDetails: {},
  };
}

function macOSColimaSnapshot(): InventorySnapshot {
  const snapshot = seededSnapshot();
  return {
    ...snapshot,
    providers: [
      {
        id: "macos_colima",
        name: "macOS Colima",
        kind: "macos_colima",
        active: true,
        status: {
          ...healthyProviderStatus(),
          currentContext: "colima",
          dockerHost: "unix:///Users/ada/.colima/default/docker.sock",
        },
        healthy: true,
      },
    ],
  };
}

function noProviderSnapshot(): InventorySnapshot {
  return {
    ...emptySnapshot(),
    providers: [],
    dockerInfo: null,
    dockerVersion: null,
    diskUsage: null,
    degradedReason: "No Docker provider configured",
  };
}

function stoppedDaemonSnapshot(): InventorySnapshot {
  const snapshot = seededSnapshot();
  const provider = snapshot.providers[0]!;
  return {
    ...snapshot,
    dockerInfo: null,
    dockerVersion: null,
    degradedReason: "Docker daemon ping failed",
    providers: [
      {
        ...provider,
        id: "linux_native",
        name: "Linux Native",
        kind: "linux_native",
        status: {
          ...healthyProviderStatus(),
          running: false,
          healthy: false,
          dockerRunning: false,
          dockerVersion: "",
          problems: [],
        },
        healthy: false,
      },
    ],
  };
}

function permissionDeniedSnapshot(): InventorySnapshot {
  const snapshot = stoppedDaemonSnapshot();
  const provider = snapshot.providers[0]!;
  return {
    ...snapshot,
    degradedReason: "permission denied while connecting to Docker socket",
    providers: [
      {
        ...provider,
        status: {
          ...provider.status,
          installed: true,
          dockerInstalled: true,
          composeInstalled: true,
          buildxInstalled: true,
          problems: [
            {
              code: "PERM_SOCKET",
              message: "Cairn cannot access the Docker socket.",
              repairHint:
                "Choose sudo-per-action in Settings or add your Linux user to the docker group, then sign out and back in.",
              recoverable: true,
            },
          ],
        },
      },
    ],
  };
}

function diskCategory(
  count: number,
  active: number,
  sizeBytes: number,
): DiskUsageCategory {
  return {
    count,
    active,
    sizeBytes,
    reclaimable: 0,
  };
}

function healthyProviderStatus(): ProviderStatus {
  return {
    installed: true,
    running: true,
    healthy: true,
    dockerInstalled: true,
    dockerRunning: true,
    composeInstalled: true,
    buildxInstalled: true,
    dockerVersion: "26.1.0",
    composeVersion: "v2.27.0",
    currentContext: "default",
  };
}
