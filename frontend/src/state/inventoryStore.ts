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

type InventoryState = {
  status: LoadStatus;
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
  setVolumes: (volumes: VolumeSummary[]) => void;
  setNetworks: (networks: NetworkSummary[]) => void;
};

let refreshPromise: Promise<void> | null = null;

export const useInventoryStore = create<InventoryState>((set) => ({
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
  refresh: async () => {
    if (refreshPromise) {
      return refreshPromise;
    }
    refreshPromise = (async () => {
      set({ status: "loading", error: null });
      try {
        const snapshot = await getInventorySnapshot();
        set({
          status: snapshot.degradedReason ? "error" : "ready",
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
        });
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
      const volumeDetails = await loadVolumeDetails(volumes);
      setInventoryPatch(set, { volumes, volumeDetails });
    } catch (error) {
      setInventoryError(set, error);
    }
  },
  refreshNetworks: async () => {
    try {
      const networks = await DockerService.ListNetworks();
      const networkDetails = await loadNetworkDetails(networks);
      setInventoryPatch(set, { networks, networkDetails });
    } catch (error) {
      setInventoryError(set, error);
    }
  },
  setContainers: (containers) => set({ containers }),
  setImages: (images) => set({ images }),
  setVolumes: (volumes) => set({ volumes }),
  setNetworks: (networks) => set({ networks }),
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

async function loadVolumeDetails(
  volumes: VolumeSummary[],
): Promise<Record<string, VolumeDetail>> {
  const entries = await Promise.allSettled(
    volumes.map(
      async (volume) =>
        [volume.name, await DockerService.GetVolume(volume.name)] as const,
    ),
  );
  return Object.fromEntries(
    entries
      .filter(
        (
          entry,
        ): entry is PromiseFulfilledResult<
          readonly [string, VolumeDetail | null]
        > => entry.status === "fulfilled",
      )
      .filter((entry) => entry.value[1] !== null)
      .map((entry) => [entry.value[0], entry.value[1] as VolumeDetail]),
  );
}

async function loadNetworkDetails(
  networks: NetworkSummary[],
): Promise<Record<string, NetworkDetail>> {
  const entries = await Promise.allSettled(
    networks.map(
      async (network) =>
        [network.id, await DockerService.GetNetwork(network.id)] as const,
    ),
  );
  return Object.fromEntries(
    entries
      .filter(
        (
          entry,
        ): entry is PromiseFulfilledResult<
          readonly [string, NetworkDetail | null]
        > => entry.status === "fulfilled",
      )
      .filter((entry) => entry.value[1] !== null)
      .map((entry) => [entry.value[0], entry.value[1] as NetworkDetail]),
  );
}
