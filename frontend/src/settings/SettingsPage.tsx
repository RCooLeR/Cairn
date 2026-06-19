import { useState } from "react";
import {
  Download,
  KeyRound,
  LogIn,
  RefreshCw,
  Server,
  ShieldAlert,
  Terminal,
  Wrench,
} from "lucide-react";

import type {
  AuditEntry,
  DockerContextInfo,
  ProviderSummary,
  RegistryAccount,
  RegistryAuthStatus,
  VersionInfo,
  WSLDistroInfo,
} from "../../bindings/github.com/RCooLeR/Cairn/internal/models/models.js";
import {
  Badge,
  Button,
  Card,
  CardBody,
  CardHeader,
  EmptyState,
  Modal,
  StatusPill,
  TableSkeleton,
} from "../components/ui";
import {
  DockerContextsTable,
  RegistryAccountsTable,
} from "../components/settings/SettingsTables";
import {
  settingBool,
  settingInt,
  settingString,
  type AppSettings,
} from "./appSettings";
import { dateMillis } from "../utils/time";
import { riskTone, type BadgeTone } from "../utils/tones";
export type SettingsSectionID =
  | "general"
  | "providers"
  | "contexts"
  | "updates"
  | "registries"
  | "metrics"
  | "terminal"
  | "agent"
  | "appearance"
  | "backups"
  | "security"
  | "advanced"
  | "about";
export type AuditRangeID = "24h" | "7d" | "30d" | "90d" | "all";
export type AuditFilterState = {
  range: AuditRangeID;
  action: string;
  status: string;
  projectID: string;
};
export function PathRecommendation() {
  return (
    <div className="rounded-card border border-warn/30 bg-warn/10 px-3 py-2 text-sm text-warn">
      Store heavy Compose projects inside the WSL distro, such as `~/projects`,
      instead of `/mnt/c/...`.
    </div>
  );
}

export function ColimaPathRecommendation() {
  return (
    <div className="rounded-card border border-info/30 bg-info/10 px-3 py-2 text-sm text-info">
      Colima mounts your home directory by default; keep heavy Compose projects
      under `$HOME` unless the profile mounts additional paths.
    </div>
  );
}

export function LinuxPathRecommendation() {
  return (
    <div className="rounded-card border border-info/30 bg-info/10 px-3 py-2 text-sm text-info">
      Keep Compose projects on the local Linux filesystem for predictable file
      watching, bind mounts, and rebuild performance.
    </div>
  );
}

