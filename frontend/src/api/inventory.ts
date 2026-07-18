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
  const providerResult = await settle(ProviderService.ListProviders());
  const info = await settle(DockerService.Info());
  const version = await settle(DockerService.Version());
  const diskUsage = await settle(DockerService.DiskUsage());
  const containers = await settle(DockerService.ListContainers({ all: true }));
  const images = await settle(DockerService.ListImages());
  const volumes = await settle(DockerService.ListVolumes());
  const networks = await settle(DockerService.ListNetworks());

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
