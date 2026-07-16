import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { PortForwardStatus } from "../../../bindings/github.com/RCooLeR/Cairn/internal/models/models.js";

const portForwardServiceMock = vi.hoisted(() => ({
  GetStatus: vi.fn(),
  SetEnabled: vi.fn(),
}));

const runtimeMock = vi.hoisted(() => ({
  on: vi.fn<
    (eventName: string, callback: (event?: unknown) => void) => () => void
  >(() => vi.fn()),
}));

vi.mock("../../api/services", () => ({
  PortForwardService: portForwardServiceMock,
}));

vi.mock("@wailsio/runtime", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@wailsio/runtime")>();
  return {
    ...actual,
    Events: { ...actual.Events, On: runtimeMock.on },
  };
});

import { PortForwardingPanel } from "./PortForwardingPanel";

function supportedStatus(enabled = false) {
  return new PortForwardStatus({ enabled, forwards: [], supported: true });
}

describe("PortForwardingPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    runtimeMock.on.mockImplementation(() => vi.fn());
  });

  it("renders an initial load error and recovers through Retry", async () => {
    portForwardServiceMock.GetStatus.mockRejectedValueOnce(
      new Error("WSL forwarding bridge is unavailable"),
    );
    render(<PortForwardingPanel />);

    expect(screen.getByText("Host port forwarding")).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent(
      "Loading port forwarding status",
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "WSL forwarding bridge is unavailable",
    );

    portForwardServiceMock.GetStatus.mockResolvedValueOnce(supportedStatus());
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));

    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Enable forwarding" }),
      ).toBeInTheDocument(),
    );
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("announces toggle progress and the resulting forwarding state", async () => {
    let finishToggle!: () => void;
    const toggle = new Promise<void>((resolve) => {
      finishToggle = resolve;
    });
    portForwardServiceMock.GetStatus.mockResolvedValueOnce(
      supportedStatus(),
    ).mockResolvedValueOnce(supportedStatus(true));
    portForwardServiceMock.SetEnabled.mockReturnValueOnce(toggle);
    render(<PortForwardingPanel />);

    const enable = await screen.findByRole("button", {
      name: "Enable forwarding",
    });
    expect(screen.getByRole("status")).toHaveTextContent(
      "Port forwarding is disabled.",
    );
    fireEvent.click(enable);
    expect(screen.getByRole("status")).toHaveTextContent(
      "Updating port forwarding.",
    );

    await act(async () => {
      finishToggle();
      await toggle;
    });

    await waitFor(() =>
      expect(screen.getByRole("status")).toHaveTextContent(
        "Port forwarding is enabled.",
      ),
    );
  });

  it("hides only after a successful unsupported response", async () => {
    portForwardServiceMock.GetStatus.mockResolvedValueOnce(
      new PortForwardStatus({ supported: false }),
    );
    render(<PortForwardingPanel />);

    expect(screen.getByText("Host port forwarding")).toBeInTheDocument();
    await waitFor(() =>
      expect(
        screen.queryByText("Host port forwarding"),
      ).not.toBeInTheDocument(),
    );
  });

  it("retains the last good status when a refresh fails, then retries", async () => {
    portForwardServiceMock.GetStatus.mockResolvedValueOnce(supportedStatus());
    render(<PortForwardingPanel />);
    await screen.findByRole("button", { name: "Enable forwarding" });

    portForwardServiceMock.GetStatus.mockRejectedValueOnce(
      new Error("refresh failed"),
    );
    const providerChanged = runtimeMock.on.mock.calls.find(
      ([eventName]) => eventName === "provider:changed",
    )?.[1];
    expect(providerChanged).toBeTypeOf("function");
    await act(async () => {
      providerChanged?.();
    });

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "refresh failed",
    );
    expect(
      screen.getByRole("button", { name: "Enable forwarding" }),
    ).toBeInTheDocument();

    portForwardServiceMock.GetStatus.mockResolvedValueOnce(
      supportedStatus(true),
    );
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Disable forwarding" }),
      ).toBeInTheDocument(),
    );
  });

  it("keeps every forwarding column reachable in a narrow viewport", async () => {
    portForwardServiceMock.GetStatus.mockResolvedValueOnce(
      new PortForwardStatus({
        enabled: true,
        supported: true,
        forwards: [
          {
            bindAddr: "0.0.0.0",
            containerName: "a-container-with-a-long-name",
            hostPort: 8080,
            protocol: "tcp",
            reason: "",
            status: "active",
          },
        ],
      }),
    );

    render(<PortForwardingPanel />);

    const table = await screen.findByRole("table");
    expect(table.parentElement).toHaveClass("overflow-x-auto");
    expect(table).toHaveClass("min-w-[720px]");
    expect(screen.getByText("a-container-with-a-long-name")).toBeVisible();
    expect(screen.getByText("active")).toBeVisible();
  });
});
