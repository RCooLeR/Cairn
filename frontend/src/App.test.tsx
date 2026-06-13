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
  ContainerSummary,
  CommandPlan,
  DashboardMetrics,
  DiskUsageCategory,
  CheatsheetEntry,
  ImageSummary,
  LogLine,
  NetworkSummary,
  Notification,
  ProjectDetail,
  ProjectSummary,
  ProviderStatus,
  TerminalSessionInfo,
  VolumeSummary,
} from "../bindings/github.com/RCooLeR/Cairn/internal/models/models.js";
import {
  HealthStatus,
  ProjectStatus,
  Risk,
  UpdateStatus,
} from "../bindings/github.com/RCooLeR/Cairn/internal/models/models.js";

import App, {
  filterContainers,
  filterImages,
  filterNetworks,
  filterProjects,
  filterVolumes,
} from "./App";
import { useAppStore } from "./state/appStore";
import { useInventoryStore } from "./state/inventoryStore";

const inventoryMock = vi.hoisted(() => ({
  getInventorySnapshot: vi.fn<() => Promise<InventorySnapshot>>(),
}));

const runtimeMock = vi.hoisted(() => ({
  on: vi.fn<
    (eventName: string, callback: (event?: unknown) => void) => () => void
  >(() => vi.fn()),
  openFile: vi.fn(),
  saveFile: vi.fn(),
  setClipboardText: vi.fn(),
}));

const dockerServiceMock = vi.hoisted(() => ({
  InspectContainerRaw: vi.fn(),
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
  PullImage: vi.fn(),
  SaveImage: vi.fn(),
  LoadImage: vi.fn(),
  SearchHub: vi.fn(),
  CreateVolume: vi.fn(),
  CreateNetwork: vi.fn(),
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
}));

const providerServiceMock = vi.hoisted(() => ({
  Detect: vi.fn(),
  ListDockerContexts: vi.fn(),
  PlanInstall: vi.fn(),
  ApplyInstall: vi.fn(),
  SetDockerContext: vi.fn(),
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
  GetNotifications: vi.fn(),
  MarkNotificationsRead: vi.fn(),
  GetCheatsheet: vi.fn(),
}));

vi.mock("./api/app", () => ({
  getAppVersion: vi.fn().mockResolvedValue({
    version: "0.1.0",
    goVersion: "go1.26.4",
  }),
}));

vi.mock("./api/inventory", () => ({
  getInventorySnapshot: inventoryMock.getInventorySnapshot,
}));

vi.mock("./api/services", () => ({
  DockerService: dockerServiceMock,
  LogsService: logsServiceMock,
  MetricsService: metricsServiceMock,
  ProviderService: providerServiceMock,
  ProjectService: projectServiceMock,
  SettingsService: settingsServiceMock,
  TerminalService: terminalServiceMock,
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
    SaveFile: runtimeMock.saveFile,
  },
  Clipboard: {
    SetText: runtimeMock.setClipboardText,
  },
}));

