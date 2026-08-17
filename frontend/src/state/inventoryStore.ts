import { create, type StoreApi } from "zustand";

import type {
  ContainerSummary,
  DiskUsage,
  DockerInfo,
  DockerVersion,
  ImageSummary,
  NetworkDetail,
  NetworkSummary,
  ProviderSummary,
  VolumeDetail,
  VolumeSummary,
} from "../../bindings/github.com/RCooLeR/Cairn/internal/models/models.js";

import { DockerService } from "../api/services";
import {
  getInventorySnapshot,
  inventorySliceNames,
  type InventorySliceName,
  type InventorySnapshot,
} from "../api/inventory";

type LoadStatus = "idle" | "loading" | "ready" | "error";

// ConnectionState tracks the Docker engine heartbeat independently of the
// inventory load status. It is driven by backend heartbeat/provider events and
// by current full-snapshot engine probes. A request may update it only if no
// newer event has advanced the connection epoch.
export type ConnectionState =
  "connecting" | "connected" | "reconnecting" | "disconnected";

export type InventorySliceState = {
  loading: boolean;
  stale: boolean;
  error: string | null;
  lastSuccessAt: number | null;
};

export type InventorySliceStates = Record<
  InventorySliceName,
  InventorySliceState
>;

export type ContainerStatsSample = {
  containerID: string;
  cpuPercent: number;
  gpuDeviceIDs?: string[];
  gpuMemoryBytes?: number;
  gpuUtilizationPercent?: number;
  memoryBytes: number;
  memoryLimitBytes?: number;
  networkRxRate: number;
  networkTxRate: number;
  restartCount?: number;
};

export type DetailLoadResult<Detail> =
  { status: "current"; detail: Detail | null } | { status: "obsolete" };

export type InventoryState = {
  status: LoadStatus;
  connection: ConnectionState;
  error: string | null;
  lastLoadedAt: number | null;
  slices: InventorySliceStates;
  providers: ProviderSummary[];
  dockerInfo: DockerInfo | null;
  dockerVersion: DockerVersion | null;
  diskUsage: DiskUsage | null;
  containers: ContainerSummary[];
  containerStats: Record<string, ContainerStatsSample>;
  containerStatsAuthoritative: boolean;
  images: ImageSummary[];
  volumes: VolumeSummary[];
  networks: NetworkSummary[];
  volumeEpoch: number;
  networkEpoch: number;
  volumeDetails: Record<string, VolumeDetail>;
  networkDetails: Record<string, NetworkDetail>;
  refresh: () => Promise<void>;
  refreshFresh: () => Promise<void>;
  refreshScope: () => Promise<void>;
  refreshContainers: () => Promise<void>;
  refreshImages: () => Promise<void>;
  refreshVolumes: () => Promise<void>;
  refreshNetworks: () => Promise<void>;
  loadVolumeDetail: (name: string) => Promise<DetailLoadResult<VolumeDetail>>;
  loadNetworkDetail: (id: string) => Promise<DetailLoadResult<NetworkDetail>>;
  setContainerStats: (samples: ContainerStatsSample[]) => void;
  setContainers: (containers: ContainerSummary[]) => void;
  setImages: (images: ImageSummary[]) => void;
  setVolumes: (volumes: VolumeSummary[]) => void;
  setNetworks: (networks: NetworkSummary[]) => void;
  setConnection: (connection: ConnectionState) => void;
};

type RefreshableSlice = "containers" | "images" | "volumes" | "networks";
type InventorySet = StoreApi<InventoryState>["setState"];
type InventoryGet = StoreApi<InventoryState>["getState"];

let refreshPromise: Promise<void> | null = null;
let freshnessEpoch = 0;
let completedFreshnessEpoch = 0;
let connectionEpoch = 0;
const sliceGenerations = createSliceGenerations();
let nextDetailRequestToken = 0;
const activeVolumeDetailRequests = new Map<string, number>();
const activeNetworkDetailRequests = new Map<string, number>();
export const maxActiveInventoryDetailRequests = 256;
export const maxCachedInventoryDetails = 256;

export function inventoryDetailRequestDiagnosticsForTest() {
  return {
    activeVolumeRequests: activeVolumeDetailRequests.size,
    activeNetworkRequests: activeNetworkDetailRequests.size,
  };
}

export function createInitialInventorySliceStates(): InventorySliceStates {
  return Object.fromEntries(
    inventorySliceNames.map((slice) => [
      slice,
      {
        loading: false,
        stale: false,
        error: null,
        lastSuccessAt: null,
      },
    ]),
  ) as InventorySliceStates;
}