export function SettingsPage({
  activeProvider,
  auditEntries,
  auditError,
  auditFilter,
  auditLoading,
  autostartBackend,
  colimaCPU,
  colimaDiskGB,
  colimaMemoryGB,
  colimaProfile,
  dockerContexts,
  dockerContextsError,
  dockerContextsLoading,
  error,
  message,
  onAutostartChange,
  onColimaCPUChange,
  onColimaDiskGBChange,
  onColimaMemoryGBChange,
  onColimaProfileChange,
  onDetect,
  onOpenSetup,
  onRefreshDockerContexts,
  onRefreshWSLDistros,
  onRefreshAudit,
  onRefreshRegistries,
  onRegistryLogin,
  onRegistryLogout,
  onRegistryTest,
  onAuditFilterChange,
  onSettingChange,
  onSaveColimaCPU,
  onSaveColimaDiskGB,
  onSaveColimaMemoryGB,
  onSaveColimaProfile,
  onSaveWSLDistro,
  onUseDockerContext,
  onUseWSLDistro,
  onWSLDistroChange,
  providers,
  registryAccounts,
  registryAccountsError,
  registryAccountsLoading,
  registryBusyKeys,
  registryStatuses,
  saving,
  section,
  settings,
  onSectionChange,
  version,
  wslDistro,
  wslDistros,
  wslDistrosError,
  wslDistrosLoading,
}: {
  activeProvider: ProviderSummary | null;
  auditEntries: AuditEntry[];
  auditError: string | null;
  auditFilter: AuditFilterState;
  auditLoading: boolean;
  autostartBackend: boolean;
  colimaCPU: number;
  colimaDiskGB: number;
  colimaMemoryGB: number;
  colimaProfile: string;
  dockerContexts: DockerContextInfo[];
  dockerContextsError: string | null;
  dockerContextsLoading: boolean;
  error: string | null;
  message: string | null;
  onAutostartChange: (enabled: boolean) => void;
  onColimaCPUChange: (value: number) => void;
  onColimaDiskGBChange: (value: number) => void;
  onColimaMemoryGBChange: (value: number) => void;
  onColimaProfileChange: (profile: string) => void;
  onDetect: () => void;
  onOpenSetup: () => void;
  onRefreshDockerContexts: () => void;
  onRefreshWSLDistros: () => void;
  onRefreshAudit: () => void;
  onRefreshRegistries: () => void;
  onRegistryLogin: (registry?: string) => void;
  onRegistryLogout: (registry: string) => void;
  onRegistryTest: (registry: string) => void;
  onAuditFilterChange: (patch: Partial<AuditFilterState>) => void;
  onSettingChange: (key: string, value: unknown) => void;
  onSaveColimaCPU: () => void;
  onSaveColimaDiskGB: () => void;
  onSaveColimaMemoryGB: () => void;
  onSaveColimaProfile: () => void;
  onSaveWSLDistro: () => void;
  onUseDockerContext: (name: string) => void;
  onUseWSLDistro: (distro: string) => void;
  onWSLDistroChange: (distro: string) => void;
  providers: ProviderSummary[];
  registryAccounts: RegistryAccount[];
  registryAccountsError: string | null;
  registryAccountsLoading: boolean;
  registryBusyKeys: Set<string>;
  registryStatuses: Record<string, RegistryAuthStatus>;
  saving: boolean;
  section: SettingsSectionID;
  settings: AppSettings;
  onSectionChange: (section: SettingsSectionID) => void;
  version: VersionInfo | null;
  wslDistro: string;
  wslDistros: WSLDistroInfo[];
  wslDistrosError: string | null;
  wslDistrosLoading: boolean;
}) {
  const [selectedAuditEntry, setSelectedAuditEntry] =
    useState<AuditEntry | null>(null);
  const activeStatus = activeProvider?.status;
  const providerKind = activeProvider?.kind || "windows_wsl_ubuntu";
  const registryCredentialMode = settingString(
    settings,
    "registry.credentials_mode",
    "docker_helper",
  );
  const registryLoginDisabled = registryCredentialMode === "none";
  const registryLoginDisabledReason =
    "Switch Credential mode to Prefer Docker credential helper before logging in from Cairn.";
  const hasUnencryptedRegistryCredentials = registryAccounts.some(
    (account) => account.source === "authsFile",
  );
  const settingsSections: Array<[SettingsSectionID, string]> = [
    ["general", "General"],
    ["providers", "Providers"],
    ["contexts", "Docker contexts"],
    ["updates", "Updates"],
    ["registries", "Registries"],
    ["metrics", "Metrics"],
    ["terminal", "Terminal"],
    ["agent", "Agent"],
    ["appearance", "Appearance"],
    ["backups", "Backups"],
    ["security", "Security & Audit"],
    ["advanced", "Advanced"],
    ["about", "About"],
  ];
  return (
    <div className="grid gap-4 xl:grid-cols-[220px_minmax(0,1fr)]">
      <div className="xl:hidden">
        <label className="sr-only" htmlFor="settings-section-select">
          Settings section
        </label>
        <select
          className="h-10 w-full rounded-control border border-border bg-bg-inset px-3 text-sm text-text-primary"
          id="settings-section-select"
          onChange={(event) =>
            onSectionChange(event.target.value as SettingsSectionID)
          }
          value={section}
        >
          {settingsSections.map(([id, label]) => (
            <option key={id} value={id}>
              {label}
            </option>
          ))}
        </select>
      </div>

      <div className="hidden space-y-2 xl:block">
        {settingsSections.map(([id, label]) => (
          <button
            className={[
              "block h-9 w-full rounded-control px-3 text-left text-sm",
              section === id
                ? "bg-accent/10 text-accent"
                : "text-text-secondary hover:bg-bg-card",
            ].join(" ")}
            key={id}
            onClick={() => onSectionChange(id)}
            type="button"
          >
            {label}
          </button>
        ))}
      </div>

      <div className="space-y-4">
        {message ? (
          <div className="rounded-card border border-ok/30 bg-ok/10 px-3 py-2 text-sm text-ok">
            {message}
          </div>
        ) : null}
        {error ? (
          <div className="rounded-card border border-error/30 bg-error/10 px-3 py-2 text-sm text-error">
            {error}
          </div>
        ) : null}

        {section === "general" ? (
          <Card>
            <CardHeader title="General" />
            <CardBody className="space-y-3">
              <SettingsSelectField
                disabled={saving}
                label="Theme"
                onChange={(value) => onSettingChange("general.theme", value)}
                options={[
                  ["dark", "Dark"],
                  ["light", "Light"],
                  ["system", "System"],
                ]}
                value={settingString(settings, "general.theme", "dark")}
              />
              <SettingsCheckboxField
                checked={settingBool(settings, "general.autostart_app", false)}
                disabled={saving}
                label="Launch Cairn at login"
                onChange={(checked) =>
                  onSettingChange("general.autostart_app", checked)
                }
              />
              <SettingsCheckboxField
                checked={autostartBackend}
                disabled={saving}
                label="Auto-start Docker backend on app launch"
                onChange={onAutostartChange}
              />
              <SettingsSelectField
                disabled
                label="Language"
                onChange={() => undefined}
                options={[["en", "English"]]}
                value={settingString(settings, "general.language", "en")}
              />
            </CardBody>
          </Card>
        ) : null}

        {section === "updates" ? (
          <Card>
            <CardHeader title="Updates" />
            <CardBody className="space-y-3">
              <SettingsSelectField
                disabled={saving}
                label="Check interval"
                onChange={(value) =>
                  onSettingChange("updates.check_interval_hours", Number(value))
                }
                options={[
                  ["0", "Manual only"],
                  ["6", "Every 6 hours"],
                  ["24", "Daily"],
                  ["168", "Weekly"],
                ]}
                value={String(
                  settingInt(settings, "updates.check_interval_hours", 24),
                )}
              />
              <SettingsCheckboxField
                checked={settingBool(settings, "updates.notify", true)}
                disabled={saving}
                label="Notify on available updates"
                onChange={(checked) =>
                  onSettingChange("updates.notify", checked)
                }
              />
              <ReadOnlySetting
                label="Default health watch"
                value="Enabled in update confirmations"
              />
            </CardBody>
          </Card>
        ) : null}

        {section === "metrics" ? (
          <Card>
            <CardHeader title="Metrics" />
            <CardBody className="space-y-3">
              <SettingsNumberSetting
                disabled={saving}
                label="Sample interval seconds"
                max={10}
                min={1}
                onSave={(value) =>
                  onSettingChange("metrics.sample_interval_seconds", value)
                }
                value={settingInt(
                  settings,
                  "metrics.sample_interval_seconds",
                  2,
                )}
              />
              <ReadOnlySetting
                label="Retention"
                value={`${settingInt(
                  settings,
                  "metrics.retention_raw_minutes",
                  60,
                )} min raw -> 24 h / 1 m -> 7 d / 15 m`}
              />
              <Button
                disabled
                disabledReason="Metrics compaction runs automatically"
              >
                Compact now
              </Button>
            </CardBody>
          </Card>
        ) : null}

        {section === "terminal" ? (
          <Card>
            <CardHeader title="Terminal" />
            <CardBody className="space-y-3">
              <SettingsTextSetting
                disabled={saving}
                label="Default shell"
                onSave={(value) =>
                  onSettingChange("terminal.default_shell", value)
                }
                placeholder="Auto-detect"
                value={settingString(settings, "terminal.default_shell", "")}
              />
              <ReadOnlySetting label="Paste guard" value="Enabled" />
            </CardBody>
          </Card>
        ) : null}

        {section === "agent" ? (
          <Card>
            <CardHeader title="Agent" />
            <CardBody className="space-y-3">
              <SettingsCheckboxField
                checked={settingBool(settings, "agent.enabled", true)}
                disabled={saving}
                label="Enable local agent"
                onChange={(checked) =>
                  onSettingChange("agent.enabled", checked)
                }
              />
              <SettingsSelectField
                disabled={saving}
                label="Provider"
                onChange={(value) => onSettingChange("agent.provider", value)}
                options={[
                  ["ollama", "Ollama"],
                  ["openai_compatible", "OpenAI-compatible"],
                ]}
                value={settingString(settings, "agent.provider", "ollama")}
              />
              <SettingsTextSetting
                disabled={saving}
                label="Endpoint"
                onSave={(value) => onSettingChange("agent.endpoint", value)}
                placeholder="http://127.0.0.1:11434"
                value={settingString(
                  settings,
                  "agent.endpoint",
                  "http://127.0.0.1:11434",
                )}
              />
              <SettingsTextSetting
                disabled={saving}
                label="Preferred model"
                onSave={(value) => onSettingChange("agent.model", value)}
                placeholder="gemma4:12b-it-q8_0"
                value={settingString(
                  settings,
                  "agent.model",
                  "gemma4:12b-it-q8_0",
                )}
              />
              <SettingsNumberSetting
                disabled={saving}
                label="Max context lines"
                max={2000}
                min={100}
                onSave={(value) =>
                  onSettingChange("agent.max_context_lines", value)
                }
                value={settingInt(settings, "agent.max_context_lines", 400)}
              />
              <ReadOnlySetting
                label="Fallback order"
                value="gemma4:12b-it-q8_0 -> gemma4:12b -> gemma4/devstral/gpt-oss/granite -> qwen2.5-coder -> deepseek-coder-v2 -> llama3.1 -> mistral -> codellama -> gemma3"
              />
            </CardBody>
          </Card>
        ) : null}

        {section === "appearance" ? (
          <Card>
            <CardHeader title="Appearance" />
            <CardBody className="space-y-3">
              <SettingsSelectField
                disabled={saving}
                label="Theme"
                onChange={(value) => onSettingChange("general.theme", value)}
                options={[
                  ["dark", "Dark"],
                  ["light", "Light"],
                  ["system", "System"],
                ]}
                value={settingString(settings, "general.theme", "dark")}
              />
              <ReadOnlySetting label="Density" value="Comfortable" />
              <ReadOnlySetting label="Reduced motion" value="System" />
            </CardBody>
          </Card>
        ) : null}

        {section === "backups" ? (
          <Card>
            <CardHeader title="Backups" />
            <CardBody className="space-y-3">
              <SettingsTextSetting
                disabled={saving}
                label="Backup directory"
                onSave={(value) => onSettingChange("backups.directory", value)}
                placeholder="Default app data folder"
                value={settingString(settings, "backups.directory", "")}
              />
              <ReadOnlySetting
                label="Provider-mapped path"
                value="Resolved by backup plans"
              />
            </CardBody>
          </Card>
        ) : null}

        {section === "security" ? (
          <Card>
            <CardHeader
              actions={
                <Button
                  icon={<RefreshCw size={15} />}
                  loading={auditLoading}
                  onClick={onRefreshAudit}
                  size="sm"
                  variant="secondary"
                >
                  Refresh
                </Button>
              }
              title="Security & Audit"
            />
            <CardBody className="space-y-3">
              <SettingsCheckboxField
                checked={settingBool(
                  settings,
                  "security.confirm_destructive",
                  true,
                )}
                disabled
                label="Destructive-action confirmation"
              />
              <ReadOnlySetting
                label="Audit retention"
                value="90 days or 50,000 rows"
              />
              <div className="grid gap-3 md:grid-cols-4">
                <SettingsSelectField
                  disabled={auditLoading}
                  label="Range"
                  onChange={(value) =>
                    onAuditFilterChange({ range: value as AuditRangeID })
                  }
                  options={[
                    ["24h", "Last 24 hours"],
                    ["7d", "Last 7 days"],
                    ["30d", "Last 30 days"],
                    ["90d", "Last 90 days"],
                    ["all", "All retained"],
                  ]}
                  value={auditFilter.range}
                />
                <label className="block">
                  <span className="text-xs font-medium uppercase text-text-muted">
                    Action
                  </span>
                  <input
                    className="mt-1 h-9 w-full rounded-control border border-border bg-bg-inset px-3 text-sm text-text-primary outline-none"
                    onChange={(event) =>
                      onAuditFilterChange({ action: event.target.value })
                    }
                    placeholder="update.apply"
                    value={auditFilter.action}
                  />
                </label>
                <SettingsSelectField
                  disabled={auditLoading}
                  label="Status"
                  onChange={(value) => onAuditFilterChange({ status: value })}
                  options={[
                    ["", "Any status"],
                    ["started", "Started"],
                    ["success", "Success"],
                    ["failed", "Failed"],
                    ["cancelled", "Cancelled"],
                  ]}
                  value={auditFilter.status}
                />
                <label className="block">
                  <span className="text-xs font-medium uppercase text-text-muted">
                    Project
                  </span>
                  <input
                    className="mt-1 h-9 w-full rounded-control border border-border bg-bg-inset px-3 text-sm text-text-primary outline-none"
                    onChange={(event) =>
                      onAuditFilterChange({ projectID: event.target.value })
                    }
                    placeholder="linux_native/app"
                    value={auditFilter.projectID}
                  />
                </label>
              </div>
              <div className="flex justify-end">
                <Button
                  disabled={auditEntries.length === 0}
                  disabledReason="No audit rows match the current filters"
                  icon={<Download size={15} />}
                  onClick={() => exportAuditCSV(auditEntries)}
                  size="sm"
                  variant="secondary"
                >
                  Export CSV
                </Button>
              </div>
              {auditError ? (
                <div className="rounded-card border border-error/30 bg-error/10 px-3 py-2 text-sm text-error">
                  {auditError}
                </div>
              ) : null}
              {auditLoading && auditEntries.length === 0 ? (
                <TableSkeleton />
              ) : null}
              {!auditLoading && auditEntries.length === 0 ? (
                <EmptyState
                  body="Confirmed actions and provider lifecycle events appear here."
                  icon={<ShieldAlert size={28} />}
                  title="No audit rows"
                />
              ) : null}
              {auditEntries.length > 0 ? (
                <AuditLogTable
                  entries={auditEntries}
                  onSelect={setSelectedAuditEntry}
                />
              ) : null}
            </CardBody>
            <AuditEntryModal
              entry={selectedAuditEntry}
              onClose={() => setSelectedAuditEntry(null)}
            />
          </Card>
        ) : null}

        {section === "advanced" ? (
          <Card>
            <CardHeader title="Advanced" />
            <CardBody className="space-y-3">
              <ReadOnlySetting label="Runtime cache" value="Managed by Cairn" />
              <Button
                disabled
                disabledReason="No cached data is ready to reset"
              >
                Reset all caches
              </Button>
            </CardBody>
          </Card>
        ) : null}

        {section === "about" ? (
          <Card>
            <CardHeader title="About" />
            <CardBody className="grid gap-3 sm:grid-cols-2">
              <ReadOnlySetting
                label="Version"
                value={version?.version ?? "Unknown"}
              />
              <ReadOnlySetting
                label="Go"
                value={version?.goVersion ?? "Unknown"}
              />
              <ReadOnlySetting label="Wails" value="v3.0.0-alpha2.103" />
              <ReadOnlySetting label="Updates" value="Not checked" />
            </CardBody>
          </Card>
        ) : null}

        {section === "providers" ? (
          <Card>
            <CardHeader
              status={
                <Badge tone={activeProvider?.healthy ? "ok" : "warn"}>
                  {activeProvider?.healthy ? "Healthy" : "Needs checks"}
                </Badge>
              }
              title="Providers"
            />
            <CardBody className="space-y-4">
              <div className="rounded-card border border-border bg-bg-inset p-3">
                <div className="flex flex-wrap items-center gap-3">
                  <Server className="text-accent" size={18} />
                  <div className="min-w-0 flex-1">
                    <div className="font-medium text-text-primary">
                      {activeProvider?.name ?? "Windows WSL Ubuntu"}
                    </div>
                    <div className="truncate text-xs text-text-muted">
                      {providerKind}
                    </div>
                  </div>
                  <Button
                    icon={<RefreshCw size={15} />}
                    onClick={onDetect}
                    size="sm"
                    variant="secondary"
                  >
                    Detect again
                  </Button>
                  <Button
                    icon={<Wrench size={15} />}
                    onClick={onOpenSetup}
                    size="sm"
                  >
                    Set up new backend
                  </Button>
                </div>
                <div className="mt-3 grid gap-2 text-sm sm:grid-cols-4">
                  <StatusPill
                    label="Docker"
                    ok={Boolean(activeStatus?.dockerRunning)}
                    value={activeStatus?.dockerVersion || "-"}
                  />
                  <StatusPill
                    label="Compose"
                    ok={Boolean(activeStatus?.composeInstalled)}
                    value={activeStatus?.composeVersion || "-"}
                  />
                  <StatusPill
                    label="Buildx"
                    ok={Boolean(activeStatus?.buildxInstalled)}
                    value={activeStatus?.backendVersion || "-"}
                  />
                  <StatusPill
                    label="Context"
                    ok={Boolean(activeStatus?.currentContext)}
                    value={activeStatus?.currentContext || "default"}
                  />
                </div>
              </div>

              <div className="grid gap-2 sm:grid-cols-2">
                {providers.map((provider) => (
                  <div
                    className="rounded-card border border-border bg-bg-inset p-3"
                    key={provider.id}
                  >
                    <div className="flex items-center justify-between gap-3">
                      <div className="min-w-0">
                        <div className="truncate font-medium text-text-primary">
                          {provider.name}
                        </div>
                        <div className="truncate text-xs text-text-muted">
                          {provider.kind}
                        </div>
                      </div>
                      <div className="flex shrink-0 gap-2">
                        {provider.active ? (
                          <Badge tone="accent">active</Badge>
                        ) : null}
                        <Badge tone={provider.healthy ? "ok" : "warn"}>
                          {provider.healthy ? "healthy" : "needs checks"}
                        </Badge>
                      </div>
                    </div>
                  </div>
                ))}
                {providers.length === 0 ? (
                  <div className="rounded-card border border-border bg-bg-inset p-3 text-sm text-text-muted">
                    No providers configured.
                  </div>
                ) : null}
              </div>

              <section className="space-y-3">
                <div>
                  <h3 className="text-sm font-semibold text-text-primary">
                    Windows WSL
                  </h3>
                  <p className="mt-1 text-sm text-text-muted">
                    Active WSL provider settings save to Cairn settings and
                    rerun detection.
                  </p>
                </div>
                <div className="rounded-card border border-info/30 bg-info/10 px-3 py-2 text-sm text-info">
                  Providers are Cairn backends. The active provider decides
                  where Docker and Compose commands run. For Windows WSL, pick
                  the distro here; Docker contexts are separate Docker CLI
                  endpoints.
                </div>
                <label className="block">
                  <span className="text-xs font-medium uppercase text-text-muted">
                    WSL distro
                  </span>
                  <input
                    className="mt-1 h-9 w-full rounded-control border border-border bg-bg-inset px-3 text-sm text-text-primary outline-none"
                    list="wsl-distro-options"
                    onBlur={onSaveWSLDistro}
                    onChange={(event) => onWSLDistroChange(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === "Enter") {
                        onSaveWSLDistro();
                      }
                    }}
                    value={wslDistro}
                  />
                  <datalist id="wsl-distro-options">
                    {Array.from(
                      new Set([
                        wslDistro,
                        ...wslDistros.map((distro) => distro.name),
                        "Ubuntu",
                        "cairn-dev",
                      ]),
                    )
                      .filter(Boolean)
                      .map((name) => (
                        <option key={name} value={name} />
                      ))}
                  </datalist>
                </label>

                <div className="rounded-card border border-border bg-bg-inset">
                  <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-3 py-2">
                    <div>
                      <div className="text-sm font-medium text-text-primary">
                        Installed WSL distros
                      </div>
                      <div className="text-xs text-text-muted">
                        Docker Desktop internal distros are hidden.
                      </div>
                    </div>
                    <Button
                      icon={<RefreshCw size={15} />}
                      loading={wslDistrosLoading}
                      onClick={onRefreshWSLDistros}
                      size="sm"
                      variant="secondary"
                    >
                      Refresh
                    </Button>
                  </div>
                  <div className="p-3">
                    {wslDistrosError ? (
                      <div className="rounded-control border border-error/30 bg-error/10 px-3 py-2 text-sm text-error">
                        {wslDistrosError}
                      </div>
                    ) : null}
                    {wslDistrosLoading && wslDistros.length === 0 ? (
                      <TableSkeleton />
                    ) : null}
                    {!wslDistrosLoading && wslDistros.length === 0 ? (
                      <EmptyState
                        body="Installed WSL distributions appear here when wsl.exe is available."
                        icon={<Server size={28} />}
                        title="No WSL distros detected"
                      />
                    ) : null}
                    {wslDistros.length > 0 ? (
                      <div className="grid gap-2 md:grid-cols-2">
                        {wslDistros.map((distro) => {
                          const selected =
                            distro.name.toLowerCase() ===
                            wslDistro.trim().toLowerCase();
                          return (
                            <div
                              className="rounded-control border border-border bg-bg-card p-3"
                              key={distro.name}
                            >
                              <div className="flex items-start justify-between gap-3">
                                <div className="min-w-0">
                                  <div className="truncate font-medium text-text-primary">
                                    {distro.name}
                                  </div>
                                  <div className="mt-1 flex flex-wrap gap-2">
                                    {selected ? (
                                      <Badge tone="accent">selected</Badge>
                                    ) : null}
                                    {distro.default ? (
                                      <Badge tone="neutral">default</Badge>
                                    ) : null}
                                    <Badge
                                      tone={
                                        distro.version === 2 ? "ok" : "warn"
                                      }
                                    >
                                      WSL {distro.version || "?"}
                                    </Badge>
                                    {distro.state ? (
                                      <Badge tone="neutral">
                                        {distro.state}
                                      </Badge>
                                    ) : null}
                                  </div>
                                </div>
                                <Button
                                  disabled={selected || saving}
                                  onClick={() => onUseWSLDistro(distro.name)}
                                  size="sm"
                                  variant={selected ? "secondary" : "primary"}
                                >
                                  Use distro
                                </Button>
                              </div>
                            </div>
                          );
                        })}
                      </div>
                    ) : null}
                  </div>
                </div>

                <label className="flex items-center justify-between gap-3 rounded-card border border-border bg-bg-inset p-3 text-sm">
                  <span>
                    <span className="block font-medium text-text-primary">
                      Start Docker backend on app launch
                    </span>
                    <span className="mt-1 block text-text-muted">
                      Current setting:{" "}
                      {String(
                        settings["provider.autostart_backend"] ??
                          autostartBackend,
                      )}
                    </span>
                  </span>
                  <input
                    checked={autostartBackend}
                    disabled={saving}
                    onChange={(event) =>
                      onAutostartChange(event.target.checked)
                    }
                    type="checkbox"
                  />
                </label>

                <div className="rounded-card border border-info/30 bg-info/10 p-3 text-sm text-info">
                  <div className="font-medium">Path mapping</div>
                  <div className="mt-2 grid gap-1 font-mono text-xs">
                    <span>
                      {"C:\\Users\\Ada\\project -> /mnt/c/Users/Ada/project"}
                    </span>
                    <span>
                      {"\\\\wsl$\\" +
                        (wslDistro || "Ubuntu") +
                        "\\home\\ada\\project -> /home/ada/project"}
                    </span>
                  </div>
                </div>
                <PathRecommendation />
              </section>

              <section className="space-y-3 border-t border-border pt-4">
                <div>
                  <h3 className="text-sm font-semibold text-text-primary">
                    Linux native
                  </h3>
                  <p className="mt-1 text-sm text-text-muted">
                    Native Docker socket and permission settings apply before
                    the next provider detection.
                  </p>
                </div>
                <SettingsTextSetting
                  disabled={saving}
                  label="Socket path"
                  onSave={(value) =>
                    onSettingChange(
                      "linux.socket_path",
                      value.trim() || "/var/run/docker.sock",
                    )
                  }
                  value={settingString(
                    settings,
                    "linux.socket_path",
                    "/var/run/docker.sock",
                  )}
                />
                <SettingsSelectField
                  disabled={saving}
                  label="Permission mode"
                  onChange={(value) =>
                    onSettingChange("linux.sudo_mode", value)
                  }
                  options={[
                    ["ask", "Ask each time"],
                    ["group", "Docker group"],
                    ["rootless", "Rootless Docker"],
                  ]}
                  value={settingString(settings, "linux.sudo_mode", "ask")}
                />
                <ReadOnlySetting
                  label="Systemd service"
                  value={
                    activeStatus?.dockerRunning ? "Docker running" : "Pending"
                  }
                />
              </section>

              <section className="space-y-3 border-t border-border pt-4">
                <div>
                  <h3 className="text-sm font-semibold text-text-primary">
                    macOS Colima
                  </h3>
                  <p className="mt-1 text-sm text-text-muted">
                    Resource changes require a Colima restart before they affect
                    the VM.
                  </p>
                </div>
                <div className="grid gap-3 sm:grid-cols-4">
                  <label className="block">
                    <span className="text-xs font-medium uppercase text-text-muted">
                      Profile
                    </span>
                    <input
                      className="mt-1 h-9 w-full rounded-control border border-border bg-bg-inset px-3 text-sm text-text-primary outline-none"
                      onBlur={onSaveColimaProfile}
                      onChange={(event) =>
                        onColimaProfileChange(event.target.value)
                      }
                      onKeyDown={(event) => {
                        if (event.key === "Enter") {
                          onSaveColimaProfile();
                        }
                      }}
                      value={colimaProfile}
                    />
                  </label>
                  <SettingsNumberField
                    label="CPU"
                    onBlur={onSaveColimaCPU}
                    onChange={onColimaCPUChange}
                    value={colimaCPU}
                  />
                  <SettingsNumberField
                    label="RAM GB"
                    onBlur={onSaveColimaMemoryGB}
                    onChange={onColimaMemoryGBChange}
                    value={colimaMemoryGB}
                  />
                  <SettingsNumberField
                    label="Disk GB"
                    onBlur={onSaveColimaDiskGB}
                    onChange={onColimaDiskGBChange}
                    value={colimaDiskGB}
                  />
                </div>
                <ColimaPathRecommendation />
              </section>
            </CardBody>
          </Card>
        ) : null}

        {section === "contexts" ? (
          <Card>
            <CardHeader
              status={
                <Button
                  icon={<RefreshCw size={15} />}
                  loading={dockerContextsLoading}
                  onClick={onRefreshDockerContexts}
                  size="sm"
                  variant="secondary"
                >
                  Refresh
                </Button>
              }
              title="Docker Contexts"
            />
            <CardBody className="space-y-3">
              <div className="rounded-card border border-info/30 bg-info/10 px-3 py-2 text-sm text-info">
                Docker contexts are Docker CLI endpoints from `docker context
                ls`. They are not WSL distros. Choosing one switches Cairn to an
                existing Docker context provider; choosing a WSL distro happens
                in Providers.
              </div>
              {dockerContextsError ? (
                <div className="rounded-card border border-error/30 bg-error/10 px-3 py-2 text-sm text-error">
                  {dockerContextsError}
                </div>
              ) : null}
              {dockerContextsLoading && dockerContexts.length === 0 ? (
                <TableSkeleton />
              ) : null}
              {!dockerContextsLoading && dockerContexts.length === 0 ? (
                <EmptyState
                  body="Detected Docker contexts appear here when the Docker CLI is available."
                  icon={<Terminal size={28} />}
                  title="No Docker contexts"
                />
              ) : null}
              {dockerContexts.length > 0 ? (
                <DockerContextsTable
                  contexts={dockerContexts}
                  onUse={onUseDockerContext}
                  saving={saving}
                />
              ) : null}
            </CardBody>
          </Card>
        ) : null}

        {section === "registries" ? (
          <Card>
            <CardHeader
              actions={
                <div className="flex gap-2">
                  <Button
                    icon={<RefreshCw size={15} />}
                    loading={registryAccountsLoading}
                    onClick={onRefreshRegistries}
                    size="sm"
                    variant="secondary"
                  >
                    Refresh
                  </Button>
                  <Button
                    disabled={registryLoginDisabled}
                    disabledReason={registryLoginDisabledReason}
                    icon={<LogIn size={15} />}
                    onClick={() => onRegistryLogin("docker.io")}
                    size="sm"
                  >
                    Log in
                  </Button>
                </div>
              }
              title="Registries"
            />
            <CardBody className="space-y-3">
              <div className="text-sm text-text-muted">
                Cairn sends secrets to `docker login` via stdin. Docker stores
                them with a backend credential helper when one is available;
                otherwise Docker may use config.json.
              </div>
              <SettingsSelectField
                disabled={saving}
                label="Credential mode"
                onChange={(value) =>
                  onSettingChange("registry.credentials_mode", value)
                }
                options={[
                  ["docker_helper", "Prefer Docker credential helper"],
                  ["none", "No Cairn-managed credentials"],
                ]}
                value={settingString(
                  settings,
                  "registry.credentials_mode",
                  "docker_helper",
                )}
              />
              {registryCredentialMode === "docker_helper" &&
              hasUnencryptedRegistryCredentials ? (
                <div className="rounded-card border border-error/30 bg-error/10 px-3 py-2 text-sm text-error">
                  Some registry credentials are still stored in Docker
                  config.json because no credential helper handled them.
                </div>
              ) : null}
              {registryLoginDisabled ? (
                <div className="rounded-card border border-info/30 bg-info/10 px-3 py-2 text-sm text-info">
                  Cairn will not accept registry secrets while this mode is
                  selected.
                </div>
              ) : null}
              {registryAccountsError ? (
                <div className="rounded-card border border-error/30 bg-error/10 px-3 py-2 text-sm text-error">
                  {registryAccountsError}
                </div>
              ) : null}
              {registryAccountsLoading && registryAccounts.length === 0 ? (
                <TableSkeleton />
              ) : registryAccounts.length === 0 ? (
                <EmptyState
                  body="Log in to a registry before pushing private images."
                  icon={<KeyRound size={28} />}
                  title="No registry accounts"
                />
              ) : (
                <RegistryAccountsTable
                  accounts={registryAccounts}
                  busyKeys={registryBusyKeys}
                  loginDisabled={registryLoginDisabled}
                  loginDisabledReason={registryLoginDisabledReason}
                  onLogin={onRegistryLogin}
                  onLogout={onRegistryLogout}
                  onTest={onRegistryTest}
                  statuses={registryStatuses}
                />
              )}
            </CardBody>
          </Card>
        ) : null}
      </div>
    </div>
  );
}

