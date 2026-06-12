import { create } from 'zustand';

import type {
  ProjectDetail,
  ProjectSummary,
} from '../../bindings/github.com/RCooLeR/Cairn/internal/models/models.js';

type ProjectState = {
  projects: ProjectSummary[];
  selectedProject: ProjectDetail | null;
  setProjects: (projects: ProjectSummary[]) => void;
  setSelectedProject: (project: ProjectDetail | null) => void;
};

export const useProjectStore = create<ProjectState>((set) => ({
  projects: [],
  selectedProject: null,
  setProjects: (projects) => set({ projects }),
  setSelectedProject: (selectedProject) => set({ selectedProject }),
}));