export const useInventoryStore = create<InventoryState>((set, get) => ({
  status: "idle",
  connection: "connecting",
  error: null,
  lastLoadedAt: null,
  slices: createInitialInventorySliceStates(),
  providers: [],
  dockerInfo: null,
  dockerVersion: null,
  diskUsage: null,
  containers: [],
  containerStats: {},
  containerStatsAuthoritative: false,
  images: [],
  volumes: [],
  networks: [],
  volumeEpoch: 0,
  networkEpoch: 0,
  volumeDetails: {},
  networkDetails: {},
  refresh: () => startOrJoinFullRefresh(set),
  refreshFresh: () => requestFreshInventory(set, false),
  refreshScope: () => requestFreshInventory(set, true),
  refreshContainers: () =>
    refreshSlice(
      "containers",
      () => DockerService.ListContainers({ all: true }),
      set,
    ),
  refreshImages: () =>
    refreshSlice("images", () => DockerService.ListImages(), set),
  refreshVolumes: () =>
    refreshSlice("volumes", () => DockerService.ListVolumes(), set),
  refreshNetworks: () =>
    refreshSlice("networks", () => DockerService.ListNetworks(), set),
  loadVolumeDetail: (name) => loadVolumeDetail(name, set, get),
  loadNetworkDetail: (id) => loadNetworkDetail(id, set, get),
  setContainerStats: (samples) => {
    const containerStats = statsSamplesByID(samples);
    set((state) => ({
      containerStats,
      containerStatsAuthoritative: true,
      containers: applyContainerStats(
        state.containers,
        Object.values(containerStats),
        true,
      ),
    }));
  },
  setContainers: (containers) =>
    setAuthoritativeSlice("containers", containers, set),
  setImages: (images) => setAuthoritativeSlice("images", images, set),
  setVolumes: (volumes) => setAuthoritativeSlice("volumes", volumes, set),
  setNetworks: (networks) => setAuthoritativeSlice("networks", networks, set),
  setConnection: (connection) => {
    connectionEpoch += 1;
    set({ connection });
  },
}));

function startOrJoinFullRefresh(set: InventorySet): Promise<void> {
  if (refreshPromise) {
    return refreshPromise;
  }

  const generations = beginSliceRequests(inventorySliceNames);
  const requestConnectionEpoch = connectionEpoch;
  const requestFreshnessEpoch = freshnessEpoch;
  set((state) => {
    const slices = markSlicesLoading(state.slices, inventorySliceNames);
    return {
      status: deriveStatus(slices, formatSliceErrors(slices)),
      slices,
    };
  });

  let complete!: () => void;
  const promise = new Promise<void>((resolve) => {
    complete = resolve;
  });
  refreshPromise = promise;
  void executeFullRefresh(set, generations, requestConnectionEpoch).then(
    () => finishFullRefresh(promise, requestFreshnessEpoch, complete),
    () => finishFullRefresh(promise, requestFreshnessEpoch, complete),
  );
  return promise;
}

async function executeFullRefresh(
  set: InventorySet,
  generations: Record<InventorySliceName, number>,
  requestConnectionEpoch: number,
) {
  try {
    const snapshot = await getInventorySnapshot();
    const completedAt = Date.now();
    set((state) =>
      applySnapshot(
        state,
        snapshot,
        generations,
        requestConnectionEpoch,
        completedAt,
      ),
    );
  } catch (error) {
    const completedAt = Date.now();
    set((state) =>
      applySnapshotFailure(
        state,
        generations,
        requestConnectionEpoch,
        error,
        completedAt,
      ),
    );
  }
}

function finishFullRefresh(
  promise: Promise<void>,
  requestFreshnessEpoch: number,
  complete: () => void,
) {
  completedFreshnessEpoch = Math.max(
    completedFreshnessEpoch,
    requestFreshnessEpoch,
  );
  if (refreshPromise === promise) {
    refreshPromise = null;
  }
  complete();
}

