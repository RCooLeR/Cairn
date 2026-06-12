import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { InventorySnapshot } from './api/inventory';
import type {
  CommandPlan,
  DiskUsageCategory,
  ProviderStatus,
} from '../bindings/github.com/RCooLeR/Cairn/internal/models/models.js';
import { HealthStatus, Risk, UpdateStatus } from '../bindings/github.com/RCooLeR/Cairn/internal/models/models.js';

import App from './App';
import { useAppStore } from './state/appStore';
import { useInventoryStore } from './state/inventoryStore';

const inventoryMock = vi.hoisted(() => ({
  getInventorySnapshot: vi.fn<() => Promise<InventorySnapshot>>(),
}));

const runtimeMock = vi.hoisted(() => ({
  on: vi.fn<(eventName: string, callback: (event?: unknown) => void) => () => void>(() => vi.fn()),
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
        Object.fromEntries(Object.entries(source ?? {}).map(([key, entry]) => [key, value(entry)])),
    Nullable:
      <T,>(element: (source: unknown) => T) =>
      (source: unknown | null) =>
        source === null ? null : element(source),
  },
  Events: {
    On: runtimeMock.on,
  },
}));

describe('App inventory shell', () => {
  beforeEach(() => {
    vi.useRealTimers();
    vi.clearAllMocks();
    inventoryMock.getInventorySnapshot.mockReset();
    dockerServiceMock.InspectContainerRaw.mockResolvedValue('{"Id":"container-1"}');
    dockerServiceMock.GetImage.mockResolvedValue(null);
    dockerServiceMock.GetNetwork.mockResolvedValue(null);
    dockerServiceMock.GetVolume.mockResolvedValue(null);
    dockerServiceMock.StartContainer.mockResolvedValue(undefined);
    dockerServiceMock.StopContainer.mockResolvedValue(undefined);
    dockerServiceMock.RestartContainer.mockResolvedValue(undefined);
    dockerServiceMock.BulkContainerAction.mockResolvedValue({ total: 1, succeeded: 1, failed: 0, items: [] });
    dockerServiceMock.PlanKillContainer.mockResolvedValue(killPlan());
    dockerServiceMock.ApplyContainerPlan.mockResolvedValue(undefined);

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
    expect(screen.getByRole('navigation', { name: 'Main navigation' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Overview' })).toBeInTheDocument();
    expect(await screen.findByText('v0.1.0')).toBeInTheDocument();
    expect(await screen.findByText('Docker Engine - Running')).toBeInTheDocument();
    expect(screen.getAllByText('cairn-dev').length).toBeGreaterThan(0);
    expect(runtimeMock.on).toHaveBeenCalledWith('objects:changed', expect.any(Function));
  });

  it('lists containers and applies search without leaving the table view', async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());

    render(<App />);

    await screen.findByText('Docker Engine - Running');
    fireEvent.click(
      within(screen.getByRole('navigation', { name: 'Main navigation' })).getByRole('button', {
        name: /Containers/,
      }),
    );

    expect(screen.getByRole('heading', { name: 'Containers' })).toBeInTheDocument();
    expect(screen.getAllByText('web').length).toBeGreaterThan(0);
    expect(screen.getByText('cairn/web:latest')).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('Search inventory'), { target: { value: 'does-not-exist' } });

    expect(screen.getByText('No containers match')).toBeInTheDocument();
  });

  it('runs safe container actions directly and refreshes inventory', async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());

    render(<App />);

    await screen.findByText('Docker Engine - Running');
    fireEvent.click(
      within(screen.getByRole('navigation', { name: 'Main navigation' })).getByRole('button', {
        name: /Containers/,
      }),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Stop web' }));

    expect(dockerServiceMock.StopContainer).toHaveBeenCalledWith('container-1', 10);
    await waitFor(() => expect(inventoryMock.getInventorySnapshot).toHaveBeenCalledTimes(2));
  });

  it('previews and confirms kill through the command-plan pipeline', async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(seededSnapshot());

    render(<App />);

    await screen.findByText('Docker Engine - Running');
    fireEvent.click(
      within(screen.getByRole('navigation', { name: 'Main navigation' })).getByRole('button', {
        name: /Containers/,
      }),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Kill web' }));

    expect(dockerServiceMock.PlanKillContainer).toHaveBeenCalledWith('container-1');
    expect(await screen.findByRole('dialog', { name: 'Kill web' })).toBeInTheDocument();
    expect(screen.getByText('docker kill web')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }));

    await waitFor(() => expect(dockerServiceMock.ApplyContainerPlan).toHaveBeenCalledWith('plan-kill-web', ''));
  });

  it('renders empty states when the daemon has no objects', async () => {
    inventoryMock.getInventorySnapshot.mockResolvedValue(emptySnapshot());

    render(<App />);

    expect(await screen.findByText('No containers yet')).toBeInTheDocument();
    fireEvent.click(
      within(screen.getByRole('navigation', { name: 'Main navigation' })).getByRole('button', {
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

    const callback = runtimeMock.on.mock.calls[0]?.[1] as ((event?: unknown) => void) | undefined;
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
    ports: [{ hostIP: '127.0.0.1', hostPort: '8080', containerPort: '80', protocol: 'tcp' }],
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

function diskCategory(count: number, active: number, sizeBytes: number): DiskUsageCategory {
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
