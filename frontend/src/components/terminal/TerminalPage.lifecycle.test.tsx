import type { TerminalSessionInfo } from "../../../bindings/github.com/RCooLeR/Cairn/internal/models/models.js";

import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const terminalServiceMock = vi.hoisted(() => ({
  CloseTerminal: vi.fn(),
  DetectContainerShells: vi.fn(),
  ListTerminalSessions: vi.fn(),
  OpenBackendTerminal: vi.fn(),
  OpenContainerTerminal: vi.fn(),
  OpenHostTerminal: vi.fn(),
  OpenProjectTerminal: vi.fn(),
  ResizeTerminal: vi.fn(),
  WriteTerminal: vi.fn(),
}));

const settingsServiceMock = vi.hoisted(() => ({
  GetCheatsheet: vi.fn(),
}));

const runtimeMock = vi.hoisted(() => {
  const listeners = new Map<string, Set<(event?: unknown) => void>>();
  return {
    clipboardText: vi.fn(),
    listeners,
    on: vi.fn((eventName: string, callback: (event?: unknown) => void) => {
      const eventListeners = listeners.get(eventName) ?? new Set();
      eventListeners.add(callback);
      listeners.set(eventName, eventListeners);
      return () => eventListeners.delete(callback);
    }),
    setClipboardText: vi.fn(),
  };
});

const xtermMock = vi.hoisted(() => ({
  dataHandlers: [] as Array<(data: string) => void>,
}));

vi.mock("../../api/services", () => ({
  SettingsService: settingsServiceMock,
  TerminalService: terminalServiceMock,
}));

vi.mock("@wailsio/runtime", () => ({
  Clipboard: {
    SetText: runtimeMock.setClipboardText,
    Text: runtimeMock.clipboardText,
  },
  Events: { On: runtimeMock.on },
}));

vi.mock("@xterm/xterm", () => ({
  Terminal: class {
    options: Record<string, unknown> = {};
    attachCustomKeyEventHandler = vi.fn();
    dispose = vi.fn();
    focus = vi.fn();
    getSelection = vi.fn(() => "");
    open = vi.fn();
    resize = vi.fn();
    write = vi.fn();
    onData = vi.fn((callback: (data: string) => void) => {
      xtermMock.dataHandlers.push(callback);
      return { dispose: vi.fn() };
    });
  },
}));

import { TerminalPage } from "./TerminalPage";

