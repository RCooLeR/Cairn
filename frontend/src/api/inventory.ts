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
    degradedReason: firstError([
      providerResult,
      info,
      version,
      diskUsage,
      containers,
      images,
      volumes,
      networks,
    ]),
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

function firstError(results: Array<Settled<unknown>>): string | null {
  const rejected = results.find(
    (result): result is PromiseRejectedResult => result.status === "rejected",
  );
  if (!rejected) {
    return null;
  }
  const reason = rejected.reason;
  if (reason instanceof Error && reason.message) {
    return reason.message;
  }
  if (typeof reason === "string") {
    return reason;
  }
  return "Docker is not reachable";
}
