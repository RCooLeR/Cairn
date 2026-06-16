import type { LucideIcon } from "lucide-react";

export type PageID =
  | "overview"
  | "projects"
  | "updates"
  | "containers"
  | "images"
  | "volumes"
  | "networks"
  | "logs"
  | "terminal"
  | "settings";

export type NavItem = {
  id: PageID;
  label: string;
  icon: LucideIcon;
};
