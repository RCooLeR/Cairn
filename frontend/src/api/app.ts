import type { VersionInfo } from "../../bindings/github.com/RCooLeR/Cairn/internal/models/models.js";
import type { CancellablePromise } from "@wailsio/runtime";

import { SettingsService } from "./services";

export function getAppVersion(): CancellablePromise<VersionInfo> {
  return SettingsService.AppVersion().then((version) => {
    if (!version) {
      throw new Error("AppVersion returned no version payload");
    }
    return version;
  });
}
