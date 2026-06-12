import type { VersionInfo } from '../../bindings/github.com/RCooLeR/Cairn/internal/models/models.js';

import { SettingsService } from './services';

export async function getAppVersion(): Promise<VersionInfo> {
  const version = await SettingsService.AppVersion();
  if (!version) {
    throw new Error('AppVersion returned no version payload');
  }
  return version;
}
