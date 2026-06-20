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
});