async function requestFreshInventory(
  set: InventorySet,
  resetScope: boolean,
): Promise<void> {
  freshnessEpoch += 1;
  if (resetScope) {
    connectionEpoch += 1;
    activeVolumeDetailRequests.clear();
    activeNetworkDetailRequests.clear();
  }
  beginSliceRequests(inventorySliceNames);
  set((state) => {
    const slices = markSlicesLoading(
      resetScope ? createInitialInventorySliceStates() : state.slices,
      inventorySliceNames,
    );
    const aggregateError = resetScope ? null : formatSliceErrors(slices);
    return {
      status: deriveStatus(slices, aggregateError),
      slices,
      ...(resetScope
        ? {
            connection: "connecting" as const,
            error: null,
            lastLoadedAt: null,
            providers: [],
            dockerInfo: null,
            dockerVersion: null,
            diskUsage: null,
            containerStats: {},
            containerStatsAuthoritative: false,
            containers: [],
            images: [],
            volumes: [],
            networks: [],
            volumeEpoch: state.volumeEpoch + 1,
            networkEpoch: state.networkEpoch + 1,
            volumeDetails: {},
            networkDetails: {},
          }
        : {}),
    };
  });

  // All callers follow the latest epoch. If a second invalidation arrives while
  // the trailing read is active, that read is invalidated and exactly one more
  // trailing read is shared by every waiter.
  while (completedFreshnessEpoch < freshnessEpoch) {
    await startOrJoinFullRefresh(set);
  }
}

function createSliceGenerations(): Record<InventorySliceName, number> {
  return Object.fromEntries(
    inventorySliceNames.map((slice) => [slice, 0]),
  ) as Record<InventorySliceName, number>;
}

function beginSliceRequests<Slice extends InventorySliceName>(
  slices: readonly Slice[],
): Record<Slice, number> {
  const generations = {} as Record<Slice, number>;
  for (const slice of slices) {
    sliceGenerations[slice] += 1;
    generations[slice] = sliceGenerations[slice];
  }
  return generations;
}

function markSlicesLoading(
  current: InventorySliceStates,
  slices: readonly InventorySliceName[],
): InventorySliceStates {
  const next = { ...current };
  for (const slice of slices) {
    next[slice] = { ...current[slice], loading: true };
  }
  return next;
}

function applySnapshot(
  state: InventoryState,
  snapshot: InventorySnapshot,
  generations: Record<InventorySliceName, number>,
  requestConnectionEpoch: number,
  completedAt: number,
): InventoryState | Partial<InventoryState> {
  const legacyFailure =
    snapshot.failures === undefined && snapshot.degradedReason
      ? errorMessage(snapshot.degradedReason)
      : null;
  const slices = { ...state.slices };
  const patch: Partial<InventoryState> = {};
  let applied = false;

  for (const slice of inventorySliceNames) {
    if (sliceGenerations[slice] !== generations[slice]) {
      continue;
    }

    applied = true;
    const failure = snapshotFailure(snapshot, slice, legacyFailure);
    if (failure) {
      slices[slice] = {
        ...slices[slice],
        loading: false,
        stale: true,
        error: failure,
      };
      continue;
    }

    applySnapshotSlice(patch, state, snapshot, slice);
    slices[slice] = {
      loading: false,
      stale: false,
      error: null,
      lastSuccessAt: completedAt,
    };
  }

  if (!applied) {
    return state;
  }

  const error = formatSliceErrors(slices);
  const connectionPatch =
    connectionEpoch === requestConnectionEpoch
      ? {
          connection: connectionAfterSnapshot(
            state.connection,
            snapshot,
            generations,
            legacyFailure,
          ),
        }
      : {};

  return {
    ...patch,
    ...connectionPatch,
    status: deriveStatus(slices, error),
    error,
    lastLoadedAt: completedAt,
    slices,
  };
}

function applySnapshotSlice(
  patch: Partial<InventoryState>,
  state: InventoryState,
  snapshot: InventorySnapshot,
  slice: InventorySliceName,
) {
  switch (slice) {
    case "containers":
      patch.containers = applyContainerStats(
        snapshot.containers,
        Object.values(state.containerStats),
        state.containerStatsAuthoritative,
      );
      break;
    case "volumes":
      activeVolumeDetailRequests.clear();
      patch.volumes = snapshot.volumes;
      patch.volumeEpoch = state.volumeEpoch + 1;
      patch.volumeDetails = reconcileVolumeDetails(
        snapshot.volumeDetails,
        snapshot.volumes,
      );
      break;
    case "networks":
      activeNetworkDetailRequests.clear();
      patch.networks = snapshot.networks;
      patch.networkEpoch = state.networkEpoch + 1;
      patch.networkDetails = reconcileNetworkDetails(
        snapshot.networkDetails,
        snapshot.networks,
      );
      break;
    default:
      Object.assign(patch, { [slice]: snapshot[slice] });
  }
}

