import type { LucideIcon } from 'lucide-react';
import type {
  CommandPlan,
  ContainerSummary,
  ImageDetail,
  ImageSummary,
  NetworkDetail,
  NetworkSummary,
  PortBinding,
  ProviderSummary,
  VolumeDetail,
  VolumeSummary,
} from '../bindings/github.com/RCooLeR/Cairn/internal/models/models.js';

import {
  Bell,
  Box,
  Container,
  Copy,
  Database,
  Eye,
  FileJson,
  Gauge,
  HardDrive,
  Network,
  Play,
  RefreshCw,
  RotateCw,
  Search,
  Server,
  Skull,
  Square,
} from 'lucide-react';
import { useCallback, useEffect, useMemo, useState } from 'react';

import { Events } from '@wailsio/runtime';

import { getAppVersion } from './api/app';
import { DockerService } from './api/services';
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
  Tooltip,
} from './components/ui';
import { useAppStore } from './state/appStore';
import { useInventoryStore } from './state/inventoryStore';

const logoUrl = '/cairn-logo.png';

type PageID = 'overview' | 'containers' | 'images' | 'volumes' | 'networks';
type FilterID = string;
type BadgeTone = 'ok' | 'warn' | 'error' | 'info' | 'neutral' | 'accent';

type NavItem = {
  id: PageID;
  label: string;
  icon: LucideIcon;
};

type InspectState = {
  open: boolean;
  title: string;
  subtitle?: string;
  rows: Array<[string, string]>;
  raw?: string;
  loading?: boolean;
  error?: string;
};

type ContainerAction = 'start' | 'stop' | 'restart' | 'kill';

type ConfirmState = {
  open: boolean;
  plan: CommandPlan | null;
  targetName: string;
  typedName: string;
  busy: boolean;
  error?: string;
};

const navItems: NavItem[] = [
  { id: 'overview', label: 'Overview', icon: Gauge },
  { id: 'containers', label: 'Containers', icon: Container },
  { id: 'images', label: 'Images', icon: Box },
  { id: 'volumes', label: 'Volumes', icon: Database },
  { id: 'networks', label: 'Networks', icon: Network },
];

const emptyInspect: InspectState = {
  open: false,
  title: '',
  rows: [],
};

const emptyConfirm: ConfirmState = {
  open: false,
  plan: null,
  targetName: '',
  typedName: '',
  busy: false,
};

