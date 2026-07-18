import type { ComponentProps } from "react";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import {
  ProviderStatus,
  ProviderSummary,
  RuntimeDiagnostics,
  WindowsDockerCLIShimStatus,
  WSLDistroInfo,
} from "../../bindings/github.com/RCooLeR/Cairn/internal/models/models.js";

const diagnosticsServiceMock = vi.hoisted(() => ({
  GetRuntimeDiagnostics: vi.fn(),
}));

const settingsServiceMock = vi.hoisted(() => ({
  GetWindowsDockerCLIShimStatus: vi.fn(),
  InstallWindowsDockerCLIShim: vi.fn(),
}));

vi.mock("../api/services", () => ({
  DiagnosticsService: diagnosticsServiceMock,
  SettingsService: settingsServiceMock,
}));

vi.mock("../components/settings/PortForwardingPanel", () => ({
  PortForwardingPanel: () => null,
}));

import { SettingsPage } from "./SettingsPage";

type SettingsPageProps = ComponentProps<typeof SettingsPage>;

function deferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, reject, resolve };
}

function runtimeDiagnostics(active: number, started = true) {
  return new RuntimeDiagnostics({
    checkedAt: null,
    logs: {
      activeOperations: 1,
      activeProducers: 3,
      activeStreams: 4,
      drainingStreams: 1,
      pendingStreams: 2,
      retainedBytes: 4096,
      reservedReaders: 8,
    },
    metrics: { activeStreams: 2, activeWatchers: 5, started },
    portForwards: { activeForwards: 7, supported: true },
    stdio: {
      active,
      activeConnections: [],
      closeTimeouts: 2,
      closed: 9,
      forcedKills: 1,
      opened: 10,
    },
    terminals: { activeSessions: 6 },
  });
}

function dockerShimStatus({
  commandPath = "C:\\Users\\roman\\bin\\docker.exe",
  distro = "Ubuntu",
  installed = true,
}: {
  commandPath?: string;
  distro?: string;
  installed?: boolean;
} = {}) {
  return new WindowsDockerCLIShimStatus({
    commandPath,
    distro,
    installed,
    needsNewShell: false,
    onUserPath: installed,
    supported: true,
  });
}

function createProps(
  overrides: Partial<SettingsPageProps> = {},
): SettingsPageProps {
  const activeProvider = new ProviderSummary({
    active: true,
    healthy: true,
    id: "windows-wsl-ubuntu",
    kind: "windows_wsl_ubuntu",
    name: "Windows WSL",
    status: new ProviderStatus({
      buildxInstalled: true,
      composeInstalled: true,
      dockerInstalled: true,
      dockerRunning: true,
      healthy: true,
      installed: true,
      running: true,
    }),
  });

  return {
    activeProvider,
    auditEntries: [],
    auditError: null,
    auditFilter: {
      action: "all",
      projectID: "all",
      range: "24h",
      status: "all",
    },
    auditLoading: false,
    autostartBackend: false,
    colimaCPU: 4,
    colimaDiskGB: 60,
    colimaMemoryGB: 8,
    colimaProfile: "default",
    dockerContexts: [],
    dockerContextsError: null,
    dockerContextsLoading: false,
    error: null,
    message: null,
    onAuditFilterChange: vi.fn(),
    onAutostartChange: vi.fn(),
    onColimaCPUChange: vi.fn(),
    onColimaDiskGBChange: vi.fn(),
    onColimaMemoryGBChange: vi.fn(),
    onColimaProfileChange: vi.fn(),
    onDetect: vi.fn(),
    onOpenSetup: vi.fn(),
    onRefreshAudit: vi.fn(),
    onRefreshDockerContexts: vi.fn(),
    onRefreshRegistries: vi.fn(),
    onRefreshWSLDistros: vi.fn(),
    onRegistryLogin: vi.fn(),
    onRegistryLogout: vi.fn(),
    onRegistryTest: vi.fn(),
    onRetrySettings: vi.fn(),
    onSaveColimaCPU: vi.fn(),
    onSaveColimaDiskGB: vi.fn(),
    onSaveColimaMemoryGB: vi.fn(),
    onSaveColimaProfile: vi.fn(),
    onSaveWSLDistro: vi.fn(async () => true),
    onSectionChange: vi.fn(),
    onSettingChange: vi.fn(async () => true),
    onUseDockerContext: vi.fn(),
    onUseWSLDistro: vi.fn(),
    onWSLDistroChange: vi.fn(),
    providers: [activeProvider],
    registryAccounts: [],
    registryAccountsError: null,
    registryAccountsLoading: false,
    registryBusyKeys: new Set<string>(),
    registryStatuses: {},
    saving: false,
    section: "advanced",
    settings: {},
    settingsLoadError: null,
    settingsStatus: "ready",
    version: null,
    wslDistro: "Ubuntu",
    wslDistros: [],
    wslDistrosError: null,
    wslDistrosLoading: false,
    ...overrides,
  };
}