describe("TerminalPage operation and session lifecycle", () => {
  beforeEach(() => {
    vi.useRealTimers();
    vi.clearAllMocks();
    runtimeMock.listeners.clear();
    xtermMock.dataHandlers.length = 0;
    runtimeMock.clipboardText.mockResolvedValue("");
    runtimeMock.setClipboardText.mockResolvedValue(undefined);
    settingsServiceMock.GetCheatsheet.mockResolvedValue([]);
    terminalServiceMock.CloseTerminal.mockResolvedValue(undefined);
    terminalServiceMock.DetectContainerShells.mockResolvedValue([]);
    terminalServiceMock.ListTerminalSessions.mockResolvedValue([]);
    terminalServiceMock.OpenBackendTerminal.mockResolvedValue(null);
    terminalServiceMock.OpenContainerTerminal.mockResolvedValue(null);
    terminalServiceMock.OpenHostTerminal.mockResolvedValue(null);
    terminalServiceMock.OpenProjectTerminal.mockResolvedValue(null);
    terminalServiceMock.ResizeTerminal.mockResolvedValue(undefined);
    terminalServiceMock.WriteTerminal.mockResolvedValue(undefined);
  });

  it("moves tab selection and focus through repeated arrows, Home, and End", async () => {
    terminalServiceMock.ListTerminalSessions.mockResolvedValue([
      terminalSession({ id: "alpha", title: "Alpha" }),
      terminalSession({ id: "bravo", title: "Bravo" }),
      terminalSession({ id: "charlie", title: "Charlie" }),
    ]);

    renderTerminalPage();
    const alpha = await screen.findByRole("tab", {
      name: "Alpha",
      selected: true,
    });
    alpha.focus();

    pressTerminalTabKey("ArrowRight", "Bravo");
    pressTerminalTabKey("ArrowRight", "Charlie");
    pressTerminalTabKey("ArrowRight", "Alpha");
    pressTerminalTabKey("ArrowLeft", "Charlie");
    pressTerminalTabKey("Home", "Alpha");
    pressTerminalTabKey("End", "Charlie");
  });

  it("keeps the only terminal tab selected and focused for every navigation key", async () => {
    terminalServiceMock.ListTerminalSessions.mockResolvedValue([
      terminalSession({ id: "alpha", title: "Alpha" }),
    ]);

    renderTerminalPage();
    const alpha = await screen.findByRole("tab", {
      name: "Alpha",
      selected: true,
    });
    alpha.focus();

    for (const key of ["ArrowLeft", "ArrowRight", "Home", "End"]) {
      pressTerminalTabKey(key, "Alpha");
    }
  });

  it("restores tab focus after closing non-active and active sessions", async () => {
    terminalServiceMock.ListTerminalSessions.mockResolvedValue([
      terminalSession({ id: "alpha", title: "Alpha" }),
      terminalSession({ id: "bravo", title: "Bravo" }),
      terminalSession({
        id: "charlie",
        kind: "container",
        title: "Charlie",
      }),
    ]);

    renderTerminalPage();
    await screen.findByRole("tab", { name: "Alpha", selected: true });

    const closeBravo = screen.getByRole("button", { name: "Close Bravo" });
    closeBravo.focus();
    fireEvent.click(closeBravo);
    await waitFor(() =>
      expect(
        screen.queryByRole("tab", { name: "Bravo" }),
      ).not.toBeInTheDocument(),
    );
    const alpha = screen.getByRole("tab", {
      name: "Alpha",
      selected: true,
    });
    expect(alpha).toHaveFocus();

    fireEvent.keyDown(alpha, { key: "ArrowRight" });
    const charlie = screen.getByRole("tab", {
      name: "Charlie",
      selected: true,
    });
    expect(charlie).toHaveFocus();

    const closeCharlie = screen.getByRole("button", {
      name: "Close Charlie",
    });
    closeCharlie.focus();
    fireEvent.click(closeCharlie);
    const closeDialog = await screen.findByRole("dialog", {
      name: "Close Terminal",
    });
    const confirmCloseButtons = within(closeDialog).getAllByRole("button", {
      name: "Close",
    });
    fireEvent.click(confirmCloseButtons[confirmCloseButtons.length - 1]);
    await waitFor(() =>
      expect(
        screen.queryByRole("tab", { name: "Charlie" }),
      ).not.toBeInTheDocument(),
    );
    expect(
      screen.getByRole("tab", { name: "Alpha", selected: true }),
    ).toHaveFocus();
    expect(terminalServiceMock.CloseTerminal).toHaveBeenNthCalledWith(
      1,
      "bravo",
    );
    expect(terminalServiceMock.CloseTerminal).toHaveBeenNthCalledWith(
      2,
      "charlie",
    );
  });

  it("catches typed-input failures and exposes stale-session recovery", async () => {
    terminalServiceMock.ListTerminalSessions.mockResolvedValue([
      terminalSession({ id: "alpha", title: "Alpha" }),
    ]);
    terminalServiceMock.WriteTerminal.mockRejectedValueOnce(
      new Error("terminal session closed"),
    );

    renderTerminalPage();
    await screen.findByRole("tab", { name: "Alpha" });

    await act(async () => {
      xtermMock.dataHandlers[0]?.("whoami");
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(terminalServiceMock.WriteTerminal).toHaveBeenCalledWith(
      "alpha",
      "d2hvYW1p",
    );
    expect(
      screen.getByText(
        "Unable to send terminal input: terminal session closed",
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByText(
        "Session is no longer available; close this tab and open a new terminal",
      ),
    ).toBeInTheDocument();
  });

  it("keeps guarded paste available for retry until the write succeeds", async () => {
    terminalServiceMock.ListTerminalSessions.mockResolvedValue([
      terminalSession({
        id: "root-shell",
        isRoot: true,
        kind: "container",
        title: "Root shell",
      }),
    ]);
    runtimeMock.clipboardText.mockResolvedValue("first\nsecond");
    terminalServiceMock.WriteTerminal.mockRejectedValueOnce(
      new Error("transport unavailable"),
    );

    renderTerminalPage();
    await screen.findByRole("tab", { name: /Root shell/ });
    fireEvent.click(screen.getByRole("button", { name: "Paste" }));

    const dialog = await screen.findByRole("dialog", {
      name: "Confirm Paste",
    });
    fireEvent.click(within(dialog).getByRole("button", { name: "Paste" }));

    expect(
      await within(dialog).findByText(
        "Unable to send terminal input: transport unavailable",
      ),
    ).toBeInTheDocument();
    expect(dialog).toBeInTheDocument();
    expect(
      within(dialog).getByRole("button", { name: "Cancel" }),
    ).toBeEnabled();

    fireEvent.click(within(dialog).getByRole("button", { name: "Paste" }));
    await waitFor(() => expect(dialog).not.toBeInTheDocument());
    expect(screen.getByText("Clipboard pasted")).toBeInTheDocument();
    expect(terminalServiceMock.WriteTerminal).toHaveBeenCalledTimes(2);
  });

  it("reports scheduled-command write failures without an unhandled rejection", async () => {
    terminalServiceMock.ListTerminalSessions.mockResolvedValue([
      terminalSession({ id: "alpha", title: "Alpha" }),
    ]);
    const onCommandConsumed = vi.fn();
    const view = renderTerminalPage({ onCommandConsumed });
    await screen.findByRole("tab", { name: "Alpha" });
    terminalServiceMock.WriteTerminal.mockRejectedValueOnce(
      new Error("backend offline"),
    );

    view.rerender(
      terminalPage({
        onCommandConsumed,
        queuedCommand: { command: "docker ps", id: 42 },
      }),
    );

    await waitFor(() => expect(onCommandConsumed).toHaveBeenCalledWith(42));
    expect(
      await screen.findByText(
        "Unable to send terminal input: backend offline",
        {},
        { timeout: 1800 },
      ),
    ).toBeInTheDocument();
    expect(terminalServiceMock.WriteTerminal).toHaveBeenCalledWith(
      "alpha",
      "ZG9ja2VyIHBzDQ==",
    );
  });

  it("catches resize failures, including an in-flight rejection after teardown", async () => {
    terminalServiceMock.ListTerminalSessions.mockResolvedValue([
      terminalSession({ id: "alpha", title: "Alpha" }),
    ]);
    terminalServiceMock.ResizeTerminal.mockRejectedValueOnce(
      new Error("resize transport down"),
    );

    const firstView = renderTerminalPage();
    expect(
      await screen.findByText(
        "Unable to resize terminal: resize transport down",
      ),
    ).toBeInTheDocument();
    firstView.unmount();

    const pendingResize = deferred<void>();
    terminalServiceMock.ResizeTerminal.mockImplementationOnce(
      () => pendingResize.promise,
    );
    const secondView = renderTerminalPage();
    await screen.findByRole("tab", { name: "Alpha" });
    await waitFor(() =>
      expect(terminalServiceMock.ResizeTerminal).toHaveBeenCalledTimes(2),
    );
    secondView.unmount();
    await act(async () => {
      pendingResize.reject(new Error("closed during teardown"));
      await Promise.resolve();
    });
  });

  it("derives a valid active tab when two close events arrive in one commit", async () => {
    terminalServiceMock.ListTerminalSessions.mockResolvedValue([
      terminalSession({ id: "alpha", title: "Alpha" }),
      terminalSession({ id: "bravo", title: "Bravo" }),
      terminalSession({ id: "charlie", title: "Charlie" }),
    ]);

    renderTerminalPage();
    expect(
      await screen.findByRole("tab", { name: "Alpha", selected: true }),
    ).toBeInTheDocument();
    screen.getByRole("tab", { name: "Alpha" }).focus();

    act(() => {
      emit("terminal:closed", { exitCode: 0, sessionID: "alpha" });
      emit("terminal:closed", { exitCode: 0, sessionID: "bravo" });
    });

    expect(
      await screen.findByRole("tab", { name: "Charlie", selected: true }),
    ).toHaveFocus();
    expect(
      screen.queryByRole("tab", { name: "Alpha" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("tab", { name: "Bravo" }),
    ).not.toBeInTheDocument();
  });
});

function renderTerminalPage(
  patch: Partial<React.ComponentProps<typeof TerminalPage>> = {},
) {
  return render(terminalPage(patch));
}

function terminalPage(
  patch: Partial<React.ComponentProps<typeof TerminalPage>> = {},
) {
  return (
    <TerminalPage
      containers={[]}
      initialSession={null}
      onCommandConsumed={vi.fn()}
      onInitialSessionConsumed={vi.fn()}
      projects={[]}
      queuedCommand={null}
      {...patch}
    />
  );
}

function terminalSession(
  patch: Partial<TerminalSessionInfo> = {},
): TerminalSessionInfo {
  return {
    createdAt: "2026-07-16T10:00:00Z",
    id: "terminal-1",
    isRoot: false,
    kind: "host",
    shell: "sh",
    title: "Host",
    ...patch,
  };
}

function emit(eventName: string, data: unknown) {
  for (const listener of runtimeMock.listeners.get(eventName) ?? []) {
    listener({ data, name: eventName });
  }
}

function pressTerminalTabKey(key: string, selectedName: string) {
  const focused = document.activeElement;
  if (!(focused instanceof HTMLElement)) {
    throw new Error("Expected a focused terminal tab");
  }
  fireEvent.keyDown(focused, { key });
  const selected = screen.getByRole("tab", {
    name: selectedName,
    selected: true,
  });
  expect(selected).toHaveFocus();
}

function deferred<T>() {
  let reject!: (reason?: unknown) => void;
  let resolve!: (value: T | PromiseLike<T>) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    reject = rejectPromise;
    resolve = resolvePromise;
  });
  return { promise, reject, resolve };
}