function SettingsNumberField({
  label,
  onBlur,
  onChange,
  value,
}: {
  label: string;
  onBlur: () => void;
  onChange: (value: number) => void;
  value: number;
}) {
  return (
    <label className="block">
      <span className="text-xs font-medium uppercase text-text-muted">
        {label}
      </span>
      <input
        className="mt-1 h-9 w-full rounded-control border border-border bg-bg-inset px-3 text-sm text-text-primary outline-none"
        min={1}
        onBlur={onBlur}
        onChange={(event) => onChange(Number(event.target.value))}
        onKeyDown={(event) => {
          if (event.key === "Enter") {
            onBlur();
          }
        }}
        type="number"
        value={value}
      />
    </label>
  );
}

function SettingsSelectField({
  disabled,
  label,
  onChange,
  options,
  value,
}: {
  disabled?: boolean;
  label: string;
  onChange: (value: string) => void;
  options: Array<[string, string]>;
  value: string;
}) {
  return (
    <label className="block">
      <span className="text-xs font-medium uppercase text-text-muted">
        {label}
      </span>
      <select
        className="mt-1 h-9 w-full rounded-control border border-border bg-bg-inset px-3 text-sm text-text-primary outline-none"
        disabled={disabled}
        onChange={(event) => onChange(event.target.value)}
        value={value}
      >
        {options.map(([id, name]) => (
          <option key={id} value={id}>
            {name}
          </option>
        ))}
      </select>
    </label>
  );
}

