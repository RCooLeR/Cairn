import { beforeEach, describe, expect, it, vi } from "vitest";

import type {
  ContainerSummary,
  ImageSummary,
  NetworkDetail,
  NetworkSummary,
  VolumeDetail,
  VolumeSummary,
} from "../../bindings/github.com/RCooLeR/Cairn/internal/models/models.js";
import type { InventorySnapshot } from "../api/inventory";
import { getInventorySnapshot } from "../api/inventory";
import { DockerService } from "../api/services";
import {
  createInitialInventorySliceStates,
  inventoryDetailRequestDiagnosticsForTest,
  maxActiveInventoryDetailRequests,
  maxCachedInventoryDetails,
  useInventoryStore,
} from "./inventoryStore";

vi.mock("../api/inventory", async (importOriginal) => {
  const original = await importOriginal<typeof import("../api/inventory")>();
  return {
    ...original,
    getInventorySnapshot: vi.fn(),
  };
});

vi.mock("../api/services", () => ({
  DockerService: {
    ListContainers: vi.fn(),
    ListImages: vi.fn(),
    ListNetworks: vi.fn(),
    ListVolumes: vi.fn(),
    GetNetwork: vi.fn(),
    GetVolume: vi.fn(),
  },
}));

describe("inventory store slice ownership", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(getInventorySnapshot).mockResolvedValue(snapshot());
    vi.mocked(DockerService.ListContainers).mockResolvedValue([]);
    vi.mocked(DockerService.ListImages).mockResolvedValue([]);
    vi.mocked(DockerService.ListNetworks).mockResolvedValue([]);
    vi.mocked(DockerService.ListVolumes).mockResolvedValue([]);
    vi.mocked(DockerService.GetNetwork).mockResolvedValue(null);
    vi.mocked(DockerService.GetVolume).mockResolvedValue(null);
    useInventoryStore.setState({
      status: "idle",
      connection: "connecting",
      error: null,
      lastLoadedAt: null,
      slices: createInitialInventorySliceStates(),
      providers: [],
      dockerInfo: null,
      dockerVersion: null,
      diskUsage: null,
      containers: [],
      containerStats: {},
      containerStatsAuthoritative: false,
      images: [],
      volumes: [],
      networks: [],
      volumeEpoch: 0,
      networkEpoch: 0,
      volumeDetails: {},
      networkDetails: {},
    });
  });

  it("merges successful full-refresh slices and retains every failed slice", async () => {
    const oldContainer = container("old-container");
    const newImage = image("new-image");
    const oldVolume = volume("old-volume");
    useInventoryStore.setState({
      containers: [oldContainer],
      images: [image("old-image")],
      volumes: [oldVolume],
    });
    vi.mocked(getInventorySnapshot).mockResolvedValue(
      snapshot({
        containers: [],
        images: [newImage],
        volumes: [],
        failures: {
          containers: "container list timed out",
          volumes: "volume socket closed",
        },
        degradedReason:
          "containers: container list timed out; volumes: volume socket closed",
      }),
    );

    await useInventoryStore.getState().refresh();

    const state = useInventoryStore.getState();
    expect(state.containers).toEqual([oldContainer]);
    expect(state.images).toEqual([newImage]);
    expect(state.volumes).toEqual([oldVolume]);
    expect(state.slices.containers).toMatchObject({
      loading: false,
      stale: true,
      error: "container list timed out",
    });
    expect(state.slices.images).toMatchObject({
      loading: false,
      stale: false,
      error: null,
    });
    expect(state.slices.images.lastSuccessAt).not.toBeNull();
    expect(state.slices.volumes).toMatchObject({
      loading: false,
      stale: true,
      error: "volume socket closed",
    });
    expect(state.error).toBe(
      "containers: container list timed out; volumes: volume socket closed",
    );
    expect(state.status).toBe("error");
    expect(state.connection).toBe("connected");
  });

  it("keeps the engine connected when a full refresh has only a resource-slice failure", async () => {
    const oldContainer = container("last-known-container");
    useInventoryStore.setState({
      connection: "connected",
      containers: [oldContainer],
    });
    vi.mocked(getInventorySnapshot).mockResolvedValue(
      snapshot({
        containers: [],
        failures: { containers: "container list timed out" },
        degradedReason: "containers: container list timed out",
      }),
    );

    await useInventoryStore.getState().refresh();

    const state = useInventoryStore.getState();
    expect(state.connection).toBe("connected");
    expect(state.containers).toEqual([oldContainer]);
    expect(state.slices.containers).toMatchObject({
      stale: true,
      error: "container list timed out",
    });
  });

  it("downgrades a connected engine only when both full-refresh engine probes fail", async () => {
    useInventoryStore.setState({ connection: "connected" });
    vi.mocked(getInventorySnapshot).mockResolvedValue(
      snapshot({
        failures: {
          dockerInfo: "info probe failed",
          dockerVersion: "version probe failed",
        },
        degradedReason:
          "dockerInfo: info probe failed; dockerVersion: version probe failed",
      }),
    );

    await useInventoryStore.getState().refresh();

    expect(useInventoryStore.getState().connection).toBe("reconnecting");
  });

  it("fails closed for a degraded legacy snapshot with ambiguous fallbacks", async () => {
    const oldContainer = container("last-known-container");
    const oldImage = image("last-known-image");
    useInventoryStore.setState({
      containers: [oldContainer],
      images: [oldImage],
    });
    const legacySnapshot = snapshot({
      containers: [],
      images: [],
      degradedReason: "legacy inventory request failed",
    });
    delete legacySnapshot.failures;
    vi.mocked(getInventorySnapshot).mockResolvedValue(legacySnapshot);

    await useInventoryStore.getState().refresh();

    const state = useInventoryStore.getState();
    expect(state.containers).toEqual([oldContainer]);
    expect(state.images).toEqual([oldImage]);
    expect(state.slices.containers).toMatchObject({
      stale: true,
      error: "legacy inventory request failed",
    });
    expect(state.slices.images).toMatchObject({
      stale: true,
      error: "legacy inventory request failed",
    });
  });

  it("does not let an older partial response overwrite a newer full refresh", async () => {
    const oldPartial = deferred<ContainerSummary[]>();
    vi.mocked(DockerService.ListContainers).mockReturnValue(
      oldPartial.promise as never,
    );

    const partialRequest = useInventoryStore.getState().refreshContainers();
    vi.mocked(getInventorySnapshot).mockResolvedValue(
      snapshot({ containers: [container("new-full")] }),
    );
    await useInventoryStore.getState().refresh();

    oldPartial.resolve([container("old-partial")]);
    await partialRequest;

    expect(useInventoryStore.getState().containers).toEqual([
      container("new-full"),
    ]);
  });

  it("does not let an older full response overwrite a newer partial refresh", async () => {
    useInventoryStore.setState({ containers: [container("seeded")] });
    const oldFull = deferred<InventorySnapshot>();
    const newPartial = deferred<ContainerSummary[]>();
    vi.mocked(getInventorySnapshot).mockReturnValue(oldFull.promise);
    vi.mocked(DockerService.ListContainers).mockReturnValue(
      newPartial.promise as never,
    );

    const fullRequest = useInventoryStore.getState().refresh();
    const partialRequest = useInventoryStore.getState().refreshContainers();
    oldFull.resolve(snapshot({ containers: [container("old-full")] }));
    await fullRequest;

    expect(useInventoryStore.getState().containers).toEqual([
      container("seeded"),
    ]);
    expect(useInventoryStore.getState().slices.containers.loading).toBe(true);

    newPartial.resolve([container("new-partial")]);
    await partialRequest;

    expect(useInventoryStore.getState().containers).toEqual([
      container("new-partial"),
    ]);
  });

  it("retains a superseding slice failure without downgrading successful engine probes", async () => {
    useInventoryStore.setState({ connection: "connected" });
    const oldFull = deferred<InventorySnapshot>();
    const newPartial = deferred<ContainerSummary[]>();
    vi.mocked(getInventorySnapshot).mockReturnValue(oldFull.promise);
    vi.mocked(DockerService.ListContainers).mockReturnValue(
      newPartial.promise as never,
    );

    const fullRequest = useInventoryStore.getState().refresh();
    const partialRequest = useInventoryStore.getState().refreshContainers();
    newPartial.reject(new Error("newer container refresh failed"));
    await partialRequest;
    oldFull.resolve(snapshot({ containers: [container("obsolete-full")] }));
    await fullRequest;

    const state = useInventoryStore.getState();
    expect(state.slices.containers.error).toBe(
      "newer container refresh failed",
    );
    expect(state.connection).toBe("connected");
  });

  it("keeps unrelated slice errors when another partial refresh recovers", async () => {
    vi.mocked(DockerService.ListContainers).mockRejectedValue(
      new Error("containers unavailable"),
    );
    vi.mocked(DockerService.ListImages).mockRejectedValue(
      new Error("images unavailable"),
    );

    await useInventoryStore.getState().refreshContainers();
    await useInventoryStore.getState().refreshImages();

    expect(useInventoryStore.getState().error).toBe(
      "containers: containers unavailable; images: images unavailable",
    );

    vi.mocked(DockerService.ListImages).mockResolvedValue([image("recovered")]);
    await useInventoryStore.getState().refreshImages();

    const state = useInventoryStore.getState();
    expect(state.images).toEqual([image("recovered")]);
    expect(state.slices.images.error).toBeNull();
    expect(state.slices.containers.error).toBe("containers unavailable");
    expect(state.error).toBe("containers: containers unavailable");
    expect(state.status).toBe("error");
  });

  it("coalesces freshness invalidations behind an in-flight ordinary refresh", async () => {
    const oldFull = deferred<InventorySnapshot>();
    vi.mocked(getInventorySnapshot)
      .mockReturnValueOnce(oldFull.promise)
      .mockResolvedValueOnce(
        snapshot({ containers: [container("fresh-provider")] }),
      );

    const ordinary = useInventoryStore.getState().refresh();
    const firstFresh = useInventoryStore.getState().refreshFresh();
    const secondFresh = useInventoryStore.getState().refreshFresh();

    expect(getInventorySnapshot).toHaveBeenCalledTimes(1);
    oldFull.resolve(snapshot({ containers: [container("old-provider")] }));
    await ordinary;
    await waitUntil(
      () => vi.mocked(getInventorySnapshot).mock.calls.length === 2,
    );
    await Promise.all([firstFresh, secondFresh]);

    expect(getInventorySnapshot).toHaveBeenCalledTimes(2);
    expect(useInventoryStore.getState().containers).toEqual([
      container("fresh-provider"),
    ]);
  });

  it("clears every old-scope inventory value before a failing scoped refresh", async () => {
    const oldVolume = volume("old-volume");
    const oldNetwork = network("old-network");
    const pending = deferred<InventorySnapshot>();
    useInventoryStore.setState({
      connection: "connected",
      error: "old error",
      lastLoadedAt: 123,
      providers: [{ id: "old-provider" } as never],
      dockerInfo: { name: "old-engine" } as never,
      dockerVersion: { serverVersion: "old-version" } as never,
      diskUsage: { totalBytes: 99 } as never,
      containers: [container("old-container")],
      containerStats: {
        "old-container": {
          containerID: "old-container",
          cpuPercent: 42,
          memoryBytes: 420,
          networkRxRate: 1,
          networkTxRate: 2,
        },
      },
      images: [image("old-image")],
      volumes: [oldVolume],
      networks: [oldNetwork],
      volumeDetails: {
        [oldVolume.name]: volumeDetail(oldVolume, "old-scope"),
      },
      networkDetails: {
        [oldNetwork.id]: networkDetail(oldNetwork, "old-scope"),
      },
    });
    vi.mocked(getInventorySnapshot).mockReturnValue(pending.promise);

    const request = useInventoryStore.getState().refreshScope();

    expect(useInventoryStore.getState()).toMatchObject({
      connection: "connecting",
      error: null,
      lastLoadedAt: null,
      providers: [],
      dockerInfo: null,
      dockerVersion: null,
      diskUsage: null,
      containers: [],
      containerStats: {},
      images: [],
      volumes: [],
      networks: [],
      volumeDetails: {},
      networkDetails: {},
    });

    pending.reject(new Error("new scope unavailable"));
    await request;

    const failed = useInventoryStore.getState();
    expect(failed.containers).toEqual([]);
    expect(failed.images).toEqual([]);
    expect(failed.volumes).toEqual([]);
    expect(failed.networks).toEqual([]);
    expect(failed.error).toContain("new scope unavailable");
  });

  it("does not let a successful snapshot overwrite a newer disconnect event", async () => {
    useInventoryStore.setState({ connection: "connected" });
    const pending = deferred<InventorySnapshot>();
    vi.mocked(getInventorySnapshot).mockReturnValue(pending.promise);

    const request = useInventoryStore.getState().refresh();
    useInventoryStore.getState().setConnection("disconnected");
    pending.resolve(snapshot());
    await request;

    expect(useInventoryStore.getState().connection).toBe("disconnected");
  });

  it("does not let failed engine probes overwrite a newer connected event", async () => {
    useInventoryStore.setState({ connection: "connecting" });
    const pending = deferred<InventorySnapshot>();
    vi.mocked(getInventorySnapshot).mockReturnValue(pending.promise);

    const request = useInventoryStore.getState().refresh();
    useInventoryStore.getState().setConnection("connected");
    pending.resolve(
      snapshot({
        failures: {
          dockerInfo: "info probe failed",
          dockerVersion: "version probe failed",
        },
      }),
    );
    await request;

    expect(useInventoryStore.getState().connection).toBe("connected");
  });

  it("derives aggregate loading while a partial refresh is pending", async () => {
    const pending = deferred<ImageSummary[]>();
    vi.mocked(DockerService.ListImages).mockReturnValue(
      pending.promise as never,
    );

    const request = useInventoryStore.getState().refreshImages();

    expect(useInventoryStore.getState().status).toBe("loading");
    expect(useInventoryStore.getState().slices.images.loading).toBe(true);

    pending.resolve([image("loaded")]);
    await request;

    expect(useInventoryStore.getState().status).toBe("ready");
    expect(useInventoryStore.getState().slices.images.loading).toBe(false);
  });

  it("treats every successful volume and network list as an incarnation boundary", async () => {
    const sharedVolume = volume("shared-volume");
    const sharedNetwork = network("shared-network");
    const cachedVolume = volumeDetail(sharedVolume, "cached-volume");
    const cachedNetwork = networkDetail(sharedNetwork, "cached-network");
    const freshVolume = volumeDetail(sharedVolume, "snapshot-volume");
    const freshNetwork = networkDetail(sharedNetwork, "snapshot-network");
    useInventoryStore.setState({
      volumes: [sharedVolume],
      networks: [sharedNetwork],
      volumeDetails: { [sharedVolume.name]: cachedVolume },
      networkDetails: { [sharedNetwork.id]: cachedNetwork },
    });
    vi.mocked(getInventorySnapshot).mockResolvedValue(
      snapshot({
        volumes: [sharedVolume],
        networks: [sharedNetwork],
        volumeDetails: { [sharedVolume.name]: freshVolume },
        networkDetails: { [sharedNetwork.id]: freshNetwork },
      }),
    );

    const before = useInventoryStore.getState();
    await useInventoryStore.getState().refresh();

    expect(useInventoryStore.getState().volumeDetails).toEqual({
      [sharedVolume.name]: freshVolume,
    });
    expect(useInventoryStore.getState().networkDetails).toEqual({
      [sharedNetwork.id]: freshNetwork,
    });
    expect(useInventoryStore.getState().volumeEpoch).toBe(
      before.volumeEpoch + 1,
    );
    expect(useInventoryStore.getState().networkEpoch).toBe(
      before.networkEpoch + 1,
    );

    vi.mocked(getInventorySnapshot).mockResolvedValue(
      snapshot({
        volumes: [sharedVolume],
        networks: [sharedNetwork],
        volumeDetails: {},
        networkDetails: {},
      }),
    );
    await useInventoryStore.getState().refreshFresh();

    expect(useInventoryStore.getState().volumeDetails).toEqual({});
    expect(useInventoryStore.getState().networkDetails).toEqual({});
    expect(useInventoryStore.getState().volumeEpoch).toBe(
      before.volumeEpoch + 2,
    );
    expect(useInventoryStore.getState().networkEpoch).toBe(
      before.networkEpoch + 2,
    );
  });

  it("discards a volume detail response from an invalidated provider scope", async () => {
    const sharedVolume = volume("shared-volume");
    const staleDetail = volumeDetail(sharedVolume, "old-provider");
    const pending = deferred<VolumeDetail | null>();
    useInventoryStore.setState({ volumes: [sharedVolume] });
    vi.mocked(DockerService.GetVolume).mockReturnValue(
      pending.promise as never,
    );
    vi.mocked(getInventorySnapshot).mockResolvedValue(
      snapshot({ volumes: [sharedVolume] }),
    );

    const detailRequest = useInventoryStore
      .getState()
      .loadVolumeDetail(sharedVolume.name);
    await useInventoryStore.getState().refreshScope();
    pending.resolve(staleDetail);

    await expect(detailRequest).resolves.toEqual({ status: "obsolete" });
    expect(useInventoryStore.getState().volumeDetails).toEqual({});
  });

  it("rejects same-key detail requests from the preceding list incarnation", async () => {
    const sharedVolume = volume("db");
    const sharedNetwork = network("shared-network");
    const staleVolume = volumeDetail(sharedVolume, "old-list");
    const staleNetwork = networkDetail(sharedNetwork, "old-list");
    const pendingVolume = deferred<VolumeDetail | null>();
    const pendingNetwork = deferred<NetworkDetail | null>();
    useInventoryStore.setState({
      volumes: [sharedVolume],
      networks: [sharedNetwork],
      volumeDetails: {
        [sharedVolume.name]: volumeDetail(sharedVolume, "old-cache"),
      },
      networkDetails: {
        [sharedNetwork.id]: networkDetail(sharedNetwork, "old-cache"),
      },
    });
    vi.mocked(DockerService.GetVolume).mockReturnValue(
      pendingVolume.promise as never,
    );
    vi.mocked(DockerService.GetNetwork).mockReturnValue(
      pendingNetwork.promise as never,
    );
    vi.mocked(DockerService.ListVolumes).mockResolvedValue([sharedVolume]);
    vi.mocked(DockerService.ListNetworks).mockResolvedValue([sharedNetwork]);

    const volumeRequest = useInventoryStore
      .getState()
      .loadVolumeDetail(sharedVolume.name);
    const networkRequest = useInventoryStore
      .getState()
      .loadNetworkDetail(sharedNetwork.id);
    await Promise.all([
      useInventoryStore.getState().refreshVolumes(),
      useInventoryStore.getState().refreshNetworks(),
    ]);
    expect(useInventoryStore.getState().volumeDetails).toEqual({});
    expect(useInventoryStore.getState().networkDetails).toEqual({});
    pendingVolume.resolve(staleVolume);
    pendingNetwork.resolve(staleNetwork);

    await expect(volumeRequest).resolves.toEqual({ status: "obsolete" });
    await expect(networkRequest).resolves.toEqual({ status: "obsolete" });
    expect(useInventoryStore.getState().volumeDetails).toEqual({});
    expect(useInventoryStore.getState().networkDetails).toEqual({});
  });

  it("keeps detail compatible when a failed parent refresh retains the resource", async () => {
    const sharedVolume = volume("shared-volume");
    const retainedDetail = volumeDetail(sharedVolume, "retained-list");
    const pending = deferred<VolumeDetail | null>();
    useInventoryStore.setState({ volumes: [sharedVolume] });
    vi.mocked(DockerService.GetVolume).mockReturnValue(
      pending.promise as never,
    );
    vi.mocked(DockerService.ListVolumes).mockRejectedValue(
      new Error("volume list failed"),
    );

    const detailRequest = useInventoryStore
      .getState()
      .loadVolumeDetail(sharedVolume.name);
    await useInventoryStore.getState().refreshVolumes();
    pending.resolve(retainedDetail);

    await expect(detailRequest).resolves.toEqual({
      status: "current",
      detail: retainedDetail,
    });
    expect(useInventoryStore.getState().volumeDetails).toEqual({
      [sharedVolume.name]: retainedDetail,
    });
    expect(useInventoryStore.getState().slices.volumes.error).toBe(
      "volume list failed",
    );
  });

  it("releases completed detail request ownership under key churn", async () => {
    const volumes = Array.from({ length: 75 }, (_, index) =>
      volume(`volume-${index}`),
    );
    const networks = Array.from({ length: 75 }, (_, index) =>
      network(`network-${index}`),
    );
    useInventoryStore.setState({ volumes, networks });
    vi.mocked(DockerService.GetVolume).mockImplementation(
      (name) =>
        Promise.resolve(volumeDetail(volume(name), `detail-${name}`)) as never,
    );
    vi.mocked(DockerService.GetNetwork).mockImplementation(
      (id) =>
        Promise.resolve(networkDetail(network(id), `detail-${id}`)) as never,
    );

    await Promise.all([
      ...volumes.map((item) =>
        useInventoryStore.getState().loadVolumeDetail(item.name),
      ),
      ...networks.map((item) =>
        useInventoryStore.getState().loadNetworkDetail(item.id),
      ),
    ]);

    expect(inventoryDetailRequestDiagnosticsForTest()).toEqual({
      activeVolumeRequests: 0,
      activeNetworkRequests: 0,
    });
  });

  it("bounds pending detail ownership and completed detail caches under key churn", async () => {
    const count = maxActiveInventoryDetailRequests + 24;
    const volumes = Array.from({ length: count }, (_, index) =>
      volume(`pending-volume-${index}`),
    );
    const networks = Array.from({ length: count }, (_, index) =>
      network(`pending-network-${index}`),
    );
    const pendingVolumes = new Map(
      volumes.map((item) => [item.name, deferred<VolumeDetail | null>()]),
    );
    const pendingNetworks = new Map(
      networks.map((item) => [item.id, deferred<NetworkDetail | null>()]),
    );
    useInventoryStore.setState({ volumes, networks });
    vi.mocked(DockerService.GetVolume).mockImplementation(
      (name) => pendingVolumes.get(name)!.promise as never,
    );
    vi.mocked(DockerService.GetNetwork).mockImplementation(
      (id) => pendingNetworks.get(id)!.promise as never,
    );

    const volumeResults = volumes.map((item) =>
      useInventoryStore.getState().loadVolumeDetail(item.name),
    );
    const networkResults = networks.map((item) =>
      useInventoryStore.getState().loadNetworkDetail(item.id),
    );

    expect(inventoryDetailRequestDiagnosticsForTest()).toEqual({
      activeVolumeRequests: maxActiveInventoryDetailRequests,
      activeNetworkRequests: maxActiveInventoryDetailRequests,
    });

    for (const item of volumes) {
      pendingVolumes
        .get(item.name)!
        .resolve(volumeDetail(item, `detail-${item.name}`));
    }
    for (const item of networks) {
      pendingNetworks
        .get(item.id)!
        .resolve(networkDetail(item, `detail-${item.id}`));
    }

    const [settledVolumes, settledNetworks] = await Promise.all([
      Promise.all(volumeResults),
      Promise.all(networkResults),
    ]);
    expect(settledVolumes[0]).toEqual({ status: "obsolete" });
    expect(settledNetworks[0]).toEqual({ status: "obsolete" });
    expect(settledVolumes[settledVolumes.length - 1]?.status).toBe("current");
    expect(settledNetworks[settledNetworks.length - 1]?.status).toBe("current");
    expect(
      Object.keys(useInventoryStore.getState().volumeDetails),
    ).toHaveLength(maxCachedInventoryDetails);
    expect(
      Object.keys(useInventoryStore.getState().networkDetails),
    ).toHaveLength(maxCachedInventoryDetails);
    expect(useInventoryStore.getState().volumeDetails[volumes[0].name]).toBe(
      undefined,
    );
    expect(useInventoryStore.getState().networkDetails[networks[0].id]).toBe(
      undefined,
    );
    expect(inventoryDetailRequestDiagnosticsForTest()).toEqual({
      activeVolumeRequests: 0,
      activeNetworkRequests: 0,
    });
  });

  it("rejects detail responses whose immutable identifier mismatches the request", async () => {
    const expectedVolume = volume("expected-volume");
    const expectedNetwork = network("expected-network");
    useInventoryStore.setState({
      volumes: [expectedVolume],
      networks: [expectedNetwork],
    });
    vi.mocked(DockerService.GetVolume).mockResolvedValue(
      volumeDetail(volume("different-volume"), "wrong") as never,
    );
    vi.mocked(DockerService.GetNetwork).mockResolvedValue(
      networkDetail(network("different-network"), "wrong") as never,
    );

    await expect(
      useInventoryStore.getState().loadVolumeDetail(expectedVolume.name),
    ).rejects.toThrow("did not match the requested volume");
    await expect(
      useInventoryStore.getState().loadNetworkDetail(expectedNetwork.id),
    ).rejects.toThrow("did not match the requested network");
    expect(useInventoryStore.getState().volumeDetails).toEqual({});
    expect(useInventoryStore.getState().networkDetails).toEqual({});
  });

  it("treats a volume detail error as obsolete when its parent is absent", async () => {
    const sharedVolume = volume("removed-volume-error");
    const pending = deferred<VolumeDetail | null>();
    useInventoryStore.setState({ volumes: [sharedVolume] });
    vi.mocked(DockerService.GetVolume).mockReturnValue(
      pending.promise as never,
    );

    const detailRequest = useInventoryStore
      .getState()
      .loadVolumeDetail(sharedVolume.name);
    // Bypass the authoritative setter to prove rejection ownership also checks
    // current parent membership rather than relying only on generations.
    useInventoryStore.setState({ volumes: [] });
    pending.reject(new Error("removed volume failed late"));

    await expect(detailRequest).resolves.toEqual({ status: "obsolete" });
  });

  it("treats a network detail error as obsolete when its parent is absent", async () => {
    const sharedNetwork = network("removed-network-error");
    const pending = deferred<NetworkDetail | null>();
    useInventoryStore.setState({ networks: [sharedNetwork] });
    vi.mocked(DockerService.GetNetwork).mockReturnValue(
      pending.promise as never,
    );

    const detailRequest = useInventoryStore
      .getState()
      .loadNetworkDetail(sharedNetwork.id);
    useInventoryStore.setState({ networks: [] });
    pending.reject(new Error("removed network failed late"));

    await expect(detailRequest).resolves.toEqual({ status: "obsolete" });
  });

  it("invalidates old volume detail across authoritative removal and same-key re-add", async () => {
    const sharedVolume = volume("recreated-volume");
    const oldDetail = volumeDetail(sharedVolume, "old-incarnation");
    const pending = deferred<VolumeDetail | null>();
    useInventoryStore.setState({ volumes: [sharedVolume] });
    vi.mocked(DockerService.GetVolume).mockReturnValue(
      pending.promise as never,
    );

    const detailRequest = useInventoryStore
      .getState()
      .loadVolumeDetail(sharedVolume.name);
    vi.mocked(DockerService.ListVolumes)
      .mockResolvedValueOnce([])
      .mockResolvedValueOnce([sharedVolume]);
    await useInventoryStore.getState().refreshVolumes();
    await useInventoryStore.getState().refreshVolumes();
    pending.resolve(oldDetail);

    await expect(detailRequest).resolves.toEqual({ status: "obsolete" });
    expect(useInventoryStore.getState().volumeDetails).toEqual({});
  });

  it("invalidates old network detail across authoritative removal and same-key re-add", async () => {
    const sharedNetwork = network("recreated-network");
    const oldDetail = networkDetail(sharedNetwork, "old-incarnation");
    const pending = deferred<NetworkDetail | null>();
    useInventoryStore.setState({ networks: [sharedNetwork] });
    vi.mocked(DockerService.GetNetwork).mockReturnValue(
      pending.promise as never,
    );

    const detailRequest = useInventoryStore
      .getState()
      .loadNetworkDetail(sharedNetwork.id);
    vi.mocked(DockerService.ListNetworks)
      .mockResolvedValueOnce([])
      .mockResolvedValueOnce([sharedNetwork]);
    await useInventoryStore.getState().refreshNetworks();
    await useInventoryStore.getState().refreshNetworks();
    pending.resolve(oldDetail);

    await expect(detailRequest).resolves.toEqual({ status: "obsolete" });
    expect(useInventoryStore.getState().networkDetails).toEqual({});
  });

  it("keeps the live stats overlay authoritative across list refreshes", async () => {
    useInventoryStore.setState({ containers: [container("live")] });
    useInventoryStore.getState().setContainerStats([
      {
        containerID: "live",
        cpuPercent: 42,
        memoryBytes: 2048,
        networkRxRate: 12,
        networkTxRate: 34,
      },
    ]);
    vi.mocked(DockerService.ListContainers).mockResolvedValue([
      { ...container("live"), cpuPercent: 1, memoryBytes: 10 },
    ]);

    await useInventoryStore.getState().refreshContainers();

    expect(useInventoryStore.getState().containers[0]).toMatchObject({
      cpuPercent: 42,
      memoryBytes: 2048,
      netRxRate: 12,
      netTxRate: 34,
    });

    vi.mocked(getInventorySnapshot).mockResolvedValue(
      snapshot({
        containers: [{ ...container("live"), cpuPercent: 2, memoryBytes: 20 }],
      }),
    );
    await useInventoryStore.getState().refreshFresh();

    expect(useInventoryStore.getState().containers[0]).toMatchObject({
      cpuPercent: 42,
      memoryBytes: 2048,
      netRxRate: 12,
      netTxRate: 34,
    });
  });

  it("keeps an empty live-stats frame authoritative across list and full refreshes", async () => {
    const staleSnapshot = {
      ...container("empty-frame"),
      cpuPercent: 17,
      memoryBytes: 1700,
      memoryLimit: 3400,
      netRxRate: 11,
      netTxRate: 12,
    };
    useInventoryStore.setState({ containers: [staleSnapshot] });

    useInventoryStore.getState().setContainerStats([]);
    expect(useInventoryStore.getState().containers[0]).toMatchObject({
      cpuPercent: 0,
      memoryBytes: 0,
      memoryLimit: 0,
      netRxRate: 0,
      netTxRate: 0,
    });

    vi.mocked(DockerService.ListContainers).mockResolvedValue([staleSnapshot]);
    await useInventoryStore.getState().refreshContainers();
    expect(useInventoryStore.getState().containers[0]).toMatchObject({
      cpuPercent: 0,
      memoryBytes: 0,
      memoryLimit: 0,
      netRxRate: 0,
      netTxRate: 0,
    });

    vi.mocked(getInventorySnapshot).mockResolvedValue(
      snapshot({ containers: [staleSnapshot] }),
    );
    await useInventoryStore.getState().refreshFresh();
    expect(useInventoryStore.getState().containers[0]).toMatchObject({
      cpuPercent: 0,
      memoryBytes: 0,
      memoryLimit: 0,
      netRxRate: 0,
      netTxRate: 0,
    });
  });

  it("keeps containers omitted from a live-stats frame cleared on refresh", async () => {
    const sampled = { ...container("sampled"), cpuPercent: 9, memoryBytes: 90 };
    const omitted = { ...container("omitted"), cpuPercent: 8, memoryBytes: 80 };
    useInventoryStore.setState({ containers: [sampled, omitted] });
    useInventoryStore.getState().setContainerStats([
      {
        containerID: sampled.id,
        cpuPercent: 42,
        memoryBytes: 420,
        networkRxRate: 4,
        networkTxRate: 2,
      },
    ]);

    vi.mocked(DockerService.ListContainers).mockResolvedValue([
      sampled,
      omitted,
    ]);
    await useInventoryStore.getState().refreshContainers();

    expect(useInventoryStore.getState().containers).toEqual([
      expect.objectContaining({
        id: sampled.id,
        cpuPercent: 42,
        memoryBytes: 420,
      }),
      expect.objectContaining({
        id: omitted.id,
        cpuPercent: 0,
        memoryBytes: 0,
      }),
    ]);
  });

  it("preserves snapshot stats without an overlay and drops old stats on a scope change", async () => {
    vi.mocked(getInventorySnapshot).mockResolvedValue(
      snapshot({
        containers: [
          { ...container("shared-id"), cpuPercent: 7, memoryBytes: 70 },
        ],
      }),
    );
    await useInventoryStore.getState().refresh();

    expect(useInventoryStore.getState().containers[0]).toMatchObject({
      cpuPercent: 7,
      memoryBytes: 70,
    });

    useInventoryStore.getState().setContainerStats([
      {
        containerID: "shared-id",
        cpuPercent: 42,
        memoryBytes: 420,
        networkRxRate: 12,
        networkTxRate: 34,
      },
    ]);
    vi.mocked(getInventorySnapshot).mockResolvedValue(
      snapshot({
        containers: [
          { ...container("shared-id"), cpuPercent: 3, memoryBytes: 30 },
        ],
      }),
    );

    await useInventoryStore.getState().refreshScope();

    expect(useInventoryStore.getState().containerStats).toEqual({});
    expect(useInventoryStore.getState().containers[0]).toMatchObject({
      cpuPercent: 3,
      memoryBytes: 30,
    });
  });

  it("treats a blank failure value as a present failed slice", async () => {
    const retained = container("last-known");
    useInventoryStore.setState({ containers: [retained] });
    vi.mocked(getInventorySnapshot).mockResolvedValue(
      snapshot({ containers: [], failures: { containers: "" } }),
    );

    await useInventoryStore.getState().refresh();

    const state = useInventoryStore.getState();
    expect(state.containers).toEqual([retained]);
    expect(state.slices.containers).toMatchObject({
      stale: true,
      error: "Docker is not reachable",
    });
  });
});

function snapshot(
  overrides: Partial<InventorySnapshot> = {},
): InventorySnapshot {
  return {
    providers: [],
    dockerInfo: null,
    dockerVersion: null,
    diskUsage: null,
    containers: [],
    images: [],
    volumes: [],
    networks: [],
    volumeDetails: {},
    networkDetails: {},
    failures: {},
    degradedReason: null,
    ...overrides,
  };
}

function container(id: string): ContainerSummary {
  return { id, name: id } as ContainerSummary;
}

function image(id: string): ImageSummary {
  return {
    id,
    repoTags: [id],
    sizeBytes: 0,
    createdAt: "2026-07-17T00:00:00Z",
    inUse: false,
  };
}

function volume(name: string): VolumeSummary {
  return { name } as VolumeSummary;
}

function network(id: string): NetworkSummary {
  return { id, name: id } as NetworkSummary;
}

function volumeDetail(summary: VolumeSummary, source: string): VolumeDetail {
  return { summary, options: { source } } as VolumeDetail;
}

function networkDetail(summary: NetworkSummary, source: string): NetworkDetail {
  return { summary, options: { source } } as NetworkDetail;
}

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