function runtimeTile(label: string) {
  const labelNode = screen.getByText(label, {
    selector: "div.text-xs.font-medium.uppercase",
  });
  if (!labelNode.parentElement) {
    throw new Error(`Runtime tile ${label} has no container`);
  }
  return within(labelNode.parentElement);
}

describe("SettingsPage diagnostic resources", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    diagnosticsServiceMock.GetRuntimeDiagnostics.mockResolvedValue(
      runtimeDiagnostics(0),
    );
    settingsServiceMock.GetWindowsDockerCLIShimStatus.mockResolvedValue(
      dockerShimStatus(),
    );
    settingsServiceMock.InstallWindowsDockerCLIShim.mockResolvedValue(
      dockerShimStatus(),
    );
  });

  it("describes project file editing as manual and temporarily unavailable", () => {
    render(<SettingsPage {...createProps({ section: "help" })} />);

    expect(
      screen.getByText(
        "Project file editing is temporarily unavailable; apply project file changes manually outside Cairn.",
      ),
    ).toBeInTheDocument();
    expect(
      screen.queryByText(
        /Project file edits are previewed and applied through Cairn plans/i,
      ),
    ).not.toBeInTheDocument();
    expect(
      screen.getByText(
        "Agent endpoints are restricted to literal IPv4 or IPv6 loopback addresses with an explicit port; DNS names, remote addresses, and redirects are rejected.",
      ),
    ).toBeInTheDocument();
  });

  it("announces save completion and actionable save failures", () => {
    const props = createProps({
      message: "Settings saved successfully",
      section: "general",
    });
    const view = render(<SettingsPage {...props} />);

    expect(screen.getByText("Settings saved successfully")).toHaveAttribute(
      "role",
      "status",
    );

    view.rerender(
      <SettingsPage
        {...props}
        error="Settings could not be saved"
        message={null}
      />,
    );

    expect(screen.getByText("Settings could not be saved")).toHaveAttribute(
      "role",
      "alert",
    );
  });

  it("preserves the focused setting input and caret when a delayed save normalizes its value", async () => {
    const save = deferred<boolean>();
    const onSettingChange = vi.fn(() => save.promise);
    const props = createProps({
      onSettingChange,
      section: "terminal",
      settings: { "terminal.default_shell": "bash" },
    });
    const view = render(<SettingsPage {...props} />);
    const input = screen.getByRole("textbox", {
      name: "Default shell",
    }) as HTMLInputElement;

    fireEvent.change(input, { target: { value: "zsh" } });
    fireEvent.blur(input);
    expect(onSettingChange).toHaveBeenCalledWith(
      "terminal.default_shell",
      "zsh",
    );

    input.focus();
    input.setSelectionRange(1, 1);
    view.rerender(
      <SettingsPage
        {...props}
        settings={{ "terminal.default_shell": "/bin/zsh" }}
      />,
    );

    const normalizedInput = screen.getByRole("textbox", {
      name: "Default shell",
    }) as HTMLInputElement;
    expect(normalizedInput).toBe(input);
    expect(normalizedInput).toHaveFocus();
    expect(normalizedInput).toHaveValue("/bin/zsh");
    expect(normalizedInput.selectionStart).toBe(1);

    await act(async () => {
      save.resolve(true);
      await save.promise;
    });
  });

  it("does not turn an initial runtime diagnostics failure into zero or false claims and recovers", async () => {
    diagnosticsServiceMock.GetRuntimeDiagnostics.mockRejectedValueOnce(
      new Error("diagnostics offline"),
    );
    render(<SettingsPage {...createProps()} />);

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "diagnostics offline",
    );
    expect(screen.queryByText("0 active")).not.toBeInTheDocument();
    expect(screen.queryByText("stopped")).not.toBeInTheDocument();
    expect(
      screen.queryByText("Native backend networking"),
    ).not.toBeInTheDocument();

    diagnosticsServiceMock.GetRuntimeDiagnostics.mockResolvedValueOnce(
      runtimeDiagnostics(4),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Refresh runtime diagnostics" }),
    );

    await waitFor(() =>
      expect(
        runtimeTile("WSL stdio transports").getByText("4 active"),
      ).toBeVisible(),
    );
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.getByText(/^Last updated /)).toBeVisible();
  });

  it("ignores a late runtime diagnostics response after a newer generation wins", async () => {
    const first = deferred<RuntimeDiagnostics>();
    const second = deferred<RuntimeDiagnostics>();
    diagnosticsServiceMock.GetRuntimeDiagnostics.mockReturnValueOnce(
      first.promise,
    ).mockReturnValueOnce(second.promise);
    const props = createProps();
    const view = render(<SettingsPage {...props} />);

    await waitFor(() =>
      expect(
        diagnosticsServiceMock.GetRuntimeDiagnostics,
      ).toHaveBeenCalledTimes(1),
    );
    view.rerender(<SettingsPage {...props} section="general" />);
    view.rerender(<SettingsPage {...props} section="advanced" />);
    await waitFor(() =>
      expect(
        diagnosticsServiceMock.GetRuntimeDiagnostics,
      ).toHaveBeenCalledTimes(2),
    );

    await act(async () => {
      second.resolve(runtimeDiagnostics(9));
    });
    await waitFor(() =>
      expect(
        runtimeTile("WSL stdio transports").getByText("9 active"),
      ).toBeVisible(),
    );

    await act(async () => {
      first.resolve(runtimeDiagnostics(1));
    });
    expect(
      runtimeTile("WSL stdio transports").getByText("9 active"),
    ).toBeVisible();
    expect(screen.queryByText("1 active")).not.toBeInTheDocument();
  });

  it("retains stale runtime values with their timestamp and replaces them after recovery", async () => {
    diagnosticsServiceMock.GetRuntimeDiagnostics.mockResolvedValueOnce(
      runtimeDiagnostics(3),
    );
    render(<SettingsPage {...createProps()} />);
    await waitFor(() =>
      expect(
        runtimeTile("WSL stdio transports").getByText("3 active"),
      ).toBeVisible(),
    );

    diagnosticsServiceMock.GetRuntimeDiagnostics.mockRejectedValueOnce(
      new Error("runtime refresh timed out"),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Refresh runtime diagnostics" }),
    );

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "runtime refresh timed out",
    );
    expect(
      screen.getByText(/Showing the last successful values from/),
    ).toBeVisible();
    expect(
      runtimeTile("WSL stdio transports").getByText("3 active"),
    ).toBeVisible();

    diagnosticsServiceMock.GetRuntimeDiagnostics.mockResolvedValueOnce(
      runtimeDiagnostics(8, false),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Refresh runtime diagnostics" }),
    );

    await waitFor(() =>
      expect(
        runtimeTile("WSL stdio transports").getByText("8 active"),
      ).toBeVisible(),
    );
    expect(runtimeTile("Metrics").getByText("stopped")).toBeVisible();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("keeps Docker shim claims unavailable after an initial failure and recovers", async () => {
    settingsServiceMock.GetWindowsDockerCLIShimStatus.mockRejectedValueOnce(
      new Error("shim probe failed"),
    );
    render(<SettingsPage {...createProps({ section: "providers" })} />);

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "shim probe failed",
    );
    expect(screen.queryByText("not installed")).not.toBeInTheDocument();
    expect(screen.queryByText("not on PATH")).not.toBeInTheDocument();
    expect(screen.queryByText("unsupported")).not.toBeInTheDocument();

    settingsServiceMock.GetWindowsDockerCLIShimStatus.mockResolvedValueOnce(
      dockerShimStatus(),
    );
    fireEvent.click(
      screen.getByRole("button", {
        name: "Refresh Docker CLI shim status",
      }),
    );

    expect(await screen.findByText("installed")).toBeVisible();
    expect(screen.getByText("on PATH")).toBeVisible();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("does not reuse a prior distro's last-known-good status when the new identity fails", async () => {
    settingsServiceMock.GetWindowsDockerCLIShimStatus.mockResolvedValueOnce(
      dockerShimStatus({
        commandPath: "C:\\Users\\roman\\bin\\docker-ubuntu.exe",
        distro: "Ubuntu",
      }),
    ).mockRejectedValueOnce(new Error("cairn-next probe failed"));
    const props = createProps({ section: "providers", wslDistro: "Ubuntu" });
    const view = render(<SettingsPage {...props} />);

    expect(
      await screen.findByText("docker-ubuntu.exe", { exact: false }),
    ).toBeVisible();
    view.rerender(<SettingsPage {...props} wslDistro="cairn-next" />);

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "cairn-next probe failed",
    );
    expect(
      screen.queryByText("docker-ubuntu.exe", { exact: false }),
    ).not.toBeInTheDocument();
    expect(screen.queryByText(/^installed$/)).not.toBeInTheDocument();
    expect(screen.queryByText(/^on PATH$/)).not.toBeInTheDocument();
  });

  it("awaits the distro save, locks selection, and refreshes the latest identity after install failure", async () => {
    const save = deferred<boolean>();
    const install = deferred<WindowsDockerCLIShimStatus>();
    const latestStatus = deferred<WindowsDockerCLIShimStatus>();
    const onSaveWSLDistro = vi.fn(() => save.promise);
    settingsServiceMock.GetWindowsDockerCLIShimStatus.mockResolvedValueOnce(
      dockerShimStatus({
        commandPath: "C:\\Users\\roman\\bin\\docker-before.exe",
        distro: "Ubuntu",
        installed: false,
      }),
    ).mockReturnValueOnce(latestStatus.promise);
    settingsServiceMock.InstallWindowsDockerCLIShim.mockReturnValueOnce(
      install.promise,
    );
    const props = createProps({
      onSaveWSLDistro,
      section: "providers",
      wslDistro: "Ubuntu",
      wslDistros: [
        new WSLDistroInfo({ default: true, name: "Ubuntu", version: 2 }),
        new WSLDistroInfo({ name: "cairn-next", version: 2 }),
      ],
    });
    const view = render(<SettingsPage {...props} />);

    expect(await screen.findByText("not installed")).toBeVisible();
    fireEvent.click(
      screen.getByRole("button", { name: "Install Docker CLI shim" }),
    );

    await waitFor(() => expect(onSaveWSLDistro).toHaveBeenCalledTimes(1));
    expect(
      settingsServiceMock.InstallWindowsDockerCLIShim,
    ).not.toHaveBeenCalled();
    expect(screen.getByRole("combobox", { name: "WSL distro" })).toBeDisabled();
    for (const button of screen.getAllByRole("button", {
      name: "Use distro",
    })) {
      expect(button).toBeDisabled();
    }

    await act(async () => {
      save.resolve(true);
      await save.promise;
    });
    await waitFor(() =>
      expect(
        settingsServiceMock.InstallWindowsDockerCLIShim,
      ).toHaveBeenCalledTimes(1),
    );

    view.rerender(
      <SettingsPage {...props} section="general" wslDistro="cairn-next" />,
    );
    view.rerender(<SettingsPage {...props} wslDistro="cairn-next" />);
    await act(async () => {
      install.reject(new Error("install access denied"));
      await install.promise.catch(() => undefined);
    });

    await waitFor(() =>
      expect(
        settingsServiceMock.GetWindowsDockerCLIShimStatus,
      ).toHaveBeenCalledTimes(2),
    );
    expect(screen.getByText("install access denied")).toBeVisible();
    await act(async () => {
      latestStatus.resolve(
        dockerShimStatus({
          commandPath: "C:\\Users\\roman\\bin\\docker-cairn-next.exe",
          distro: "cairn-next",
          installed: false,
        }),
      );
      await latestStatus.promise;
    });

    expect(
      await screen.findByText("docker-cairn-next.exe", { exact: false }),
    ).toBeVisible();
    expect(screen.getByText("install access denied")).toBeVisible();
    expect(
      screen.queryByText("docker-before.exe", { exact: false }),
    ).not.toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "WSL distro" })).toBeEnabled();
  });

  it("does not install when the selected distro could not be saved", async () => {
    const onSaveWSLDistro = vi.fn(async () => false);
    settingsServiceMock.GetWindowsDockerCLIShimStatus.mockResolvedValueOnce(
      dockerShimStatus({ installed: false }),
    ).mockResolvedValueOnce(dockerShimStatus({ installed: false }));
    render(
      <SettingsPage
        {...createProps({
          onSaveWSLDistro,
          section: "providers",
        })}
      />,
    );

    expect(await screen.findByText("not installed")).toBeVisible();
    fireEvent.click(
      screen.getByRole("button", { name: "Install Docker CLI shim" }),
    );

    expect(
      await screen.findByText(
        "Save the selected WSL distro before installing the Docker CLI shim.",
      ),
    ).toBeVisible();
    expect(onSaveWSLDistro).toHaveBeenCalledTimes(1);
    expect(
      settingsServiceMock.InstallWindowsDockerCLIShim,
    ).not.toHaveBeenCalled();
    await waitFor(() =>
      expect(
        settingsServiceMock.GetWindowsDockerCLIShimStatus,
      ).toHaveBeenCalledTimes(2),
    );
  });

  it("warns when the selected distro differs from the installed shim target", async () => {
    settingsServiceMock.GetWindowsDockerCLIShimStatus.mockResolvedValueOnce(
      dockerShimStatus({ distro: "Ubuntu", installed: true }),
    );
    render(
      <SettingsPage
        {...createProps({ section: "providers", wslDistro: "cairn-next" })}
      />,
    );

    const warning = await screen.findByRole("alert");
    expect(warning).toHaveTextContent("installed shim targets");
    expect(warning).toHaveTextContent("Ubuntu");
    expect(warning).toHaveTextContent("cairn-next");
    expect(warning).toHaveTextContent("Reinstall");
  });

  it("keeps an install authoritative and refreshes status for the latest distro", async () => {
    const install = deferred<WindowsDockerCLIShimStatus>();
    const postInstallStatus = deferred<WindowsDockerCLIShimStatus>();
    settingsServiceMock.GetWindowsDockerCLIShimStatus.mockResolvedValueOnce(
      dockerShimStatus({
        commandPath: "C:\\Users\\roman\\bin\\docker-before.exe",
        installed: false,
      }),
    ).mockReturnValueOnce(postInstallStatus.promise);
    settingsServiceMock.InstallWindowsDockerCLIShim.mockReturnValueOnce(
      install.promise,
    );
    const props = createProps({ section: "providers", wslDistro: "Ubuntu" });
    const view = render(<SettingsPage {...props} />);

    expect(await screen.findByText("not installed")).toBeVisible();
    const installButton = screen.getByRole("button", {
      name: "Install Docker CLI shim",
    });
    fireEvent.click(installButton);
    fireEvent.click(installButton);
    await waitFor(() =>
      expect(
        settingsServiceMock.InstallWindowsDockerCLIShim,
      ).toHaveBeenCalledTimes(1),
    );

    view.rerender(
      <SettingsPage {...props} section="general" wslDistro="cairn-next" />,
    );
    view.rerender(<SettingsPage {...props} wslDistro="cairn-next" />);
    await act(async () => {
      await new Promise((resolve) => window.setTimeout(resolve, 0));
    });
    expect(
      settingsServiceMock.GetWindowsDockerCLIShimStatus,
    ).toHaveBeenCalledTimes(1);

    await act(async () => {
      install.resolve(
        dockerShimStatus({
          commandPath: "C:\\Users\\roman\\bin\\docker-install-result.exe",
          distro: "Ubuntu",
        }),
      );
      await install.promise;
    });
    await waitFor(() =>
      expect(
        settingsServiceMock.GetWindowsDockerCLIShimStatus,
      ).toHaveBeenCalledTimes(2),
    );

    await act(async () => {
      postInstallStatus.resolve(
        dockerShimStatus({
          commandPath: "C:\\Users\\roman\\bin\\docker-current.exe",
          distro: "cairn-next",
        }),
      );
      await postInstallStatus.promise;
    });
    expect(
      await screen.findByText("docker-current.exe", { exact: false }),
    ).toBeVisible();
    expect(screen.getByText("cairn-next")).toBeVisible();
    expect(
      screen.queryByText("docker-install-result.exe", { exact: false }),
    ).not.toBeInTheDocument();
  });

  it("retains stale Docker shim values, recovers, and ignores a reversed response", async () => {
    settingsServiceMock.GetWindowsDockerCLIShimStatus.mockResolvedValueOnce(
      dockerShimStatus({
        commandPath: "C:\\Users\\roman\\bin\\docker-current.exe",
      }),
    );
    const props = createProps({ section: "providers" });
    const view = render(<SettingsPage {...props} />);
    expect(
      await screen.findByText("docker-current.exe", { exact: false }),
    ).toBeVisible();

    settingsServiceMock.GetWindowsDockerCLIShimStatus.mockRejectedValueOnce(
      new Error("shim refresh failed"),
    );
    fireEvent.click(
      screen.getByRole("button", {
        name: "Refresh Docker CLI shim status",
      }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "shim refresh failed",
    );
    expect(
      screen.getByText(/Showing the last successful values from/),
    ).toBeVisible();
    expect(
      screen.getByText("docker-current.exe", { exact: false }),
    ).toBeVisible();

    settingsServiceMock.GetWindowsDockerCLIShimStatus.mockResolvedValueOnce(
      dockerShimStatus({ installed: false }),
    );
    fireEvent.click(
      screen.getByRole("button", {
        name: "Refresh Docker CLI shim status",
      }),
    );
    expect(await screen.findByText("not installed")).toBeVisible();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();

    const older = deferred<WindowsDockerCLIShimStatus>();
    const newer = deferred<WindowsDockerCLIShimStatus>();
    settingsServiceMock.GetWindowsDockerCLIShimStatus.mockReturnValueOnce(
      older.promise,
    ).mockReturnValueOnce(newer.promise);
    view.rerender(<SettingsPage {...props} wslDistro="cairn-dev" />);
    await waitFor(() =>
      expect(
        settingsServiceMock.GetWindowsDockerCLIShimStatus,
      ).toHaveBeenCalledTimes(4),
    );
    view.rerender(<SettingsPage {...props} wslDistro="cairn-next" />);
    await waitFor(() =>
      expect(
        settingsServiceMock.GetWindowsDockerCLIShimStatus,
      ).toHaveBeenCalledTimes(5),
    );

    await act(async () => {
      newer.resolve(
        dockerShimStatus({
          commandPath: "C:\\Users\\roman\\bin\\docker-new.exe",
          distro: "cairn-next",
        }),
      );
    });
    expect(
      await screen.findByText("docker-new.exe", { exact: false }),
    ).toBeVisible();

    await act(async () => {
      older.resolve(
        dockerShimStatus({
          commandPath: "C:\\Users\\roman\\bin\\docker-old.exe",
          distro: "cairn-dev",
          installed: false,
        }),
      );
    });
    expect(screen.getByText("docker-new.exe", { exact: false })).toBeVisible();
    expect(
      screen.queryByText("docker-old.exe", { exact: false }),
    ).not.toBeInTheDocument();
    expect(screen.getByText("installed")).toBeVisible();
  });
});
