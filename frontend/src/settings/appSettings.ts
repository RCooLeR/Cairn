export type AppSettings = Record<string, unknown>;

export type PermissionMode = "ask" | "group" | "rootless";
export type ThemePreference = "dark" | "light" | "system";

export function settingString(
  settings: AppSettings,
  key: string,
  fallback: string,
) {
  return typeof settings[key] === "string" ? settings[key] : fallback;
}

export function settingBool(
  settings: AppSettings,
  key: string,
  fallback: boolean,
) {
  return typeof settings[key] === "boolean" ? settings[key] : fallback;
}

export function settingInt(
  settings: AppSettings,
  key: string,
  fallback: number,
) {
  const value = settings[key];
  return typeof value === "number" && Number.isFinite(value) ? value : fallback;
}

export function normalizePermissionMode(value: unknown): PermissionMode {
  return value === "group" || value === "rootless" ? value : "ask";
}

export function normalizeThemePreference(value: unknown): ThemePreference {
  return value === "light" || value === "system" ? value : "dark";
}

export function normalizeStringSetting(value: unknown, fallback: string) {
  return typeof value === "string" && value.trim() ? value : fallback;
}

export function normalizeIntSetting(value: unknown, fallback: number) {
  return typeof value === "number" && Number.isFinite(value) ? value : fallback;
}

export function normalizeBoolSetting(value: unknown, fallback: boolean) {
  return typeof value === "boolean" ? value : fallback;
}