function SettingsCheckboxField({
  checked,
  disabled,
  label,
  onChange,
}: {
  checked: boolean;
  disabled?: boolean;
  label: string;
  onChange?: (checked: boolean) => void;
}) {
  return (
    <label className="flex items-center justify-between gap-3 rounded-card border border-border bg-bg-inset p-3 text-sm">
      <span className="font-medium text-text-primary">{label}</span>
      <input
        checked={checked}
        disabled={disabled}
        onChange={(event) => onChange?.(event.target.checked)}
        type="checkbox"
      />
    </label>
  );
}

function SettingsTextSetting({
  disabled,
  label,
  onSave,
  placeholder,
  value,
}: {
  disabled?: boolean;
  label: string;
  onSave: (value: string) => void;
  placeholder?: string;
  value: string;
}) {
  const save = (input: HTMLInputElement) => onSave(input.value);
  return (
    <label className="block">
      <span className="text-xs font-medium uppercase text-text-muted">
        {label}
      </span>
      <input
        className="mt-1 h-9 w-full rounded-control border border-border bg-bg-inset px-3 text-sm text-text-primary outline-none"
        defaultValue={value}
        disabled={disabled}
        key={value}
        onBlur={(event) => save(event.currentTarget)}
        onKeyDown={(event) => {
          if (event.key === "Enter") {
            save(event.currentTarget);
          }
        }}
        placeholder={placeholder}
      />
    </label>
  );
}

