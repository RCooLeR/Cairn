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
  setContainers: (containers) => set({ containers }),
  setImages: (images) => set({ images }),
  setVolumes: (volumes) => set({ volumes }),
  setNetworks: (networks) => set({ networks }),
}));
