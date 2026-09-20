import type { RegistryAccount } from "../../bindings/github.com/RCooLeR/Cairn/internal/models/models.js";

export function normalizeRegistryHostForUI(raw: string) {
  const value = raw
    .trim()
    .toLowerCase()
    .replace(/^https?:\/\//, "")
    .replace(/\/$/, "")
    .replace(/\/v[12]$/, "");
  if (
    value === "" ||
    value === "index.docker.io" ||
    value === "registry-1.docker.io" ||
    value === "docker.io/v1"
  ) {
    return "docker.io";
  }
  return value;
}

export function registryStorageLabel(account: RegistryAccount) {
  if (account.source === "authsFile") {
    return "unencrypted Docker config.json";
  }
  if (account.source === "credHelper") {
    return "credential helper";
  }
  if (account.source === "credsStore") {
    return "credential store";
  }
  return account.source || "Docker";
}
