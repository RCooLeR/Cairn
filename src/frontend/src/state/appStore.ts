import { create } from "zustand";

import type { VersionInfo } from "../../bindings/github.com/RCooLeR/Cairn/internal/models/models.js";
import { getAppVersion } from "../api/app";
import { frontendVersion } from "../version";

type AppState = {
  version: VersionInfo | null;
  versionLoading: boolean;
  versionError: string | null;
  loadVersion: () => Promise<void>;
  retryVersion: () => Promise<void>;
};

type CancellableVersionRequest = PromiseLike<VersionInfo> & {
  cancel?: (cause?: unknown) => PromiseLike<void> | void;
};

type ActiveVersionRequest = {
  request: CancellableVersionRequest;
  promise: Promise<void>;
};

export const APP_VERSION_BOOTSTRAP_TIMEOUT_MS = 10_000;

let requestGeneration = 0;
let activeVersionRequest: ActiveVersionRequest | null = null;

class AppVersionBootstrapTimeoutError extends Error {
  constructor() {
    super("App version request timed out after 10 seconds.");
    this.name = "AppVersionBootstrapTimeoutError";
  }
}

function versionErrorMessage(error: unknown) {
  return error instanceof Error ? error.message : "Unable to load app version";
}

function cancelVersionRequest(
  request: CancellableVersionRequest,
  cause: unknown,
) {
  if (typeof request.cancel !== "function") {
    return;
  }
  try {
    void Promise.resolve(request.cancel(cause)).catch(() => undefined);
  } catch {
    // Cancellation is best effort. The generation guard still prevents a late
    // request from publishing after the bounded bootstrap has settled.
  }
}

function waitForVersionRequest(
  request: CancellableVersionRequest,
): Promise<VersionInfo> {
  return new Promise<VersionInfo>((resolve, reject) => {
    let settled = false;
    const timeout = globalThis.setTimeout(() => {
      if (settled) {
        return;
      }
      settled = true;
      const error = new AppVersionBootstrapTimeoutError();
      cancelVersionRequest(request, error);
      reject(error);
    }, APP_VERSION_BOOTSTRAP_TIMEOUT_MS);

    request.then(
      (version) => {
        if (settled) {
          return;
        }
        settled = true;
        globalThis.clearTimeout(timeout);
        resolve(version);
      },
      (error: unknown) => {
        if (settled) {
          return;
        }
        settled = true;
        globalThis.clearTimeout(timeout);
        reject(error);
      },
    );
  });
}

export const useAppStore = create<AppState>((set, get) => {
  const startVersionRequest = (retry: boolean) => {
    if (activeVersionRequest !== null) {
      return activeVersionRequest.promise;
    }
    if (!retry && get().version !== null) {
      return Promise.resolve();
    }

    const generation = ++requestGeneration;
    set(
      retry
        ? { versionLoading: true }
        : { versionLoading: true, versionError: null },
    );
    let request: CancellableVersionRequest;
    try {
      request = getAppVersion();
    } catch (error: unknown) {
      request = Promise.reject(error);
    }

    const promise = (async () => {
      try {
        const version = await waitForVersionRequest(request);
        if (generation === requestGeneration) {
          set({ version, versionError: null });
        }
      } catch (error: unknown) {
        if (generation === requestGeneration) {
          set({
            version: {
              version: frontendVersion,
              goVersion: "Unavailable",
            },
            versionError: versionErrorMessage(error),
          });
        }
      } finally {
        if (generation === requestGeneration) {
          activeVersionRequest = null;
          set({ versionLoading: false });
        }
      }
    })();

    activeVersionRequest = { promise, request };
    return promise;
  };

  return {
    version: null,
    versionLoading: false,
    versionError: null,
    loadVersion: () => startVersionRequest(false),
    retryVersion: () => startVersionRequest(true),
  };
});

/**
 * Invalidates the module-owned request as well as its visible state. Keeping
 * this boundary explicit lets tests prove that a late, non-cancellable Wails
 * response cannot overwrite a newer bootstrap generation.
 */
export function resetAppVersionBootstrapForTest() {
  requestGeneration += 1;
  if (activeVersionRequest !== null) {
    cancelVersionRequest(
      activeVersionRequest.request,
      new Error("App version bootstrap reset"),
    );
  }
  activeVersionRequest = null;
  useAppStore.setState({
    version: null,
    versionLoading: false,
    versionError: null,
  });
}
