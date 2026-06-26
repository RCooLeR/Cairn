import { beforeEach, describe, expect, it, vi } from "vitest";

import { getInventorySnapshot } from "./inventory";
import { DockerService, ProviderService } from "./services";

vi.mock("./services", () => ({
  DockerService: {
    DiskUsage: vi.fn(),
    GetNetwork: vi.fn(),
    GetVolume: vi.fn(),
    Info: vi.fn(),
    ListContainers: vi.fn(),
    ListImages: vi.fn(),
    ListNetworks: vi.fn(),
    ListVolumes: vi.fn(),
    Version: vi.fn(),
  },
  ProviderService: {
    ListProviders: vi.fn(),
  },
}));

describe("getInventorySnapshot", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(ProviderService.ListProviders).mockResolvedValue([]);
    vi.mocked(DockerService.Info).mockResolvedValue(null);
    vi.mocked(DockerService.Version).mockResolvedValue(null);
    vi.mocked(DockerService.DiskUsage).mockResolvedValue(null);
    vi.mocked(DockerService.ListContainers).mockResolvedValue([]);
    vi.mocked(DockerService.ListImages).mockResolvedValue([]);
    vi.mocked(DockerService.ListVolumes).mockResolvedValue([]);
    vi.mocked(DockerService.ListNetworks).mockResolvedValue([]);
    vi.mocked(DockerService.GetVolume).mockResolvedValue(null);
    vi.mocked(DockerService.GetNetwork).mockResolvedValue(null);
  });

  it("surfaces provider-list failures as degraded inventory state", async () => {
    vi.mocked(ProviderService.ListProviders).mockRejectedValue(
      new Error("provider table is locked"),
    );

    const snapshot = await getInventorySnapshot();

    expect(snapshot.providers).toEqual([]);
    expect(snapshot.degradedReason).toBe("provider table is locked");
  });

  it("does not eagerly inspect every volume and network during snapshot load", async () => {
    vi.mocked(DockerService.ListVolumes).mockResolvedValue([
      { name: "demo_data" },
    ] as never);
    vi.mocked(DockerService.ListNetworks).mockResolvedValue([
      { id: "net1", name: "demo_default" },
    ] as never);

    const snapshot = await getInventorySnapshot();

    expect(snapshot.volumes).toHaveLength(1);
    expect(snapshot.networks).toHaveLength(1);
    expect(snapshot.volumeDetails).toEqual({});
    expect(snapshot.networkDetails).toEqual({});
    expect(DockerService.GetVolume).not.toHaveBeenCalled();
    expect(DockerService.GetNetwork).not.toHaveBeenCalled();
  });

  it("serializes inventory calls so WSL stdio transports can be reused", async () => {
    const info = deferred<null>();
    vi.mocked(DockerService.Info).mockReturnValue(info.promise as never);

    const snapshotPromise = getInventorySnapshot();
    await waitUntil(() => vi.mocked(DockerService.Info).mock.calls.length > 0);

    expect(DockerService.Version).not.toHaveBeenCalled();

    info.resolve(null);
    await waitUntil(
      () => vi.mocked(DockerService.Version).mock.calls.length > 0,
    );
    await snapshotPromise;
  });
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

async function waitUntil(predicate: () => boolean) {
  for (let i = 0; i < 20; i += 1) {
    if (predicate()) {
      return;
    }
    await Promise.resolve();
  }
  throw new Error("Timed out waiting for condition");
}
