import type { LucideIcon } from 'lucide-react';
import type {
  CommandPlan,
  ContainerSummary,
  HubSearchResult,
  ImageDetail,
  ImageSummary,
  MountSpec,
  NetworkDetail,
  NetworkSummary,
  PortMapping,
  PortBinding,
  ProjectDetail,
  ProjectSummary,
  ProviderSummary,
  RunImageRequest,
  VolumeDetail,
  VolumeSummary,
} from '../bindings/github.com/RCooLeR/Cairn/internal/models/models.js';

import {
  Bell,
  Box,
  Container,
  Copy,
  Database,
  Download,
  Eye,
  FileJson,
  FolderOpen,
  Gauge,
  HardDrive,
  LayoutGrid,
  List,
  MoreVertical,
  PackagePlus,
  Pencil,
  Plus,
  Network,
  Play,
  RefreshCw,
  RotateCw,
  Search,
  Server,
  Skull,
  Square,
  Upload,
} from 'lucide-react';
import { useCallback, useEffect, useMemo, useState } from 'react';

import Editor from '@monaco-editor/react';
import { Dialogs, Events } from '@wailsio/runtime';

import { getAppVersion } from './api/app';
import { DockerService, ProjectService } from './api/services';
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

type PageID =
  | 'overview'
  | 'projects'
  | 'containers'
  | 'images'
  | 'volumes'
  | 'networks';
type FilterID = string;
type BadgeTone = 'ok' | 'warn' | 'error' | 'info' | 'neutral' | 'accent';
type LoadStatus = 'idle' | 'loading' | 'ready' | 'error';
type ProjectViewMode = 'grid' | 'list';
type ProjectSortID = 'name' | 'activity' | 'cpu';
type ProjectTabID = 'overview' | 'services' | 'containers' | 'compose';

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