function connectionAfterSnapshot(
  current: ConnectionState,
  snapshot: InventorySnapshot,
  generations: Record<InventorySliceName, number>,
  legacyFailure: string | null,
): ConnectionState {
  const ownsDockerInfo = sliceGenerations.dockerInfo === generations.dockerInfo;
  const ownsDockerVersion =
    sliceGenerations.dockerVersion === generations.dockerVersion;
  const dockerInfoFailed = Boolean(
    snapshotFailure(snapshot, "dockerInfo", legacyFailure),
  );
  const dockerVersionFailed = Boolean(
    snapshotFailure(snapshot, "dockerVersion", legacyFailure),
  );
  if (
    (ownsDockerInfo && !dockerInfoFailed) ||
    (ownsDockerVersion && !dockerVersionFailed)
  ) {
    return "connected";
  }
  if (
    ownsDockerInfo &&
    dockerInfoFailed &&
    ownsDockerVersion &&
    dockerVersionFailed &&
    current === "connected"
  ) {
    return "reconnecting";
  }
  return current;
}

function applySnapshotFailure(
  state: InventoryState,
  generations: Record<InventorySliceName, number>,
  requestConnectionEpoch: number,
  error: unknown,
  completedAt: number,
): InventoryState | Partial<InventoryState> {
  const slices = { ...state.slices };
  const message = errorMessage(error);
  let applied = false;

  for (const slice of inventorySliceNames) {
    if (sliceGenerations[slice] !== generations[slice]) {
      continue;
    }
    applied = true;
    slices[slice] = {
      ...slices[slice],
      loading: false,
      stale: true,
      error: message,
    };
  }

  if (!applied) {
    return state;
  }

  const aggregateError = formatSliceErrors(slices);
  const connectionPatch =
    connectionEpoch === requestConnectionEpoch
      ? {
          connection:
            state.connection === "connected"
              ? ("reconnecting" as const)
              : state.connection,
        }
      : {};
  return {
    ...connectionPatch,
    status: deriveStatus(slices, aggregateError),
    error: aggregateError,
    lastLoadedAt: completedAt,
    slices,
  };
}

async function refreshSlice<Slice extends RefreshableSlice>(
  slice: Slice,
  load: () => Promise<InventoryState[Slice]>,
  set: InventorySet,
): Promise<void> {
  const generation = beginSliceRequests([slice])[slice];
  set((state) => {
    const slices = markSlicesLoading(state.slices, [slice]);
    return {
      status: deriveStatus(slices, formatSliceErrors(slices)),
      slices,
    };
  });

  try {
    const value = await load();
    const completedAt = Date.now();
    set((state) => {
      if (sliceGenerations[slice] !== generation) {
        return state;
      }
      return applySliceSuccess(state, slice, value, completedAt);
    });
  } catch (error) {
    const completedAt = Date.now();
    set((state) => {
      if (sliceGenerations[slice] !== generation) {
        return state;
      }
      const slices = {
        ...state.slices,
        [slice]: {
          ...state.slices[slice],
          loading: false,
          stale: true,
          error: errorMessage(error),
        },
      };
      const aggregateError = formatSliceErrors(slices);
      return {
        status: deriveStatus(slices, aggregateError),
        error: aggregateError,
        lastLoadedAt: completedAt,
        slices,
      };
    });
  }
}

function setAuthoritativeSlice<Slice extends RefreshableSlice>(
  slice: Slice,
  value: InventoryState[Slice],
  set: InventorySet,
) {
  beginSliceRequests([slice]);
  const completedAt = Date.now();
  set((state) => applySliceSuccess(state, slice, value, completedAt));
}

function applySliceSuccess<Slice extends RefreshableSlice>(
  state: InventoryState,
  slice: Slice,
  value: InventoryState[Slice],
  completedAt: number,
): Partial<InventoryState> {
  const slices = {
    ...state.slices,
    [slice]: {
      loading: false,
      stale: false,
      error: null,
      lastSuccessAt: completedAt,
    },
  };
  const error = formatSliceErrors(slices);
  const patch: Partial<InventoryState> = {
    status: deriveStatus(slices, error),
    error,
    lastLoadedAt: completedAt,
    slices,
  };
  switch (slice) {
    case "containers":
      patch.containers = applyContainerStats(
        value as ContainerSummary[],
        Object.values(state.containerStats),
        state.containerStatsAuthoritative,
      );
      break;
    case "volumes":
      activeVolumeDetailRequests.clear();
      patch.volumes = value as VolumeSummary[];
      patch.volumeEpoch = state.volumeEpoch + 1;
      patch.volumeDetails = {};
      break;
    case "networks":
      activeNetworkDetailRequests.clear();
      patch.networks = value as NetworkSummary[];
      patch.networkEpoch = state.networkEpoch + 1;
      patch.networkDetails = {};
      break;
    default:
      Object.assign(patch, { [slice]: value });
  }
  return patch;
}

