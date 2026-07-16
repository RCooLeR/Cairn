import { describe, expect, it, vi } from "vitest";

import type { VersionInfo } from "../../bindings/github.com/RCooLeR/Cairn/internal/models/models.js";

const servicesMock = vi.hoisted(() => ({
  AppVersion: vi.fn(),
}));

vi.mock("./services", () => ({
  SettingsService: {
    AppVersion: servicesMock.AppVersion,
  },
}));

import { getAppVersion } from "./app";

describe("getAppVersion", () => {
  it("preserves cancellation on the transformed Wails request", async () => {
    const version: VersionInfo = {
      version: "1.2.3",
      goVersion: "go-test",
    };
    const request = fakeCancellableResult(version);
    servicesMock.AppVersion.mockReturnValue(request.source);

    const result = getAppVersion();

    expect(result.cancel).toBe(request.cancel);
    await expect(result).resolves.toEqual(version);
    await result.cancel("test cancellation");
    expect(request.cancel).toHaveBeenCalledWith("test cancellation");
  });

  it("rejects an empty payload without converting to a native promise", async () => {
    const request = fakeCancellableResult<VersionInfo | null>(null);
    servicesMock.AppVersion.mockReturnValue(request.source);

    const result = getAppVersion();

    expect(result.cancel).toBe(request.cancel);
    await expect(result).rejects.toThrow(
      "AppVersion returned no version payload",
    );
  });
});

function fakeCancellableResult<T>(value: T) {
  const cancel = vi.fn(() => Promise.resolve());
  return {
    cancel,
    source: {
      then(onFulfilled: (result: T) => unknown) {
        return Object.assign(Promise.resolve(value).then(onFulfilled), {
          cancel,
        });
      },
    },
  };
}
