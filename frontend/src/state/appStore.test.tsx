import { useEffect } from "react";
import { act, render, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { VersionInfo } from "../../bindings/github.com/RCooLeR/Cairn/internal/models/models.js";

const appApiMock = vi.hoisted(() => ({
  getAppVersion: vi.fn<() => Promise<VersionInfo>>(),
}));

vi.mock("../api/app", () => ({
  getAppVersion: appApiMock.getAppVersion,
}));

import {
  APP_VERSION_BOOTSTRAP_TIMEOUT_MS,
  resetAppVersionBootstrapForTest,
  useAppStore,
} from "./appStore";

describe("app version bootstrap", () => {
  beforeEach(() => {
    appApiMock.getAppVersion.mockReset();
    resetAppVersionBootstrapForTest();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("keeps one module-owned request through owner unmount and remount", async () => {
    const request = deferred<VersionInfo>();
    appApiMock.getAppVersion.mockReturnValue(request.promise);

    const firstOwner = render(<VersionBootstrapOwner />);
    expect(appApiMock.getAppVersion).toHaveBeenCalledTimes(1);
    expect(useAppStore.getState().versionLoading).toBe(true);

    firstOwner.unmount();
    const secondOwner = render(<VersionBootstrapOwner />);
    expect(appApiMock.getAppVersion).toHaveBeenCalledTimes(1);

    await act(async () => {
      request.resolve(version("1.2.3"));
      await request.promise;
    });

    await waitFor(() => {
      expect(useAppStore.getState()).toMatchObject({
        version: version("1.2.3"),
        versionLoading: false,
        versionError: null,
      });
    });

    await useAppStore.getState().loadVersion();
    expect(appApiMock.getAppVersion).toHaveBeenCalledTimes(1);
    secondOwner.unmount();
  });

  it("ignores a delayed result after its generation is invalidated", async () => {
    const staleRequest = deferred<VersionInfo>();
    const currentRequest = deferred<VersionInfo>();
    appApiMock.getAppVersion
      .mockReturnValueOnce(staleRequest.promise)
      .mockReturnValueOnce(currentRequest.promise);

    const staleLoad = useAppStore.getState().loadVersion();
    resetAppVersionBootstrapForTest();
    const currentLoad = useAppStore.getState().loadVersion();

    await act(async () => {
      currentRequest.resolve(version("2.0.0"));
      await currentLoad;
    });
    await act(async () => {
      staleRequest.resolve(version("0.9.0"));
      await staleLoad;
    });

    expect(useAppStore.getState()).toMatchObject({
      version: version("2.0.0"),
      versionLoading: false,
      versionError: null,
    });
    expect(appApiMock.getAppVersion).toHaveBeenCalledTimes(2);
  });

  it("publishes the stamped fallback once when the shared request fails", async () => {
    appApiMock.getAppVersion.mockRejectedValue(new Error("backend offline"));

    await act(async () => {
      await Promise.all([
        useAppStore.getState().loadVersion(),
        useAppStore.getState().loadVersion(),
      ]);
    });

    expect(useAppStore.getState()).toMatchObject({
      version: {
        goVersion: "Unavailable",
      },
      versionLoading: false,
      versionError: "backend offline",
    });
    expect(useAppStore.getState().version?.version).toMatch(/^\d+\.\d+\.\d+/);
    expect(appApiMock.getAppVersion).toHaveBeenCalledTimes(1);
  });

  it("times out, cancels when supported, and ignores a late result", async () => {
    vi.useFakeTimers();
    const request = cancellableDeferred<VersionInfo>();
    appApiMock.getAppVersion.mockReturnValue(request.promise);

    const load = useAppStore.getState().loadVersion();
    expect(useAppStore.getState().versionLoading).toBe(true);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(APP_VERSION_BOOTSTRAP_TIMEOUT_MS);
      await load;
    });

    expect(request.cancel).toHaveBeenCalledWith(
      expect.objectContaining({ name: "AppVersionBootstrapTimeoutError" }),
    );
    expect(useAppStore.getState()).toMatchObject({
      versionLoading: false,
      versionError: "App version request timed out after 10 seconds.",
    });
    const fallback = useAppStore.getState().version;

    await act(async () => {
      request.resolve(version("0.9.0"));
      await request.promise;
      await Promise.resolve();
    });

    expect(useAppStore.getState().version).toEqual(fallback);
    expect(useAppStore.getState().versionError).toBe(
      "App version request timed out after 10 seconds.",
    );
  });

  it("deduplicates an explicit retry and replaces a failure fallback", async () => {
    appApiMock.getAppVersion
      .mockRejectedValueOnce(new Error("backend starting"))
      .mockResolvedValueOnce(version("3.0.0"));

    await useAppStore.getState().loadVersion();
    expect(useAppStore.getState().versionError).toBe("backend starting");

    await useAppStore.getState().loadVersion();
    expect(appApiMock.getAppVersion).toHaveBeenCalledTimes(1);

    const firstRetry = useAppStore.getState().retryVersion();
    const secondRetry = useAppStore.getState().retryVersion();
    expect(firstRetry).toBe(secondRetry);

    await Promise.all([firstRetry, secondRetry]);

    expect(useAppStore.getState()).toMatchObject({
      version: version("3.0.0"),
      versionLoading: false,
      versionError: null,
    });
    expect(appApiMock.getAppVersion).toHaveBeenCalledTimes(2);
  });
});

function VersionBootstrapOwner() {
  const loadVersion = useAppStore((state) => state.loadVersion);

  useEffect(() => {
    void loadVersion();
  }, [loadVersion]);

  return null;
}

function deferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, reject, resolve };
}

function cancellableDeferred<T>() {
  const request = deferred<T>();
  const cancel = vi.fn(() => Promise.resolve());
  return {
    ...request,
    cancel,
    promise: Object.assign(request.promise, { cancel }),
  };
}

function version(value: string): VersionInfo {
  return {
    version: value,
    goVersion: "go-test",
  };
}