function SettingsNumberSetting({
  disabled,
  label,
  max,
  min,
  onSave,
  value,
}: {
  disabled?: boolean;
  label: string;
  max?: number;
  min?: number;
  onSave: (value: number) => void;
  value: number;
}) {
  const save = (input: HTMLInputElement) => {
    const parsed = Number(input.value);
    if (!Number.isFinite(parsed)) {
      input.value = String(value);
      return;
    }
    const lowerBounded = min === undefined ? parsed : Math.max(min, parsed);
    const nextValue =
      max === undefined ? lowerBounded : Math.min(max, lowerBounded);
    input.value = String(nextValue);
    onSave(nextValue);
  };
  return (
    <label className="block">
      <span className="text-xs font-medium uppercase text-text-muted">
        {label}
      </span>
      <input
        className="mt-1 h-9 w-full rounded-control border border-border bg-bg-inset px-3 text-sm text-text-primary outline-none"
        defaultValue={String(value)}
        disabled={disabled}
        key={value}
        max={max}
        min={min}
        onBlur={(event) => save(event.currentTarget)}
        onKeyDown={(event) => {
          if (event.key === "Enter") {
            save(event.currentTarget);
          }
        }}
        type="number"
      />
    </label>
  );
}

function ReadOnlySetting({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-card border border-border bg-bg-inset p-3 text-sm">
      <div className="text-xs font-medium uppercase text-text-muted">
        {label}
      </div>
      <div className="mt-1 text-text-primary">{value}</div>
    </div>
  );
}

