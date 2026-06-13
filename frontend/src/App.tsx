import type { LucideIcon } from 'lucide-react';
import type {
  AuditEntry,
  CommandPlan,
  ContainerSummary,
  DashboardMetrics,
  ExportResult,
  HubSearchResult,
  ImageDetail,
  ImageSummary,
  LogLine,
  MetricRankItem,
  MountSpec,
  NetworkDetail,
  NetworkSummary,
  PortMapping,
  PortBinding,
  ProviderProblem,
  ProjectDetail,
  ProjectSummary,
  ProviderSummary,
  RunImageRequest,
  StatsScope,
  VolumeDetail,
  VolumeSummary,
} from '../bindings/github.com/RCooLeR/Cairn/internal/models/models.js';

import {
  Activity,
  AlertTriangle,
  ArrowDown,
  BarChart3,
  Bell,
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
  LayoutGrid,
  List,
  MemoryStick,
  MoreVertical,
  PackagePlus,
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
  ShieldAlert,
  Skull,
  Square,
  Terminal,
  Trash2,
  Upload,
  Wifi,
  Wrench,
  WrapText,
} from 'lucide-react';
import {
  type ReactNode,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
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
} from 'recharts';

import Editor from '@monaco-editor/react';
import { Clipboard, Dialogs, Events } from '@wailsio/runtime';

import { getAppVersion } from './api/app';
import {
  DockerService,
  LogsService,
  MetricsService,
  ProviderService,
  ProjectService,
  SettingsService,
} from './api/services';
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
  Toast,
  Tooltip,
} from './components/ui';
import {
  CommandPalette,
  TerminalPage,
  type TerminalCommandRequest,
} from './components/terminal/TerminalPage';
import { useAppStore } from './state/appStore';
import { useInventoryStore } from './state/inventoryStore';

const logoUrl = '/cairn-logo.png';

type PageID =
  | 'overview'
  | 'projects'
  | 'containers'
  | 'images'
  | 'volumes'
  | 'networks'
  | 'logs'
  | 'terminal';
type FilterID = string;
type BadgeTone = 'ok' | 'warn' | 'error' | 'info' | 'neutral' | 'accent';
type StatusToneID = 'ok' | 'warn' | 'error' | 'info' | 'neutral';
type LoadStatus = 'idle' | 'loading' | 'ready' | 'error';
type ProjectViewMode = 'grid' | 'list';
type ProjectSortID = 'name' | 'activity' | 'cpu';
type ProjectTabID = 'overview' | 'services' | 'containers' | 'compose';
type LogScope = 'all' | 'project' | 'service' | 'container';
type LogLevelFilter = 'error' | 'warn' | 'info' | 'debug' | 'unknown';
type PermissionMode = 'ask' | 'group' | 'rootless';

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
type ProjectAction =
  | 'start'
  | 'stop'
  | 'restart'
  | 'pull'
  | 'redeploy'
  | 'down'
  | 'down-volumes';
type ConfirmPlanKind = 'container' | 'project';

