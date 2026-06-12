import { create } from 'zustand';

import type {
  ContainerSummary,
  ImageSummary,
  NetworkSummary,
  VolumeSummary,
} from '../../bindings/github.com/RCooLeR/Cairn/internal/models/models.js';

type InventoryState = {
  containers: ContainerSummary[];
  images: ImageSummary[];
  volumes: VolumeSummary[];
  networks: NetworkSummary[];
  setContainers: (containers: ContainerSummary[]) => void;
  setImages: (images: ImageSummary[]) => void;
  setVolumes: (volumes: VolumeSummary[]) => void;
  setNetworks: (networks: NetworkSummary[]) => void;
};

export const useInventoryStore = create<InventoryState>((set) => ({
  containers: [],
  images: [],
  volumes: [],
  networks: [],
  setContainers: (containers) => set({ containers }),
  setImages: (images) => set({ images }),
  setVolumes: (volumes) => set({ volumes }),
  setNetworks: (networks) => set({ networks }),
}));
