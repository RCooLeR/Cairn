import { CheckCircle2, LogIn, LogOut, ShieldAlert } from "lucide-react";

import type {
  DockerContextInfo,
  RegistryAccount,
  RegistryAuthStatus,
} from "../../../bindings/github.com/RCooLeR/Cairn/internal/models/models.js";
import {
  normalizeRegistryHostForUI,
  registryStorageLabel,
} from "../../settings/registryUi";
import { Badge, Button, Tooltip } from "../ui";

type DockerContextsTableProps = {
  contexts: DockerContextInfo[];
  onUse: (name: string) => void;
  saving: boolean;
};

export function DockerContextsTable({
  contexts,
  onUse,
  saving,
}: DockerContextsTableProps) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[620px] border-separate border-spacing-0 text-sm">
        <thead>
          <tr className="text-left text-xs uppercase text-text-muted">
            <th className="border-b border-border px-3 py-2">Name</th>
            <th className="border-b border-border px-3 py-2">Host</th>
            <th className="border-b border-border px-3 py-2">Current</th>
            <th className="border-b border-border px-3 py-2 text-right">
              Action
            </th>
          </tr>
        </thead>
        <tbody>
          {contexts.map((context) => {
            const insecure = isUnencryptedDockerHost(context.dockerHost);
            return (
              <tr key={context.name}>
                <td className="border-b border-border/70 px-3 py-2 font-medium text-text-primary">
                  {context.name}
                  {context.description ? (
                    <div className="mt-1 text-xs font-normal text-text-muted">
                      {context.description}
                    </div>
                  ) : null}
                </td>
                <td className="border-b border-border/70 px-3 py-2">
                  <div className="max-w-[280px] truncate font-mono text-xs text-text-secondary">
                    {context.dockerHost || "-"}
                  </div>
                  {insecure ? (
                    <Badge tone="error">unencrypted tcp://</Badge>
                  ) : null}
                </td>
                <td className="border-b border-border/70 px-3 py-2">
                  {context.current ? (
                    <Badge tone="ok">current</Badge>
                  ) : (
                    <Badge tone="neutral">available</Badge>
                  )}
                </td>
                <td className="border-b border-border/70 px-3 py-2 text-right">
                  <Button
                    disabled={saving}
                    icon={<CheckCircle2 size={15} />}
                    onClick={() => onUse(context.name)}
                    size="sm"
                    variant={context.current ? "secondary" : "primary"}
                  >
                    Use this context
                  </Button>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

type RegistryAccountsTableProps = {
  accounts: RegistryAccount[];
  busyKeys: Set<string>;
  loginDisabled?: boolean;
  loginDisabledReason?: string;
  statuses: Record<string, RegistryAuthStatus>;
  onLogin: (registry?: string) => void;
  onLogout: (registry: string) => void;
  onTest: (registry: string) => void;
};

export function RegistryAccountsTable({
  accounts,
  busyKeys,
  loginDisabled,
  loginDisabledReason,
  onLogin,
  onLogout,
  onTest,
  statuses,
}: RegistryAccountsTableProps) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[720px] border-separate border-spacing-0 text-sm">
        <thead>
          <tr className="text-left text-xs uppercase text-text-muted">
            <th className="border-b border-border px-3 py-2">Registry</th>
            <th className="border-b border-border px-3 py-2">Username</th>
            <th className="border-b border-border px-3 py-2">Storage</th>
            <th className="border-b border-border px-3 py-2">Status</th>
            <th className="border-b border-border px-3 py-2 text-right">
              Actions
            </th>
          </tr>
        </thead>
        <tbody>
          {accounts.map((account) => {
            const registry = normalizeRegistryHostForUI(account.registry);
            const status = statuses[registry];
            return (
              <tr key={`${registry}:${account.username ?? ""}`}>
                <td className="border-b border-border/70 px-3 py-2 font-medium text-text-primary">
                  {registry}
                </td>
                <td className="border-b border-border/70 px-3 py-2">
                  {account.username || "-"}
                </td>
                <td className="border-b border-border/70 px-3 py-2">
                  <Badge tone={account.source === "authsFile" ? "error" : "ok"}>
                    {registryStorageLabel(account)}
                  </Badge>
                </td>
                <td className="border-b border-border/70 px-3 py-2">
                  <RegistryStatusBadge account={account} status={status} />
                </td>
                <td className="border-b border-border/70 px-3 py-2">
                  <div className="flex justify-end gap-1">
                    <Tooltip label="Test auth">
                      <Button
                        aria-label={`Test ${registry}`}
                        icon={<ShieldAlert size={15} />}
                        loading={busyKeys.has(`test:${registry}`)}
                        onClick={() => onTest(registry)}
                        size="icon"
                        variant="ghost"
                      />
                    </Tooltip>
                    <Tooltip label="Log in">
                      <Button
                        aria-label={`Log in ${registry}`}
                        disabled={loginDisabled}
                        disabledReason={loginDisabledReason}
                        icon={<LogIn size={15} />}
                        onClick={() => onLogin(registry)}
                        size="icon"
                        variant="ghost"
                      />
                    </Tooltip>
                    <Tooltip label="Log out">
                      <Button
                        aria-label={`Log out ${registry}`}
                        icon={<LogOut size={15} />}
                        loading={busyKeys.has(`logout:${registry}`)}
                        onClick={() => onLogout(registry)}
                        size="icon"
                        variant="ghost"
                      />
                    </Tooltip>
                  </div>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function RegistryStatusBadge({
  account,
  status,
}: {
  account: RegistryAccount;
  status?: RegistryAuthStatus;
}) {
  if (status?.error) {
    return <Badge tone="error">Auth failed</Badge>;
  }
  if (status?.loggedIn) {
    return <Badge tone="ok">Verified</Badge>;
  }
  return <Badge tone={account.loggedIn ? "warn" : "neutral"}>Unverified</Badge>;
}

function isUnencryptedDockerHost(host?: string) {
  return String(host ?? "")
    .trim()
    .toLowerCase()
    .startsWith("tcp://");
}