async function loadVolumeDetail(
  name: string,
  set: InventorySet,
  get: InventoryGet,
): Promise<DetailLoadResult<VolumeDetail>> {
  const request = beginDetailRequest(
    name,
    get().volumeEpoch,
    activeVolumeDetailRequests,
  );
  try {
    let detail: VolumeDetail | null;
    try {
      detail = await DockerService.GetVolume(name);
    } catch (error) {
      const state = get();
      if (
        !detailRequestIsCurrent(
          request,
          state.volumeEpoch,
          activeVolumeDetailRequests,
        ) ||
        !state.volumes.some((volume) => volume.name === name)
      ) {
        return { status: "obsolete" };
      }
      throw error;
    }

    const current = get();
    if (
      !detailRequestIsCurrent(
        request,
        current.volumeEpoch,
        activeVolumeDetailRequests,
      ) ||
      !current.volumes.some((volume) => volume.name === name)
    ) {
      return { status: "obsolete" };
    }

    if (detail) {
      if (detail.summary?.name !== name) {
        throw new Error("Volume detail did not match the requested volume");
      }
      let applied = false;
      set((state) => {
        if (
          !detailRequestIsCurrent(
            request,
            state.volumeEpoch,
            activeVolumeDetailRequests,
          ) ||
          !state.volumes.some((volume) => volume.name === name)
        ) {
          return state;
        }
        applied = true;
        return {
          volumeDetails: upsertBoundedDetail(
            state.volumeDetails,
            name,
            detail!,
          ),
        };
      });
      if (!applied) {
        return { status: "obsolete" };
      }
    }
    return { status: "current", detail };
  } finally {
    finishDetailRequest(request, activeVolumeDetailRequests);
  }
}

async function loadNetworkDetail(
  id: string,
  set: InventorySet,
  get: InventoryGet,
): Promise<DetailLoadResult<NetworkDetail>> {
  const request = beginDetailRequest(
    id,
    get().networkEpoch,
    activeNetworkDetailRequests,
  );
  try {
    let detail: NetworkDetail | null;
    try {
      detail = await DockerService.GetNetwork(id);
    } catch (error) {
      const state = get();
      if (
        !detailRequestIsCurrent(
          request,
          state.networkEpoch,
          activeNetworkDetailRequests,
        ) ||
        !state.networks.some((network) => network.id === id)
      ) {
        return { status: "obsolete" };
      }
      throw error;
    }

    const current = get();
    if (
      !detailRequestIsCurrent(
        request,
        current.networkEpoch,
        activeNetworkDetailRequests,
      ) ||
      !current.networks.some((network) => network.id === id)
    ) {
      return { status: "obsolete" };
    }

    if (detail) {
      if (detail.summary?.id !== id) {
        throw new Error("Network detail did not match the requested network");
      }
      let applied = false;
      set((state) => {
        if (
          !detailRequestIsCurrent(
            request,
            state.networkEpoch,
            activeNetworkDetailRequests,
          ) ||
          !state.networks.some((network) => network.id === id)
        ) {
          return state;
        }
        applied = true;
        return {
          networkDetails: upsertBoundedDetail(
            state.networkDetails,
            id,
            detail!,
          ),
        };
      });
      if (!applied) {
        return { status: "obsolete" };
      }
    }
    return { status: "current", detail };
  } finally {
    finishDetailRequest(request, activeNetworkDetailRequests);
  }
}

type DetailRequest = {
  key: string;
  listEpoch: number;
  token: number;
};

function beginDetailRequest(
  key: string,
  listEpoch: number,
  activeRequests: Map<string, number>,
): DetailRequest {
  nextDetailRequestToken += 1;
  if (
    !activeRequests.has(key) &&
    activeRequests.size >= maxActiveInventoryDetailRequests
  ) {
    const oldestKey = activeRequests.keys().next().value;
    if (typeof oldestKey === "string") {
      activeRequests.delete(oldestKey);
    }
  }
  activeRequests.delete(key);
  activeRequests.set(key, nextDetailRequestToken);
  return {
    key,
    listEpoch,
    token: nextDetailRequestToken,
  };
}