describe("App inventory shell", () => {
  beforeEach(() => {
    vi.useRealTimers();
    vi.clearAllMocks();
    inventoryMock.getInventorySnapshot.mockReset();
    dockerServiceMock.InspectContainerRaw.mockResolvedValue(
      '{"Id":"container-1"}',
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
    dockerServiceMock.PullImage.mockResolvedValue("pull-stream");
    dockerServiceMock.SaveImage.mockResolvedValue("save-job");
    dockerServiceMock.LoadImage.mockResolvedValue("load-job");
    dockerServiceMock.SearchHub.mockResolvedValue([]);
    dockerServiceMock.CreateVolume.mockResolvedValue({
      name: "created_volume",
      driver: "local",
      inUse: false,
    });
    dockerServiceMock.CreateNetwork.mockResolvedValue({
      id: "network-new",
      name: "created_network",
      driver: "bridge",
      internal: false,
      attachable: false,
    });
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
    providerServiceMock.Detect.mockResolvedValue(healthyProviderStatus());
    providerServiceMock.ListDockerContexts.mockResolvedValue([
      {
        name: "desktop-linux",
        description: "Docker Desktop",
        current: true,
        dockerHost: "unix:///var/run/docker.sock",
      },
    ]);
    providerServiceMock.PlanInstall.mockResolvedValue(wslInstallPlan());
    providerServiceMock.ApplyInstall.mockResolvedValue({
      planID: "plan-wsl-install",
      streamID: "setup-stream",
    });
    providerServiceMock.SetDockerContext.mockResolvedValue(undefined);
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
      "linux.sudo_mode": "ask",
      "provider.autostart_backend": true,
      "windows.wsl_distro": "Ubuntu",
      "macos.colima_profile": "default",
      "macos.colima_cpu": 2,
      "macos.colima_memory_gb": 4,
      "macos.colima_disk_gb": 60,
    });
    settingsServiceMock.SetSetting.mockResolvedValue(undefined);
    settingsServiceMock.GetNotifications.mockResolvedValue([]);
    settingsServiceMock.MarkNotificationsRead.mockResolvedValue(undefined);
    settingsServiceMock.GetCheatsheet.mockResolvedValue(seededCheatsheet());
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
  });

  afterEach(() => {
    vi.useRealTimers();
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
    expect(await screen.findByText("v0.1.0")).toBeInTheDocument();
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

    fireEvent.click(bell);
    fireEvent.click(
      await screen.findByRole("button", { name: "Mark all read" }),
    );
    await waitFor(() =>
      expect(settingsServiceMock.MarkNotificationsRead).toHaveBeenCalledWith(
        [],
      ),
    );
  });

  it("updates dashboard charts from stats samples and deep-links container filters", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());
    projectServiceMock.RefreshProjects.mockResolvedValue([seededProject()]);

    render(<App />);

    await screen.findByText("Docker Engine - Running");
    await waitFor(() =>
      expect(metricsServiceMock.StartStatsStream).toHaveBeenCalled(),
    );

    emitRuntimeEvent("stats:sample", {
      streamID: "stats-stream-1",
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
    expect(screen.getByText("Top Containers")).toBeInTheDocument();

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

  it("opens terminal sessions from the Terminal page", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());
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
      within(dialog).getByRole("button", { name: "Confirm preview" }),
    ).toBeDisabled();
    fireEvent.change(
      within(dialog).getByLabelText("Type DELETE VOLUMES to confirm"),
      {
        target: { value: "DELETE VOLUMES" },
      },
    );

    expect(
      within(dialog).getByRole("button", { name: "Confirm preview" }),
    ).toBeEnabled();
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

  it("opens project detail tabs with services, containers, and Compose config", async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());
    projectServiceMock.RefreshProjects.mockResolvedValue([seededProject()]);
    projectServiceMock.GetProject.mockResolvedValue(seededProjectDetail());

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

    fireEvent.click(screen.getByRole("button", { name: "Compose" }));
    expect(screen.getByText("valid")).toBeInTheDocument();
    expect(screen.getByTestId("monaco-viewer")).toHaveTextContent("services:");
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
        logLine({
          level: "error",
          stream: "stderr",
          text: "ERROR failed request",
        }),
      ],
    });

    expect(await screen.findByText(/server started/)).toBeInTheDocument();
    expect(screen.getByText(/failed request/)).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("Search logs"), {
      target: { value: "failed" },
    });
    await waitFor(() => expect(screen.getByText("1/1")).toBeInTheDocument());
    fireEvent.click(screen.getByLabelText("Matches only"));

    expect(screen.queryByText(/server started/)).not.toBeInTheDocument();
    expect(screen.getByText(/request/)).toBeInTheDocument();
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
    expect(screen.getByText(/docker run -d/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Run" }));

    await waitFor(() =>
      expect(dockerServiceMock.RunImage).toHaveBeenCalledWith(
        expect.objectContaining({
          imageRef: "cairn/web:latest",
          name: "web",
          detach: true,
          pullIfMissing: true,
        }),
      ),
    );
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

  it("pulls, saves, and loads images from image modals", async () => {
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
      within(dialog).getByRole("button", { name: "Create install plan" }),
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

    fireEvent.click(within(dialog).getByRole("button", { name: "Install" }));
    await waitFor(() =>
      expect(providerServiceMock.ApplyInstall).toHaveBeenCalledWith(
        "plan-wsl-install",
      ),
    );
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
    const distroInput = screen.getByLabelText("WSL distro");
    fireEvent.change(distroInput, { target: { value: "cairn-dev" } });
    fireEvent.blur(distroInput);

    await waitFor(() =>
      expect(settingsServiceMock.SetSetting).toHaveBeenCalledWith(
        "windows.wsl_distro",
        "cairn-dev",
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
      within(dialog).getByRole("button", { name: "Create install plan" }),
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

    fireEvent.click(within(dialog).getByRole("button", { name: "Install" }));
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
    expect(callbacks.length).toBeGreaterThan(0);
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
});

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
        containers: [container],
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
    title: "Install Docker Engine in Ubuntu on WSL",
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
    title: "Install and start Colima",
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
