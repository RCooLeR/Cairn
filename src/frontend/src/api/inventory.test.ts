import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  getInventorySnapshot,
  INVENTORY_SLICE_TIMEOUT_MS,
  INVENTORY_SNAPSHOT_TIMEOUT_MS,
} from "./inventory";
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
  afterEach(() => vi.useRealTimers());
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
    expect(snapshot.failures).toEqual({
      providers: "provider table is locked",
    });
    expect(snapshot.degradedReason).toBe("providers: provider table is locked");
  });

  it("reports every failed slice instead of hiding failures after the first", async () => {
    vi.mocked(DockerService.ListContainers).mockRejectedValue(
      new Error("container list timed out"),
    );
    vi.mocked(DockerService.ListVolumes).mockRejectedValue(
      new Error("volume socket closed"),
    );

    const snapshot = await getInventorySnapshot();

    expect(snapshot.failures).toEqual({
      containers: "container list timed out",
      volumes: "volume socket closed",
    });
    expect(snapshot.degradedReason).toBe(
      "containers: container list timed out; volumes: volume socket closed",
    );
  });

  it("normalizes blank rejection reasons to a useful failure message", async () => {
    vi.mocked(DockerService.ListContainers).mockRejectedValue("");
    vi.mocked(DockerService.ListImages).mockRejectedValue(new Error("   "));

    const snapshot = await getInventorySnapshot();

    expect(snapshot.failures).toMatchObject({
      containers: "Docker is not reachable",
      images: "Docker is not reachable",
    });
    expect(snapshot.degradedReason).toContain(
      "containers: Docker is not reachable",
    );
    expect(snapshot.degradedReason).toContain(
      "images: Docker is not reachable",
    );
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

  it("continues after a stalled slice and keeps successful inventory", async () => {
    vi.useFakeTimers();
    const cancel = vi.fn();
    vi.mocked(DockerService.DiskUsage).mockReturnValue(
      Object.assign(new Promise(() => undefined), { cancel }) as never,
    );
    vi.mocked(DockerService.ListContainers).mockResolvedValue([
      { id: "running", state: "running" },
    ] as never);

    const pending = getInventorySnapshot();
    await vi.advanceTimersByTimeAsync(INVENTORY_SLICE_TIMEOUT_MS);
    const snapshot = await pending;

    expect(snapshot.failures).toEqual({
      diskUsage: "Disk usage timed out. Please retry.",
    });
    expect(snapshot.containers).toHaveLength(1);
    expect(cancel).toHaveBeenCalledOnce();
    expect(vi.getTimerCount()).toBe(0);
  });

  it("bounds the whole snapshot when multiple reads stop responding", async () => {
    vi.useFakeTimers();
    const stalled = () => new Promise(() => undefined) as never;
    vi.mocked(ProviderService.ListProviders).mockImplementation(stalled);
    vi.mocked(DockerService.Info).mockImplementation(stalled);
    vi.mocked(DockerService.Version).mockImplementation(stalled);
    vi.mocked(DockerService.DiskUsage).mockImplementation(stalled);
    const pending = getInventorySnapshot();

    await vi.advanceTimersByTimeAsync(INVENTORY_SNAPSHOT_TIMEOUT_MS);
    const snapshot = await pending;

    expect(Object.keys(snapshot.failures ?? {})).toHaveLength(8);
    expect(DockerService.ListContainers).not.toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
  });

  it("keeps other inventory when one bridge call throws synchronously", async () => {
    vi.mocked(DockerService.DiskUsage).mockImplementation(() => {
      throw new Error("bridge unavailable");
    });
    const snapshot = await getInventorySnapshot();
    expect(snapshot.failures).toEqual({ diskUsage: "bridge unavailable" });
    expect(DockerService.ListNetworks).toHaveBeenCalledOnce();
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
