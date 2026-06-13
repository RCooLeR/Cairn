import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { InventorySnapshot } from './api/inventory';
import type {
  CommandPlan,
  DiskUsageCategory,
  ProjectDetail,
  ProjectSummary,
  ProviderStatus,
} from '../bindings/github.com/RCooLeR/Cairn/internal/models/models.js';
import {
  HealthStatus,
  ProjectStatus,
  Risk,
  UpdateStatus,
} from '../bindings/github.com/RCooLeR/Cairn/internal/models/models.js';

import App from './App';
import { useAppStore } from './state/appStore';
import { useInventoryStore } from './state/inventoryStore';

const inventoryMock = vi.hoisted(() => ({
  getInventorySnapshot: vi.fn<() => Promise<InventorySnapshot>>(),
}));

const runtimeMock = vi.hoisted(() => ({
  on: vi.fn<
    (eventName: string, callback: (event?: unknown) => void) => () => void
  >(() => vi.fn()),
  openFile: vi.fn(),
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

vi.mock('./api/app', () => ({
  getAppVersion: vi.fn().mockResolvedValue({
    version: '0.1.0',
    goVersion: 'go1.26.4',
  }),
}));

vi.mock('./api/inventory', () => ({
  getInventorySnapshot: inventoryMock.getInventorySnapshot,
}));

vi.mock('./api/services', () => ({
  DockerService: dockerServiceMock,
  ProjectService: projectServiceMock,
}));

vi.mock('@wailsio/runtime', () => ({
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
  },
}));

describe('App inventory shell', () => {
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
    dockerServiceMock.RunImage.mockResolvedValue('container-new');
    dockerServiceMock.PullImage.mockResolvedValue('pull-stream');
    dockerServiceMock.SaveImage.mockResolvedValue('save-job');
    dockerServiceMock.LoadImage.mockResolvedValue('load-job');
    dockerServiceMock.SearchHub.mockResolvedValue([]);
    dockerServiceMock.CreateVolume.mockResolvedValue({
      name: 'created_volume',
      driver: 'local',
      inUse: false,
    });
    dockerServiceMock.CreateNetwork.mockResolvedValue({
      id: 'network-new',
      name: 'created_network',
      driver: 'bridge',
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
    projectServiceMock.PlanRedeployProject.mockResolvedValue(projectRedeployPlan());
    projectServiceMock.PlanDownProject.mockResolvedValue(projectDownVolumesPlan());
    projectServiceMock.ApplyProjectPlan.mockResolvedValue(undefined);
    runtimeMock.openFile.mockResolvedValue('');

    useAppStore.setState({
      version: null,
      versionLoading: false,
      versionError: null,
    });
    useInventoryStore.setState({
      status: 'idle',
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

  it('renders seeded Docker inventory and subscribes to object refresh events', async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());

    render(<App />);

    expect(screen.getByRole('img', { name: 'Cairn' })).toBeInTheDocument();
    expect(
      screen.getByRole('navigation', { name: 'Main navigation' }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole('heading', { name: 'Overview' }),
    ).toBeInTheDocument();
    expect(await screen.findByText('v0.1.0')).toBeInTheDocument();
    expect(
      await screen.findByText('Docker Engine - Running'),
    ).toBeInTheDocument();
    expect(screen.getAllByText('cairn-dev').length).toBeGreaterThan(0);
    expect(runtimeMock.on).toHaveBeenCalledWith(
      'objects:changed',
      expect.any(Function),
    );
  });

  it('lists containers and applies search without leaving the table view', async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());

    render(<App />);

    await screen.findByText('Docker Engine - Running');
    fireEvent.click(
      within(
        screen.getByRole('navigation', { name: 'Main navigation' }),
      ).getByRole('button', {
        name: /Containers/,
      }),
    );

    expect(
      screen.getByRole('heading', { name: 'Containers' }),
    ).toBeInTheDocument();
    expect(screen.getAllByText('web').length).toBeGreaterThan(0);
    expect(screen.getByText('cairn/web:latest')).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('Search inventory'), {
      target: { value: 'does-not-exist' },
    });

    expect(screen.getByText('No containers match')).toBeInTheDocument();
  });

  it('renders Compose projects and applies project filters', async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());
    projectServiceMock.RefreshProjects.mockResolvedValue([seededProject()]);

    render(<App />);

    await screen.findByText('Docker Engine - Running');
    fireEvent.click(
      within(
        screen.getByRole('navigation', { name: 'Main navigation' }),
      ).getByRole('button', {
        name: /Projects/,
      }),
    );

    expect(
      await screen.findByRole('heading', { name: 'Projects' }),
    ).toBeInTheDocument();
    expect(await screen.findByText('app-db')).toBeInTheDocument();
    expect(screen.getByText('1/2')).toBeInTheDocument();
    expect(screen.getByText('2 updates')).toBeInTheDocument();
    expect(screen.getByText('8080->80/tcp')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /Stopped/ }));

    expect(screen.getByText('No projects found')).toBeInTheDocument();
  });

  it('runs safe project lifecycle actions from project cards', async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());
    projectServiceMock.RefreshProjects.mockResolvedValue([seededProject()]);

    render(<App />);

    await screen.findByText('Docker Engine - Running');
    fireEvent.click(
      within(
        screen.getByRole('navigation', { name: 'Main navigation' }),
      ).getByRole('button', {
        name: /Projects/,
      }),
    );
    await screen.findByText('app-db');
    fireEvent.click(screen.getByRole('button', { name: 'Stop app-db' }));

    await waitFor(() =>
      expect(projectServiceMock.StopProject).toHaveBeenCalledWith(
        'linux_native/app-db',
      ),
    );
    await waitFor(() =>
      expect(projectServiceMock.RefreshProjects).toHaveBeenCalledTimes(2),
    );
  });

  it('confirms dangerous project plans through the project apply endpoint', async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());
    projectServiceMock.RefreshProjects.mockResolvedValue([seededProject()]);

    render(<App />);

    await screen.findByText('Docker Engine - Running');
    fireEvent.click(
      within(
        screen.getByRole('navigation', { name: 'Main navigation' }),
      ).getByRole('button', {
        name: /Projects/,
      }),
    );
    await screen.findByText('app-db');
    fireEvent.click(
      screen.getByRole('button', { name: 'Down with volumes app-db' }),
    );

    await waitFor(() =>
      expect(projectServiceMock.PlanDownProject).toHaveBeenCalledWith(
        'linux_native/app-db',
        true,
      ),
    );
    expect(
      await screen.findByRole('dialog', { name: 'Down app-db with volumes' }),
    ).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('Type app-db to confirm'), {
      target: { value: 'app-db' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }));

    await waitFor(() =>
      expect(projectServiceMock.ApplyProjectPlan).toHaveBeenCalledWith(
        'plan-down-volumes',
        'app-db',
      ),
    );
  });

  it('imports a Compose project through the folder picker', async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());
    projectServiceMock.RefreshProjects.mockResolvedValueOnce(
      [],
    ).mockResolvedValueOnce([seededProject()]);
    runtimeMock.openFile.mockResolvedValue(
      'E:\\Development\\projects\\apps\\rcooler\\Cairn\\testdata\\projects\\app-db',
    );

    render(<App />);

    await screen.findByText('Docker Engine - Running');
    fireEvent.click(
      within(
        screen.getByRole('navigation', { name: 'Main navigation' }),
      ).getByRole('button', {
        name: /Projects/,
      }),
    );
    fireEvent.click(
      await screen.findByRole('button', { name: 'Import Project' }),
    );
    expect(
      await screen.findByRole('dialog', { name: 'Import Project' }),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Browse' }));
    await waitFor(() =>
      expect(runtimeMock.openFile).toHaveBeenCalledWith(
        expect.objectContaining({ CanChooseDirectories: true }),
      ),
    );
    expect(screen.getByLabelText('Folder')).toHaveValue(
      'E:\\Development\\projects\\apps\\rcooler\\Cairn\\testdata\\projects\\app-db',
    );

    fireEvent.click(screen.getByRole('button', { name: 'Import' }));

    await waitFor(() =>
      expect(projectServiceMock.ImportProject).toHaveBeenCalledWith({
        folderPath:
          'E:\\Development\\projects\\apps\\rcooler\\Cairn\\testdata\\projects\\app-db',
        composeFilePaths: [],
      }),
    );
    expect(await screen.findByText('Imported app-db')).toBeInTheDocument();
    await waitFor(() =>
      expect(projectServiceMock.RefreshProjects).toHaveBeenCalledTimes(2),
    );
  });

  it('runs safe container actions directly and refreshes inventory', async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());

    render(<App />);

    await screen.findByText('Docker Engine - Running');
    fireEvent.click(
      within(
        screen.getByRole('navigation', { name: 'Main navigation' }),
      ).getByRole('button', {
        name: /Containers/,
      }),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Stop web' }));

    expect(dockerServiceMock.StopContainer).toHaveBeenCalledWith(
      'container-1',
      10,
    );
    await waitFor(() =>
      expect(inventoryMock.getInventorySnapshot).toHaveBeenCalledTimes(2),
    );
  });

  it('previews and confirms kill through the command-plan pipeline', async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());

    render(<App />);

    await screen.findByText('Docker Engine - Running');
    fireEvent.click(
      within(
        screen.getByRole('navigation', { name: 'Main navigation' }),
      ).getByRole('button', {
        name: /Containers/,
      }),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Kill web' }));

    expect(dockerServiceMock.PlanKillContainer).toHaveBeenCalledWith(
      'container-1',
    );
    expect(
      await screen.findByRole('dialog', { name: 'Kill web' }),
    ).toBeInTheDocument();
    expect(screen.getByText('docker kill web')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }));

    await waitFor(() =>
      expect(dockerServiceMock.ApplyContainerPlan).toHaveBeenCalledWith(
        'plan-kill-web',
        '',
      ),
    );
  });

  it('runs an image from the row wizard and refreshes inventory', async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());

    render(<App />);

    await screen.findByText('Docker Engine - Running');
    fireEvent.click(
      within(
        screen.getByRole('navigation', { name: 'Main navigation' }),
      ).getByRole('button', {
        name: /Images/,
      }),
    );
    fireEvent.click(
      screen.getByRole('button', { name: 'Run cairn/web:latest' }),
    );

    expect(
      await screen.findByRole('dialog', { name: 'Run Image' }),
    ).toBeInTheDocument();
    expect(screen.getByLabelText('Image ref')).toHaveValue('cairn/web:latest');
    fireEvent.click(screen.getByRole('button', { name: 'Next' }));
    expect(screen.getByText(/docker run -d/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Run' }));

    await waitFor(() =>
      expect(dockerServiceMock.RunImage).toHaveBeenCalledWith(
        expect.objectContaining({
          imageRef: 'cairn/web:latest',
          name: 'web',
          detach: true,
          pullIfMissing: true,
        }),
      ),
    );
    await waitFor(() =>
      expect(inventoryMock.getInventorySnapshot).toHaveBeenCalledTimes(2),
    );
  });

  it('renames a container through the modal', async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());

    render(<App />);

    await screen.findByText('Docker Engine - Running');
    fireEvent.click(
      within(
        screen.getByRole('navigation', { name: 'Main navigation' }),
      ).getByRole('button', {
        name: /Containers/,
      }),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Rename web' }));
    fireEvent.change(screen.getByLabelText('New name'), {
      target: { value: 'web-renamed' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Rename' }));

    await waitFor(() =>
      expect(dockerServiceMock.RenameContainer).toHaveBeenCalledWith(
        'container-1',
        'web-renamed',
      ),
    );
  });

  it('pulls, saves, and loads images from image modals', async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());

    render(<App />);

    await screen.findByText('Docker Engine - Running');
    fireEvent.click(
      within(
        screen.getByRole('navigation', { name: 'Main navigation' }),
      ).getByRole('button', {
        name: /Images/,
      }),
    );

    fireEvent.click(screen.getByRole('button', { name: 'Pull image' }));
    fireEvent.change(screen.getByLabelText('Image ref'), {
      target: { value: 'redis' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Pull' }));
    await waitFor(() =>
      expect(dockerServiceMock.PullImage).toHaveBeenCalledWith('redis:latest'),
    );

    fireEvent.click(
      screen.getByRole('button', { name: 'Save cairn/web:latest' }),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() =>
      expect(dockerServiceMock.SaveImage).toHaveBeenCalledWith(
        ['cairn/web:latest'],
        'cairn_web_latest.tar',
      ),
    );

    fireEvent.click(screen.getByRole('button', { name: 'Load tar' }));
    fireEvent.change(screen.getByLabelText('Source tar'), {
      target: { value: '/tmp/image.tar' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Load' }));
    await waitFor(() =>
      expect(dockerServiceMock.LoadImage).toHaveBeenCalledWith(
        '/tmp/image.tar',
      ),
    );
  });

  it('creates volumes and networks from page actions', async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());

    render(<App />);

    await screen.findByText('Docker Engine - Running');
    fireEvent.click(
      within(
        screen.getByRole('navigation', { name: 'Main navigation' }),
      ).getByRole('button', {
        name: /Volumes/,
      }),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Create volume' }));
    fireEvent.change(screen.getByLabelText('Name'), {
      target: { value: 'demo_data' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Create' }));
    await waitFor(() =>
      expect(dockerServiceMock.CreateVolume).toHaveBeenCalledWith(
        expect.objectContaining({ name: 'demo_data', driver: 'local' }),
      ),
    );

    fireEvent.click(
      within(
        screen.getByRole('navigation', { name: 'Main navigation' }),
      ).getByRole('button', {
        name: /Networks/,
      }),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Create network' }));
    fireEvent.change(screen.getByLabelText('Name'), {
      target: { value: 'demo_net' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Create' }));
    await waitFor(() =>
      expect(dockerServiceMock.CreateNetwork).toHaveBeenCalledWith(
        expect.objectContaining({ name: 'demo_net', driver: 'bridge' }),
      ),
    );
  });

  it('renders empty states when the daemon has no objects', async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(emptySnapshot());

    render(<App />);

    expect(await screen.findByText('No containers yet')).toBeInTheDocument();
    fireEvent.click(
      within(
        screen.getByRole('navigation', { name: 'Main navigation' }),
      ).getByRole('button', {
        name: /Images/,
      }),
    );

    expect(screen.getByText('No images match')).toBeInTheDocument();
  });

  it('refreshes inventory when Docker object events arrive', async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());

    render(<App />);

    await screen.findByText('Docker Engine - Running');
    expect(inventoryMock.getInventorySnapshot).toHaveBeenCalledTimes(1);
    vi.useFakeTimers();

    const callback = runtimeMock.on.mock.calls[0]?.[1] as
      | ((event?: unknown) => void)
      | undefined;
    expect(callback).toEqual(expect.any(Function));
    callback?.({ name: 'objects:changed', data: undefined });

    await vi.advanceTimersByTimeAsync(500);

    expect(inventoryMock.getInventorySnapshot).toHaveBeenCalledTimes(2);
  });
});

function seededSnapshot(): InventorySnapshot {
  const container = {
    id: 'container-1',
    name: 'web',
    image: 'cairn/web:latest',
    imageID: 'sha256:image-1',
    status: 'Up 2 minutes',
    state: 'running',
    health: HealthStatus.HealthStatusHealthy,
    projectID: 'cairn',
    service: 'web',
    ports: [
      {
        hostIP: '127.0.0.1',
        hostPort: '8080',
        containerPort: '80',
        protocol: 'tcp',
      },
    ],
    cpuPercent: 2.4,
    memoryBytes: 64 * 1024 * 1024,
    memoryLimit: 512 * 1024 * 1024,
    restarts: 0,
    createdAt: '2026-06-13T08:00:00Z',
  };
  const volume = {
    name: 'cairn_data',
    driver: 'local',
    mountpoint: '/var/lib/docker/volumes/cairn_data/_data',
    labels: { 'com.docker.compose.project': 'cairn' },
    sizeBytes: 2048,
    inUse: true,
  };
  const network = {
    id: 'network-1',
    name: 'cairn_default',
    driver: 'bridge',
    scope: 'local',
    internal: false,
    attachable: true,
    labels: { 'com.docker.compose.project': 'cairn' },
  };

  return {
    providers: [
      {
        id: 'wsl-cairn-dev',
        name: 'cairn-dev',
        kind: 'wsl',
        active: true,
        status: healthyProviderStatus(),
        healthy: true,
      },
    ],
    dockerInfo: {
      id: 'engine-1',
      name: 'cairn-dev',
      serverVersion: '26.1.0',
      storageDriver: 'overlay2',
      operatingSystem: 'Ubuntu 24.04',
      architecture: 'x86_64',
      cpus: 8,
      memoryBytes: 8 * 1024 * 1024 * 1024,
    },
    dockerVersion: {
      clientVersion: '26.1.0',
      serverVersion: '26.1.0',
      apiVersion: '1.45',
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
        id: 'sha256:image-1',
        repoTags: ['cairn/web:latest'],
        repoDigests: ['cairn/web@sha256:digest'],
        sizeBytes: 128 * 1024 * 1024,
        createdAt: '2026-06-12T08:00:00Z',
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
        subnet: '172.19.0.0/16',
        gateway: '172.19.0.1',
        containers: [container],
      },
    },
    degradedReason: null,
  };
}

function seededProject(): ProjectSummary {
  return {
    id: 'linux_native/app-db',
    name: 'app-db',
    providerID: 'linux_native',
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
        hostIP: '127.0.0.1',
        hostPort: '8080',
        containerPort: '80',
        protocol: 'tcp',
      },
    ],
    workingDir:
      'E:\\Development\\projects\\apps\\rcooler\\Cairn\\testdata\\projects\\app-db',
    lastChangedAt: '2026-06-13T08:00:00Z',
  };
}

function seededProjectDetail(): ProjectDetail {
  return {
    summary: seededProject(),
    services: [
      {
        name: 'app',
        image: 'cairn/app:latest',
        replicas: 1,
        running: 1,
        status: ProjectStatus.ProjectStatusRunning,
        health: HealthStatus.HealthStatusHealthy,
        ports: [
          {
            hostIP: '127.0.0.1',
            hostPort: '8080',
            containerPort: '80',
            protocol: 'tcp',
          },
        ],
        cpuPercent: 10,
        memoryBytes: 128 * 1024 * 1024,
      },
      {
        name: 'db',
        image: 'postgres:16',
        replicas: 1,
        running: 0,
        status: ProjectStatus.ProjectStatusStopped,
        health: HealthStatus.HealthStatusUnknown,
      },
    ],
  };
}

function killPlan(): CommandPlan {
  return {
    planID: 'plan-kill-web',
    title: 'Kill web',
    risk: Risk.RiskNeedsConfirmation,
    commands: [
      {
        order: 1,
        command: 'docker kill web',
        risk: Risk.RiskNeedsConfirmation,
        explanation: 'Immediately sends SIGKILL to the selected container.',
      },
    ],
    effects: ['web: Immediately sends SIGKILL to the selected container.'],
    expiresAt: '2026-06-13T08:10:00Z',
  };
}

function projectRedeployPlan(): CommandPlan {
  return {
    planID: 'plan-redeploy',
    title: 'Redeploy app-db',
    risk: Risk.RiskDestructive,
    commands: [
      {
        order: 1,
        command: 'docker compose -f compose.yaml up -d --force-recreate',
        workingDir:
          'E:\\Development\\projects\\apps\\rcooler\\Cairn\\testdata\\projects\\app-db',
        risk: Risk.RiskDestructive,
        explanation:
          'Runs docker compose up -d --force-recreate for the project.',
      },
    ],
    effects: [
      'app-db: Runs docker compose up -d --force-recreate for the project.',
    ],
    expiresAt: '2026-06-13T08:10:00Z',
  };
}

function projectDownVolumesPlan(): CommandPlan {
  return {
    planID: 'plan-down-volumes',
    title: 'Down app-db with volumes',
    risk: Risk.RiskDangerous,
    commands: [
      {
        order: 1,
        command: 'docker compose -f compose.yaml down --volumes',
        workingDir:
          'E:\\Development\\projects\\apps\\rcooler\\Cairn\\testdata\\projects\\app-db',
        risk: Risk.RiskDangerous,
        explanation:
          'Runs docker compose down --volumes and removes named volumes declared by the project.',
      },
    ],
    effects: [
      'app-db: Runs docker compose down --volumes and removes named volumes declared by the project.',
    ],
    requiresTypedName: 'app-db',
    expiresAt: '2026-06-13T08:10:00Z',
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
    dockerVersion: '26.1.0',
    composeVersion: 'v2.27.0',
    currentContext: 'default',
  };
}