function detailRequestIsCurrent(
  request: DetailRequest,
  listEpoch: number,
  activeRequests: Map<string, number>,
) {
  return (
    listEpoch === request.listEpoch &&
    activeRequests.get(request.key) === request.token
  );
}

function finishDetailRequest(
  request: DetailRequest,
  activeRequests: Map<string, number>,
) {
  if (activeRequests.get(request.key) === request.token) {
    activeRequests.delete(request.key);
  }
}

function reconcileVolumeDetails(
  incoming: Record<string, VolumeDetail>,
  volumes: VolumeSummary[],
) {
  const names = new Set(volumes.map((volume) => volume.name));
  return Object.fromEntries(
    Object.entries(incoming)
      .filter(
        ([name, detail]) => names.has(name) && detail.summary?.name === name,
      )
      .slice(-maxCachedInventoryDetails),
  );
}

function reconcileNetworkDetails(
  incoming: Record<string, NetworkDetail>,
  networks: NetworkSummary[],
) {
  const ids = new Set(networks.map((network) => network.id));
  return Object.fromEntries(
    Object.entries(incoming)
      .filter(([id, detail]) => ids.has(id) && detail.summary?.id === id)
      .slice(-maxCachedInventoryDetails),
  );
}

function upsertBoundedDetail<Detail>(
  current: Record<string, Detail>,
  key: string,
  detail: Detail,
): Record<string, Detail> {
  const entries = Object.entries(current).filter(
    ([currentKey]) => currentKey !== key,
  );
  return Object.fromEntries(
    entries.slice(-(maxCachedInventoryDetails - 1)).concat([[key, detail]]),
  );
}

function statsSamplesByID(samples: ContainerStatsSample[]) {
  return Object.fromEntries(
    samples.map((sample) => [sample.containerID, sample]),
  );
}

function applyContainerStats(
  containers: ContainerSummary[],
  samples: ContainerStatsSample[],
  clearMissing = false,
) {
  const byID = new Map(samples.map((sample) => [sample.containerID, sample]));
  let changed = false;
  const next = containers.map((container) => {
    const sample = byID.get(container.id);
    if (!sample) {
      if (!clearMissing) {
        return container;
      }
      const cleared = clearContainerStats(container);
      changed = changed || cleared !== container;
      return cleared;
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

function clearContainerStats(container: ContainerSummary) {
  if (
    (container.cpuPercent ?? 0) === 0 &&
    (container.gpuDeviceIDs?.length ?? 0) === 0 &&
    (container.gpuMemoryBytes ?? 0) === 0 &&
    (container.gpuUtilizationPercent ?? 0) === 0 &&
    (container.memoryBytes ?? 0) === 0 &&
    (container.memoryLimit ?? 0) === 0 &&
    (container.netRxRate ?? 0) === 0 &&
    (container.netTxRate ?? 0) === 0
  ) {
    return container;
  }
  return {
    ...container,
    cpuPercent: 0,
    gpuDeviceIDs: [],
    gpuMemoryBytes: 0,
    gpuUtilizationPercent: 0,
    memoryBytes: 0,
    memoryLimit: 0,
    netRxRate: 0,
    netTxRate: 0,
  };
}

function deriveStatus(
  slices: InventorySliceStates,
  error: string | null,
): LoadStatus {
  if (inventorySliceNames.some((slice) => slices[slice].loading)) {
    return "loading";
  }
  if (error) {
    return "error";
  }
  if (
    inventorySliceNames.some((slice) => slices[slice].lastSuccessAt !== null)
  ) {
    return "ready";
  }
  return "idle";
}

function snapshotFailure(
  snapshot: InventorySnapshot,
  slice: InventorySliceName,
  legacyFailure: string | null,
): string | null {
  if (
    snapshot.failures &&
    Object.prototype.hasOwnProperty.call(snapshot.failures, slice)
  ) {
    return errorMessage(snapshot.failures[slice]);
  }
  return legacyFailure;
}

function formatSliceErrors(slices: InventorySliceStates): string | null {
  const errors = inventorySliceNames.flatMap((slice) => {
    const message = slices[slice].error;
    return message ? [`${slice}: ${message}`] : [];
  });
  return errors.length > 0 ? errors.join("; ") : null;
}

function errorMessage(error: unknown): string {
  const message =
    error instanceof Error
      ? error.message.trim()
      : typeof error === "string"
        ? error.trim()
        : "";
  return message || "Docker is not reachable";
}
