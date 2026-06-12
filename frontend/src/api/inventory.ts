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
} from '../../bindings/github.com/RCooLeR/Cairn/internal/models/models.js';

import { DockerService, ProviderService } from './services';

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
  const providers = await ProviderService.ListProviders().catch(() => []);
  const [info, version, diskUsage, containers, images, volumes, networks] = await Promise.allSettled([
    DockerService.Info(),
    DockerService.Version(),
    DockerService.DiskUsage(),
    DockerService.ListContainers({ all: true }),
    DockerService.ListImages(),
    DockerService.ListVolumes(),
    DockerService.ListNetworks(),
  ]);

  const volumeSummaries = valueOr(volumes, []);
  const networkSummaries = valueOr(networks, []);

  const [volumeDetails, networkDetails] = await Promise.all([
    loadVolumeDetails(volumeSummaries),
    loadNetworkDetails(networkSummaries),
  ]);

  return {
    providers,
    dockerInfo: valueOr(info, null),
    dockerVersion: valueOr(version, null),
    diskUsage: valueOr(diskUsage, null),
    containers: valueOr(containers, []),
    images: valueOr(images, []),
    volumes: volumeSummaries,
    networks: networkSummaries,
    volumeDetails,
    networkDetails,
    degradedReason: firstError([info, version, diskUsage, containers, images, volumes, networks]),
  };
}

async function loadVolumeDetails(volumes: VolumeSummary[]): Promise<Record<string, VolumeDetail>> {
  const entries = await Promise.allSettled(
    volumes.map(async (volume) => [volume.name, await DockerService.GetVolume(volume.name)] as const),
  );
  return Object.fromEntries(
    entries
      .filter((entry): entry is PromiseFulfilledResult<readonly [string, VolumeDetail | null]> => entry.status === 'fulfilled')
      .filter((entry) => entry.value[1] !== null)
      .map((entry) => [entry.value[0], entry.value[1] as VolumeDetail]),
  );
}

async function loadNetworkDetails(networks: NetworkSummary[]): Promise<Record<string, NetworkDetail>> {
  const entries = await Promise.allSettled(
    networks.map(async (network) => [network.id, await DockerService.GetNetwork(network.id)] as const),
  );
  return Object.fromEntries(
    entries
      .filter((entry): entry is PromiseFulfilledResult<readonly [string, NetworkDetail | null]> => entry.status === 'fulfilled')
      .filter((entry) => entry.value[1] !== null)
      .map((entry) => [entry.value[0], entry.value[1] as NetworkDetail]),
  );
}

function valueOr<T>(result: Settled<T>, fallback: T): T {
  return result.status === 'fulfilled' ? result.value : fallback;
}

function firstError(results: Array<Settled<unknown>>): string | null {
  const rejected = results.find((result): result is PromiseRejectedResult => result.status === 'rejected');
  if (!rejected) {
    return null;
  }
  const reason = rejected.reason;
  if (reason instanceof Error && reason.message) {
    return reason.message;
  }
  if (typeof reason === 'string') {
    return reason;
  }
  return 'Docker is not reachable';
}
