import { create } from 'zustand';

import type {
  ProviderStatus,
  ProviderSummary,
} from '../../bindings/github.com/RCooLeR/Cairn/internal/models/models.js';

type ProviderState = {
  providers: ProviderSummary[];
  activeProviderID: string | null;
  status: ProviderStatus | null;
  setProviders: (providers: ProviderSummary[]) => void;
  setActiveProviderID: (providerID: string | null) => void;
  setStatus: (status: ProviderStatus | null) => void;
};

export const useProviderStore = create<ProviderState>((set) => ({
  providers: [],
  activeProviderID: null,
  status: null,
  setProviders: (providers) => set({ providers }),
  setActiveProviderID: (activeProviderID) => set({ activeProviderID }),
  setStatus: (status) => set({ status }),
}));
