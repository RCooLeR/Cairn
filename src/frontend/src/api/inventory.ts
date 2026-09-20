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

import { DockerService, ProviderService } from "./services";
import { boundedRead } from "./boundedRead";

export const INVENTORY_SNAPSHOT_TIMEOUT_MS = 60_000;
export const INVENTORY_PROVIDER_TIMEOUT_MS = 35_000;
export const INVENTORY_SLICE_TIMEOUT_MS = 10_000;

export const inventorySliceNames = [
  "providers",
  "dockerInfo",
  "dockerVersion",
  "diskUsage",
  "containers",
  "images",
  "volumes",
  "networks",
] as const;

export type InventorySliceName = (typeof inventorySliceNames)[number];

export type InventorySnapshot = {
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
  /**
   * Named failures let consumers distinguish an unavailable slice from a
   * legitimate empty/null response. Optional for compatibility with snapshots
   * produced by older callers and test fixtures.
   */
  failures?: Partial<Record<InventorySliceName, string>>;
  degradedReason: string | null;
};

type Settled<T> = PromiseSettledResult<T>;

export async function getInventorySnapshot(): Promise<InventorySnapshot> {
  const deadline = Date.now() + INVENTORY_SNAPSHOT_TIMEOUT_MS;
  const read = <T>(
    load: () => Promise<T>,
    label: string,
    timeoutMS = INVENTORY_SLICE_TIMEOUT_MS,
  ) =>
    settle(
      boundedRead(load, label, Math.min(timeoutMS, deadline - Date.now())),
    );
  // Keep requests sequential to reuse the WSL stdio transport, while bounding
  // both an individual read and the whole snapshot. A stalled slice must not
  // permanently block refreshFresh or leave the Start action spinning.
  const providerResult = await read(
    () => ProviderService.ListProviders(),
    "Provider discovery",
    INVENTORY_PROVIDER_TIMEOUT_MS,
  );
  const info = await read(() => DockerService.Info(), "Docker information");
  const version = await read(() => DockerService.Version(), "Docker version");
  const diskUsage = await read(() => DockerService.DiskUsage(), "Disk usage");
  const containers = await read(
    () => DockerService.ListContainers({ all: true }),
    "Container list",
  );
  const images = await read(() => DockerService.ListImages(), "Image list");
  const volumes = await read(() => DockerService.ListVolumes(), "Volume list");
  const networks = await read(
    () => DockerService.ListNetworks(),
    "Network list",
  );

  const volumeSummaries = valueOr(volumes, []);
  const networkSummaries = valueOr(networks, []);
  const failures = collectFailures({
    providers: providerResult,
    dockerInfo: info,
    dockerVersion: version,
    diskUsage,
    containers,
    images,
    volumes,
    networks,
  });

  return {
    providers: valueOr(providerResult, []),
    dockerInfo: valueOr(info, null),
    dockerVersion: valueOr(version, null),
    diskUsage: valueOr(diskUsage, null),
    containers: valueOr(containers, []),
    images: valueOr(images, []),
    volumes: volumeSummaries,
    networks: networkSummaries,
    volumeDetails: {},
    networkDetails: {},
    failures,
    degradedReason: formatFailures(failures),
  };
}

async function settle<T>(promise: Promise<T>): Promise<Settled<T>> {
  try {
    return { status: "fulfilled", value: await promise };
  } catch (reason) {
    return { status: "rejected", reason };
  }
}

function valueOr<T>(result: Settled<T>, fallback: T): T {
  return result.status === "fulfilled" ? result.value : fallback;
}

function collectFailures(
  results: Record<InventorySliceName, Settled<unknown>>,
): Partial<Record<InventorySliceName, string>> {
  const failures: Partial<Record<InventorySliceName, string>> = {};
  for (const slice of inventorySliceNames) {
    const result = results[slice];
    if (result.status === "rejected") {
      failures[slice] = errorMessage(result.reason);
    }
  }
  return failures;
}

function errorMessage(reason: unknown): string {
  const message =
    reason instanceof Error
      ? reason.message.trim()
      : typeof reason === "string"
        ? reason.trim()
        : "";
  return message || "Docker is not reachable";
}

function formatFailures(
  failures: Partial<Record<InventorySliceName, string>>,
): string | null {
  const messages = inventorySliceNames.flatMap((slice) => {
    const message = failures[slice];
    return message ? [`${slice}: ${message}`] : [];
  });
  return messages.length > 0 ? messages.join("; ") : null;
}