function AuditLogTable({
  entries,
  onSelect,
}: {
  entries: AuditEntry[];
  onSelect: (entry: AuditEntry) => void;
}) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[760px] border-separate border-spacing-0 text-sm">
        <thead>
          <tr className="text-left text-xs uppercase text-text-muted">
            <th className="border-b border-border px-3 py-2">Time</th>
            <th className="border-b border-border px-3 py-2">Action</th>
            <th className="border-b border-border px-3 py-2">Target</th>
            <th className="border-b border-border px-3 py-2">Risk</th>
            <th className="border-b border-border px-3 py-2">Status</th>
            <th className="border-b border-border px-3 py-2">Duration</th>
            <th className="border-b border-border px-3 py-2 text-right">
              Details
            </th>
          </tr>
        </thead>
        <tbody>
          {entries.map((entry) => (
            <tr key={entry.id}>
              <td className="border-b border-border/70 px-3 py-2 text-text-secondary">
                {formatAuditTime(entry.ts)}
              </td>
              <td className="border-b border-border/70 px-3 py-2 font-medium text-text-primary">
                {entry.action}
              </td>
              <td className="border-b border-border/70 px-3 py-2">
                <div className="max-w-[220px] truncate text-text-secondary">
                  {entry.target || "-"}
                </div>
              </td>
              <td className="border-b border-border/70 px-3 py-2">
                <Badge tone={riskTone(auditMetadataString(entry, "risk"))}>
                  {auditMetadataString(entry, "risk") || "unknown"}
                </Badge>
              </td>
              <td className="border-b border-border/70 px-3 py-2">
                <Badge tone={auditStatusTone(entry.result)}>
                  {entry.result || "unknown"}
                </Badge>
              </td>
              <td className="border-b border-border/70 px-3 py-2 text-text-secondary">
                {formatDurationMS(auditMetadataNumber(entry, "durationMS"))}
              </td>
              <td className="border-b border-border/70 px-3 py-2 text-right">
                <Button
                  aria-label={`View audit ${entry.action}`}
                  onClick={() => onSelect(entry)}
                  size="sm"
                  variant="secondary"
                >
                  View
                </Button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function AuditEntryModal({
  entry,
  onClose,
}: {
  entry: AuditEntry | null;
  onClose: () => void;
}) {
  const command = entry ? auditMetadataString(entry, "command") : "";
  return (
    <Modal onClose={onClose} open={Boolean(entry)} size="lg" title="Audit row">
      {entry ? (
        <div className="space-y-4">
          <div className="grid gap-3 sm:grid-cols-2">
            <ReadOnlySetting label="Action" value={entry.action || "-"} />
            <ReadOnlySetting label="Status" value={entry.result || "-"} />
            <ReadOnlySetting label="Target" value={entry.target || "-"} />
            <ReadOnlySetting
              label="Target type"
              value={auditMetadataString(entry, "targetType") || "-"}
            />
            <ReadOnlySetting
              label="Project"
              value={auditMetadataString(entry, "projectID") || "-"}
            />
            <ReadOnlySetting
              label="Provider"
              value={auditMetadataString(entry, "providerID") || "-"}
            />
            <ReadOnlySetting
              label="Duration"
              value={formatDurationMS(auditMetadataNumber(entry, "durationMS"))}
            />
            <ReadOnlySetting label="Time" value={formatAuditTime(entry.ts)} />
          </div>
          <div>
            <div className="text-xs font-medium uppercase text-text-muted">
              Command
            </div>
            <pre className="mt-1 max-h-44 overflow-auto rounded-card border border-border bg-bg-inset p-3 font-mono text-xs text-text-primary">
              {command || "No command recorded"}
            </pre>
          </div>
          {entry.error ? (
            <div className="rounded-card border border-error/30 bg-error/10 p-3 text-error">
              {entry.error}
            </div>
          ) : null}
        </div>
      ) : null}
    </Modal>
  );
}

function exportAuditCSV(entries: AuditEntry[]) {
  const header = [
    "time",
    "action",
    "target",
    "risk",
    "status",
    "duration_ms",
    "project_id",
    "provider_id",
    "command",
    "error",
  ];
  const rows = entries.map((entry) => [
    formatAuditTime(entry.ts),
    entry.action,
    entry.target || "",
    auditMetadataString(entry, "risk"),
    entry.result,
    String(auditMetadataNumber(entry, "durationMS") ?? ""),
    auditMetadataString(entry, "projectID"),
    auditMetadataString(entry, "providerID"),
    auditMetadataString(entry, "command"),
    entry.error || "",
  ]);
  const csv = [header, ...rows]
    .map((row) => row.map(csvCell).join(","))
    .join("\n");
  const blob = new Blob([csv], { type: "text/csv;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = `cairn-audit-${new Date().toISOString().slice(0, 10)}.csv`;
  anchor.click();
  URL.revokeObjectURL(url);
}

export function csvCell(value: string) {
  const safeValue = /^[=+\-@\t\r]/.test(value) ? `'${value}` : value;
  return `"${safeValue.replace(/"/g, '""')}"`;
}

export function auditMetadataString(entry: AuditEntry, key: string) {
  const value = entry.metadata?.[key];
  if (typeof value === "string") {
    return value;
  }
  if (typeof value === "number" || typeof value === "boolean") {
    return String(value);
  }
  return "";
}

function auditMetadataNumber(entry: AuditEntry, key: string) {
  const value = entry.metadata?.[key];
  if (typeof value === "number" && Number.isFinite(value)) {
    return value;
  }
  if (typeof value === "string") {
    const parsed = Number(value);
    return Number.isFinite(parsed) ? parsed : null;
  }
  return null;
}

function auditStatusTone(status: string): BadgeTone {
  switch (status) {
    case "success":
      return "ok";
    case "failed":
      return "error";
    case "cancelled":
      return "warn";
    case "started":
      return "info";
    default:
      return "neutral";
  }
}

function formatDurationMS(value: number | null) {
  if (value === null) {
    return "-";
  }
  if (value >= 1000) {
    return `${(value / 1000).toFixed(value >= 10000 ? 0 : 1)} s`;
  }
  return `${value} ms`;
}

function formatAuditTime(value: unknown) {
  const millis = dateMillis(value);
  return millis ? new Date(millis).toLocaleString() : "-";
}
