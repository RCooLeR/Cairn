import { useCallback, useEffect, useState } from "react";
import { Network, Power } from "lucide-react";
import { Events } from "@wailsio/runtime";

import type {
  PortForward,
  PortForwardStatus,
} from "../../../bindings/github.com/RCooLeR/Cairn/internal/models/models.js";
import { PortForwardService } from "../../api/services";
import { Badge, Button, Card, CardBody, CardHeader, EmptyState } from "../ui";

function bindLabel(bindAddr: string): string {
  if (bindAddr === "0.0.0.0") {
    return "0.0.0.0 (LAN)";
  }
  if (bindAddr === "127.0.0.1") {
    return "127.0.0.1 (local)";
  }
  return bindAddr;
}

function forwardRowKey(forward: PortForward): string {
  return `${forward.protocol}/${forward.hostPort}`;
}

export function PortForwardingPanel() {
  const [status, setStatus] = useState<PortForwardStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    try {
      setStatus(await PortForwardService.GetStatus());
      setError(null);
    } catch (err) {
      setError(
        err instanceof Error
          ? err.message
          : "Unable to load port forwarding status",
      );
    }
  }, []);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      void refresh();
    }, 0);
    const off = Events.On("portforward:changed", () => {
      void refresh();
    });
    const offProvider = Events.On("provider:changed", () => {
      void refresh();
    });
    const offDocker = Events.On("docker:connected", () => {
      void refresh();
    });
    return () => {
      window.clearTimeout(timer);
      off();
      offProvider();
      offDocker();
    };
  }, [refresh]);

  // Only the WSL backend forwards host ports; native/Colima bind them directly.
  if (!status?.supported) {
    return null;
  }

  const forwards = status.forwards ?? [];
  const toggle = async () => {
    setBusy(true);
    setError(null);
    try {
      await PortForwardService.SetEnabled(!status.enabled);
      await refresh();
    } catch (err) {
      setError(
        err instanceof Error ? err.message : "Unable to change port forwarding",
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card>
      <CardHeader
        status={
          <Badge tone={status.enabled ? "ok" : "neutral"}>
            {status.enabled ? "On" : "Off"}
          </Badge>
        }
        title="Host port forwarding"
      />
      <CardBody className="space-y-3">
        <p className="text-sm text-text-muted">
          Binds published container ports on the Windows host and proxies them
          into WSL, so <code>-p</code> behaves like Docker Desktop. Ports a
          container publishes on <code>0.0.0.0</code> are mirrored to the
          Windows LAN interface; loopback-only publishes use WSL localhost.
        </p>
        <div className="flex items-center gap-3">
          <Button
            icon={<Power size={15} />}
            loading={busy}
            onClick={() => {
              void toggle();
            }}
            size="sm"
            variant={status.enabled ? "secondary" : "primary"}
          >
            {status.enabled ? "Disable forwarding" : "Enable forwarding"}
          </Button>
        </div>
        {error ? (
          <div className="rounded-card border border-error/30 bg-error/10 px-3 py-2 text-sm text-error">
            {error}
          </div>
        ) : null}
        {forwards.length === 0 ? (
          <EmptyState
            body={
              status.enabled
                ? "Publish a container port with -p and it will appear here."
                : "Forwarding is off, so no host ports are bound."
            }
            icon={<Network size={28} />}
            title="No forwarded ports"
          />
        ) : (
          <div className="overflow-hidden rounded-card border border-border">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-border bg-bg-inset text-left text-xs uppercase text-text-muted">
                  <th className="px-3 py-2 font-medium">Host port</th>
                  <th className="px-3 py-2 font-medium">Protocol</th>
                  <th className="px-3 py-2 font-medium">Bind</th>
                  <th className="px-3 py-2 font-medium">Container</th>
                  <th className="px-3 py-2 font-medium">Status</th>
                </tr>
              </thead>
              <tbody>
                {forwards.map((forward) => (
                  <tr
                    className="border-b border-border last:border-0"
                    key={forwardRowKey(forward)}
                  >
                    <td className="px-3 py-2 font-mono text-text-primary">
                      {forward.hostPort}
                    </td>
                    <td className="px-3 py-2 uppercase text-text-secondary">
                      {forward.protocol}
                    </td>
                    <td className="px-3 py-2 text-text-secondary">
                      {bindLabel(forward.bindAddr)}
                    </td>
                    <td className="truncate px-3 py-2 text-text-secondary">
                      {forward.containerName || "-"}
                    </td>
                    <td className="px-3 py-2">
                      {forward.status === "active" ? (
                        <Badge tone="ok">active</Badge>
                      ) : (
                        <Badge tone="error">{forward.reason || "error"}</Badge>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </CardBody>
    </Card>
  );
}