function App() {
  const version = useAppStore((state) => state.version);
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

  const [activePage, setActivePage] = useState<PageID>('overview');
  const [search, setSearch] = useState('');
  const [containerFilter, setContainerFilter] = useState<FilterID>('all');
  const [imageFilter, setImageFilter] = useState<FilterID>('all');
  const [volumeFilter, setVolumeFilter] = useState<FilterID>('all');
  const [inspect, setInspect] = useState<InspectState>(emptyInspect);
  const [confirm, setConfirm] = useState<ConfirmState>(emptyConfirm);
  const [selectedContainerIDs, setSelectedContainerIDs] = useState(() => new Set<string>());
  const [busyActionIDs, setBusyActionIDs] = useState(() => new Set<string>());
  const [actionError, setActionError] = useState<string | null>(null);

  const navigate = useCallback((page: PageID) => {
    setActivePage(page);
    setSearch('');
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
          setVersionError(error instanceof Error ? error.message : 'Unable to load app version');
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
    void refreshInventory();
  }, [refreshInventory]);

  useEffect(() => {
    let timer: number | undefined;
    const off = Events.On('objects:changed', () => {
      window.clearTimeout(timer);
      timer = window.setTimeout(() => {
        void refreshInventory();
      }, 500);
    });
    return () => {
      window.clearTimeout(timer);
      off();
    };
  }, [refreshInventory]);

  const activeProvider = useMemo(() => activeProviderSummary(providers), [providers]);
  const runningContainers = containers.filter((container) => container.state === 'running').length;
  const unhealthyContainers = containers.filter((container) => container.health === 'unhealthy').length;
  const diskTotal = diskUsage?.totalBytes ?? 0;
  const diskReclaimable = diskUsage?.reclaimable ?? 0;
  const versionLabel = version?.version ? `v${version.version}` : 'v1.0 workspace';
  const pageTitle = navItems.find((item) => item.id === activePage)?.label ?? 'Overview';
  const dockerRunning = !inventoryError && Boolean(dockerInfo || dockerVersion);
  const providerName = activeProvider?.name ?? 'No provider selected';
  const statusLabel = dockerRunning ? 'Running' : 'Stopped';

  const imageUseCounts = useMemo(() => imageUsageCounts(containers), [containers]);

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

  const refreshAfterAction = useCallback(async () => {
    await refreshInventory();
  }, [refreshInventory]);

  const runContainerAction = useCallback(async (action: ContainerAction, container: ContainerSummary) => {
    const key = `${action}:${container.id}`;
    setActionError(null);
    setActionBusy(key, true);
    try {
      if (action === 'start') {
        await DockerService.StartContainer(container.id);
      } else if (action === 'stop') {
        await DockerService.StopContainer(container.id, 10);
      } else if (action === 'restart') {
        await DockerService.RestartContainer(container.id, 10);
      } else {
        const plan = await DockerService.PlanKillContainer(container.id);
        if (!plan) {
          throw new Error('Kill plan was empty');
        }
        setConfirm({
          open: true,
          plan,
          targetName: container.name,
          typedName: '',
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
      setActionError(error instanceof Error ? error.message : 'Container action failed');
    } finally {
      setActionBusy(key, false);
    }
  }, [refreshAfterAction, setActionBusy]);

  const runBulkContainerAction = useCallback(async (action: Exclude<ContainerAction, 'kill'>) => {
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
        setActionError(`${result.failed} of ${result.total} container actions failed`);
      }
    } catch (error: unknown) {
      setActionError(error instanceof Error ? error.message : 'Bulk container action failed');
    } finally {
      setActionBusy(key, false);
    }
  }, [refreshAfterAction, selectedContainerIDs, setActionBusy]);

  const applyConfirmedPlan = useCallback(async () => {
    if (!confirm.plan) {
      return;
    }
    setConfirm((current) => ({ ...current, busy: true, error: undefined }));
    try {
      await DockerService.ApplyContainerPlan(confirm.plan.planID, confirm.typedName);
      setConfirm(emptyConfirm);
      setSelectedContainerIDs(new Set<string>());
      await refreshAfterAction();
    } catch (error: unknown) {
      setConfirm((current) => ({
        ...current,
        busy: false,
        error: error instanceof Error ? error.message : 'Unable to apply plan',
      }));
    }
  }, [confirm.plan, confirm.typedName, refreshAfterAction]);

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

  const openContainerInspect = useCallback((container: ContainerSummary) => {
    setInspect({
      open: true,
      title: container.name,
      subtitle: shortID(container.id),
      rows: containerRows(container),
      loading: true,
    });
    DockerService.InspectContainerRaw(container.id)
      .then((raw) => {
        setInspect({
          open: true,
          title: container.name,
          subtitle: shortID(container.id),
          rows: containerRows(container),
          raw: formatJSON(raw),
        });
      })
      .catch((error: unknown) => {
        setInspect({
          open: true,
          title: container.name,
          subtitle: shortID(container.id),
          rows: containerRows(container),
          error: error instanceof Error ? error.message : 'Unable to inspect container',
        });
      });
  }, []);

  const openImageInspect = useCallback((image: ImageSummary) => {
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
          throw new Error('Image detail was empty');
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
          error: error instanceof Error ? error.message : 'Unable to inspect image',
        });
      });
  }, [imageUseCounts]);

  const openVolumeInspect = useCallback((volume: VolumeSummary) => {
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
          error: error instanceof Error ? error.message : 'Unable to inspect volume',
        });
      });
  }, [volumeDetails]);

  const openNetworkInspect = useCallback((network: NetworkSummary) => {
    const detail = networkDetails[network.id];
    setInspect({
      open: true,
      title: network.name,
      subtitle: shortID(network.id),
      rows: networkRows(network, detail),
      raw: detail ? JSON.stringify(detail, null, 2) : undefined,
      loading: !detail,
    });
    if (detail) {
      return;
    }
    DockerService.GetNetwork(network.id)
      .then((nextDetail) => {
        setInspect({
          open: true,
          title: network.name,
          subtitle: shortID(network.id),
          rows: networkRows(network, nextDetail ?? undefined),
          raw: nextDetail ? JSON.stringify(nextDetail, null, 2) : undefined,
        });
      })
      .catch((error: unknown) => {
        setInspect({
          open: true,
          title: network.name,
          subtitle: shortID(network.id),
          rows: networkRows(network),
          error: error instanceof Error ? error.message : 'Unable to inspect network',
        });
      });
  }, [networkDetails]);

  const content = (() => {
    switch (activePage) {
      case 'containers':
        return (
          <ContainersPage
            actionBusyIDs={busyActionIDs}
            containers={containers}
            filter={containerFilter}
            loading={inventoryStatus === 'loading'}
            onAction={runContainerAction}
            onBulkAction={runBulkContainerAction}
            onFilterChange={setContainerFilter}
            onInspect={openContainerInspect}
            onToggleSelection={toggleContainerSelection}
            search={search}
            selectedIDs={selectedContainerIDs}
          />
        );
      case 'images':
        return (
          <ImagesPage
            filter={imageFilter}
            imageUseCounts={imageUseCounts}
            images={images}
            loading={inventoryStatus === 'loading'}
            onFilterChange={setImageFilter}
            onInspect={openImageInspect}
            search={search}
          />
        );
      case 'volumes':
        return (
          <VolumesPage
            filter={volumeFilter}
            loading={inventoryStatus === 'loading'}
            onFilterChange={setVolumeFilter}
            onInspect={openVolumeInspect}
            search={search}
            volumeDetails={volumeDetails}
            volumes={volumes}
          />
        );
      case 'networks':
        return (
          <NetworksPage
            loading={inventoryStatus === 'loading'}
            networkDetails={networkDetails}
            networks={networks}
            onInspect={openNetworkInspect}
            search={search}
          />
        );
      default:
        return (
          <OverviewPage
            containers={containers}
            diskReclaimable={diskReclaimable}
            diskTotal={diskTotal}
            dockerRunning={dockerRunning}
            images={images}
            networks={networks}
            onNavigate={navigate}
            provider={activeProvider}
            runningContainers={runningContainers}
            unhealthyContainers={unhealthyContainers}
            volumes={volumes}
          />
        );
    }
  })();

  return (
    <main className="min-h-screen bg-bg-app text-text-primary">
      <div className="grid min-h-screen grid-cols-[236px_1fr]">
        <aside className="flex min-h-screen flex-col border-r border-border bg-bg-panel">
          <div className="flex h-16 items-center gap-3 border-b border-border px-4">
            <img src={logoUrl} alt="Cairn" className="h-9 max-w-32 object-contain" />
            <div className="min-w-0">
              <div className="text-sm font-semibold">Cairn</div>
              <div className="truncate text-xs text-text-muted">{versionLabel}</div>
            </div>
          </div>

          <nav className="flex-1 space-y-1 px-2 py-3" aria-label="Main navigation">
            {navItems.map((item) => {
              const Icon = item.icon;
              const active = activePage === item.id;
              const badge = item.id === 'containers' ? String(containers.length) : undefined;
              return (
                <button
                  key={item.id}
                  className={[
                    'flex h-10 w-full items-center gap-3 rounded-control px-3 text-left text-sm transition',
                    active
                      ? 'bg-accent/10 text-accent shadow-[inset_3px_0_0_rgb(45_212_167)]'
                      : 'text-text-secondary hover:bg-bg-card hover:text-text-primary',
                  ].join(' ')}
                  onClick={() => navigate(item.id)}
                  type="button"
                >
                  <Icon size={18} strokeWidth={1.8} />
                  <span className="flex-1 truncate">{item.label}</span>
                  {badge ? <Badge>{badge}</Badge> : null}
                </button>
              );
            })}
          </nav>

          <div className="border-t border-border p-3">
            <div className="rounded-card border border-border bg-bg-inset p-3">
              <div className="flex items-center gap-2 text-sm">
                <StatusDot tone={dockerRunning ? 'ok' : 'neutral'} />
                <span className="font-medium">Docker Engine</span>
                <span className="ml-auto text-xs text-text-muted">{statusLabel}</span>
              </div>
              <div className="mt-2 truncate font-mono text-xs text-text-muted">{providerName}</div>
              <div className="mt-2 truncate text-xs text-text-muted">
                {dockerVersion?.serverVersion ? `Engine ${dockerVersion.serverVersion}` : 'No engine version'}
              </div>
            </div>
          </div>
        </aside>

        <section className="flex min-w-0 flex-col">
          <header className="flex h-16 shrink-0 items-center justify-between border-b border-border bg-bg-app px-6">
            <div className="min-w-0">
              <h1 className="truncate text-xl font-semibold tracking-normal">{pageTitle}</h1>
              <p className="truncate text-sm text-text-muted">
                {dockerInfo?.name ?? providerName}
                {lastLoadedAt ? ` - refreshed ${relativeTime(lastLoadedAt)}` : ''}
              </p>
            </div>
            <div className="flex items-center gap-2">
              <SearchBox value={search} onChange={setSearch} />
              <Tooltip label="Refresh">
                <Button
                  aria-label="Refresh"
                  icon={<RefreshCw size={17} />}
                  loading={inventoryStatus === 'loading'}
                  onClick={() => {
                    void refreshInventory();
                  }}
                  size="icon"
                  variant="secondary"
                />
              </Tooltip>
              <Tooltip label="Notifications">
                <Button aria-label="Notifications" icon={<Bell size={17} />} size="icon" variant="secondary" />
              </Tooltip>
            </div>
          </header>

          {inventoryError ? (
            <div className="border-b border-border bg-warn/10 px-6 py-3 text-sm text-warn">
              Docker is not reachable
            </div>
          ) : null}
          {actionError ? (
            <div className="border-b border-border bg-error/10 px-6 py-3 text-sm text-error">
              {actionError}
            </div>
          ) : null}

          <div className="min-h-0 flex-1 overflow-auto p-6">{content}</div>
        </section>
      </div>

      <InspectModal inspect={inspect} onClose={() => setInspect(emptyInspect)} />
      <ConfirmPlanModal
        confirm={confirm}
        onApply={() => {
          void applyConfirmedPlan();
        }}
        onChangeTypedName={(typedName) => setConfirm((current) => ({ ...current, typedName }))}
        onClose={() => setConfirm(emptyConfirm)}
      />
    </main>
  );
}