type ConfirmState = {
  open: boolean;
  plan: CommandPlan | null;
  planKind: ConfirmPlanKind;
  targetName: string;
  typedName: string;
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

type CreateVolumeState = {
  open: boolean;
  name: string;
  driver: string;
  driverOptsText: string;
  labelsText: string;
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

type ExportLogsState = {
  open: boolean;
  path: string;
  format: 'log' | 'jsonl';
  range: 'buffer' | 'tail';
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

type DashboardMetricID = 'cpu' | 'memory' | 'network';
type DashboardRangeID = '5m' | '1h' | '24h';

type StatsSample = {
  projectID?: string;
  serviceID?: string;
  containerID: string;
  containerName?: string;
  health?: string;
  restartCount?: number;
  uptimeSeconds?: number;
  cpuPercent: number;
  memoryBytes: number;
  memoryLimitBytes?: number;
  networkRxRate: number;
  networkTxRate: number;
  sampledAt: unknown;
};

type StatsSamplePayload = {
  streamID: string;
  samples?: StatsSample[];
};

type DashboardChartPoint = {
  ts: number;
  label: string;
  cpu: number;
  memory: number;
  netRx: number;
  netTx: number;
};

type SparkPoint = {
  label: string;
  value: number;
};

type CleanupState = {
  open: boolean;
  includeImages: boolean;
  includeContainers: boolean;
  includeBuildCache: boolean;
  includeVolumes: boolean;
  typedName: string;
};

const navItems: NavItem[] = [
  { id: 'overview', label: 'Overview', icon: Gauge },
  { id: 'projects', label: 'Projects', icon: LayoutGrid },
  { id: 'containers', label: 'Containers', icon: Container },
  { id: 'images', label: 'Images', icon: Box },
  { id: 'volumes', label: 'Volumes', icon: Database },
  { id: 'networks', label: 'Networks', icon: Network },
  { id: 'logs', label: 'Logs', icon: ScrollText },
  { id: 'terminal', label: 'Terminal', icon: Terminal },
];

const emptyInspect: InspectState = {
  open: false,
  title: '',
  rows: [],
};

const emptyConfirm: ConfirmState = {
  open: false,
  plan: null,
  planKind: 'container',
  targetName: '',
  typedName: '',
  busy: false,
};

const emptyRename: RenameState = {
  open: false,
  container: null,
  name: '',
  busy: false,
};

const emptyRunImage: RunImageState = {
  open: false,
  step: 1,
  imageRef: '',
  imageLocked: false,
  name: '',
  pullIfMissing: true,
  portsText: '',
  envText: '',
  volumesText: '',
  networkID: '',
  restartPolicy: 'no',
  commandText: '',
  user: '',
  hubQuery: '',
  hubResults: [],
  hubLoading: false,
  busy: false,
};

const emptyPullImage: PullImageState = {
  open: false,
  ref: '',
  tag: 'latest',
  query: '',
  results: [],
  loadingResults: false,
  busy: false,
};

const emptySaveImage: SaveImageState = {
  open: false,
  refsText: '',
  destPath: '',
  busy: false,
};

const emptyLoadImage: LoadImageState = {
  open: false,
  srcPath: '',
  busy: false,
};

const emptyCreateVolume: CreateVolumeState = {
  open: false,
  name: '',
  driver: 'local',
  driverOptsText: '',
  labelsText: '',
  busy: false,
};

const emptyCreateNetwork: CreateNetworkState = {
  open: false,
  name: '',
  driver: 'bridge',
  customDriver: '',
  subnet: '',
  gateway: '',
  internal: false,
  attachable: false,
  labelsText: '',
  busy: false,
};

const emptyImportProject: ImportProjectState = {
  open: false,
  folderPath: '',
  busy: false,
  imported: null,
};

const emptyExportLogs: ExportLogsState = {
  open: false,
  path: '',
  format: 'jsonl',
  range: 'buffer',
  busy: false,
  result: null,
};

const emptyCleanup: CleanupState = {
  open: false,
  includeImages: true,
  includeContainers: true,
  includeBuildCache: true,
  includeVolumes: false,
  typedName: '',
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
  const [projects, setProjects] = useState<ProjectSummary[]>([]);
  const [projectsStatus, setProjectsStatus] = useState<LoadStatus>('idle');
  const [projectsError, setProjectsError] = useState<string | null>(null);
  const [activeProjectID, setActiveProjectID] = useState<string | null>(null);
  const [projectDetail, setProjectDetail] = useState<ProjectDetail | null>(
    null,
  );
  const [projectDetailStatus, setProjectDetailStatus] =
    useState<LoadStatus>('idle');
  const [projectDetailError, setProjectDetailError] = useState<string | null>(
    null,
  );
  const [projectTab, setProjectTab] = useState<ProjectTabID>('overview');
  const [projectFilter, setProjectFilter] = useState<FilterID>('all');
  const [projectSort, setProjectSort] = useState<ProjectSortID>('name');
  const [projectView, setProjectView] = useState<ProjectViewMode>(() => {
    const saved = window.localStorage.getItem('cairn.projects.view');
    return saved === 'list' ? 'list' : 'grid';
  });
  const [containerFilter, setContainerFilter] = useState<FilterID>('all');
  const [imageFilter, setImageFilter] = useState<FilterID>('all');
  const [volumeFilter, setVolumeFilter] = useState<FilterID>('all');
  const [inspect, setInspect] = useState<InspectState>(emptyInspect);
  const [confirm, setConfirm] = useState<ConfirmState>(emptyConfirm);
  const [rename, setRename] = useState<RenameState>(emptyRename);
  const [runImage, setRunImage] = useState<RunImageState>(emptyRunImage);
  const [pullImage, setPullImage] = useState<PullImageState>(emptyPullImage);
  const [saveImage, setSaveImage] = useState<SaveImageState>(emptySaveImage);
  const [loadImage, setLoadImage] = useState<LoadImageState>(emptyLoadImage);
  const [createVolume, setCreateVolume] =
    useState<CreateVolumeState>(emptyCreateVolume);
  const [createNetwork, setCreateNetwork] =
    useState<CreateNetworkState>(emptyCreateNetwork);
  const [importProject, setImportProject] =
    useState<ImportProjectState>(emptyImportProject);
  const [selectedContainerIDs, setSelectedContainerIDs] = useState(
    () => new Set<string>(),
  );
  const [busyActionIDs, setBusyActionIDs] = useState(() => new Set<string>());
  const [actionError, setActionError] = useState<string | null>(null);
  const [providerActionBusy, setProviderActionBusy] = useState(false);
  const [repairOpen, setRepairOpen] = useState(false);
  const [repairError, setRepairError] = useState<string | null>(null);
  const [repairSaving, setRepairSaving] = useState(false);
  const [permissionMode, setPermissionMode] =
    useState<PermissionMode>('ask');
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [queuedTerminalCommand, setQueuedTerminalCommand] =
    useState<TerminalCommandRequest | null>(null);

  const navigate = useCallback((page: PageID) => {
    setActivePage(page);
    setSearch('');
  }, []);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault();
        setPaletteOpen(true);
      }
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, []);

  const runPaletteCommand = useCallback(
    (command: string) => {
      setQueuedTerminalCommand({ id: Date.now(), command });
      navigate('terminal');
    },
    [navigate],
  );

  const showContainers = useCallback(
    (filter: FilterID = 'all') => {
      setContainerFilter(filter);
      navigate('containers');
    },
    [navigate],
  );

  const refreshProjects = useCallback(async () => {
    setProjectsStatus('loading');
    setProjectsError(null);
    try {
      const nextProjects = await ProjectService.RefreshProjects();
      setProjects(nextProjects ?? []);
      setProjectsStatus('ready');
    } catch (error: unknown) {
      setProjectsError(
        error instanceof Error ? error.message : 'Unable to refresh projects',
      );
      setProjectsStatus('error');
    }
  }, []);

  const refreshProjectDetail = useCallback(async (projectID: string) => {
    setProjectDetailStatus('loading');
    setProjectDetailError(null);
    try {
      const detail = await ProjectService.GetProject(projectID);
      if (!detail) {
        throw new Error('Project was not found');
      }
      setProjectDetail(detail);
      setProjectDetailStatus('ready');
    } catch (error: unknown) {
      setProjectDetail(null);
      setProjectDetailError(
        error instanceof Error ? error.message : 'Unable to load project',
      );
      setProjectDetailStatus('error');
    }
  }, []);

  const openProjectDetail = useCallback(
    (project: ProjectSummary) => {
      setActiveProjectID(project.id);
      setProjectTab('overview');
      void refreshProjectDetail(project.id);
    },
    [refreshProjectDetail],
  );

  const closeProjectDetail = useCallback(() => {
    setActiveProjectID(null);
    setProjectDetail(null);
    setProjectDetailStatus('idle');
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
          setVersionError(
            error instanceof Error
              ? error.message
              : 'Unable to load app version',
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
        setPermissionMode(
          normalizePermissionMode(settings?.['linux.sudo_mode']),
        );
      })
      .catch(() => {
        if (active) {
          setPermissionMode('ask');
        }
      });
    return () => {
      active = false;
    };
  }, []);

  useEffect(() => {
    void refreshInventory();
  }, [refreshInventory]);

  useEffect(() => {
    void refreshProjects();
  }, [refreshProjects]);

  useEffect(() => {
    let timer: number | undefined;
    const off = Events.On('objects:changed', () => {
      window.clearTimeout(timer);
      timer = window.setTimeout(() => {
        void refreshInventory();
        void refreshProjects();
        if (activeProjectID) {
          void refreshProjectDetail(activeProjectID);
        }
      }, 500);
    });
    return () => {
      window.clearTimeout(timer);
      off();
    };
  }, [activeProjectID, refreshInventory, refreshProjectDetail, refreshProjects]);

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
                : 'Docker Hub search is offline',
          }));
        });
    }, 300);
    return () => window.clearTimeout(timer);
  }, [pullImage.open, pullImage.query]);

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
                : 'Docker Hub search is offline',
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
    (container) => container.state === 'running',
  ).length;
  const unhealthyContainers = containers.filter(
    (container) => container.health === 'unhealthy',
  ).length;
  const diskTotal = diskUsage?.totalBytes ?? 0;
  const diskReclaimable = diskUsage?.reclaimable ?? 0;
  const versionLabel = version?.version
    ? `v${version.version}`
    : 'v1.0 workspace';
  const pageTitle =
    navItems.find((item) => item.id === activePage)?.label ?? 'Overview';
  const providerStatus = activeProvider?.status;
  const providerProblems = providerStatus?.problems ?? [];
  const providerWarnings = providerStatus?.warnings ?? [];
  const permissionProblem =
    providerProblems.find((problem) => problem.code === 'PERM_SOCKET') ?? null;
  const providerRepairNeeded = providerProblems.length > 0;
  const dockerRunning =
    !inventoryError &&
    Boolean(dockerInfo || dockerVersion || providerStatus?.dockerRunning);
  const noProviderConfigured =
    inventoryStatus !== 'loading' && providers.length === 0;
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
    ? 'Repair the Docker provider before running Docker actions'
    : dockerStopped
      ? 'Start Docker Engine before running Docker actions'
      : noProviderConfigured
        ? 'Set up a Docker provider before running Docker actions'
        : 'Docker is not reachable';
  const providerName = activeProvider?.name ?? 'No provider selected';
  const statusLabel = dockerRunning
    ? 'Running'
    : providerRepairNeeded
      ? 'Error'
      : 'Stopped';
  const statusTone: StatusToneID = dockerRunning
    ? 'ok'
    : providerRepairNeeded
      ? 'error'
      : 'neutral';

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

  const refreshAfterAction = useCallback(async () => {
    await refreshInventory();
  }, [refreshInventory]);

  const changeProjectView = useCallback((view: ProjectViewMode) => {
    setProjectView(view);
    window.localStorage.setItem('cairn.projects.view', view);
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
        error instanceof Error ? error.message : 'Provider detection failed',
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
        error instanceof Error ? error.message : 'Unable to start Docker',
      );
    } finally {
      setProviderActionBusy(false);
    }
  }, [activeProvider?.id, refreshInventory, refreshProjects]);

  const savePermissionMode = useCallback(async () => {
    setRepairSaving(true);
    setRepairError(null);
    try {
      await SettingsService.SetSetting('linux.sudo_mode', permissionMode);
      setRepairOpen(false);
      await retryProviderDetection();
    } catch (error: unknown) {
      setRepairError(
        error instanceof Error
          ? error.message
          : 'Unable to save permission mode',
      );
    } finally {
      setRepairSaving(false);
    }
  }, [permissionMode, retryProviderDetection]);

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

  const runContainerAction = useCallback(
    async (action: ContainerAction, container: ContainerSummary) => {
      if (!ensureDockerReady()) {
        return;
      }
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
          planKind: 'container',
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
        setActionError(
          error instanceof Error ? error.message : 'Container action failed',
        );
      } finally {
        setActionBusy(key, false);
      }
    },
    [ensureDockerReady, refreshAfterAction, setActionBusy],
  );

  const runBulkContainerAction = useCallback(
    async (action: Exclude<ContainerAction, 'kill'>) => {
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
            : 'Bulk container action failed',
        );
      } finally {
        setActionBusy(key, false);
      }
    },
    [ensureDockerReady, refreshAfterAction, selectedContainerIDs, setActionBusy],
  );

  const applyConfirmedPlan = useCallback(async () => {
    if (!confirm.plan) {
      return;
    }
    setConfirm((current) => ({ ...current, busy: true, error: undefined }));
    try {
      if (confirm.planKind === 'project') {
        await ProjectService.ApplyProjectPlan(
          confirm.plan.planID,
          confirm.typedName,
        );
      } else {
        await DockerService.ApplyContainerPlan(
          confirm.plan.planID,
          confirm.typedName,
        );
      }
      setConfirm(emptyConfirm);
      setSelectedContainerIDs(new Set<string>());
      if (confirm.planKind === 'project') {
        await refreshProjects();
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
        error: error instanceof Error ? error.message : 'Unable to apply plan',
      }));
    }
  }, [
    confirm.plan,
    confirm.planKind,
    confirm.typedName,
    activeProjectID,
    refreshAfterAction,
    refreshProjectDetail,
    refreshProjects,
  ]);

  const runProjectAction = useCallback(
    async (action: ProjectAction, project: ProjectSummary) => {
      if (!ensureDockerReady()) {
        return;
      }
      const key = projectActionBusyKey(action, project.id);
      setActionError(null);
      setActionBusy(key, true);
      try {
        if (action === 'start') {
          await ProjectService.StartProject(project.id);
        } else if (action === 'stop') {
          await ProjectService.StopProject(project.id);
        } else if (action === 'restart') {
          await ProjectService.RestartProject(project.id);
        } else if (action === 'pull') {
          await ProjectService.PullProject(project.id);
        } else {
          const plan =
            action === 'redeploy'
              ? await ProjectService.PlanRedeployProject(project.id)
              : await ProjectService.PlanDownProject(
                  project.id,
                  action === 'down-volumes',
                );
          if (!plan) {
            throw new Error('Project plan was empty');
          }
          setConfirm({
            open: true,
            plan,
            planKind: 'project',
            targetName: project.name,
            typedName: '',
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
          error instanceof Error ? error.message : 'Project action failed',
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

  const openRunImageModal = useCallback((image?: ImageSummary) => {
    const ref = image ? primaryImageRef(image) : '';
    setRunImage({
      ...emptyRunImage,
      open: true,
      imageRef: ref,
      imageLocked: Boolean(image),
      name: ref ? suggestContainerName(ref) : '',
      hubQuery: ref ? '' : '',
    });
  }, []);

  const submitRunImage = useCallback(async () => {
    if (!ensureDockerReady()) {
      return;
    }
    setRunImage((current) => ({ ...current, busy: true, error: undefined }));
    try {
      const req = buildRunImageRequest(runImage);
      await DockerService.RunImage(req);
      setRunImage(emptyRunImage);
      await refreshAfterAction();
      setActivePage('containers');
    } catch (error: unknown) {
      setRunImage((current) => ({
        ...current,
        busy: false,
        error: error instanceof Error ? error.message : 'Unable to run image',
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
          error instanceof Error ? error.message : 'Unable to rename container',
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
        error: error instanceof Error ? error.message : 'Unable to pull image',
      }));
    }
  }, [ensureDockerReady, pullImage.ref, pullImage.tag, refreshAfterAction]);

  const openSaveImageModal = useCallback((image: ImageSummary) => {
    const ref = primaryImageRef(image);
    setSaveImage({
      ...emptySaveImage,
      open: true,
      refsText: ref,
      destPath: `${ref.replace(/[/:@]/g, '_') || 'image'}.tar`,
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
        error: error instanceof Error ? error.message : 'Unable to save image',
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
        error: error instanceof Error ? error.message : 'Unable to load image',
      }));
    }
  }, [ensureDockerReady, loadImage.srcPath, refreshAfterAction]);

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
          error instanceof Error ? error.message : 'Unable to create volume',
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
          createNetwork.driver === 'custom'
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
          error instanceof Error ? error.message : 'Unable to create network',
      }));
    }
  }, [createNetwork, ensureDockerReady, refreshAfterAction]);

  const browseImportFolder = useCallback(async () => {
    try {
      const selected = await Dialogs.OpenFile({
        Title: 'Import Compose Project',
        Message: 'Choose a Compose project folder',
        ButtonText: 'Import',
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
            : 'Unable to open folder picker',
      }));
    }
  }, []);

  const submitImportProject = useCallback(async () => {
    const folderPath = importProject.folderPath.trim();
    if (!folderPath) {
      setImportProject((current) => ({
        ...current,
        error: 'Choose a project folder',
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
      setActivePage('projects');
    } catch (error: unknown) {
      setImportProject((current) => ({
        ...current,
        busy: false,
        imported: null,
        error:
          error instanceof Error ? error.message : 'Unable to import project',
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
          error:
            error instanceof Error
              ? error.message
              : 'Unable to inspect container',
        });
      });
  }, []);

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
            error:
              error instanceof Error
                ? error.message
                : 'Unable to inspect image',
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
                : 'Unable to inspect volume',
          });
        });
    },
    [volumeDetails],
  );

  const openNetworkInspect = useCallback(
    (network: NetworkSummary) => {
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
            error:
              error instanceof Error
                ? error.message
                : 'Unable to inspect network',
          });
        });
    },
    [networkDetails],
  );

  const content = (() => {
    switch (activePage) {
      case 'projects':
        if (activeProjectID) {
          return (
            <ProjectDetailPage
              actionBusyIDs={busyActionIDs}
              detail={projectDetail}
              error={projectDetailError}
              loading={projectDetailStatus === 'loading'}
              mutationsDisabled={mutationsDisabled}
              mutationDisabledReason={mutationDisabledReason}
              onAction={runProjectAction}
              onBack={closeProjectDetail}
              onRefresh={() => {
                void refreshProjectDetail(activeProjectID);
              }}
              onTabChange={setProjectTab}
              tab={projectTab}
            />
          );
        }
        return (
          <ProjectsPage
            error={projectsError}
            actionBusyIDs={busyActionIDs}
            filter={projectFilter}
            loading={projectsStatus === 'loading'}
            mutationsDisabled={mutationsDisabled}
            mutationDisabledReason={mutationDisabledReason}
            onAction={runProjectAction}
            onFilterChange={setProjectFilter}
            onImport={() =>
              setImportProject({ ...emptyImportProject, open: true })
            }
            onOpen={openProjectDetail}
            onRefresh={refreshProjects}
            onSortChange={setProjectSort}
            onViewChange={changeProjectView}
            projects={projects}
            search={search}
            sort={projectSort}
            view={projectView}
          />
        );
      case 'logs':
        return (
          <LogsPage
            containers={containers}
            dockerRunning={dockerRunning}
            inventoryLoading={inventoryStatus === 'loading'}
            projects={projects}
            projectsLoading={projectsStatus === 'loading'}
          />
        );
      case 'terminal':
        return (
          <TerminalPage
            containers={containers}
            onCommandConsumed={(id) =>
              setQueuedTerminalCommand((current) =>
                current?.id === id ? null : current,
              )
            }
            projects={projects}
            queuedCommand={queuedTerminalCommand}
          />
        );
      case 'containers':
        return (
          <ContainersPage
            actionBusyIDs={busyActionIDs}
            containers={containers}
            filter={containerFilter}
            loading={inventoryStatus === 'loading'}
            mutationsDisabled={mutationsDisabled}
            mutationDisabledReason={mutationDisabledReason}
            onAction={runContainerAction}
            onBulkAction={runBulkContainerAction}
            onFilterChange={setContainerFilter}
            onInspect={openContainerInspect}
            onRename={openRenameModal}
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
            mutationsDisabled={mutationsDisabled}
            mutationDisabledReason={mutationDisabledReason}
            onFilterChange={setImageFilter}
            onInspect={openImageInspect}
            onLoad={() => setLoadImage({ ...emptyLoadImage, open: true })}
            onPull={() => setPullImage({ ...emptyPullImage, open: true })}
            onRun={openRunImageModal}
            onSave={openSaveImageModal}
            search={search}
          />
        );
      case 'volumes':
        return (
          <VolumesPage
            filter={volumeFilter}
            loading={inventoryStatus === 'loading'}
            mutationsDisabled={mutationsDisabled}
            mutationDisabledReason={mutationDisabledReason}
            onCreate={() =>
              setCreateVolume({ ...emptyCreateVolume, open: true })
            }
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
            mutationsDisabled={mutationsDisabled}
            mutationDisabledReason={mutationDisabledReason}
            networkDetails={networkDetails}
            networks={networks}
            onCreate={() =>
              setCreateNetwork({ ...emptyCreateNetwork, open: true })
            }
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
            mutationsDisabled={mutationsDisabled}
            mutationDisabledReason={mutationDisabledReason}
            onImportProject={() =>
              setImportProject({ ...emptyImportProject, open: true })
            }
            onNavigate={navigate}
            onOpenTerminal={() => navigate('terminal')}
            onOpenProject={openProjectDetail}
            onShowContainers={showContainers}
            provider={activeProvider}
            projects={projects}
            projectsLoading={projectsStatus === 'loading'}
            runningContainers={runningContainers}
            unhealthyContainers={unhealthyContainers}
            volumes={volumes}
          />
        );
    }
  })();

  return (
    <main className="min-h-screen bg-bg-app text-text-primary">
      <div className="grid min-h-screen grid-cols-1 lg:grid-cols-[236px_1fr]">
        <aside className="flex flex-col border-b border-border bg-bg-panel lg:min-h-screen lg:border-b-0 lg:border-r">
          <div className="flex h-16 items-center gap-3 border-b border-border px-4">
            <img
              src={logoUrl}
              alt="Cairn"
              className="h-9 max-w-32 object-contain"
            />
            <div className="min-w-0">
              <div className="text-sm font-semibold">Cairn</div>
              <div className="truncate text-xs text-text-muted">
                {versionLabel}
              </div>
            </div>
          </div>

          <nav
            className="flex gap-2 overflow-x-auto px-2 py-3 lg:flex-1 lg:flex-col lg:space-y-1 lg:overflow-visible"
            aria-label="Main navigation"
          >
            {navItems.map((item) => {
              const Icon = item.icon;
              const active = activePage === item.id;
              const badge =
                item.id === 'containers'
                  ? String(containers.length)
                  : undefined;
              return (
                <button
                  key={item.id}
                  className={[
                    'flex h-10 w-auto shrink-0 items-center gap-3 rounded-control px-3 text-left text-sm transition lg:w-full',
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

          <div className="hidden border-t border-border p-3 lg:block">
            <div className="rounded-card border border-border bg-bg-inset p-3">
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
                  : 'No engine version'}
              </div>
              {!dockerRunning ? (
                <div className="mt-3 flex gap-2">
                  <Button
                    icon={<Wrench size={14} />}
                    loading={providerActionBusy}
                    onClick={() => {
                      if (dockerStopped) {
                        void startProvider();
                      } else {
                        setRepairOpen(true);
                      }
                    }}
                    size="sm"
                    variant="secondary"
                  >
                    {dockerStopped ? 'Start' : 'Repair'}
                  </Button>
                </div>
              ) : null}
            </div>
          </div>
        </aside>

        <section className="flex min-w-0 flex-col">
          <header className="flex h-auto shrink-0 flex-col items-stretch gap-3 border-b border-border bg-bg-app px-4 py-3 sm:flex-row sm:items-center sm:justify-between lg:h-16 lg:px-6 lg:py-0">
            <div className="min-w-0">
              <h1 className="truncate text-xl font-semibold tracking-normal">
                {pageTitle}
              </h1>
              <p className="truncate text-sm text-text-muted">
                {dockerInfo?.name ?? providerName}
                {lastLoadedAt
                  ? ` - refreshed ${relativeTime(lastLoadedAt)}`
                  : ''}
              </p>
            </div>
            <div className="flex w-full items-center gap-2 sm:w-auto">
              <SearchBox value={search} onChange={setSearch} />
              <Tooltip label="Refresh">
                <Button
                  aria-label="Refresh"
                  icon={<RefreshCw size={17} />}
                  loading={
                    activePage === 'projects'
                      ? projectsStatus === 'loading'
                      : inventoryStatus === 'loading'
                  }
                  onClick={() => {
                    if (activePage === 'projects') {
                      void refreshProjects();
                    } else {
                      void refreshInventory();
                    }
                  }}
                  size="icon"
                  variant="secondary"
                />
              </Tooltip>
              <Tooltip label="Notifications">
                <Button
                  aria-label="Notifications"
                  icon={<Bell size={17} />}
                  size="icon"
                  variant="secondary"
                />
              </Tooltip>
            </div>
          </header>

          <GlobalStateBanner
            busy={providerActionBusy}
            dockerStopped={dockerStopped}
            inventoryError={inventoryError}
            noProviderConfigured={noProviderConfigured}
            onOpenRepair={() => setRepairOpen(true)}
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
          {actionError ? (
            <div className="border-b border-border bg-error/10 px-6 py-3 text-sm text-error">
              {actionError}
            </div>
          ) : null}

          <div className="min-h-0 flex-1 overflow-auto p-6">
            <DegradedFrame stale={staleMode}>{content}</DegradedFrame>
          </div>
        </section>
      </div>

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
      <RepairProviderModal
        busy={repairSaving || providerActionBusy}
        error={repairError}
        onChangePermissionMode={setPermissionMode}
        onClose={() => setRepairOpen(false)}
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
            portsText: appendLine(current.portsText, '0:80/tcp'),
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
  busy,
  dockerStopped,
  inventoryError,
  noProviderConfigured,
  onOpenRepair,
  onRetry,
  onStart,
  permissionProblem,
  providerProblems,
  providerRepairNeeded,
  providerWarnings,
}: {
  busy: boolean;
  dockerStopped: boolean;
  inventoryError: string | null;
  noProviderConfigured: boolean;
  onOpenRepair: () => void;
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
        tone: 'error' as const,
        icon: <ShieldAlert size={17} />,
        title: primaryProblem?.message ?? 'Provider repair is required',
        body:
          primaryProblem?.repairHint ??
          'Review the provider checks and choose a repair path.',
      }
    : noProviderConfigured
      ? {
          tone: 'warn' as const,
          icon: <AlertTriangle size={17} />,
          title: 'No Docker provider configured',
          body: 'Set up a provider before running Docker actions.',
        }
      : dockerStopped || inventoryError
        ? {
            tone: 'warn' as const,
            icon: <AlertTriangle size={17} />,
            title: 'Docker is not reachable',
            body:
              inventoryError ??
              'Cached data is visible; Docker actions are disabled until the engine is running.',
          }
        : warning
          ? {
              tone: 'info' as const,
              icon: <AlertTriangle size={17} />,
              title: warning.message,
              body: 'Provider warning',
            }
          : null;

  if (!state) {
    return null;
  }

  const toneClass =
    state.tone === 'error'
      ? 'border-error/30 bg-error/10 text-error'
      : state.tone === 'warn'
        ? 'border-warn/30 bg-warn/10 text-warn'
        : 'border-info/30 bg-info/10 text-info';

  return (
    <div className={`border-b px-6 py-3 ${toneClass}`}>
      <div className="flex flex-wrap items-center gap-3 text-sm">
        <span className="shrink-0">{state.icon}</span>
        <div className="min-w-0 flex-1">
          <div className="font-medium">{state.title}</div>
          <div className="text-xs opacity-90">{state.body}</div>
        </div>
        {providerRepairNeeded || noProviderConfigured ? (
          <Button
            icon={<Wrench size={15} />}
            onClick={onOpenRepair}
            size="sm"
            variant="secondary"
          >
            Repair
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
        <Button
          icon={<RefreshCw size={15} />}
          loading={busy}
          onClick={onRetry}
          size="sm"
          variant="secondary"
        >
          Retry
        </Button>
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
  onRetry: () => void;
  onSavePermission: () => void;
  open: boolean;
  permissionMode: PermissionMode;
  permissionProblem: ProviderProblem | null;
  problems: ProviderProblem[];
  provider: ProviderSummary | null;
}) {
  return (
    <Modal onClose={onClose} open={open} size="lg" title="Repair Docker Provider">
      <div className="space-y-5">
        <div className="flex items-start gap-3 rounded-card border border-border bg-bg-inset p-4">
          <Wrench className="mt-0.5 text-accent" size={19} />
          <div className="min-w-0">
            <div className="font-medium text-text-primary">
              {provider?.name ?? 'No provider selected'}
            </div>
            <div className="mt-1 text-sm text-text-muted">
              Provider checks list the exact failure and repair hint from the backend.
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
                Socket access was denied. Pick how Cairn should work with this Linux backend.
              </p>
            </div>
            <div className="grid gap-2">
              <PermissionOption
                checked={permissionMode === 'ask'}
                description="Cairn prompts only when an action needs sudo. The sudo password is never stored."
                label="Use sudo per action"
                onChange={() => onChangePermissionMode('ask')}
                value="ask"
              />
              <PermissionOption
                checked={permissionMode === 'group'}
                description="Convenient, less isolated. The docker group is root-equivalent and requires signing out and back in."
                label="Add user to docker group"
                onChange={() => onChangePermissionMode('group')}
                value="group"
              />
              <PermissionOption
                checked={permissionMode === 'rootless'}
                description="Use the rootless Docker socket when rootless Docker is already configured."
                label="Use rootless Docker socket"
                onChange={() => onChangePermissionMode('rootless')}
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
        </div>
      </div>
    </Modal>
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
  provider: ProviderSummary | null;
  dockerRunning: boolean;
  mutationsDisabled: boolean;
  mutationDisabledReason: string;
  containers: ContainerSummary[];
  images: ImageSummary[];
  volumes: VolumeSummary[];
  projects: ProjectSummary[];
  projectsLoading: boolean;
  runningContainers: number;
  unhealthyContainers: number;
  diskTotal: number;
  diskReclaimable: number;
  onImportProject: () => void;
  onNavigate: (page: PageID) => void;
  onOpenTerminal: () => void;
  onOpenProject: (project: ProjectSummary) => void;
  onShowContainers: (filter: FilterID) => void;
};

function OverviewPage({
  containers,
  diskReclaimable,
  diskTotal,
  dockerRunning,
  images,
  mutationsDisabled,
  mutationDisabledReason,
  onImportProject,
  onNavigate,
  onOpenTerminal,
  onOpenProject,
  onShowContainers,
  provider,
  projects,
  projectsLoading,
  runningContainers,
  unhealthyContainers,
  volumes,
}: OverviewProps) {
  const [dashboard, setDashboard] = useState<DashboardMetrics | null>(null);
  const [dashboardStatus, setDashboardStatus] =
    useState<LoadStatus>('loading');
  const [dashboardError, setDashboardError] = useState<string | null>(null);
  const [metric, setMetric] = useState<DashboardMetricID>('cpu');
  const [range, setRange] = useState<DashboardRangeID>('5m');
  const [stacked, setStacked] = useState(false);
  const [chartPaused, setChartPaused] = useState(false);
  const chartPausedRef = useRef(false);
  const [chartPoints, setChartPoints] = useState<DashboardChartPoint[]>([]);
  const [latestSamples, setLatestSamples] = useState<Record<string, StatsSample>>(
    {},
  );
  const [containerSparks, setContainerSparks] = useState<
    Record<string, SparkPoint[]>
  >({});
  const [projectSparks, setProjectSparks] = useState<
    Record<string, SparkPoint[]>
  >({});
  const [logPeek, setLogPeek] = useState<LogLine[]>([]);
  const [cleanup, setCleanup] = useState<CleanupState>(emptyCleanup);
  const statsStreamIDRef = useRef<string | null>(null);
  const logStreamIDRef = useRef<string | null>(null);

  const loadDashboard = useCallback(async () => {
    if (!dockerRunning) {
      setDashboardError(null);
      setDashboardStatus('ready');
      return;
    }
    setDashboardStatus((current) => (current === 'ready' ? current : 'loading'));
    setDashboardError(null);
    try {
      const nextDashboard = await MetricsService.GetDashboardMetrics();
      setDashboard(nextDashboard);
      setDashboardStatus('ready');
    } catch (error: unknown) {
      setDashboardError(
        error instanceof Error ? error.message : 'Unable to load dashboard',
      );
      setDashboardStatus('error');
    }
  }, [dockerRunning]);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      void loadDashboard();
    }, 0);
    return () => window.clearTimeout(timer);
  }, [loadDashboard]);

  useEffect(() => {
    let timer: number | undefined;
    const off = Events.On('objects:changed', () => {
      window.clearTimeout(timer);
      timer = window.setTimeout(() => {
        void loadDashboard();
      }, 500);
    });
    return () => {
      window.clearTimeout(timer);
      off();
    };
  }, [loadDashboard]);

  useEffect(() => {
    chartPausedRef.current = chartPaused;
  }, [chartPaused]);

  useEffect(() => {
    const off = Events.On('stats:sample', (event) => {
      const payload = eventPayload<StatsSamplePayload>(event);
      if (!payload || payload.streamID !== statsStreamIDRef.current) {
        return;
      }
      const samples = (payload.samples ?? []).filter(isStatsSample);
      if (samples.length === 0) {
        return;
      }
      const label = sampleLabel(samples[0]);
      setLatestSamples((current) => {
        const next = { ...current };
        for (const sample of samples) {
          next[sample.containerID] = sample;
        }
        return next;
      });
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
      setProjectSparks((current) =>
        appendSparkEntries(current, projectSparkEntries(samples, label)),
      );
      if (!chartPausedRef.current) {
        setChartPoints((current) =>
          trimChartPoints(current.concat(aggregateChartPoint(samples, label))),
        );
      }
    });
    return () => off();
  }, []);

  useEffect(() => {
    if (!dockerRunning) {
      return undefined;
    }
    let cancelled = false;
    let activeStreamID: string | null = null;
    const scope: StatsScope = { kind: 'all', ids: [] };
    MetricsService.StartStatsStream(scope)
      .then((streamID) => {
        if (cancelled) {
          void MetricsService.StopStream(streamID);
          return;
        }
        activeStreamID = streamID;
        statsStreamIDRef.current = streamID;
      })
      .catch((error: unknown) => {
        setDashboardError(
          error instanceof Error ? error.message : 'Unable to start metrics',
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

  useEffect(() => {
    const offLines = Events.On('logs:lines', (event) => {
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
      scope: 'all',
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
  const paused = containers.filter((container) => container.state === 'paused')
    .length;
  const topRows = useMemo(
    () => dashboardTopRows(dashboard?.top ?? [], latestSamples),
    [dashboard?.top, latestSamples],
  );
  const recentContainers = useMemo(
    () =>
      [...containers]
        .sort((left, right) => dateMillis(right.createdAt) - dateMillis(left.createdAt))
        .slice(0, 6),
    [containers],
  );
  const liveProjects = useMemo(
    () =>
      [...projects]
        .sort(
          (left, right) =>
            projectActivityScore(right, projectSparks[right.id]) -
            projectActivityScore(left, projectSparks[left.id]),
        )
        .slice(0, 5),
    [projectSparks, projects],
  );
  const updateSummary = useMemo(() => summarizeProjectUpdates(projects), [projects]);
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
          disabled
          disabledReason="Update checks begin in Phase 8"
          icon={<RefreshCw size={15} />}
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
          disabled
          disabledReason="Engine restart requires provider command planning"
          icon={<RotateCw size={15} />}
          size="sm"
        >
          Restart Docker
        </Button>
      </div>

      {dashboardStatus === 'error' && dashboardError ? (
        <div className="rounded-card border border-warn/30 bg-warn/10 px-4 py-3 text-sm text-warn">
          {dashboardError}
        </div>
      ) : null}

      <section className="grid gap-4 xl:grid-cols-[1fr_1.35fr]">
        <EngineHeroCard dockerRunning={dockerRunning} provider={provider} />
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
              <Button
                icon={<Terminal size={15} />}
                onClick={onOpenTerminal}
              >
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
          onPauseChange={setChartPaused}
          onRangeChange={setRange}
          onStackedChange={setStacked}
          paused={chartPaused || !dockerRunning}
          points={chartPoints}
          range={range}
          stacked={stacked}
        />
        <ProjectsMiniList
          loading={projectsLoading}
          onOpenProject={onOpenProject}
          onViewAll={() => onNavigate('projects')}
          projectSparks={projectSparks}
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
          <LogsPeekPanel lines={logPeek} onOpenLogs={() => onNavigate('logs')} />
          <UpdatesCard
            onOpenProjects={() => onNavigate('projects')}
            projects={projects}
            summary={updateSummary}
          />
        </div>
      </section>

      <section className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_320px]">
        <TopContainersTable rows={topRows} />
        <RecentEventsPanel events={dashboard?.recentEvents ?? []} />
      </section>

      <CleanupModal
        diskReclaimable={reclaimableBytes}
        onChange={(patch) =>
          setCleanup((current) => ({ ...current, ...patch }))
        }
        onClose={() => setCleanup(emptyCleanup)}
        state={cleanup}
      />
    </div>
  );
}

function EngineHeroCard({
  dockerRunning,
  provider,
}: {
  dockerRunning: boolean;
  provider: ProviderSummary | null;
}) {
  const context = provider?.status?.currentContext || 'default';
  const version = provider?.status?.dockerVersion || 'unknown';
  return (
    <Card className={!dockerRunning ? 'border-neutral/30 bg-bg-inset' : undefined}>
      <CardBody className="flex items-center justify-between gap-5">
        <div className="min-w-0">
          <div className="flex items-center gap-3">
            <StatusDot
              pulse={!dockerRunning && provider?.healthy}
              tone={dockerRunning ? 'ok' : 'neutral'}
            />
            <div className="min-w-0">
              <div className="text-lg font-semibold">
                Docker Engine - {dockerRunning ? 'Running' : 'Stopped'}
              </div>
              <div className="truncate text-sm text-text-muted">
                {provider?.name ?? 'No provider selected'}
              </div>
            </div>
          </div>
          <div className="mt-5 grid gap-3 text-sm sm:grid-cols-3">
            <StatusPill label="Provider" ok={provider?.healthy ?? false} />
            <StatusPill label="Context" ok={dockerRunning} value={context} />
            <StatusPill label="Engine" ok={dockerRunning} value={version} />
          </div>
        </div>
        <Server className="h-16 w-16 text-accent/70" strokeWidth={1.4} />
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
}) {
  const stopped = Math.max(0, containers.length - runningContainers);
  return (
    <section
      className="grid gap-3 sm:grid-cols-2 2xl:grid-cols-5"
      aria-label="Docker object counts"
    >
      <MetricButton
        hint="Compose stacks"
        label="Projects"
        onClick={() => onNavigate('projects')}
        value={counts.projects}
      />
      <MetricButton
        hint={`${runningContainers} running / ${stopped} stopped`}
        label="Containers"
        onClick={() => onShowContainers('all')}
        value={counts.containers}
      />
      <MetricButton
        hint={`${imageDanglingCount(images)} dangling`}
        label="Images"
        onClick={() => onNavigate('images')}
        value={counts.images}
      />
      <MetricButton
        hint={`${volumes.filter((volume) => volume.inUse).length} in use`}
        label="Volumes"
        onClick={() => onNavigate('volumes')}
        value={counts.volumes}
      />
      <button
        className={[
          'rounded-card border border-border bg-bg-card p-4 text-left transition',
          mutationsDisabled
            ? 'cursor-not-allowed opacity-60'
            : 'hover:border-border-strong hover:bg-bg-panel',
        ].join(' ')}
        disabled={mutationsDisabled}
        title={mutationsDisabled ? mutationDisabledReason : undefined}
        onClick={onCleanUp}
        type="button"
      >
        <div className="text-sm text-text-secondary">Disk</div>
        <div className="mt-3 text-2xl font-semibold">{formatBytes(diskTotal)}</div>
        <div className="mt-2 text-xs text-text-muted">
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
    metric === 'cpu'
      ? `${(latest?.cpu ?? 0).toFixed(1)}% CPU`
      : metric === 'memory'
        ? `${formatBytes(latest?.memory ?? 0)} memory`
        : `${formatRate(latest?.netRx ?? 0)} RX / ${formatRate(latest?.netTx ?? 0)} TX`;
  const Icon = metric === 'cpu' ? Cpu : metric === 'memory' ? MemoryStick : Wifi;
  return (
    <Card>
      <CardHeader
        actions={
          <div className="flex flex-wrap items-center justify-end gap-2">
            {(['5m', '1h', '24h'] as DashboardRangeID[]).map((item) => (
              <button
                className={[
                  'h-8 rounded-control border px-2 text-xs transition',
                  range === item
                    ? 'border-accent/40 bg-accent/10 text-accent'
                    : 'border-border bg-bg-inset text-text-secondary hover:text-text-primary',
                ].join(' ')}
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
                'h-8 rounded-control border px-2 text-xs transition',
                stacked
                  ? 'border-accent/40 bg-accent/10 text-accent'
                  : 'border-border bg-bg-inset text-text-secondary hover:text-text-primary',
              ].join(' ')}
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
                {paused ? 'Paused' : `${points.length}/300 points`}
              </div>
            </div>
          </div>
          <div className="flex rounded-control border border-border bg-bg-inset p-0.5">
            {(['cpu', 'memory', 'network'] as DashboardMetricID[]).map((item) => (
              <button
                className={[
                  'h-8 rounded-control px-3 text-xs font-medium capitalize transition',
                  metric === item
                    ? 'bg-bg-card text-text-primary'
                    : 'text-text-secondary hover:text-text-primary',
                ].join(' ')}
                key={item}
                onClick={() => onMetricChange(item)}
                type="button"
              >
                {item}
              </button>
            ))}
          </div>
        </div>
        <div
          className="mt-4 h-72"
          onMouseEnter={() => onPauseChange(true)}
          onMouseLeave={() => onPauseChange(false)}
        >
          <ResponsiveContainer height="100%" width="100%">
            <AreaChart data={points} margin={{ bottom: 0, left: 0, right: 8, top: 8 }}>
              <CartesianGrid stroke="rgba(255,255,255,0.1)" vertical={false} />
              <XAxis dataKey="label" minTickGap={28} stroke="#8B949E" tick={{ fontSize: 11 }} />
              <YAxis
                stroke="#8B949E"
                tick={{ fontSize: 11 }}
                tickFormatter={(value) =>
                  metric === 'memory'
                    ? formatBytes(Number(value))
                    : metric === 'network'
                      ? formatRate(Number(value))
                      : `${Number(value).toFixed(0)}%`
                }
                width={56}
              />
              <RechartsTooltip content={<DashboardChartTooltip metric={metric} />} />
              {metric === 'cpu' ? (
                <Area
                  dataKey="cpu"
                  fill="#2DD4A7"
                  fillOpacity={0.22}
                  isAnimationActive={false}
                  name="CPU"
                  stroke="#2DD4A7"
                  strokeWidth={2}
                  type="monotone"
                />
              ) : null}
              {metric === 'memory' ? (
                <Area
                  dataKey="memory"
                  fill="#A78BFA"
                  fillOpacity={0.2}
                  isAnimationActive={false}
                  name="Memory"
                  stroke="#A78BFA"
                  strokeWidth={2}
                  type="monotone"
                />
              ) : null}
              {metric === 'network' ? (
                <>
                  <Area
                    dataKey="netRx"
                    fill="#4D9FFF"
                    fillOpacity={0.18}
                    isAnimationActive={false}
                    name="RX"
                    stackId={stacked ? 'network' : undefined}
                    stroke="#4D9FFF"
                    strokeWidth={2}
                    type="monotone"
                  />
                  <Area
                    dataKey="netTx"
                    fill="#80B7FF"
                    fillOpacity={0.14}
                    isAnimationActive={false}
                    name="TX"
                    stackId={stacked ? 'network' : undefined}
                    stroke="#80B7FF"
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
          {entry.name}: {formatMetricValue(metric, Number(entry.value ?? 0), entry.dataKey)}
        </div>
      ))}
    </div>
  );
}

function ProjectsMiniList({
  loading,
  onOpenProject,
  onViewAll,
  projectSparks,
  projects,
}: {
  projects: ProjectSummary[];
  loading: boolean;
  projectSparks: Record<string, SparkPoint[]>;
  onOpenProject: (project: ProjectSummary) => void;
  onViewAll: () => void;
}) {
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
        {loading && projects.length === 0 ? <Skeleton className="h-32" /> : null}
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
                  <StatusDot tone={dotTone(projectStatusTone(project.status))} />
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
                color="#2DD4A7"
                points={projectSparks[project.id] ?? projectSparkPoints(project)}
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
  const data = [
    { name: 'Running', value: running, color: '#2DD4A7', filter: 'running' },
    { name: 'Stopped', value: stopped, color: '#8B949E', filter: 'stopped' },
    { name: 'Unhealthy', value: unhealthy, color: '#F0605D', filter: 'unhealthy' },
    { name: 'Paused', value: paused, color: '#F5B83D', filter: 'paused' },
  ].filter((item) => item.value > 0);
  return (
    <Card>
      <CardHeader
        actions={
          <Badge tone={unhealthy > 0 ? 'error' : 'ok'}>
            {unhealthy} unhealthy
          </Badge>
        }
        title="Container Status"
      />
      <CardBody>
        <div className="grid gap-4 lg:grid-cols-[220px_1fr]">
          <div className="h-56">
            <ResponsiveContainer height="100%" width="100%">
              <RechartsPieChart>
                <Pie
                  data={data.length > 0 ? data : [{ name: 'None', value: 1, color: '#8B949E', filter: 'all' }]}
                  dataKey="value"
                  innerRadius={58}
                  isAnimationActive={false}
                  nameKey="name"
                  outerRadius={86}
                >
                  {(data.length > 0
                    ? data
                    : [{ name: 'None', value: 1, color: '#8B949E', filter: 'all' }]
                  ).map((item) => (
                    <Cell fill={item.color} key={item.name} />
                  ))}
                </Pie>
              </RechartsPieChart>
            </ResponsiveContainer>
          </div>
          <div className="min-w-0 space-y-2">
            {[
              ['running', 'Running', running, 'ok'],
              ['stopped', 'Stopped', stopped, 'neutral'],
              ['unhealthy', 'Unhealthy', unhealthy, unhealthy > 0 ? 'error' : 'neutral'],
              ['paused', 'Paused', paused, 'warn'],
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
                      {container.projectID || '-'}
                    </td>
                    <td className="px-3 py-2">
                      <Badge tone={containerTone(container)}>
                        {container.state || 'unknown'}
                      </Badge>
                    </td>
                    <td className="px-3 py-2">
                      <Sparkline
                        color="#2DD4A7"
                        points={
                          containerSparks[container.id] ??
                          containerSparkPoints(container)
                        }
                      />
                    </td>
                    <td className="truncate px-3 py-2 text-text-muted">
                      {formatBytes(sample?.memoryBytes ?? container.memoryBytes)}
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
              <div className="grid grid-cols-[auto_1fr] gap-2" key={`${line.ts}-${line.text}`}>
                <span className={logLevelClass(normalizeLogLevel(line.level))}>
                  {normalizeLogLevel(line.level).toUpperCase()}
                </span>
                <span className="truncate text-text-secondary">{line.text}</span>
              </div>
            ))
          )}
        </button>
      </CardBody>
    </Card>
  );
}

function UpdatesCard({
  onOpenProjects,
  projects,
  summary,
}: {
  projects: ProjectSummary[];
  summary: { image: number; base: number; rebuild: number };
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
          <Button
            disabled
            disabledReason="Registry update checks begin in Phase 8"
            icon={<RefreshCw size={15} />}
            size="sm"
          >
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
        <Button className="mt-4 w-full" onClick={onOpenProjects} variant="secondary">
          Open Updates
        </Button>
      </CardBody>
    </Card>
  );
}

function TopContainersTable({ rows }: { rows: MetricRankItem[] }) {
  return (
    <Card>
      <CardHeader
        actions={<BarChart3 size={16} className="text-text-muted" />}
        title="Top Containers"
      />
      <CardBody>
        <div className="overflow-hidden rounded-control border border-border">
          <table className="w-full table-fixed text-sm">
            <thead className="bg-bg-inset text-xs uppercase text-text-muted">
              <tr>
                <th className="px-3 py-2 text-left font-medium">Name</th>
                <th className="px-3 py-2 text-left font-medium">Kind</th>
                <th className="px-3 py-2 text-left font-medium">CPU</th>
                <th className="px-3 py-2 text-left font-medium">Memory</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => (
                <tr className="border-t border-border" key={row.id}>
                  <td className="truncate px-3 py-2">{row.name}</td>
                  <td className="px-3 py-2 text-text-muted">{row.kind}</td>
                  <td className="px-3 py-2">{(row.cpuPercent ?? 0).toFixed(1)}%</td>
                  <td className="px-3 py-2 text-text-muted">
                    {formatBytes(row.memoryBytes)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {rows.length === 0 ? (
            <div className="p-4 text-sm text-text-muted">No stats samples yet</div>
          ) : null}
        </div>
      </CardBody>
    </Card>
  );
}

function RecentEventsPanel({ events }: { events: AuditEntry[] }) {
  return (
    <Card>
      <CardHeader
        actions={<Activity size={16} className="text-text-muted" />}
        title="Recent Events"
      />
      <CardBody>
        <div className="space-y-2">
          {events.slice(0, 10).map((event) => (
            <div
              className="rounded-control border border-border bg-bg-inset px-3 py-2 text-sm"
              key={event.id}
            >
              <div className="flex items-center justify-between gap-2">
                <span className="min-w-0 truncate">{event.action}</span>
                <Badge tone={event.result === 'success' ? 'ok' : 'neutral'}>
                  {event.result || 'event'}
                </Badge>
              </div>
              <div className="mt-1 truncate text-xs text-text-muted">
                {event.target || event.actor || 'docker'} -{' '}
                {relativeTime(dateMillis(event.ts))}
              </div>
            </div>
          ))}
          {events.length === 0 ? (
            <div className="text-sm text-text-muted">No recent Docker events</div>
          ) : null}
        </div>
      </CardBody>
    </Card>
  );
}

function CleanupModal({
  diskReclaimable,
  onChange,
  onClose,
  state,
}: {
  state: CleanupState;
  diskReclaimable: number;
  onChange: (patch: Partial<CleanupState>) => void;
  onClose: () => void;
}) {
  const requiresTypedName = state.includeVolumes;
  const typedReady = !requiresTypedName || state.typedName === 'DELETE VOLUMES';
  return (
    <Modal onClose={onClose} open={state.open} size="md" title="Clean Up Docker Space">
      <div className="space-y-4">
        <div className="rounded-control border border-warn/30 bg-warn/10 p-3 text-sm text-warn">
          {formatBytes(diskReclaimable)} is currently reclaimable.
        </div>
        <div className="grid gap-2">
          {[
            ['includeImages', 'Unused images'],
            ['includeContainers', 'Stopped containers'],
            ['includeBuildCache', 'Build cache'],
            ['includeVolumes', 'Unused volumes'],
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
                    typedName: key === 'includeVolumes' ? '' : state.typedName,
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
              Type DELETE VOLUMES to confirm
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
        <div className="flex justify-end gap-2">
          <Button onClick={onClose} variant="ghost">
            Cancel
          </Button>
          <Button
            disabled={!typedReady}
            disabledReason="Type the confirmation text first"
            onClick={onClose}
            variant="danger"
          >
            Confirm preview
          </Button>
        </div>
      </div>
    </Modal>
  );
}

function Sparkline({
  color,
  points,
}: {
  points: SparkPoint[];
  color: string;
}) {
  const data = points.length > 0 ? points : [{ label: '0', value: 0 }];
  return (
    <div className="h-10 w-full min-w-0">
      <ResponsiveContainer height="100%" width="100%">
        <LineChart data={data} margin={{ bottom: 2, left: 0, right: 0, top: 2 }}>
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
  { id: 'error', label: 'ERROR', tone: 'error' },
  { id: 'warn', label: 'WARN', tone: 'warn' },
  { id: 'info', label: 'INFO', tone: 'info' },
  { id: 'debug', label: 'DEBUG', tone: 'neutral' },
  { id: 'unknown', label: 'unknown', tone: 'neutral' },
];

const logBufferLimit = 50000;
const logRowOverscan = 8;

type LogsPageProps = {
  containers: ContainerSummary[];
  dockerRunning: boolean;
  projects: ProjectSummary[];
  inventoryLoading: boolean;
  projectsLoading: boolean;
};

type LogOption = {
  id: string;
  label: string;
  hint?: string;
};

function LogsPage({
  containers,
  dockerRunning,
  inventoryLoading,
  projects,
  projectsLoading,
}: LogsPageProps) {
  const [scope, setScope] = useState<LogScope>('all');
  const [selectedProjectID, setSelectedProjectID] = useState('');
  const [selectedServiceID, setSelectedServiceID] = useState('');
  const [selectedContainerIDs, setSelectedContainerIDs] = useState<string[]>(
    [],
  );
  const [lines, setLines] = useState<LogLine[]>([]);
  const [streamID, setStreamID] = useState<string | null>(null);
  const streamIDRef = useRef<string | null>(null);
  const [streamStatus, setStreamStatus] = useState<LoadStatus>('idle');
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
  const [query, setQuery] = useState('');
  const [debouncedQuery, setDebouncedQuery] = useState('');
  const [hideNonMatching, setHideNonMatching] = useState(false);
  const [activeMatch, setActiveMatch] = useState(0);
  const [scrollTop, setScrollTop] = useState(0);
  const [viewportHeight, setViewportHeight] = useState(520);
  const [exportLogs, setExportLogs] =
    useState<ExportLogsState>(emptyExportLogs);
  const [exportToast, setExportToast] = useState<ExportResult | null>(null);
  const viewerRef = useRef<HTMLDivElement>(null);

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

  useEffect(() => {
    if (scope === 'project' && !selectedProjectID && projectOptions[0]) {
      setSelectedProjectID(projectOptions[0].id);
    }
    if (scope === 'service' && !selectedServiceID && serviceOptions[0]) {
      setSelectedServiceID(serviceOptions[0].id);
    }
    if (
      scope === 'container' &&
      selectedContainerIDs.length === 0 &&
      containerOptions[0]
    ) {
      setSelectedContainerIDs([containerOptions[0].id]);
    }
  }, [
    containerOptions,
    projectOptions,
    scope,
    selectedContainerIDs.length,
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
    window.addEventListener('resize', update);
    const observer =
      typeof ResizeObserver === 'undefined'
        ? null
        : new ResizeObserver(update);
    observer?.observe(node);
    return () => {
      window.removeEventListener('resize', update);
      observer?.disconnect();
    };
  }, []);

  useEffect(() => {
    const offLines = Events.On('logs:lines', (event) => {
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
    const offEOF = Events.On('logs:eof', (event) => {
      const payload = eventPayload<LogErrorPayload>(event);
      if (!payload || payload.streamID !== streamIDRef.current) {
        return;
      }
      setStreamEnded(true);
      setStreamStatus('ready');
    });
    const offError = Events.On('logs:error', (event) => {
      const payload = eventPayload<LogErrorPayload>(event);
      if (!payload || payload.streamID !== streamIDRef.current) {
        return;
      }
      setStreamError(payload.error ?? 'Log stream failed');
      setStreamStatus('error');
    });
    return () => {
      offLines();
      offEOF();
      offError();
    };
  }, []);

  const streamIDs = useMemo(() => {
    if (scope === 'project') {
      return selectedProjectID ? [selectedProjectID] : [];
    }
    if (scope === 'service') {
      return selectedServiceID ? [selectedServiceID] : [];
    }
    if (scope === 'container') {
      return selectedContainerIDs;
    }
    return [];
  }, [scope, selectedContainerIDs, selectedProjectID, selectedServiceID]);
  const canStream = dockerRunning && (scope === 'all' || streamIDs.length > 0);

  useEffect(() => {
    if (!canStream) {
      setLines([]);
      setStreamID(null);
      setStreamStatus('idle');
      setStreamError(null);
      setStreamEnded(false);
      return undefined;
    }

    let cancelled = false;
    let activeStreamID: string | null = null;
    setLines([]);
    setStreamID(null);
    setStreamStatus('loading');
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
        setStreamStatus('ready');
      })
      .catch((error: unknown) => {
        if (!cancelled) {
          setStreamError(
            error instanceof Error ? error.message : 'Unable to start logs',
          );
          setStreamStatus('error');
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
    paused && pausedAt !== null ? Math.min(pausedAt, lines.length) : lines.length;
  const pausedNewCount = paused ? Math.max(0, lines.length - pausedViewLength) : 0;
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
  const virtualEnd = Math.min(filteredLines.length, virtualStart + visibleCount);
  const virtualRows = filteredLines.slice(virtualStart, virtualEnd);
  const newLinesWhileUnpinned =
    !follow && !paused && unpinnedAt !== null
      ? Math.max(0, filteredLines.length - unpinnedAt)
      : 0;

  useEffect(() => {
    if (!follow || paused) {
      return;
    }
    window.requestAnimationFrame(() => {
      const node = viewerRef.current;
      if (node) {
        node.scrollTop = node.scrollHeight;
      }
    });
  }, [filteredLines.length, follow, paused]);

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
      Title: 'Export Logs',
      Message: 'Choose a log export file',
      ButtonText: 'Export',
      Filename: `cairn-${scope}-logs.${format}`,
      Filters: [
        {
          DisplayName: format === 'jsonl' ? 'JSON Lines' : 'Log file',
          Pattern: format === 'jsonl' ? '*.jsonl' : '*.log',
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
      setExportLogs({ ...emptyExportLogs, result });
      setExportToast(result);
    } catch (error: unknown) {
      setExportLogs((current) => ({
        ...current,
        busy: false,
        error: error instanceof Error ? error.message : 'Unable to export logs',
      }));
    }
  }, [exportLogs.path, scope, streamIDs]);

  const streamLabel =
    scope === 'all'
      ? 'All scopes'
      : streamIDs.length > 0
        ? `${streamIDs.length} selected`
        : 'No scope selected';
  const emptyTitle =
    !canStream
      ? 'Pick a project, service, or container'
      : streamStatus === 'loading'
      ? 'Opening log stream'
      : 'No visible logs';

  return (
    <div className="relative min-h-full space-y-4">
      <Card>
        <CardBody className="space-y-3">
          <div className="flex flex-wrap items-center gap-2">
            <div
              aria-label="Log scope"
              className="flex rounded-control border border-border bg-bg-inset p-1"
              role="group"
            >
              {(['all', 'project', 'service', 'container'] as LogScope[]).map(
                (nextScope) => (
                  <button
                    className={[
                      'h-8 rounded-control px-3 text-xs font-medium capitalize transition',
                      scope === nextScope
                        ? 'bg-accent text-bg-app'
                        : 'text-text-secondary hover:bg-bg-card hover:text-text-primary',
                    ].join(' ')}
                    key={nextScope}
                    onClick={() => setScope(nextScope)}
                    type="button"
                  >
                    {nextScope}
                  </button>
                ),
              )}
            </div>

            {scope === 'project' ? (
              <LogSelect
                ariaLabel="Project scope"
                disabled={projectsLoading}
                onChange={setSelectedProjectID}
                options={projectOptions}
                value={selectedProjectID}
              />
            ) : null}
            {scope === 'service' ? (
              <LogSelect
                ariaLabel="Service scope"
                disabled={inventoryLoading}
                onChange={setSelectedServiceID}
                options={serviceOptions}
                value={selectedServiceID}
              />
            ) : null}
            {scope === 'container' ? (
              <select
                aria-label="Container scope"
                className="h-20 min-w-60 rounded-control border border-border bg-bg-inset px-3 py-2 text-sm text-text-primary"
                multiple
                onChange={(event) =>
                  setSelectedContainerIDs(
                    Array.from(event.currentTarget.selectedOptions).map(
                      (option) => option.value,
                    ),
                  )
                }
                value={selectedContainerIDs}
              >
                {containerOptions.map((option) => (
                  <option key={option.id} value={option.id}>
                    {option.label}
                  </option>
                ))}
              </select>
            ) : null}

            <Badge tone={streamStatus === 'error' ? 'error' : 'info'}>
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
                {matchRows.length > 0 ? activeMatch + 1 : 0}/
                {matchRows.length}
              </Badge>
            </div>

            <Tooltip label={paused ? 'Resume stream display' : 'Pause display'}>
              <Button
                icon={
                  paused ? <Play size={16} /> : <Pause size={16} />
                }
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
                variant={paused ? 'primary' : 'secondary'}
              >
                {paused ? 'Resume' : 'Pause'}
              </Button>
            </Tooltip>
            <Tooltip label="Pin to newest logs">
              <Button
                icon={<ArrowDown size={16} />}
                onClick={scrollToBottom}
                variant={follow ? 'primary' : 'secondary'}
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
                variant={showTimestamps ? 'primary' : 'secondary'}
              />
            </Tooltip>
            <Tooltip label="Toggle line wrap">
              <Button
                aria-label="Toggle line wrap"
                icon={<WrapText size={16} />}
                onClick={() => setWrapLines((current) => !current)}
                size="icon"
                variant={wrapLines ? 'primary' : 'secondary'}
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
                  'h-7 rounded-control border px-2 text-xs font-medium transition',
                  levelFilters.has(level.id)
                    ? 'border-accent bg-accent/10 text-text-primary'
                    : 'border-border bg-bg-inset text-text-muted hover:text-text-primary',
                ].join(' ')}
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
                  ? 'The selected stream has not produced visible lines.'
                  : 'Select a scope before opening a stream.'
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
            <div style={{ height: totalHeight, position: 'relative' }}>
              {virtualRows.map((line, offset) => {
                const rowIndex = virtualStart + offset;
                return (
                  <LogRow
                    activeSearch={matchRows[activeMatch] === rowIndex}
                    key={`${rowIndex}:${line.ts}:${line.containerID ?? line.containerName ?? ''}:${line.text}`}
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

      {exportToast ? (
        <div className="fixed bottom-5 right-5 z-40">
          <Toast
            action={
              <Button
                icon={<FolderOpen size={15} />}
                onClick={() => {
                  void Clipboard.SetText(exportToast.path);
                }}
                size="sm"
                variant="secondary"
              >
                Open folder
              </Button>
            }
            body={`${formatCount(exportToast.lineCount)} lines saved`}
            level="ok"
            title="Logs exported"
          />
        </div>
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
  const isSkipMarker = line.stream === 'system' && line.text.includes('skipped');
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
        'absolute left-0 right-0 grid items-start gap-2 border-b border-border/60 px-3 py-1',
        showTimestamp
          ? 'grid-cols-[96px_128px_64px_1fr]'
          : 'grid-cols-[128px_64px_1fr]',
        line.stream === 'stderr' ? 'border-l-2 border-l-error/70' : '',
        activeSearch ? 'bg-accent/10' : 'hover:bg-bg-inset',
      ].join(' ')}
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
      <Tooltip label={line.level ? 'detected' : 'undetected'}>
        <span>
          <Badge tone={levelTone(normalizeLogLevel(line.level))}>
            {line.level || 'LOG'}
          </Badge>
        </span>
      </Tooltip>
      <span
        className={[
          'min-w-0 text-text-primary',
          wrap ? 'whitespace-pre-wrap break-words' : 'truncate whitespace-pre',
        ].join(' ')}
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
              const format = event.currentTarget.value as 'log' | 'jsonl';
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
              onChange({ range: event.currentTarget.value as 'buffer' | 'tail' })
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
              onChange={(event) => onChange({ path: event.currentTarget.value })}
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
        {state.error ? <div className="text-sm text-error">{state.error}</div> : null}
      </div>
    </Modal>
  );
}

type ProjectsPageProps = {
  projects: ProjectSummary[];
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
  onRefresh,
  onSortChange,
  onViewChange,
  projects,
  search,
  sort,
  view,
}: ProjectsPageProps) {
  const filtered = useMemo(
    () => sortProjects(filterProjects(projects, search, filter), sort),
    [filter, projects, search, sort],
  );
  const [projectSparks, setProjectSparks] = useState<
    Record<string, SparkPoint[]>
  >({});

  useEffect(() => {
    if (projects.length === 0 || mutationsDisabled) {
      return undefined;
    }
    let cancelled = false;
    let activeStreamID: string | null = null;
    const streamIDRef = { current: null as string | null };
    const off = Events.On('stats:sample', (event) => {
      const payload = eventPayload<StatsSamplePayload>(event);
      if (!payload || payload.streamID !== streamIDRef.current) {
        return;
      }
      const samples = (payload.samples ?? []).filter(isStatsSample);
      if (samples.length === 0) {
        return;
      }
      setProjectSparks((current) =>
        appendSparkEntries(
          current,
          projectSparkEntries(samples, sampleLabel(samples[0])),
        ),
      );
    });
    MetricsService.StartStatsStream({ kind: 'all', ids: [] })
      .then((streamID) => {
        if (cancelled) {
          void MetricsService.StopStream(streamID);
          return;
        }
        activeStreamID = streamID;
        streamIDRef.current = streamID;
      })
      .catch(() => {
        setProjectSparks({});
      });
    return () => {
      cancelled = true;
      off();
      if (activeStreamID) {
        void MetricsService.StopStream(activeStreamID);
      }
    };
  }, [mutationsDisabled, projects.length]);

  if (loading && projects.length === 0) {
    return <TableSkeleton />;
  }
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <FilterChips
          active={filter}
          items={[
            ['all', 'All', projects.length],
            [
              'running',
              'Running',
              projects.filter((project) => project.status === 'running').length,
            ],
            [
              'stopped',
              'Stopped',
              projects.filter((project) => project.status === 'stopped').length,
            ],
            [
              'partial',
              'Partial',
              projects.filter((project) => project.status === 'partial').length,
            ],
            [
              'unhealthy',
              'Unhealthy',
              projects.filter((project) => project.health === 'unhealthy')
                .length,
            ],
            [
              'updates',
              'Updates available',
              projects.filter((project) => projectUpdateCount(project) > 0)
                .length,
            ],
            [
              'high-cpu',
              'High CPU',
              projects.filter((project) => project.cpuPercent >= 80).length,
            ],
            [
              'recent',
              'Recently changed',
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
              onSortChange(event.target.value as ProjectSortID)
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
                onClick={() => onViewChange('grid')}
                size="icon"
                variant={view === 'grid' ? 'secondary' : 'ghost'}
              />
            </Tooltip>
            <Tooltip label="List view">
              <Button
                aria-label="List view"
                icon={<List size={16} />}
                onClick={() => onViewChange('list')}
                size="icon"
                variant={view === 'list' ? 'secondary' : 'ghost'}
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
      ) : view === 'grid' ? (
        <section
          className="grid grid-cols-[repeat(auto-fill,minmax(320px,1fr))] gap-4"
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

function ProjectCard({
  actionBusyIDs,
  mutationsDisabled,
  mutationDisabledReason,
  onAction,
  onOpen,
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
}) {
  const updates = projectUpdateCount(project);
  const workdirMissing = project.status === 'error';
  const primaryAction: ProjectAction =
    project.status === 'running' ? 'stop' : 'start';
  const lifecycleDisabled =
    mutationsDisabled || workdirMissing || !project.workingDir;
  const disabledReason = mutationsDisabled
    ? mutationDisabledReason
    : workdirMissing
      ? 'Re-link folder before running project actions'
      : 'No workdir';
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
                {project.status || 'unknown'}
              </Badge>
            </div>
            <div className="mt-1 truncate text-xs text-text-muted">
              {project.workingDir || 'No workdir'}
            </div>
          </div>
          <Tooltip label="More">
            <Button
              aria-label={`More actions for ${project.name}`}
              icon={<MoreVertical size={16} />}
              size="icon"
              variant="ghost"
            />
          </Tooltip>
        </div>

        <div className="grid grid-cols-3 gap-2 text-sm">
          <MiniMetric
            label="Services"
            value={`${project.servicesRunning}/${project.servicesTotal}`}
          />
          <MiniMetric label="CPU" value={`${project.cpuPercent.toFixed(1)}%`} />
          <MiniMetric label="RAM" value={formatBytes(project.memoryBytes)} />
        </div>

        <div className="h-10 overflow-hidden rounded-control border border-border bg-bg-inset px-2 py-2">
          <Sparkline
            color="#2DD4A7"
            points={sparkPoints ?? projectSparkPoints(project)}
          />
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <Badge tone={healthTone(project.health)}>
            {project.health || 'unknown'}
          </Badge>
          {updates > 0 ? (
            <Badge tone="warn">{updates} updates</Badge>
          ) : (
            <Badge tone="neutral">0 updates</Badge>
          )}
          {workdirMissing ? <Badge tone="warn">Workdir missing</Badge> : null}
          <PortList ports={project.ports ?? []} />
        </div>

        <div className="flex items-center gap-1 border-t border-border pt-3">
          <Tooltip label={project.status === 'running' ? 'Stop' : 'Start'}>
            <Button
              aria-label={`${project.status === 'running' ? 'Stop' : 'Start'} ${project.name}`}
              disabled={lifecycleDisabled}
              disabledReason={disabledReason}
              icon={
                project.status === 'running' ? (
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
              disabled={lifecycleDisabled}
              disabledReason={disabledReason}
              icon={<RotateCw size={15} />}
              loading={busy('restart')}
              onClick={() => onAction('restart', project)}
              size="icon"
              variant="ghost"
            />
          </Tooltip>
          <Tooltip label="Pull images">
            <Button
              aria-label={`Pull images ${project.name}`}
              disabled={lifecycleDisabled}
              disabledReason={disabledReason}
              icon={<Download size={15} />}
              loading={busy('pull')}
              onClick={() => onAction('pull', project)}
              size="icon"
              variant="ghost"
            />
          </Tooltip>
          <Tooltip label="Redeploy">
            <Button
              aria-label={`Redeploy ${project.name}`}
              disabled={lifecycleDisabled}
              disabledReason={disabledReason}
              icon={<PackagePlus size={15} />}
              loading={busy('redeploy')}
              onClick={() => onAction('redeploy', project)}
              size="icon"
              variant="ghost"
            />
          </Tooltip>
          <Tooltip label="Down">
            <Button
              aria-label={`Down ${project.name}`}
              disabled={lifecycleDisabled}
              disabledReason={disabledReason}
              icon={<Square size={15} />}
              loading={busy('down')}
              onClick={() => onAction('down', project)}
              size="icon"
              variant="danger"
            />
          </Tooltip>
          <Tooltip label="Down with volumes">
            <Button
              aria-label={`Down with volumes ${project.name}`}
              disabled={lifecycleDisabled}
              disabledReason={disabledReason}
              icon={<Skull size={15} />}
              loading={busy('down-volumes')}
              onClick={() => onAction('down-volumes', project)}
              size="icon"
              variant="danger"
            />
          </Tooltip>
          <Tooltip label="Open folder">
            <Button
              aria-label={`Open folder ${project.name}`}
              disabled={!project.workingDir}
              icon={<FolderOpen size={15} />}
              size="icon"
              variant="ghost"
            />
          </Tooltip>
          <span className="ml-auto text-xs text-text-muted">
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
  projects,
}: {
  projects: ProjectSummary[];
  actionBusyIDs: Set<string>;
  mutationsDisabled: boolean;
  mutationDisabledReason: string;
  onAction: (action: ProjectAction, project: ProjectSummary) => void;
  onOpen: (project: ProjectSummary) => void;
}) {
  return (
    <DataTable
      columns={[
        {
          id: 'name',
          header: 'Name',
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
          id: 'status',
          header: 'Status',
          render: (project) => (
            <Badge tone={projectStatusTone(project.status)}>
              {project.status}
            </Badge>
          ),
          sortValue: (project) => project.status,
          sortable: true,
        },
        {
          id: 'services',
          header: 'Services',
          render: (project) =>
            `${project.servicesRunning}/${project.servicesTotal}`,
          sortValue: (project) => project.servicesTotal,
          sortable: true,
        },
        {
          id: 'health',
          header: 'Health',
          render: (project) => (
            <Badge tone={healthTone(project.health)}>{project.health}</Badge>
          ),
          sortValue: (project) => project.health,
          sortable: true,
        },
        {
          id: 'cpu',
          header: 'CPU',
          render: (project) => `${project.cpuPercent.toFixed(1)}%`,
          sortValue: (project) => project.cpuPercent,
          sortable: true,
        },
        {
          id: 'ram',
          header: 'RAM',
          render: (project) => formatBytes(project.memoryBytes),
          sortValue: (project) => project.memoryBytes,
          sortable: true,
        },
        {
          id: 'ports',
          header: 'Ports',
          render: (project) => <PortList ports={project.ports ?? []} />,
        },
        {
          id: 'changed',
          header: 'Last changed',
          render: (project) => relativeTime(dateMillis(project.lastChangedAt)),
          sortValue: (project) => dateMillis(project.lastChangedAt),
          sortable: true,
        },
        {
          id: 'workdir',
          header: 'Workdir',
          render: (project) => project.workingDir || '-',
          sortValue: (project) => project.workingDir || '',
          sortable: true,
        },
        {
          id: 'actions',
          header: '',
          render: (project) => (
            <ProjectRowActions
              actionBusyIDs={actionBusyIDs}
              mutationsDisabled={mutationsDisabled}
              mutationDisabledReason={mutationDisabledReason}
              onAction={onAction}
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
  project,
}: {
  project: ProjectSummary;
  actionBusyIDs: Set<string>;
  mutationsDisabled: boolean;
  mutationDisabledReason: string;
  onAction: (action: ProjectAction, project: ProjectSummary) => void;
}) {
  const workdirMissing = project.status === 'error';
  const lifecycleDisabled =
    mutationsDisabled || workdirMissing || !project.workingDir;
  const disabledReason = mutationsDisabled
    ? mutationDisabledReason
    : workdirMissing
      ? 'Re-link folder before running project actions'
      : 'No workdir';
  const primaryAction: ProjectAction =
    project.status === 'running' ? 'stop' : 'start';
  const busy = (action: ProjectAction) =>
    actionBusyIDs.has(projectActionBusyKey(action, project.id));
  return (
    <div className="flex justify-end gap-1">
      <Tooltip label={primaryAction === 'stop' ? 'Stop' : 'Start'}>
        <Button
          aria-label={`${primaryAction === 'stop' ? 'Stop' : 'Start'} ${project.name}`}
          disabled={lifecycleDisabled}
          disabledReason={disabledReason}
          icon={
            primaryAction === 'stop' ? <Square size={15} /> : <Play size={15} />
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
          disabled={lifecycleDisabled}
          disabledReason={disabledReason}
          icon={<RotateCw size={15} />}
          loading={busy('restart')}
          onClick={() => onAction('restart', project)}
          size="icon"
          variant="ghost"
        />
      </Tooltip>
      <Tooltip label="Pull images">
        <Button
          aria-label={`Pull images ${project.name}`}
          disabled={lifecycleDisabled}
          disabledReason={disabledReason}
          icon={<Download size={15} />}
          loading={busy('pull')}
          onClick={() => onAction('pull', project)}
          size="icon"
          variant="ghost"
        />
      </Tooltip>
      <Tooltip label="Redeploy">
        <Button
          aria-label={`Redeploy ${project.name}`}
          disabled={lifecycleDisabled}
          disabledReason={disabledReason}
          icon={<PackagePlus size={15} />}
          loading={busy('redeploy')}
          onClick={() => onAction('redeploy', project)}
          size="icon"
          variant="ghost"
        />
      </Tooltip>
      <Tooltip label="Down with volumes">
        <Button
          aria-label={`Down with volumes ${project.name}`}
          disabled={lifecycleDisabled}
          disabledReason={disabledReason}
          icon={<Skull size={15} />}
          loading={busy('down-volumes')}
          onClick={() => onAction('down-volumes', project)}
          size="icon"
          variant="danger"
        />
      </Tooltip>
    </div>
  );
}

const projectTabs: Array<[ProjectTabID, string]> = [
  ['overview', 'Overview'],
  ['services', 'Services'],
  ['containers', 'Containers'],
  ['compose', 'Compose'],
];

function ProjectDetailPage({
  actionBusyIDs,
  detail,
  error,
  loading,
  mutationsDisabled,
  mutationDisabledReason,
  onAction,
  onBack,
  onRefresh,
  onTabChange,
  tab,
}: {
  detail: ProjectDetail | null;
  actionBusyIDs: Set<string>;
  loading: boolean;
  error: string | null;
  mutationsDisabled: boolean;
  mutationDisabledReason: string;
  tab: ProjectTabID;
  onAction: (action: ProjectAction, project: ProjectSummary) => void;
  onBack: () => void;
  onRefresh: () => void;
  onTabChange: (tab: ProjectTabID) => void;
}) {
  if (loading && !detail) {
    return <TableSkeleton />;
  }
  if (!detail) {
    return (
      <EmptyState
        body={error ?? 'Project detail is unavailable.'}
        icon={<LayoutGrid size={28} />}
        title="Project not found"
      />
    );
  }

  const project = detail.summary;
  const primaryAction: ProjectAction =
    project.status === 'running' ? 'stop' : 'start';
  const lifecycleDisabled =
    mutationsDisabled || project.status === 'error' || !project.workingDir;
  const disabledReason = mutationsDisabled
    ? mutationDisabledReason
    : project.status === 'error'
      ? 'Re-link folder before running project actions'
      : 'No workdir';
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
              {project.status || 'unknown'}
            </Badge>
            <Badge tone="info">{project.providerID}</Badge>
          </div>
          <div className="mt-2 max-w-3xl truncate text-sm text-text-muted">
            {project.workingDir || 'No workdir'} · changed{' '}
            {relativeTime(dateMillis(project.lastChangedAt))}
          </div>
        </div>
        <div className="flex flex-wrap justify-end gap-2">
          <Button
            disabled={lifecycleDisabled}
            disabledReason={disabledReason}
            icon={primaryAction === 'stop' ? <Square size={15} /> : <Play size={15} />}
            loading={busy(primaryAction)}
            onClick={() => onAction(primaryAction, project)}
          >
            {primaryAction === 'stop' ? 'Stop' : 'Start'}
          </Button>
          <Button
            disabled={lifecycleDisabled}
            disabledReason={disabledReason}
            icon={<RotateCw size={15} />}
            loading={busy('restart')}
            onClick={() => onAction('restart', project)}
          >
            Restart
          </Button>
          <Button
            disabled={lifecycleDisabled}
            disabledReason={disabledReason}
            icon={<PackagePlus size={15} />}
            loading={busy('redeploy')}
            onClick={() => onAction('redeploy', project)}
          >
            Redeploy
          </Button>
          <Button
            disabled={lifecycleDisabled}
            disabledReason={disabledReason}
            icon={<Download size={15} />}
            loading={busy('pull')}
            onClick={() => onAction('pull', project)}
          >
            Pull
          </Button>
          <Button
            disabled={lifecycleDisabled}
            disabledReason={disabledReason}
            icon={<Skull size={15} />}
            loading={busy('down-volumes')}
            onClick={() => onAction('down-volumes', project)}
            variant="danger"
          >
            Down + volumes
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

      <div className="flex flex-wrap gap-2 border-b border-border">
        {projectTabs.map(([id, label]) => (
          <button
            className={[
              'border-b-2 px-3 py-2 text-sm font-medium transition',
              tab === id
                ? 'border-accent text-accent'
                : 'border-transparent text-text-secondary hover:text-text-primary',
            ].join(' ')}
            key={id}
            onClick={() => onTabChange(id)}
            type="button"
          >
            {label}
          </button>
        ))}
      </div>

      {tab === 'overview' ? (
        <ProjectOverviewTab detail={detail} />
      ) : null}
      {tab === 'services' ? <ProjectServicesTab detail={detail} /> : null}
      {tab === 'containers' ? <ProjectContainersTab detail={detail} /> : null}
      {tab === 'compose' ? <ProjectComposeTab detail={detail} /> : null}
    </div>
  );
}

function ProjectOverviewTab({ detail }: { detail: ProjectDetail }) {
  const project = detail.summary;
  const services = detail.services ?? [];
  const containers = detail.containers ?? [];
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
          tone={project.servicesRunning === project.servicesTotal ? 'ok' : 'warn'}
          value={project.servicesRunning}
        />
        <StatusBlock
          label="Containers"
          tone="neutral"
          value={containers.length}
        />
        <StatusBlock
          label="Updates"
          tone={projectUpdateCount(project) > 0 ? 'warn' : 'ok'}
          value={projectUpdateCount(project)}
        />
      </div>

      <section className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
        {services.map((service) => (
          <Card key={service.name}>
            <CardBody className="space-y-3">
              <div className="flex items-start justify-between gap-2">
                <div className="min-w-0">
                  <h3 className="truncate font-semibold text-text-primary">
                    {service.name}
                  </h3>
                  <div className="mt-1 truncate font-mono text-xs text-text-muted">
                    {service.image || 'build'}
                  </div>
                </div>
                <Badge tone={projectStatusTone(service.status)}>
                  {service.status || 'unknown'}
                </Badge>
              </div>
              <div className="grid grid-cols-3 gap-2">
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
              </div>
              <div className="flex flex-wrap items-center gap-2">
                <Badge tone={healthTone(service.health)}>
                  {service.health || 'unknown'}
                </Badge>
                <PortList ports={service.ports ?? []} />
              </div>
            </CardBody>
          </Card>
        ))}
      </section>
    </div>
  );
}

function ProjectServicesTab({ detail }: { detail: ProjectDetail }) {
  return (
    <DataTable
      columns={[
        {
          id: 'name',
          header: 'Name',
          render: (service) => (
            <span className="font-medium text-text-primary">
              {service.name}
            </span>
          ),
          sortValue: (service) => service.name,
          sortable: true,
        },
        {
          id: 'image',
          header: 'Image',
          render: (service) => service.image || 'build',
          sortValue: (service) => service.image || '',
          sortable: true,
        },
        {
          id: 'replicas',
          header: 'Replicas',
          render: (service) => `${service.running}/${service.replicas}`,
          sortValue: (service) => service.replicas,
          sortable: true,
        },
        {
          id: 'status',
          header: 'Status',
          render: (service) => (
            <Badge tone={projectStatusTone(service.status)}>
              {service.status}
            </Badge>
          ),
          sortValue: (service) => service.status,
          sortable: true,
        },
        {
          id: 'health',
          header: 'Health',
          render: (service) => (
            <Badge tone={healthTone(service.health)}>{service.health}</Badge>
          ),
          sortValue: (service) => service.health,
          sortable: true,
        },
        {
          id: 'ports',
          header: 'Ports',
          render: (service) => <PortList ports={service.ports ?? []} />,
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

function ProjectContainersTab({ detail }: { detail: ProjectDetail }) {
  return (
    <DataTable
      columns={[
        {
          id: 'name',
          header: 'Name',
          render: (container) => (
            <span className="font-medium text-text-primary">
              {container.name}
            </span>
          ),
          sortValue: (container) => container.name,
          sortable: true,
        },
        {
          id: 'service',
          header: 'Service',
          render: (container) => container.service || '-',
          sortValue: (container) => container.service || '',
          sortable: true,
        },
        {
          id: 'image',
          header: 'Image',
          render: (container) => container.image,
          sortValue: (container) => container.image,
          sortable: true,
        },
        {
          id: 'state',
          header: 'State',
          render: (container) => (
            <Badge tone={containerTone(container)}>
              {container.state || container.status}
            </Badge>
          ),
          sortValue: (container) => container.state,
          sortable: true,
        },
        {
          id: 'ports',
          header: 'Ports',
          render: (container) => <PortList ports={container.ports ?? []} />,
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
      rows={detail.containers ?? []}
    />
  );
}

function ProjectComposeTab({ detail }: { detail: ProjectDetail }) {
  const rawFiles = detail.compose?.rawFiles ?? [];
  const [selection, setSelection] = useState('resolved');
  const activeSelection =
    selection === 'resolved' || rawFiles.some((file) => file.path === selection)
      ? selection
      : 'resolved';
  const rawFile = rawFiles.find((file) => file.path === activeSelection);
  const value =
    activeSelection === 'resolved'
      ? detail.compose?.resolvedYAML ?? ''
      : rawFile?.content ?? '';
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <Badge tone={detail.compose?.valid ? 'ok' : 'error'}>
          {detail.compose?.valid ? 'valid' : 'invalid'}
        </Badge>
        {(detail.compose?.envFiles ?? []).map((file) => (
          <Badge key={file} tone="neutral">
            {file}
          </Badge>
        ))}
      </div>
      {detail.compose?.errors?.length ? (
        <div className="rounded-card border border-error/30 bg-error/10 p-3 text-sm text-error">
          {detail.compose.errors.join('\n')}
        </div>
      ) : null}
      <div className="flex flex-wrap gap-2">
        <Button
          onClick={() => setSelection('resolved')}
          variant={activeSelection === 'resolved' ? 'primary' : 'secondary'}
        >
          Resolved
        </Button>
        {rawFiles.map((file) => (
          <Button
            key={file.path}
            onClick={() => setSelection(file.path)}
            variant={activeSelection === file.path ? 'primary' : 'secondary'}
          >
            {shortPath(file.path)}
          </Button>
        ))}
      </div>
      <div className="overflow-hidden rounded-card border border-border">
        <Editor
          height="420px"
          language="yaml"
          options={{
            minimap: { enabled: false },
            readOnly: true,
            scrollBeyondLastLine: false,
            wordWrap: 'on',
          }}
          theme="vs-dark"
          value={value || '# No Compose content available'}
        />
      </div>
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
  onBulkAction: (action: Exclude<ContainerAction, 'kill'>) => void;
  onFilterChange: (filter: FilterID) => void;
  onInspect: (container: ContainerSummary) => void;
  onRename: (container: ContainerSummary) => void;
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
  onRename,
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
          [
            'running',
            'Running',
            containers.filter((container) => container.state === 'running')
              .length,
          ],
          [
            'stopped',
            'Stopped',
            containers.filter((container) => container.state === 'exited')
              .length,
          ],
          [
            'paused',
            'Paused',
            containers.filter((container) => container.state === 'paused')
              .length,
          ],
          [
            'unhealthy',
            'Unhealthy',
            containers.filter((container) => container.health === 'unhealthy')
              .length,
          ],
          [
            'ungrouped',
            'Ungrouped',
            containers.filter((container) => !container.projectID).length,
          ],
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
                <div className="truncate text-text-primary">
                  {container.name}
                </div>
                <div className="truncate text-xs text-text-muted">
                  {container.service || shortID(container.id)}
                </div>
              </div>
            ),
            sortable: true,
            sortValue: (container) => container.name,
          },
          {
            id: 'status',
            header: 'Status',
            render: (container) => (
              <Badge tone={containerTone(container)}>
                {container.state || 'unknown'}
              </Badge>
            ),
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
            render: (container) => (
              <span title={container.image}>{container.image}</span>
            ),
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
            render: (container) =>
              formatMemory(container.memoryBytes, container.memoryLimit),
            sortable: true,
            sortValue: (container) => container.memoryBytes ?? 0,
          },
          {
            id: 'health',
            header: 'Health',
            render: (container) => (
              <Badge tone={healthTone(container.health)}>
                {container.health || 'unknown'}
              </Badge>
            ),
            sortable: true,
            sortValue: (container) => container.health,
          },
          {
            id: 'restarts',
            header: 'Restarts',
            render: (container) => (
              <span
                className={
                  (container.restarts ?? 0) > 3 ? 'text-error' : undefined
                }
              >
                {container.restarts ?? 0}
              </span>
            ),
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
  mutationsDisabled: boolean;
  mutationDisabledReason: string;
  onFilterChange: (filter: FilterID) => void;
  onInspect: (image: ImageSummary) => void;
  onLoad: () => void;
  onPull: () => void;
  onRun: (image?: ImageSummary) => void;
  onSave: (image: ImageSummary) => void;
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
  onRun,
  onSave,
  search,
}: ImagesPageProps) {
  const filtered = useMemo(
    () => filterImages(images, imageUseCounts, search, filter),
    [filter, imageUseCounts, images, search],
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
            ['all', 'All', images.length],
            [
              'in-use',
              'In use',
              images.filter(
                (image) => (imageUseCounts[image.id] ?? 0) > 0 || image.inUse,
              ).length,
            ],
            [
              'unused',
              'Unused',
              images.filter(
                (image) =>
                  (imageUseCounts[image.id] ?? 0) === 0 && !image.inUse,
              ).length,
            ],
            ['dangling', 'Dangling', imageDanglingCount(images)],
            [
              'updates',
              'Update available',
              images.filter(
                (image) =>
                  image.updateStatus && image.updateStatus !== 'unknown',
              ).length,
            ],
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
            render: (image) => (
              <Badge
                tone={
                  (imageUseCounts[image.id] ?? 0) > 0 || image.inUse
                    ? 'accent'
                    : 'neutral'
                }
              >
                {imageUseCounts[image.id] ?? (image.inUse ? '>=1' : 0)}
              </Badge>
            ),
          },
          {
            id: 'update',
            header: 'Update',
            render: (image) => (
              <Badge tone={updateTone(image.updateStatus)}>
                {image.updateStatus || 'unknown'}
              </Badge>
            ),
          },
          {
            id: 'actions',
            header: '',
            render: (image) => (
              <ImageRowActions
                image={image}
                mutationsDisabled={mutationsDisabled}
                mutationDisabledReason={mutationDisabledReason}
                onInspect={() => onInspect(image)}
                onRun={() => onRun(image)}
                onSave={() => onSave(image)}
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
  onCreate: () => void;
  onFilterChange: (filter: FilterID) => void;
  onInspect: (volume: VolumeSummary) => void;
};

function VolumesPage({
  filter,
  loading,
  mutationsDisabled,
  mutationDisabledReason,
  onCreate,
  onFilterChange,
  onInspect,
  search,
  volumeDetails,
  volumes,
}: VolumesPageProps) {
  const filtered = useMemo(
    () => filterVolumes(volumes, search, filter),
    [filter, search, volumes],
  );
  if (loading && volumes.length === 0) {
    return <TableSkeleton />;
  }
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <FilterChips
          active={filter}
          items={[
            ['all', 'All', volumes.length],
            [
              'in-use',
              'In use',
              volumes.filter((volume) => volume.inUse).length,
            ],
            [
              'unused',
              'Unused',
              volumes.filter((volume) => !volume.inUse).length,
            ],
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
        columns={[
          {
            id: 'name',
            header: 'Name',
            render: (volume) => (
              <span className="text-text-primary">{volume.name}</span>
            ),
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
            render: (volume) =>
              volume.sizeBytes ? formatBytes(volume.sizeBytes) : '-',
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
            render: (volume) => (
              <Badge tone={volume.inUse ? 'accent' : 'neutral'}>
                {volumeDetails[volume.name]?.containers?.length ??
                  (volume.inUse ? '>=1' : 0)}
              </Badge>
            ),
          },
          {
            id: 'mountpoint',
            header: 'Mountpoint',
            render: (volume) => (
              <span title={volume.mountpoint}>{volume.mountpoint || '-'}</span>
            ),
          },
          {
            id: 'actions',
            header: '',
            render: (volume) => (
              <RowActions
                id={volume.name}
                label={volume.name}
                onInspect={() => onInspect(volume)}
              />
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
};

function NetworksPage({
  loading,
  mutationsDisabled,
  mutationDisabledReason,
  networkDetails,
  networks,
  onCreate,
  onInspect,
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
        columns={[
          {
            id: 'name',
            header: 'Name',
            render: (network) => (
              <span className="text-text-primary">{network.name}</span>
            ),
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
            render: (network) => (
              <Badge tone="neutral">
                {networkDetails[network.id]?.containers?.length ?? 0}
              </Badge>
            ),
          },
          {
            id: 'internal',
            header: 'Internal',
            render: (network) => (
              <Badge tone={network.internal ? 'info' : 'neutral'}>
                {network.internal ? 'yes' : 'no'}
              </Badge>
            ),
            sortable: true,
            sortValue: (network) => (network.internal ? 1 : 0),
          },
          {
            id: 'actions',
            header: '',
            render: (network) => (
              <RowActions
                id={network.id}
                label={network.name}
                onInspect={() => onInspect(network)}
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

function SearchBox({
  onChange,
  value,
}: {
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

function RowActions({
  id,
  label,
  onInspect,
}: {
  id: string;
  label: string;
  onInspect: () => void;
}) {
  return (
    <div className="flex justify-end gap-1">
      <Tooltip label="Inspect">
        <Button
          aria-label={`Inspect ${label}`}
          icon={<Eye size={15} />}
          onClick={onInspect}
          size="icon"
          variant="ghost"
        />
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

function ImageRowActions({
  image,
  mutationsDisabled,
  mutationDisabledReason,
  onInspect,
  onRun,
  onSave,
}: {
  image: ImageSummary;
  mutationsDisabled: boolean;
  mutationDisabledReason: string;
  onInspect: () => void;
  onRun: () => void;
  onSave: () => void;
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
      <RowActions id={image.id} label={label} onInspect={onInspect} />
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
  const canStop =
    container.state === 'running' ||
    container.state === 'paused' ||
    container.state === 'restarting';
  const canStart =
    container.state !== 'running' && container.state !== 'restarting';
  return (
    <div className="flex justify-end gap-1">
      <Tooltip label={canStart ? 'Start' : 'Stop'}>
        <Button
          aria-label={`${canStart ? 'Start' : 'Stop'} ${container.name}`}
          disabled={
            mutationsDisabled ||
            busyIDs.has(`${canStart ? 'start' : 'stop'}:${container.id}`)
          }
          disabledReason={mutationDisabledReason}
          icon={canStart ? <Play size={15} /> : <Square size={15} />}
          onClick={() => onAction(canStart ? 'start' : 'stop', container)}
          size="icon"
          variant="ghost"
        />
      </Tooltip>
      <Tooltip label="Restart">
        <Button
          aria-label={`Restart ${container.name}`}
          disabled={
            mutationsDisabled || !canStop || busyIDs.has(`restart:${container.id}`)
          }
          disabledReason={
            mutationsDisabled
              ? mutationDisabledReason
              : 'Container is not running'
          }
          icon={<RotateCw size={15} />}
          onClick={() => onAction('restart', container)}
          size="icon"
          variant="ghost"
        />
      </Tooltip>
      <Tooltip label="Kill">
        <Button
          aria-label={`Kill ${container.name}`}
          disabled={
            mutationsDisabled || !canStop || busyIDs.has(`kill:${container.id}`)
          }
          disabledReason={
            mutationsDisabled
              ? mutationDisabledReason
              : 'Container is not running'
          }
          icon={<Skull size={15} />}
          onClick={() => onAction('kill', container)}
          size="icon"
          variant="ghost"
        />
      </Tooltip>
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
  onAction: (action: Exclude<ContainerAction, 'kill'>) => void;
}) {
  return (
    <div className="flex items-center gap-1">
      <Button
        icon={<Play size={15} />}
        disabled={mutationsDisabled}
        disabledReason={mutationDisabledReason}
        loading={busyIDs.has('bulk:start')}
        onClick={() => onAction('start')}
        size="sm"
        variant="secondary"
      >
        Start
      </Button>
      <Button
        icon={<Square size={15} />}
        disabled={mutationsDisabled}
        disabledReason={mutationDisabledReason}
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

function eventPayload<T>(event: unknown): T | null {
  if (!event) {
    return null;
  }
  if (typeof event === 'object' && 'data' in event) {
    return ((event as { data?: T }).data ?? null) as T | null;
  }
  return event as T;
}

function isLogLine(value: unknown): value is LogLine {
  if (!value || typeof value !== 'object') {
    return false;
  }
  const candidate = value as Partial<LogLine>;
  return typeof candidate.text === 'string' && typeof candidate.stream === 'string';
}

function normalizeLogLevel(level?: string): LogLevelFilter {
  const value = level?.toLowerCase();
  if (value === 'error' || value === 'warn' || value === 'info' || value === 'debug') {
    return value;
  }
  return 'unknown';
}

function levelTone(level: LogLevelFilter): BadgeTone {
  if (level === 'error') {
    return 'error';
  }
  if (level === 'warn') {
    return 'warn';
  }
  if (level === 'info') {
    return 'info';
  }
  return 'neutral';
}

function logSource(line: LogLine) {
  return line.containerName || line.service || shortID(line.containerID ?? '') || 'system';
}

function logSourceKey(line: LogLine) {
  return line.containerID || line.containerName || line.service || 'system';
}

function projectName(projects: ProjectSummary[], id: string) {
  return projects.find((project) => project.id === id)?.name ?? id;
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
  const date = value instanceof Date ? value : new Date(String(value ?? ''));
  if (Number.isNaN(date.getTime())) {
    return '--:--:--.---';
  }
  const hh = String(date.getHours()).padStart(2, '0');
  const mm = String(date.getMinutes()).padStart(2, '0');
  const ss = String(date.getSeconds()).padStart(2, '0');
  const ms = String(date.getMilliseconds()).padStart(3, '0');
  return `${hh}:${mm}:${ss}.${ms}`;
}

function renderAnsiText(text: string, query: string) {
  const ansiPattern = new RegExp(`${String.fromCharCode(27)}\\[([0-9;]*)m`, 'g');
  const nodes: JSX.Element[] = [];
  let className = '';
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
    .split(';')
    .filter(Boolean)
    .map((code) => Number.parseInt(code, 10));
  if (codes.length === 0 || codes.includes(0)) {
    return '';
  }
  let next = current;
  for (const code of codes) {
    if (code === 1) {
      next = `${next} font-semibold`.trim();
    } else if (code === 31) {
      next = 'text-error';
    } else if (code === 32) {
      next = 'text-ok';
    } else if (code === 33) {
      next = 'text-warn';
    } else if (code === 34) {
      next = 'text-info';
    } else if (code === 36) {
      next = 'text-accent';
    } else if (code === 90) {
      next = 'text-text-muted';
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
      parts.push(<span key={`${keyPrefix}-text-${key}`}>{text.slice(cursor, index)}</span>);
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
    parts.push(<span key={`${keyPrefix}-text-${key}`}>{text.slice(cursor)}</span>);
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
    .join(', ');
  const parts = [
    `scope ${scope}`,
    `selected ${ids.length || 'all'}`,
    `levels ${selectedLevels}`,
  ];
  if (source) {
    parts.push(`source ${source}`);
  }
  if (query) {
    parts.push(`search ${query}`);
  }
  return parts.join(' | ');
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
      title={inspect.title || 'Inspect'}
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
      title={plan?.title ?? 'Confirm action'}
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
      title={`Rename ${state.container?.name ?? 'container'}`}
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
            'docker',
            'rename',
            state.container?.name ?? '',
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
              {state.step === 1 ? 'Next' : 'Run'}
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
                  ['', 'bridge'],
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
                  ['no', 'no'],
                  ['on-failure', 'on-failure'],
                  ['unless-stopped', 'unless-stopped'],
                  ['always', 'always'],
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
                  {secretKeys(state.envText).join(', ')}
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
        <CodePreview value={joinPreview(['docker', 'pull', ref])} />
        <FormError error={state.error} />
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
          value={joinPreview(['docker', 'save', '-o', state.destPath, ...refs])}
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
          value={joinPreview(['docker', 'load', '-i', state.srcPath])}
        />
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
  const customDriver = state.driver === 'custom';
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
            ['bridge', 'bridge'],
            ['overlay', 'overlay'],
            ['custom', 'custom'],
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
  const wslMount = state.folderPath.replace(/\\/g, '/').startsWith('/mnt/');
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
                {previewName || '-'}
              </div>
              <div className="mt-3 text-xs text-text-muted">
                {state.imported?.summary.id ?? 'Pending validation'}
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
              {result.description || '-'}
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
      {value || '-'}
    </pre>
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

function StatusPill({
  label,
  ok,
  value,
}: {
  label: string;
  ok: boolean;
  value?: string;
}) {
  return (
    <div className="rounded-control border border-border bg-bg-inset p-3">
      <div className="flex items-center gap-2">
        <StatusDot tone={ok ? 'ok' : 'neutral'} />
        <span>{label}</span>
      </div>
      {value ? (
        <div className="mt-1 truncate text-xs text-text-muted">{value}</div>
      ) : null}
    </div>
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
        <Badge
          key={`${port.hostIP}-${port.hostPort}-${port.containerPort}-${port.protocol}`}
        >
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
    return 'Image ref is required';
  }
  try {
    parsePorts(state.portsText);
    parseEnv(state.envText);
    parseMounts(state.volumesText);
  } catch (error) {
    return error instanceof Error ? error.message : 'Invalid run configuration';
  }
  return '';
}

function parsePorts(value: string): PortMapping[] {
  return splitLines(value).map((line) => {
    const [portPart, protocol = 'tcp'] = line.split('/');
    const parts = portPart.split(':');
    const containerPort = parts.pop()?.trim() ?? '';
    const hostPort = parts.pop()?.trim() ?? '';
    const hostIP = parts.join(':').trim();
    if (!containerPort) {
      throw new Error('Container port is required');
    }
    return {
      hostIP,
      hostPort,
      containerPort,
      protocol: protocol.trim() || 'tcp',
    };
  });
}

function parseEnv(value: string) {
  return splitLines(value).map((line) => {
    const [name, envValue = ''] = line.split(/=(.*)/s);
    if (!name.trim()) {
      throw new Error('Environment key is required');
    }
    return { name: name.trim(), value: envValue };
  });
}

function parseMounts(value: string): MountSpec[] {
  return splitLines(value).map((line) => {
    if (line.includes('type=') || line.includes('target=')) {
      const values = parseCommaKeyValue(line);
      const mountType = values.type || 'volume';
      const target = values.target || values.destination || '';
      const source = values.source || values.src || '';
      if (!target || !source) {
        throw new Error('Mount source and target are required');
      }
      return {
        type: mountType,
        source,
        target,
        volumeName: mountType === 'volume' ? source : '',
        readOnly:
          values.ro === 'true' ||
          values.readonly === 'true' ||
          values.mode === 'ro',
      };
    }
    const parts = line.split(':');
    const mode = parts.length > 3 ? parts.pop() : 'rw';
    const target = parts.pop()?.trim() ?? '';
    const source = parts.slice(1).join(':').trim();
    const mountType = parts[0]?.trim() || 'volume';
    if (!target || !source) {
      throw new Error('Mount source and target are required');
    }
    return {
      type: mountType,
      source,
      target,
      volumeName: mountType === 'volume' ? source : '',
      readOnly: mode === 'ro',
    };
  });
}

function parseKeyValueLines(value: string) {
  const pairs = splitLines(value);
  if (pairs.length === 0) {
    return undefined;
  }
  const out: Record<string, string> = {};
  for (const line of pairs) {
    const [key, nextValue = ''] = line.split(/=(.*)/s);
    if (key.trim()) {
      out[key.trim()] = nextValue;
    }
  }
  return out;
}

function parseCommaKeyValue(value: string) {
  const out: Record<string, string> = {};
  for (const raw of value.split(',')) {
    const [key, nextValue = ''] = raw.split(/=(.*)/s);
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

function splitCommand(value: string) {
  const trimmed = value.trim();
  if (!trimmed) {
    return [];
  }
  const matches = trimmed.match(/"([^"]*)"|'([^']*)'|[^\s]+/g) ?? [];
  return matches.map((part) => part.replace(/^["']|["']$/g, ''));
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
  const slash = cleanRef.lastIndexOf('/');
  const colon = cleanRef.lastIndexOf(':');
  if (colon > slash || cleanRef.includes('@')) {
    return cleanRef;
  }
  return `${cleanRef}:${cleanTag}`;
}

function suggestContainerName(ref: string) {
  const withoutDigest = ref.split('@')[0] ?? ref;
  const withoutTag = withoutDigest.replace(/:[^/:]+$/, '');
  const name = withoutTag.split('/').pop() || 'container';
  return name.replace(/[^a-zA-Z0-9_.-]/g, '-');
}

function dockerRunPreview(state: RunImageState) {
  const args = ['docker', 'run', '-d'];
  if (state.name.trim()) {
    args.push('--name', state.name.trim());
  }
  for (const port of parsePorts(state.portsText)) {
    const host = [port.hostIP, port.hostPort].filter(Boolean).join(':');
    args.push(
      '-p',
      `${host ? `${host}:` : ''}${port.containerPort}/${port.protocol || 'tcp'}`,
    );
  }
  for (const env of parseEnv(state.envText)) {
    args.push(
      '-e',
      `${env.name}=${secretLikeKey(env.name) ? '********' : env.value}`,
    );
  }
  for (const mount of parseMounts(state.volumesText)) {
    args.push(
      '--mount',
      `type=${mount.type},source=${mount.source || mount.volumeName},target=${mount.target},${mount.readOnly ? 'ro' : 'rw'}`,
    );
  }
  if (state.networkID) {
    args.push('--network', state.networkID);
  }
  if (state.restartPolicy && state.restartPolicy !== 'no') {
    args.push('--restart', state.restartPolicy);
  }
  if (state.user.trim()) {
    args.push('--user', state.user.trim());
  }
  args.push(state.imageRef.trim() || '<image>');
  args.push(...splitCommand(state.commandText));
  return joinPreview(args);
}

function safeDockerRunPreview(state: RunImageState) {
  try {
    return dockerRunPreview(state);
  } catch {
    return 'docker run -d';
  }
}

function dockerVolumePreview(state: CreateVolumeState) {
  const args = ['docker', 'volume', 'create'];
  if (state.driver.trim()) {
    args.push('--driver', state.driver.trim());
  }
  for (const [key, value] of Object.entries(
    parseKeyValueLines(state.driverOptsText) ?? {},
  )) {
    args.push('--opt', `${key}=${value}`);
  }
  for (const [key, value] of Object.entries(
    parseKeyValueLines(state.labelsText) ?? {},
  )) {
    args.push('--label', `${key}=${value}`);
  }
  args.push(state.name.trim() || '<name>');
  return joinPreview(args);
}

function dockerNetworkPreview(state: CreateNetworkState) {
  const args = ['docker', 'network', 'create'];
  const driver = state.driver === 'custom' ? state.customDriver : state.driver;
  if (driver.trim()) {
    args.push('--driver', driver.trim());
  }
  if (state.subnet.trim()) {
    args.push('--subnet', state.subnet.trim());
  }
  if (state.gateway.trim()) {
    args.push('--gateway', state.gateway.trim());
  }
  if (state.internal) {
    args.push('--internal');
  }
  if (state.attachable) {
    args.push('--attachable');
  }
  for (const [key, value] of Object.entries(
    parseKeyValueLines(state.labelsText) ?? {},
  )) {
    args.push('--label', `${key}=${value}`);
  }
  args.push(state.name.trim() || '<name>');
  return joinPreview(args);
}

function joinPreview(args: string[]) {
  return args.filter(Boolean).map(quotePreviewArg).join(' ');
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
  return ['pass', 'password', 'token', 'secret', 'key', 'auth'].some((marker) =>
    lower.includes(marker),
  );
}

function activeProviderSummary(
  providers: ProviderSummary[],
): ProviderSummary | null {
  return providers.find((provider) => provider.active) ?? providers[0] ?? null;
}

function filterContainers(
  containers: ContainerSummary[],
  search: string,
  filter: FilterID,
) {
  const needle = normalize(search);
  return containers.filter((container) => {
    const matchesSearch = [
      container.name,
      container.image,
      container.id,
      container.projectID,
      container.service,
    ]
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

function filterImages(
  images: ImageSummary[],
  counts: Record<string, number>,
  search: string,
  filter: FilterID,
) {
  const needle = normalize(search);
  return images.filter((image) => {
    const refs = imageRefs(image);
    const matchesSearch = [
      image.id,
      ...refs,
      ...(image.repoDigests ?? []),
    ].some((value) => normalize(value).includes(needle));
    const inUse = (counts[image.id] ?? 0) > 0 || image.inUse;
    const matchesFilter =
      filter === 'all' ||
      (filter === 'in-use' && inUse) ||
      (filter === 'unused' && !inUse) ||
      (filter === 'dangling' && imageDangling(image)) ||
      (filter === 'updates' &&
        Boolean(image.updateStatus && image.updateStatus !== 'unknown'));
    return matchesSearch && matchesFilter;
  });
}

function filterVolumes(
  volumes: VolumeSummary[],
  search: string,
  filter: FilterID,
) {
  const needle = normalize(search);
  return volumes.filter((volume) => {
    const matchesSearch = [
      volume.name,
      volume.driver,
      volume.mountpoint,
      volume.labels?.[composeProjectLabel],
    ]
      .filter(Boolean)
      .some((value) => normalize(value).includes(needle));
    const matchesFilter =
      filter === 'all' ||
      (filter === 'in-use' && volume.inUse) ||
      (filter === 'unused' && !volume.inUse);
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

function normalizePermissionMode(value: unknown): PermissionMode {
  return value === 'group' || value === 'rootless' ? value : 'ask';
}

function imageUsageCounts(containers: ContainerSummary[]) {
  return containers.reduce<Record<string, number>>((counts, container) => {
    if (container.imageID) {
      counts[container.imageID] = (counts[container.imageID] ?? 0) + 1;
    }
    return counts;
  }, {});
}

function filterProjects(
  projects: ProjectSummary[],
  search: string,
  filter: FilterID,
) {
  const query = search.trim().toLowerCase();
  return projects.filter((project) => {
    const matchesSearch = [
      project.name,
      project.id,
      project.providerID,
      project.workingDir,
    ]
      .filter(Boolean)
      .some((value) => value.toLowerCase().includes(query));
    if (!matchesSearch) {
      return false;
    }
    switch (filter) {
      case 'running':
        return project.status === 'running';
      case 'stopped':
        return project.status === 'stopped';
      case 'partial':
        return project.status === 'partial';
      case 'unhealthy':
        return project.health === 'unhealthy';
      case 'updates':
        return projectUpdateCount(project) > 0;
      case 'high-cpu':
        return project.cpuPercent >= 80;
      case 'recent':
        return isRecentlyChanged(project.lastChangedAt);
      default:
        return true;
    }
  });
}

function sortProjects(projects: ProjectSummary[], sort: ProjectSortID) {
  return [...projects].sort((left, right) => {
    if (sort === 'activity') {
      return dateMillis(right.lastChangedAt) - dateMillis(left.lastChangedAt);
    }
    if (sort === 'cpu') {
      return right.cpuPercent - left.cpuPercent;
    }
    return left.name.localeCompare(right.name, undefined, {
      numeric: true,
      sensitivity: 'base',
    });
  });
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
    return [<Badge key="up-to-date" tone="ok">Up to date</Badge>];
  }
  const out = [];
  if (badges.imageUpdates > 0) {
    out.push(<Badge key="image" tone="warn">{badges.imageUpdates} image</Badge>);
  }
  if (badges.baseUpdates > 0) {
    out.push(<Badge key="base" tone="warn">{badges.baseUpdates} base</Badge>);
  }
  if (badges.rebuildNeeded > 0) {
    out.push(<Badge key="rebuild" tone="warn">{badges.rebuildNeeded} rebuild</Badge>);
  }
  if (badges.pinned > 0) {
    out.push(<Badge key="pinned" tone="neutral">{badges.pinned} pinned</Badge>);
  }
  if (badges.unknownBase > 0) {
    out.push(<Badge key="unknown" tone="neutral">{badges.unknownBase} unknown</Badge>);
  }
  return out;
}

function isStatsSample(value: unknown): value is StatsSample {
  if (!value || typeof value !== 'object') {
    return false;
  }
  const sample = value as Partial<StatsSample>;
  return (
    typeof sample.containerID === 'string' &&
    typeof sample.cpuPercent === 'number' &&
    typeof sample.memoryBytes === 'number'
  );
}

function sampleLabel(sample: StatsSample) {
  const date = toDate(sample.sampledAt) ?? new Date();
  return date.toLocaleTimeString(undefined, {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
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

function projectSparkEntries(samples: StatsSample[], label: string) {
  const grouped = new Map<string, number>();
  for (const sample of samples) {
    if (!sample.projectID) {
      continue;
    }
    grouped.set(
      sample.projectID,
      (grouped.get(sample.projectID) ?? 0) + sample.cpuPercent,
    );
  }
  return Array.from(grouped.entries()).map(([id, value]) => ({
    id,
    label,
    value,
  }));
}

function dashboardTopRows(
  dashboardTop: MetricRankItem[],
  latestSamples: Record<string, StatsSample>,
) {
  const liveRows = Object.values(latestSamples)
    .map(
      (sample): MetricRankItem => ({
        id: sample.containerID,
        name: sample.containerName || shortID(sample.containerID),
        kind: 'container',
        cpuPercent: sample.cpuPercent,
        memoryBytes: sample.memoryBytes,
      }),
    )
    .sort(
      (left, right) =>
        (right.cpuPercent ?? 0) - (left.cpuPercent ?? 0) ||
        (right.memoryBytes ?? 0) - (left.memoryBytes ?? 0),
    )
    .slice(0, 8);
  return liveRows.length > 0 ? liveRows : dashboardTop.slice(0, 8);
}

function projectActivityScore(project: ProjectSummary, points?: SparkPoint[]) {
  const latest = points?.[points.length - 1]?.value ?? project.cpuPercent;
  return latest + project.memoryBytes / 1024 / 1024 / 1024;
}

function projectSparkPoints(project: ProjectSummary): SparkPoint[] {
  const baseline = Math.max(0.2, project.cpuPercent);
  return sparkBars(project.id).map((height, index) => ({
    label: String(index),
    value: Math.max(0, (height / 100) * baseline),
  }));
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
  if (metric === 'memory') {
    return formatBytes(value);
  }
  if (metric === 'network' || key === 'netRx' || key === 'netTx') {
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
    case 'error':
      return 'text-error';
    case 'warn':
      return 'text-warn';
    case 'info':
      return 'text-info';
    default:
      return 'text-text-muted';
  }
}

function cleanupPreviewCommands(state: CleanupState) {
  const commands = [];
  if (state.includeImages) {
    commands.push('docker image prune --all');
  }
  if (state.includeContainers) {
    commands.push('docker container prune');
  }
  if (state.includeBuildCache) {
    commands.push('docker builder prune');
  }
  if (state.includeVolumes) {
    commands.push('docker volume prune');
  }
  return commands.length > 0 ? commands : ['docker system df'];
}

function projectActionBusyKey(action: ProjectAction, projectID: string) {
  return `project:${action}:${projectID}`;
}

function isRecentlyChanged(value: unknown) {
  const changedAt = dateMillis(value);
  return changedAt > 0 && Date.now() - changedAt < 24 * 60 * 60 * 1000;
}

function projectStatusTone(status: string): BadgeTone {
  switch (status) {
    case 'running':
      return 'ok';
    case 'partial':
      return 'warn';
    case 'error':
      return 'error';
    case 'stopped':
      return 'neutral';
    default:
      return 'info';
  }
}

function dotTone(tone: BadgeTone): StatusToneID {
  return tone === 'accent' ? 'info' : tone;
}

function sparkBars(seed: string) {
  const source = seed || 'project';
  return Array.from({ length: 24 }, (_, index) => {
    const code = source.charCodeAt(index % source.length) || 17;
    return 18 + ((code + index * 13) % 70);
  });
}

function projectNameFromPath(path: string) {
  const normalized = path.trim().replace(/\\/g, '/').replace(/\/+$/, '');
  return (
    normalized
      .split('/')
      .pop()
      ?.toLowerCase()
      .replace(/[^a-z0-9_-]/g, '-') ?? ''
  );
}

function composeFileCandidates(folderPath: string) {
  if (!folderPath.trim()) {
    return [];
  }
  const separator = folderPath.includes('\\') ? '\\' : '/';
  const base = folderPath.replace(/[\\/]+$/, '');
  return [
    'compose.yaml',
    'compose.yml',
    'docker-compose.yml',
    'docker-compose.yaml',
  ].map((name) => `${base}${separator}${name}`);
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
  if (
    status === 'error' ||
    status === 'auth_required' ||
    status === 'rate_limited'
  ) {
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
  return limit
    ? `${formatBytes(used)} / ${formatBytes(limit)}`
    : formatBytes(used);
}

function shortID(value: string) {
  if (!value) {
    return '-';
  }
  const clean = value.replace(/^sha256:/, '');
  return clean.length > 12 ? `${clean.slice(0, 12)}` : clean;
}

function shortPath(value: string) {
  const normalized = value.replace(/\\/g, '/');
  return normalized.split('/').filter(Boolean).slice(-2).join('/') || value;
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
  return date.toLocaleString(undefined, {
    dateStyle: 'medium',
    timeStyle: 'short',
  });
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
  const tags =
    image.repoTags?.filter((tag) => tag && tag !== '<none>:<none>') ?? [];
  return tags.length > 0 ? tags : (image.repoDigests ?? []);
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

function imageRows(
  image: ImageSummary,
  usedBy: number,
): Array<[string, string]> {
  return [
    ['Image ID', image.id],
    ['Reference', primaryImageRef(image)],
    ['Size', formatBytes(image.sizeBytes)],
    ['Created', formatDate(image.createdAt)],
    ['Used by', String(usedBy || (image.inUse ? '>=1' : 0))],
    ['Update', image.updateStatus ?? 'unknown'],
  ];
}

function imageDetailRows(
  detail: ImageDetail,
  usedBy: number,
): Array<[string, string]> {
  return [
    ...imageRows(detail.summary, usedBy),
    ['Architecture', detail.architecture || '-'],
    ['OS', detail.os || '-'],
    ['Author', detail.author || '-'],
    ['Layers', String(detail.layers?.length ?? 0)],
  ];
}

function volumeRows(
  volume: VolumeSummary,
  detail?: VolumeDetail,
): Array<[string, string]> {
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

function networkRows(
  network: NetworkSummary,
  detail?: NetworkDetail,
): Array<[string, string]> {
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
