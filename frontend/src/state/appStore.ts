import { create } from "zustand";

import type { VersionInfo } from "../../bindings/github.com/RCooLeR/Cairn/internal/models/models.js";

type AppState = {
  version: VersionInfo | null;
  versionLoading: boolean;
  versionError: string | null;
  setVersion: (version: VersionInfo) => void;
  setVersionLoading: (loading: boolean) => void;
  setVersionError: (message: string | null) => void;
};

export const useAppStore = create<AppState>((set) => ({
  version: null,
  versionLoading: false,
  versionError: null,
  setVersion: (version) => set({ version, versionError: null }),
  setVersionLoading: (versionLoading) => set({ versionLoading }),
  setVersionError: (versionError) => set({ versionError }),
}));