type OverviewProps = {
  provider: ProviderSummary | null;
  dockerRunning: boolean;
  containers: ContainerSummary[];
  images: ImageSummary[];
  volumes: VolumeSummary[];
  networks: NetworkSummary[];
  runningContainers: number;
  unhealthyContainers: number;
  diskTotal: number;
  diskReclaimable: number;
  onNavigate: (page: PageID) => void;
};

function OverviewPage({
  containers,
  diskReclaimable,
  diskTotal,
  dockerRunning,
  images,
  networks,
  onNavigate,
  provider,
  runningContainers,
  unhealthyContainers,
  volumes,
}: OverviewProps) {
  const stopped = containers.length - runningContainers;
  return (
    <div className="space-y-6">
      <section className="grid grid-cols-[1.2fr_1fr] gap-4">
        <Card>
          <CardBody className="flex items-center justify-between">
            <div className="min-w-0">
              <div className="flex items-center gap-3">
                <StatusDot pulse={!dockerRunning && provider?.healthy} tone={dockerRunning ? 'ok' : 'neutral'} />
                <div>
                  <div className="text-lg font-semibold">Docker Engine - {dockerRunning ? 'Running' : 'Stopped'}</div>
                  <div className="text-sm text-text-muted">{provider?.name ?? 'No provider selected'}</div>
                </div>
              </div>
              <div className="mt-4 grid grid-cols-3 gap-3 text-sm">
                <StatusPill label="Provider" ok={provider?.healthy ?? false} />
                <StatusPill label="Docker" ok={dockerRunning} />
                <StatusPill label="Inventory" ok={containers.length + images.length + volumes.length + networks.length > 0} />
              </div>
            </div>
            <Server className="h-16 w-16 text-accent/70" strokeWidth={1.4} />
          </CardBody>
        </Card>

        <section className="grid grid-cols-2 gap-3" aria-label="Docker object counts">
          <MetricButton label="Containers" value={containers.length} hint={`${runningContainers} running`} onClick={() => onNavigate('containers')} />
          <MetricButton label="Images" value={images.length} hint={`${imageDanglingCount(images)} dangling`} onClick={() => onNavigate('images')} />
          <MetricButton label="Volumes" value={volumes.length} hint={`${volumes.filter((volume) => volume.inUse).length} in use`} onClick={() => onNavigate('volumes')} />
          <MetricButton label="Networks" value={networks.length} hint={`${networks.filter((network) => network.internal).length} internal`} onClick={() => onNavigate('networks')} />
        </section>
      </section>

      <section className="grid grid-cols-[1fr_360px] gap-4">
        <Card>
          <CardHeader
            actions={<Badge tone={unhealthyContainers > 0 ? 'error' : 'ok'}>{unhealthyContainers} unhealthy</Badge>}
            title="Container Status"
          />
          <CardBody>
            <div className="grid grid-cols-3 gap-3">
              <StatusBlock label="Running" tone="ok" value={runningContainers} />
              <StatusBlock label="Stopped" tone="neutral" value={stopped} />
              <StatusBlock label="Unhealthy" tone={unhealthyContainers > 0 ? 'error' : 'neutral'} value={unhealthyContainers} />
            </div>
            <div className="mt-5 space-y-2">
              {containers.slice(0, 6).map((container) => (
                <div className="grid grid-cols-[1fr_auto_auto] items-center gap-3 text-sm" key={container.id}>
                  <div className="min-w-0 truncate">{container.name}</div>
                  <Badge tone={containerTone(container)}>{container.state || 'unknown'}</Badge>
                  <span className="text-xs text-text-muted">{formatBytes(container.memoryBytes)}</span>
                </div>
              ))}
              {containers.length === 0 ? (
                <EmptyState
                  body="Import a project or run an image to populate the local inventory."
                  icon={<Container size={28} />}
                  title="No containers yet"
                />
              ) : null}
            </div>
          </CardBody>
        </Card>

        <Card>
          <CardHeader actions={<HardDrive size={16} className="text-text-muted" />} title="Disk Usage" />
          <CardBody>
            <div className="text-3xl font-semibold">{formatBytes(diskTotal)}</div>
            <div className="mt-1 text-sm text-text-muted">{formatBytes(diskReclaimable)} reclaimable</div>
            <div className="mt-5 h-3 overflow-hidden rounded-full bg-bg-inset">
              <div className="h-full bg-accent" style={{ width: `${diskTotal > 0 ? Math.max(8, ((diskTotal - diskReclaimable) / diskTotal) * 100) : 0}%` }} />
            </div>
            <div className="mt-5 grid grid-cols-2 gap-2 text-xs text-text-muted">
              <span>Images {images.length}</span>
              <span>Volumes {volumes.length}</span>
              <span>Containers {containers.length}</span>
              <span>Networks {networks.length}</span>
            </div>
          </CardBody>
        </Card>
      </section>
    </div>
  );
}

