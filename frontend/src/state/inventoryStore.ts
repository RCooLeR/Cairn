import { create } from "zustand";

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
import { getInventorySnapshot } from "../api/inventory";

type LoadStatus = "idle" | "loading" | "ready" | "error";

// ConnectionState tracks the Docker engine heartbeat independently of the
// inventory load status. It is driven by the backend docker:connected /
// docker:reconnecting / docker:disconnected events (plus successful snapshots),
// so a transient blip shows a calm "reconnecting" state rather than flashing a
// hard error from a single failed inventory call.
export type ConnectionState =
  | "connecting"
  | "connected"
  | "reconnecting"
  | "disconnected";

type InventoryState = {
  status: LoadStatus;
  connection: ConnectionState;
  error: string | null;
  lastLoadedAt: number | null;
  providers: ProviderSummary[];
  dockerInfo: DockerInfo | null;
  dockerVersion: DockerVersion | null;
  diskUsage: DiskUsage | null;
  containers: ContainerSummary[];
  images: ImageSummary[];
  volumes: VolumeSummary[];
  networks: NetworkSummary[];
  volumeDetails: Record<string, VolumeDetail>;
  networkDetails: Record<string, NetworkDetail>;
  refresh: () => Promise<void>;
  refreshContainers: () => Promise<void>;
  refreshImages: () => Promise<void>;
  refreshVolumes: () => Promise<void>;
  refreshNetworks: () => Promise<void>;
  setContainers: (containers: ContainerSummary[]) => void;
  setImages: (images: ImageSummary[]) => void;
  setNetworkDetail: (id: string, detail: NetworkDetail) => void;
  setVolumes: (volumes: VolumeSummary[]) => void;
  setVolumeDetail: (name: string, detail: VolumeDetail) => void;
  setNetworks: (networks: NetworkSummary[]) => void;
  setConnection: (connection: ConnectionState) => void;
};

let refreshPromise: Promise<void> | null = null;

export const useInventoryStore = create<InventoryState>((set) => ({
  status: "idle",
  connection: "connecting",
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
  refresh: async () => {
    if (refreshPromise) {
      return refreshPromise;
    }
    refreshPromise = (async () => {
      set({ status: "loading", error: null });
      try {
        const snapshot = await getInventorySnapshot();
        set((state) => ({
          status: snapshot.degradedReason ? "error" : "ready",
          // A clean snapshot proves the engine is reachable. A degraded snapshot
          // while we believed we were connected means the engine just turned
          // flaky: downgrade to a calm "reconnecting" rather than leaving a
          // stale "connected" that hides the degraded state with no banner.
          // Startup ("connecting") and already-down states are left for the
          // heartbeat events to resolve.
          connection: snapshot.degradedReason
            ? state.connection === "connected"
              ? "reconnecting"
              : state.connection
            : "connected",
          error: snapshot.degradedReason,
          lastLoadedAt: Date.now(),
          providers: snapshot.providers,
          dockerInfo: snapshot.dockerInfo,
          dockerVersion: snapshot.dockerVersion,
          diskUsage: snapshot.diskUsage,
          containers: snapshot.containers,
          images: snapshot.images,
          volumes: snapshot.volumes,
          networks: snapshot.networks,
          volumeDetails: snapshot.volumeDetails,
          networkDetails: snapshot.networkDetails,
        }));
      } catch (error) {
        set({
          status: "error",
          error:
            error instanceof Error ? error.message : "Docker is not reachable",
          lastLoadedAt: Date.now(),
        });
      } finally {
        refreshPromise = null;
      }
    })();
    return refreshPromise;
  },
  refreshContainers: async () => {
    try {
      const containers = await DockerService.ListContainers({ all: true });
      setInventoryPatch(set, { containers });
    } catch (error) {
      setInventoryError(set, error);
    }
  },
  refreshImages: async () => {
    try {
      const images = await DockerService.ListImages();
      setInventoryPatch(set, { images });
    } catch (error) {
      setInventoryError(set, error);
    }
  },
  refreshVolumes: async () => {
    try {
      const volumes = await DockerService.ListVolumes();
      setInventoryPatch(set, { volumes });
    } catch (error) {
      setInventoryError(set, error);
    }
  },
  refreshNetworks: async () => {
    try {
      const networks = await DockerService.ListNetworks();
      setInventoryPatch(set, { networks });
    } catch (error) {
      setInventoryError(set, error);
    }
  },
  setContainers: (containers) => set({ containers }),
  setImages: (images) => set({ images }),
  setNetworkDetail: (id, detail) =>
    set((current) => ({
      networkDetails: { ...current.networkDetails, [id]: detail },
    })),
  setVolumes: (volumes) => set({ volumes }),
  setVolumeDetail: (name, detail) =>
    set((current) => ({
      volumeDetails: { ...current.volumeDetails, [name]: detail },
    })),
  setNetworks: (networks) => set({ networks }),
  setConnection: (connection) => set({ connection }),
}));

function setInventoryPatch(
  set: (partial: Partial<InventoryState>) => void,
  partial: Partial<InventoryState>,
) {
  set({
    ...partial,
    status: "ready",
    error: null,
    lastLoadedAt: Date.now(),
  });
}

function setInventoryError(
  set: (partial: Partial<InventoryState>) => void,
  error: unknown,
) {
  set({
    status: "error",
    error: error instanceof Error ? error.message : "Docker is not reachable",
    lastLoadedAt: Date.now(),
  });
}