const navItems: NavItem[] = [
  { id: 'overview', label: 'Overview', icon: Gauge },
  { id: 'projects', label: 'Projects', icon: LayoutGrid },
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

  const navigate = useCallback((page: PageID) => {
    setActivePage(page);
    setSearch('');
  }, []);

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
  const dockerRunning = !inventoryError && Boolean(dockerInfo || dockerVersion);
  const providerName = activeProvider?.name ?? 'No provider selected';
  const statusLabel = dockerRunning ? 'Running' : 'Stopped';

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

  const runContainerAction = useCallback(
    async (action: ContainerAction, container: ContainerSummary) => {
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
    [refreshAfterAction, setActionBusy],
  );

  const runBulkContainerAction = useCallback(
    async (action: Exclude<ContainerAction, 'kill'>) => {
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
    [refreshAfterAction, selectedContainerIDs, setActionBusy],
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
    [activeProjectID, refreshProjectDetail, refreshProjects, setActionBusy],
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
  }, [refreshAfterAction, runImage]);

  const openRenameModal = useCallback((container: ContainerSummary) => {
    setRename({
      ...emptyRename,
      open: true,
      container,
      name: container.name,
    });
  }, []);

  const submitRename = useCallback(async () => {
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
  }, [refreshAfterAction, rename.container, rename.name]);

  const submitPullImage = useCallback(async () => {
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
  }, [pullImage.ref, pullImage.tag, refreshAfterAction]);

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
  }, [saveImage.destPath, saveImage.refsText]);

  const submitLoadImage = useCallback(async () => {
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
  }, [loadImage.srcPath, refreshAfterAction]);

  const submitCreateVolume = useCallback(async () => {
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
    refreshAfterAction,
  ]);

  const submitCreateNetwork = useCallback(async () => {
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
  }, [createNetwork, refreshAfterAction]);

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
                <StatusDot tone={dockerRunning ? 'ok' : 'neutral'} />
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

      <InspectModal
        inspect={inspect}
        onClose={() => setInspect(emptyInspect)}
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
                <StatusDot
                  pulse={!dockerRunning && provider?.healthy}
                  tone={dockerRunning ? 'ok' : 'neutral'}
                />
                <div>
                  <div className="text-lg font-semibold">
                    Docker Engine - {dockerRunning ? 'Running' : 'Stopped'}
                  </div>
                  <div className="text-sm text-text-muted">
                    {provider?.name ?? 'No provider selected'}
                  </div>
                </div>
              </div>
              <div className="mt-4 grid grid-cols-3 gap-3 text-sm">
                <StatusPill label="Provider" ok={provider?.healthy ?? false} />
                <StatusPill label="Docker" ok={dockerRunning} />
                <StatusPill
                  label="Inventory"
                  ok={
                    containers.length +
                      images.length +
                      volumes.length +
                      networks.length >
                    0
                  }
                />
              </div>
            </div>
            <Server className="h-16 w-16 text-accent/70" strokeWidth={1.4} />
          </CardBody>
        </Card>

        <section
          className="grid grid-cols-2 gap-3"
          aria-label="Docker object counts"
        >
          <MetricButton
            label="Containers"
            value={containers.length}
            hint={`${runningContainers} running`}
            onClick={() => onNavigate('containers')}
          />
          <MetricButton
            label="Images"
            value={images.length}
            hint={`${imageDanglingCount(images)} dangling`}
            onClick={() => onNavigate('images')}
          />
          <MetricButton
            label="Volumes"
            value={volumes.length}
            hint={`${volumes.filter((volume) => volume.inUse).length} in use`}
            onClick={() => onNavigate('volumes')}
          />
          <MetricButton
            label="Networks"
            value={networks.length}
            hint={`${networks.filter((network) => network.internal).length} internal`}
            onClick={() => onNavigate('networks')}
          />
        </section>
      </section>

      <section className="grid grid-cols-[1fr_360px] gap-4">
        <Card>
          <CardHeader
            actions={
              <Badge tone={unhealthyContainers > 0 ? 'error' : 'ok'}>
                {unhealthyContainers} unhealthy
              </Badge>
            }
            title="Container Status"
          />
          <CardBody>
            <div className="grid grid-cols-3 gap-3">
              <StatusBlock
                label="Running"
                tone="ok"
                value={runningContainers}
              />
              <StatusBlock label="Stopped" tone="neutral" value={stopped} />
              <StatusBlock
                label="Unhealthy"
                tone={unhealthyContainers > 0 ? 'error' : 'neutral'}
                value={unhealthyContainers}
              />
            </div>
            <div className="mt-5 space-y-2">
              {containers.slice(0, 6).map((container) => (
                <div
                  className="grid grid-cols-[1fr_auto_auto] items-center gap-3 text-sm"
                  key={container.id}
                >
                  <div className="min-w-0 truncate">{container.name}</div>
                  <Badge tone={containerTone(container)}>
                    {container.state || 'unknown'}
                  </Badge>
                  <span className="text-xs text-text-muted">
                    {formatBytes(container.memoryBytes)}
                  </span>
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
          <CardHeader
            actions={<HardDrive size={16} className="text-text-muted" />}
            title="Disk Usage"
          />
          <CardBody>
            <div className="text-3xl font-semibold">
              {formatBytes(diskTotal)}
            </div>
            <div className="mt-1 text-sm text-text-muted">
              {formatBytes(diskReclaimable)} reclaimable
            </div>
            <div className="mt-5 h-3 overflow-hidden rounded-full bg-bg-inset">
              <div
                className="h-full bg-accent"
                style={{
                  width: `${diskTotal > 0 ? Math.max(8, ((diskTotal - diskReclaimable) / diskTotal) * 100) : 0}%`,
                }}
              />
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

type ProjectsPageProps = {
  projects: ProjectSummary[];
  actionBusyIDs: Set<string>;
  filter: FilterID;
  search: string;
  sort: ProjectSortID;
  view: ProjectViewMode;
  loading: boolean;
  error: string | null;
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
              onAction={onAction}
              onOpen={onOpen}
              project={project}
            />
          ))}
        </section>
      ) : (
        <ProjectList
          actionBusyIDs={actionBusyIDs}
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
  onAction,
  onOpen,
  project,
}: {
  project: ProjectSummary;
  actionBusyIDs: Set<string>;
  onAction: (action: ProjectAction, project: ProjectSummary) => void;
  onOpen: (project: ProjectSummary) => void;
}) {
  const updates = projectUpdateCount(project);
  const workdirMissing = project.status === 'error';
  const primaryAction: ProjectAction =
    project.status === 'running' ? 'stop' : 'start';
  const lifecycleDisabled = workdirMissing || !project.workingDir;
  const disabledReason = workdirMissing
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
          <div className="flex h-full items-end gap-1">
            {sparkBars(project.id).map((height, index) => (
              <span
                className="flex-1 rounded-sm bg-accent/50"
                key={index}
                style={{ height: `${height}%` }}
              />
            ))}
          </div>
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
  onAction,
  onOpen,
  projects,
}: {
  projects: ProjectSummary[];
  actionBusyIDs: Set<string>;
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
  onAction,
  project,
}: {
  project: ProjectSummary;
  actionBusyIDs: Set<string>;
  onAction: (action: ProjectAction, project: ProjectSummary) => void;
}) {
  const workdirMissing = project.status === 'error';
  const lifecycleDisabled = workdirMissing || !project.workingDir;
  const disabledReason = workdirMissing
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
  const lifecycleDisabled = project.status === 'error' || !project.workingDir;
  const disabledReason =
    project.status === 'error'
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
            icon={<Download size={15} />}
            onClick={onPull}
            variant="secondary"
          >
            Pull image
          </Button>
          <Button
            icon={<Upload size={15} />}
            onClick={onLoad}
            variant="secondary"
          >
            Load tar
          </Button>
          <Button
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
  onCreate: () => void;
  onFilterChange: (filter: FilterID) => void;
  onInspect: (volume: VolumeSummary) => void;
};

function VolumesPage({
  filter,
  loading,
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
        <Button icon={<Plus size={15} />} onClick={onCreate} variant="primary">
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
  onCreate: () => void;
  onInspect: (network: NetworkSummary) => void;
};

function NetworksPage({
  loading,
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
        <Button icon={<Plus size={15} />} onClick={onCreate} variant="primary">
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
  onInspect,
  onRun,
  onSave,
}: {
  image: ImageSummary;
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
          icon={<Play size={15} />}
          onClick={onRun}
          size="icon"
          variant="ghost"
        />
      </Tooltip>
      <Tooltip label="Save to tar">
        <Button
          aria-label={`Save ${label}`}
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
  onAction,
  onInspect,
  onRename,
}: {
  busyIDs: Set<string>;
  container: ContainerSummary;
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
          disabled={busyIDs.has(
            `${canStart ? 'start' : 'stop'}:${container.id}`,
          )}
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
      <Tooltip label="Rename">
        <Button
          aria-label={`Rename ${container.name}`}
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