type ContainersPageProps = {
  containers: ContainerSummary[];
  filter: FilterID;
  search: string;
  loading: boolean;
  selectedIDs: Set<string>;
  actionBusyIDs: Set<string>;
  onAction: (action: ContainerAction, container: ContainerSummary) => void;
  onBulkAction: (action: Exclude<ContainerAction, 'kill'>) => void;
  onFilterChange: (filter: FilterID) => void;
  onInspect: (container: ContainerSummary) => void;
  onToggleSelection: (id: string) => void;
};

function ContainersPage({
  actionBusyIDs,
  containers,
  filter,
  loading,
  onAction,
  onBulkAction,
  onFilterChange,
  onInspect,
  onToggleSelection,
  search,
  selectedIDs,
}: ContainersPageProps) {
  const filtered = useMemo(
    () => filterContainers(containers, search, filter),
    [containers, filter, search],
  );
  if (loading && containers.length === 0) {
    return <TableSkeleton />;
  }
  return (
    <div className="space-y-4">
      <FilterChips
        active={filter}
        items={[
          ['all', 'All', containers.length],
          ['running', 'Running', containers.filter((container) => container.state === 'running').length],
          ['stopped', 'Stopped', containers.filter((container) => container.state === 'exited').length],
          ['paused', 'Paused', containers.filter((container) => container.state === 'paused').length],
          ['unhealthy', 'Unhealthy', containers.filter((container) => container.health === 'unhealthy').length],
          ['ungrouped', 'Ungrouped', containers.filter((container) => !container.projectID).length],
        ]}
        onChange={onFilterChange}
      />
      <DataTable
        columns={[
          {
            id: 'name',
            header: 'Name',
            render: (container) => (
              <div className="min-w-0">
                <div className="truncate text-text-primary">{container.name}</div>
                <div className="truncate text-xs text-text-muted">{container.service || shortID(container.id)}</div>
              </div>
            ),
            sortable: true,
            sortValue: (container) => container.name,
          },
          {
            id: 'status',
            header: 'Status',
            render: (container) => <Badge tone={containerTone(container)}>{container.state || 'unknown'}</Badge>,
            sortable: true,
            sortValue: (container) => container.state,
          },
          {
            id: 'project',
            header: 'Project',
            render: (container) => container.projectID || '-',
            sortable: true,
            sortValue: (container) => container.projectID || '',
          },
          {
            id: 'image',
            header: 'Image',
            render: (container) => <span title={container.image}>{container.image}</span>,
            sortable: true,
            sortValue: (container) => container.image,
          },
          {
            id: 'ports',
            header: 'Ports',
            render: (container) => <PortList ports={container.ports ?? []} />,
          },
          {
            id: 'memory',
            header: 'Memory',
            render: (container) => formatMemory(container.memoryBytes, container.memoryLimit),
            sortable: true,
            sortValue: (container) => container.memoryBytes ?? 0,
          },
          {
            id: 'health',
            header: 'Health',
            render: (container) => <Badge tone={healthTone(container.health)}>{container.health || 'unknown'}</Badge>,
            sortable: true,
            sortValue: (container) => container.health,
          },
          {
            id: 'restarts',
            header: 'Restarts',
            render: (container) => <span className={(container.restarts ?? 0) > 3 ? 'text-error' : undefined}>{container.restarts ?? 0}</span>,
            sortable: true,
            sortValue: (container) => container.restarts ?? 0,
          },
          {
            id: 'actions',
            header: '',
            render: (container) => (
              <ContainerRowActions
                busyIDs={actionBusyIDs}
                container={container}
                onAction={onAction}
                onInspect={onInspect}
              />
            ),
          },
        ]}
        bulkActions={<ContainerBulkActions busyIDs={actionBusyIDs} onAction={onBulkAction} />}
        empty={
          <EmptyState
            body="Run your first container or import a Compose project."
            icon={<Container size={28} />}
            title="No containers match"
          />
        }
        getRowID={(container) => container.id}
        onToggleRow={onToggleSelection}
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
  onFilterChange: (filter: FilterID) => void;
  onInspect: (image: ImageSummary) => void;
};

function ImagesPage({ filter, imageUseCounts, images, loading, onFilterChange, onInspect, search }: ImagesPageProps) {
  const filtered = useMemo(() => filterImages(images, imageUseCounts, search, filter), [filter, imageUseCounts, images, search]);
  if (loading && images.length === 0) {
    return <TableSkeleton />;
  }
  return (
    <div className="space-y-4">
      <FilterChips
        active={filter}
        items={[
          ['all', 'All', images.length],
          ['in-use', 'In use', images.filter((image) => (imageUseCounts[image.id] ?? 0) > 0 || image.inUse).length],
          ['unused', 'Unused', images.filter((image) => (imageUseCounts[image.id] ?? 0) === 0 && !image.inUse).length],
          ['dangling', 'Dangling', imageDanglingCount(images)],
          ['updates', 'Update available', images.filter((image) => image.updateStatus && image.updateStatus !== 'unknown').length],
        ]}
        onChange={onFilterChange}
      />
      <DataTable
        columns={[
          {
            id: 'repo',
            header: 'Repository',
            render: (image) => imageRepo(image),
            sortable: true,
            sortValue: (image) => imageRepo(image),
          },
          {
            id: 'tag',
            header: 'Tag',
            render: (image) => imageTag(image),
            sortable: true,
            sortValue: (image) => imageTag(image),
          },
          {
            id: 'id',
            header: 'Image ID',
            render: (image) => <MonoCopy value={image.id} />,
            sortable: true,
            sortValue: (image) => image.id,
          },
          {
            id: 'size',
            header: 'Size',
            render: (image) => formatBytes(image.sizeBytes),
            sortable: true,
            sortValue: (image) => image.sizeBytes,
          },
          {
            id: 'created',
            header: 'Created',
            render: (image) => formatDate(image.createdAt),
            sortable: true,
            sortValue: (image) => dateMillis(image.createdAt),
          },
          {
            id: 'used-by',
            header: 'Used by',
            render: (image) => <Badge tone={(imageUseCounts[image.id] ?? 0) > 0 || image.inUse ? 'accent' : 'neutral'}>{imageUseCounts[image.id] ?? (image.inUse ? '>=1' : 0)}</Badge>,
          },
          {
            id: 'update',
            header: 'Update',
            render: (image) => <Badge tone={updateTone(image.updateStatus)}>{image.updateStatus || 'unknown'}</Badge>,
          },
          {
            id: 'actions',
            header: '',
            render: (image) => <RowActions id={image.id} label={primaryImageRef(image)} onInspect={() => onInspect(image)} />,
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
  onFilterChange: (filter: FilterID) => void;
  onInspect: (volume: VolumeSummary) => void;
};

function VolumesPage({ filter, loading, onFilterChange, onInspect, search, volumeDetails, volumes }: VolumesPageProps) {
  const filtered = useMemo(() => filterVolumes(volumes, search, filter), [filter, search, volumes]);
  if (loading && volumes.length === 0) {
    return <TableSkeleton />;
  }
  return (
    <div className="space-y-4">
      <FilterChips
        active={filter}
        items={[
          ['all', 'All', volumes.length],
          ['in-use', 'In use', volumes.filter((volume) => volume.inUse).length],
          ['unused', 'Unused', volumes.filter((volume) => !volume.inUse).length],
        ]}
        onChange={onFilterChange}
      />
      <DataTable
        columns={[
          {
            id: 'name',
            header: 'Name',
            render: (volume) => <span className="text-text-primary">{volume.name}</span>,
            sortable: true,
            sortValue: (volume) => volume.name,
          },
          {
            id: 'driver',
            header: 'Driver',
            render: (volume) => volume.driver,
            sortable: true,
            sortValue: (volume) => volume.driver,
          },
          {
            id: 'size',
            header: 'Size',
            render: (volume) => volume.sizeBytes ? formatBytes(volume.sizeBytes) : '-',
            sortable: true,
            sortValue: (volume) => volume.sizeBytes ?? 0,
          },
          {
            id: 'project',
            header: 'Project',
            render: (volume) => volume.labels?.[composeProjectLabel] ?? '-',
            sortable: true,
            sortValue: (volume) => volume.labels?.[composeProjectLabel] ?? '',
          },
          {
            id: 'used-by',
            header: 'Used by',
            render: (volume) => <Badge tone={volume.inUse ? 'accent' : 'neutral'}>{volumeDetails[volume.name]?.containers?.length ?? (volume.inUse ? '>=1' : 0)}</Badge>,
          },
          {
            id: 'mountpoint',
            header: 'Mountpoint',
            render: (volume) => <span title={volume.mountpoint}>{volume.mountpoint || '-'}</span>,
          },
          {
            id: 'actions',
            header: '',
            render: (volume) => <RowActions id={volume.name} label={volume.name} onInspect={() => onInspect(volume)} />,
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
  onInspect: (network: NetworkSummary) => void;
};

function NetworksPage({ loading, networkDetails, networks, onInspect, search }: NetworksPageProps) {
  const filtered = useMemo(() => filterNetworks(networks, search), [networks, search]);
  if (loading && networks.length === 0) {
    return <TableSkeleton />;
  }
  return (
    <DataTable
      columns={[
        {
          id: 'name',
          header: 'Name',
          render: (network) => <span className="text-text-primary">{network.name}</span>,
          sortable: true,
          sortValue: (network) => network.name,
        },
        {
          id: 'driver',
          header: 'Driver',
          render: (network) => network.driver,
          sortable: true,
          sortValue: (network) => network.driver,
        },
        {
          id: 'scope',
          header: 'Scope',
          render: (network) => network.scope || '-',
          sortable: true,
          sortValue: (network) => network.scope || '',
        },
        {
          id: 'subnet',
          header: 'Subnet',
          render: (network) => networkDetails[network.id]?.subnet || '-',
        },
        {
          id: 'gateway',
          header: 'Gateway',
          render: (network) => networkDetails[network.id]?.gateway || '-',
        },
        {
          id: 'containers',
          header: 'Containers',
          render: (network) => <Badge tone="neutral">{networkDetails[network.id]?.containers?.length ?? 0}</Badge>,
        },
        {
          id: 'internal',
          header: 'Internal',
          render: (network) => <Badge tone={network.internal ? 'info' : 'neutral'}>{network.internal ? 'yes' : 'no'}</Badge>,
          sortable: true,
          sortValue: (network) => (network.internal ? 1 : 0),
        },
        {
          id: 'actions',
          header: '',
          render: (network) => <RowActions id={network.id} label={network.name} onInspect={() => onInspect(network)} />,
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
  );
}

function SearchBox({ onChange, value }: { value: string; onChange: (value: string) => void }) {
  return (
    <label className="flex h-9 w-72 items-center gap-2 rounded-control border border-border bg-bg-inset px-3 text-sm text-text-muted">
      <Search size={16} />
      <input
        aria-label="Search inventory"
        className="min-w-0 flex-1 bg-transparent text-text-primary outline-none placeholder:text-text-muted"
        onChange={(event) => onChange(event.target.value)}
        placeholder="Search"
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
            'inline-flex h-8 items-center gap-2 rounded-full border px-3 text-xs font-medium transition',
            active === id
              ? 'border-accent/40 bg-accent/10 text-accent'
              : 'border-border bg-bg-inset text-text-secondary hover:text-text-primary',
          ].join(' ')}
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

function RowActions({ id, label, onInspect }: { id: string; label: string; onInspect: () => void }) {
  return (
    <div className="flex justify-end gap-1">
      <Tooltip label="Inspect">
        <Button aria-label={`Inspect ${label}`} icon={<Eye size={15} />} onClick={onInspect} size="icon" variant="ghost" />
      </Tooltip>
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

function ContainerRowActions({
  busyIDs,
  container,
  onAction,
  onInspect,
}: {
  busyIDs: Set<string>;
  container: ContainerSummary;
  onAction: (action: ContainerAction, container: ContainerSummary) => void;
  onInspect: (container: ContainerSummary) => void;
}) {
  const canStop = container.state === 'running' || container.state === 'paused' || container.state === 'restarting';
  const canStart = container.state !== 'running' && container.state !== 'restarting';
  return (
    <div className="flex justify-end gap-1">
      <Tooltip label={canStart ? 'Start' : 'Stop'}>
        <Button
          aria-label={`${canStart ? 'Start' : 'Stop'} ${container.name}`}
          disabled={busyIDs.has(`${canStart ? 'start' : 'stop'}:${container.id}`)}
          icon={canStart ? <Play size={15} /> : <Square size={15} />}
          onClick={() => onAction(canStart ? 'start' : 'stop', container)}
          size="icon"
          variant="ghost"
        />
      </Tooltip>
      <Tooltip label="Restart">
        <Button
          aria-label={`Restart ${container.name}`}
          disabled={!canStop || busyIDs.has(`restart:${container.id}`)}
          disabledReason="Container is not running"
          icon={<RotateCw size={15} />}
          onClick={() => onAction('restart', container)}
          size="icon"
          variant="ghost"
        />
      </Tooltip>
      <Tooltip label="Kill">
        <Button
          aria-label={`Kill ${container.name}`}
          disabled={!canStop || busyIDs.has(`kill:${container.id}`)}
          disabledReason="Container is not running"
          icon={<Skull size={15} />}
          onClick={() => onAction('kill', container)}
          size="icon"
          variant="ghost"
        />
      </Tooltip>
      <RowActions id={container.id} label={container.name} onInspect={() => onInspect(container)} />
    </div>
  );
}

function ContainerBulkActions({
  busyIDs,
  onAction,
}: {
  busyIDs: Set<string>;
  onAction: (action: Exclude<ContainerAction, 'kill'>) => void;
}) {
  return (
    <div className="flex items-center gap-1">
      <Button
        icon={<Play size={15} />}
        loading={busyIDs.has('bulk:start')}
        onClick={() => onAction('start')}
        size="sm"
        variant="secondary"
      >
        Start
      </Button>
      <Button
        icon={<Square size={15} />}
        loading={busyIDs.has('bulk:stop')}
        onClick={() => onAction('stop')}
        size="sm"
        variant="secondary"
      >
        Stop
      </Button>
      <Button
        icon={<RotateCw size={15} />}
        loading={busyIDs.has('bulk:restart')}
        onClick={() => onAction('restart')}
        size="sm"
        variant="secondary"
      >
        Restart
      </Button>
    </div>
  );
}

function InspectModal({ inspect, onClose }: { inspect: InspectState; onClose: () => void }) {
  return (
    <Modal open={inspect.open} onClose={onClose} size="lg" title={inspect.title || 'Inspect'}>
      {inspect.subtitle ? <div className="mb-3 font-mono text-xs text-text-muted">{inspect.subtitle}</div> : null}
      <div className="grid grid-cols-2 gap-3">
        {inspect.rows.map(([label, value]) => (
          <div className="rounded-control border border-border bg-bg-inset p-3" key={label}>
            <div className="text-xs text-text-muted">{label}</div>
            <div className="mt-1 truncate font-mono text-xs text-text-primary" title={value}>
              {value || '-'}
            </div>
          </div>
        ))}
      </div>
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
        <details className="mt-4 rounded-control border border-border bg-bg-inset" open>
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
  const typedName = plan?.requiresTypedName ?? '';
  const typedReady = !typedName || confirm.typedName === typedName;
  return (
    <Modal
      busy={confirm.busy}
      danger={plan?.risk === 'destructive' || plan?.risk === 'dangerous'}
      footer={
        <div className="flex justify-end gap-2">
          <Button disabled={confirm.busy} onClick={onClose} variant="secondary">
            Cancel
          </Button>
          <Button disabled={!typedReady} loading={confirm.busy} onClick={onApply} variant="danger">
            Confirm
          </Button>
        </div>
      }
      onClose={onClose}
      open={confirm.open}
      size="lg"
      title={plan?.title ?? 'Confirm action'}
    >
      {plan ? (
        <div className="space-y-4">
          <div className="flex items-center gap-2">
            <Badge tone={riskTone(plan.risk)}>{plan.risk}</Badge>
            <span className="text-text-muted">Plan expires {formatDate(plan.expiresAt)}</span>
          </div>
          <div>
            <div className="mb-2 text-sm font-medium text-text-primary">Effects</div>
            <ul className="space-y-2">
              {plan.effects?.map((effect) => (
                <li className="rounded-control border border-border bg-bg-inset p-3" key={effect}>
                  {effect}
                </li>
              ))}
            </ul>
          </div>
          <div>
            <div className="mb-2 text-sm font-medium text-text-primary">Commands</div>
            <div className="space-y-2">
              {plan.commands?.map((command) => (
                <div className="rounded-control border border-border bg-bg-inset p-3" key={`${command.order}-${command.command}`}>
                  <div className="font-mono text-xs text-text-primary">{command.command}</div>
                  <div className="mt-2 text-xs text-text-muted">{command.explanation}</div>
                </div>
              ))}
            </div>
          </div>
          {typedName ? (
            <label className="block">
              <span className="text-sm font-medium text-text-primary">Type {typedName} to confirm</span>
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

function MetricButton({ hint, label, onClick, value }: { label: string; value: number; hint: string; onClick: () => void }) {
  return (
    <button
      className="rounded-card border border-border bg-bg-card p-4 text-left transition hover:border-border-strong hover:bg-bg-panel"
      onClick={onClick}
      type="button"
    >
      <div className="text-sm text-text-secondary">{label}</div>
      <div className="mt-3 text-2xl font-semibold">{value}</div>
      <div className="mt-2 text-xs text-text-muted">{hint}</div>
    </button>
  );
}

function StatusPill({ label, ok }: { label: string; ok: boolean }) {
  return (
    <div className="rounded-control border border-border bg-bg-inset p-3">
      <div className="flex items-center gap-2">
        <StatusDot tone={ok ? 'ok' : 'neutral'} />
        <span>{label}</span>
      </div>
    </div>
  );
}

function StatusBlock({ label, tone, value }: { label: string; value: number; tone: BadgeTone }) {
  return (
    <div className="rounded-control border border-border bg-bg-inset p-3">
      <Badge tone={tone}>{label}</Badge>
      <div className="mt-3 text-2xl font-semibold">{value}</div>
    </div>
  );
}

function TableSkeleton() {
  return (
    <div className="space-y-3">
      <Skeleton className="h-8 w-80" />
      <Skeleton className="h-[420px] w-full" />
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
        <Badge key={`${port.hostIP}-${port.hostPort}-${port.containerPort}-${port.protocol}`}>
          {port.hostPort ? `${port.hostPort}->` : ''}
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

function activeProviderSummary(providers: ProviderSummary[]): ProviderSummary | null {
  return providers.find((provider) => provider.active) ?? providers[0] ?? null;
}

function filterContainers(containers: ContainerSummary[], search: string, filter: FilterID) {
  const needle = normalize(search);
  return containers.filter((container) => {
    const matchesSearch = [container.name, container.image, container.id, container.projectID, container.service]
      .filter(Boolean)
      .some((value) => normalize(value).includes(needle));
    const matchesFilter =
      filter === 'all' ||
      (filter === 'stopped' && container.state === 'exited') ||
      (filter === 'ungrouped' && !container.projectID) ||
      container.state === filter ||
      (filter === 'unhealthy' && container.health === 'unhealthy');
    return matchesSearch && matchesFilter;
  });
}

function filterImages(images: ImageSummary[], counts: Record<string, number>, search: string, filter: FilterID) {
  const needle = normalize(search);
  return images.filter((image) => {
    const refs = imageRefs(image);
    const matchesSearch = [image.id, ...refs, ...(image.repoDigests ?? [])].some((value) => normalize(value).includes(needle));
    const inUse = (counts[image.id] ?? 0) > 0 || image.inUse;
    const matchesFilter =
      filter === 'all' ||
      (filter === 'in-use' && inUse) ||
      (filter === 'unused' && !inUse) ||
      (filter === 'dangling' && imageDangling(image)) ||
      (filter === 'updates' && Boolean(image.updateStatus && image.updateStatus !== 'unknown'));
    return matchesSearch && matchesFilter;
  });
}

function filterVolumes(volumes: VolumeSummary[], search: string, filter: FilterID) {
  const needle = normalize(search);
  return volumes.filter((volume) => {
    const matchesSearch = [volume.name, volume.driver, volume.mountpoint, volume.labels?.[composeProjectLabel]]
      .filter(Boolean)
      .some((value) => normalize(value).includes(needle));
    const matchesFilter = filter === 'all' || (filter === 'in-use' && volume.inUse) || (filter === 'unused' && !volume.inUse);
    return matchesSearch && matchesFilter;
  });
}

function filterNetworks(networks: NetworkSummary[], search: string) {
  const needle = normalize(search);
  return networks.filter((network) =>
    [network.name, network.id, network.driver, network.scope]
      .filter(Boolean)
      .some((value) => normalize(value).includes(needle)),
  );
}

function normalize(value: unknown) {
  return String(value ?? '').toLowerCase();
}

function imageUsageCounts(containers: ContainerSummary[]) {
  return containers.reduce<Record<string, number>>((counts, container) => {
    if (container.imageID) {
      counts[container.imageID] = (counts[container.imageID] ?? 0) + 1;
    }
    return counts;
  }, {});
}

function containerTone(container: ContainerSummary): BadgeTone {
  if (container.health === 'unhealthy') {
    return 'error';
  }
  switch (container.state) {
    case 'running':
      return 'ok';
    case 'paused':
    case 'restarting':
      return 'warn';
    case 'dead':
      return 'error';
    default:
      return 'neutral';
  }
}

function healthTone(health: string): BadgeTone {
  switch (health) {
    case 'healthy':
      return 'ok';
    case 'starting':
      return 'warn';
    case 'unhealthy':
      return 'error';
    default:
      return 'neutral';
  }
}

function updateTone(status?: string): BadgeTone {
  if (!status || status === 'unknown') {
    return 'neutral';
  }
  if (status === 'up_to_date') {
    return 'ok';
  }
  if (status === 'error' || status === 'auth_required' || status === 'rate_limited') {
    return 'error';
  }
  return 'warn';
}

function riskTone(risk?: string): BadgeTone {
  switch (risk) {
    case 'dangerous':
    case 'destructive':
      return 'error';
    case 'needs_confirmation':
      return 'warn';
    case 'safe':
      return 'ok';
    default:
      return 'neutral';
  }
}

function formatBytes(value?: number) {
  if (!value || value <= 0) {
    return '0 B';
  }
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
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
    return '-';
  }
  return limit ? `${formatBytes(used)} / ${formatBytes(limit)}` : formatBytes(used);
}

function shortID(value: string) {
  if (!value) {
    return '-';
  }
  const clean = value.replace(/^sha256:/, '');
  return clean.length > 12 ? `${clean.slice(0, 12)}` : clean;
}

function dateMillis(value: unknown) {
  const date = toDate(value);
  return date?.getTime() ?? 0;
}

function formatDate(value: unknown) {
  const date = toDate(value);
  if (!date) {
    return '-';
  }
  return date.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' });
}

function relativeTime(value: number) {
  const diff = Math.max(0, Date.now() - value);
  if (diff < 60_000) {
    return 'just now';
  }
  const minutes = Math.floor(diff / 60_000);
  if (minutes < 60) {
    return `${minutes}m ago`;
  }
  return `${Math.floor(minutes / 60)}h ago`;
}

function toDate(value: unknown): Date | null {
  if (!value) {
    return null;
  }
  const date = value instanceof Date ? value : new Date(String(value));
  return Number.isNaN(date.getTime()) ? null : date;
}

function imageRefs(image: ImageSummary) {
  const tags = image.repoTags?.filter((tag) => tag && tag !== '<none>:<none>') ?? [];
  return tags.length > 0 ? tags : image.repoDigests ?? [];
}

function primaryImageRef(image: ImageSummary) {
  return imageRefs(image)[0] ?? shortID(image.id);
}

function imageRepo(image: ImageSummary) {
  const ref = primaryImageRef(image);
  const slash = ref.includes('/') ? ref.lastIndexOf('/') : -1;
  const colon = ref.lastIndexOf(':');
  if (colon > slash) {
    return ref.slice(0, colon);
  }
  return ref;
}

function imageTag(image: ImageSummary) {
  const ref = primaryImageRef(image);
  const slash = ref.includes('/') ? ref.lastIndexOf('/') : -1;
  const colon = ref.lastIndexOf(':');
  if (colon > slash) {
    return ref.slice(colon + 1);
  }
  return '<none>';
}

function imageDangling(image: ImageSummary) {
  return imageRefs(image).length === 0;
}

function imageDanglingCount(images: ImageSummary[]) {
  return images.filter(imageDangling).length;
}

function containerRows(container: ContainerSummary): Array<[string, string]> {
  return [
    ['ID', container.id],
    ['Image', container.image],
    ['Image ID', container.imageID ?? '-'],
    ['Status', container.state],
    ['Health', container.health],
    ['Project', container.projectID ?? '-'],
    ['Service', container.service ?? '-'],
    ['Created', formatDate(container.createdAt)],
    ['Restarts', String(container.restarts ?? 0)],
  ];
}

function imageRows(image: ImageSummary, usedBy: number): Array<[string, string]> {
  return [
    ['Image ID', image.id],
    ['Reference', primaryImageRef(image)],
    ['Size', formatBytes(image.sizeBytes)],
    ['Created', formatDate(image.createdAt)],
    ['Used by', String(usedBy || (image.inUse ? '>=1' : 0))],
    ['Update', image.updateStatus ?? 'unknown'],
  ];
}

function imageDetailRows(detail: ImageDetail, usedBy: number): Array<[string, string]> {
  return [
    ...imageRows(detail.summary, usedBy),
    ['Architecture', detail.architecture || '-'],
    ['OS', detail.os || '-'],
    ['Author', detail.author || '-'],
    ['Layers', String(detail.layers?.length ?? 0)],
  ];
}

function volumeRows(volume: VolumeSummary, detail?: VolumeDetail): Array<[string, string]> {
  return [
    ['Name', volume.name],
    ['Driver', volume.driver],
    ['Size', volume.sizeBytes ? formatBytes(volume.sizeBytes) : '-'],
    ['In use', volume.inUse ? 'yes' : 'no'],
    ['Containers', String(detail?.containers?.length ?? 0)],
    ['Mountpoint', volume.mountpoint ?? '-'],
    ['Project', volume.labels?.[composeProjectLabel] ?? '-'],
  ];
}

function networkRows(network: NetworkSummary, detail?: NetworkDetail): Array<[string, string]> {
  return [
    ['ID', network.id],
    ['Name', network.name],
    ['Driver', network.driver],
    ['Scope', network.scope ?? '-'],
    ['Subnet', detail?.subnet ?? '-'],
    ['Gateway', detail?.gateway ?? '-'],
    ['Containers', String(detail?.containers?.length ?? 0)],
    ['Internal', network.internal ? 'yes' : 'no'],
    ['Attachable', network.attachable ? 'yes' : 'no'],
  ];
}

function formatJSON(raw: string) {
  try {
    return JSON.stringify(JSON.parse(raw), null, 2);
  } catch {
    return raw;
  }
}

const composeProjectLabel = 'com.docker.compose.project';

export default App;
