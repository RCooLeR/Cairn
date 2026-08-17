import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import {
  RegistryAccount,
  RegistryAuthStatus,
} from "../../../bindings/github.com/RCooLeR/Cairn/internal/models/models.js";
import { RegistryAccountsTable } from "./SettingsTables";

const account = new RegistryAccount({
  loggedIn: true,
  registry: "docker.io",
  source: "credentialHelper",
  username: "ada",
});

function renderRegistryTable(status: RegistryAuthStatus) {
  render(
    <RegistryAccountsTable
      accounts={[account]}
      busyKeys={new Set<string>()}
      onLogin={vi.fn()}
      onLogout={vi.fn()}
      onTest={vi.fn()}
      statuses={{ "docker.io": status }}
    />,
  );
}

describe("RegistryAccountsTable", () => {
  it("renders structured auth failures with actionable accessible detail", () => {
    const runtimeError = JSON.stringify({
      message: "E_REGISTRY_AUTH: Docker credential helper is unavailable",
      cause: {
        code: "E_REGISTRY_AUTH",
        message: "Docker credential helper is unavailable",
        detail: "No configured helper responded in the active backend.",
        repairHints: [
          "Install and initialize a Docker credential helper.",
          "Select No Cairn-managed credentials for external login.",
        ],
      },
      kind: "RuntimeError",
    });
    renderRegistryTable(
      new RegistryAuthStatus({
        error: runtimeError,
        loggedIn: false,
        registry: "docker.io",
      }),
    );

    const alert = screen.getByRole("alert", {
      name: "Authentication failed for docker.io",
    });
    expect(alert).toHaveTextContent("Auth failed");
    expect(alert).toHaveTextContent("Registry login required");
    expect(alert).toHaveTextContent("Docker credential helper is unavailable");
    expect(alert).toHaveTextContent(
      "No configured helper responded in the active backend.",
    );
    expect(alert).toHaveTextContent(
      "Install and initialize a Docker credential helper.",
    );
    expect(alert).toHaveTextContent("E_REGISTRY_AUTH");
    expect(alert).not.toHaveTextContent(runtimeError);
  });

  it("exposes successful auth verification as a live status", () => {
    renderRegistryTable(
      new RegistryAuthStatus({
        loggedIn: true,
        registry: "docker.io",
        username: "ada",
      }),
    );

    expect(
      screen.getByRole("status", {
        name: "Authentication verified for docker.io",
      }),
    ).toHaveTextContent("Verified");
  });
});
