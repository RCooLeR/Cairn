import type {
  Risk,
  TerminalSessionInfo,
} from "../../../bindings/github.com/RCooLeR/Cairn/internal/models/models.js";

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
  selection: "",
  writes: [] as Uint8Array[],
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
    getSelection = vi.fn(() => xtermMock.selection);
    open = vi.fn();
    resize = vi.fn();
    write = vi.fn((data: Uint8Array) => xtermMock.writes.push(data));
    onData = vi.fn((callback: (data: string) => void) => {
      xtermMock.dataHandlers.push(callback);
      return { dispose: vi.fn() };
    });
  },
}));

import {
  ClipboardProvider,
  type CopyText,
  writeClipboardText,
} from "../../hooks/useClipboard";
import { TerminalPage } from "./TerminalPage";

const terminalTestCopyText: CopyText = (text) =>
  writeClipboardText(text, { SetText: runtimeMock.setClipboardText });

describe("TerminalPage operation and session lifecycle", () => {
  beforeEach(() => {
    vi.useRealTimers();
    vi.clearAllMocks();
    runtimeMock.listeners.clear();
    xtermMock.dataHandlers.length = 0;
    xtermMock.selection = "";
    xtermMock.writes.length = 0;
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

  it("preserves a session opened while the initial session list is pending", async () => {
    const pendingSessions = deferred<TerminalSessionInfo[]>();
    const initialSession = terminalSession({
      id: "new-session",
      title: "New session",
    });
    terminalServiceMock.ListTerminalSessions.mockReturnValue(
      pendingSessions.promise,
    );

    renderTerminalPage({ initialSession });
    expect(
      await screen.findByRole("tab", {
        name: "New session",
        selected: true,
      }),
    ).toBeInTheDocument();

    await act(async () => {
      pendingSessions.resolve([
        terminalSession({ id: "older-session", title: "Older session" }),
      ]);
      await pendingSessions.promise;
    });

    expect(
      screen.getByRole("tab", { name: "New session", selected: true }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("tab", { name: "Older session" }),
    ).toBeInTheDocument();
  });

  it("closes a terminal whose open request resolves after navigation", async () => {
    const pendingOpen = deferred<TerminalSessionInfo | null>();
    const lateSession = terminalSession({
      id: "late-host",
      title: "Late host",
    });
    terminalServiceMock.OpenHostTerminal.mockReturnValueOnce(
      pendingOpen.promise,
    );

    const firstView = renderTerminalPage();
    fireEvent.click(await screen.findByRole("button", { name: "Host" }));
    await waitFor(() =>
      expect(terminalServiceMock.OpenHostTerminal).toHaveBeenCalledWith({
        cols: 120,
        rows: 30,
      }),
    );
    firstView.unmount();

    renderTerminalPage();
    await waitFor(() =>
      expect(terminalServiceMock.ListTerminalSessions).toHaveBeenCalledTimes(2),
    );
    expect(screen.queryByRole("tab", { name: "Late host" })).toBeNull();

    await act(async () => {
      pendingOpen.resolve(lateSession);
      await pendingOpen.promise;
      await Promise.resolve();
    });

    await waitFor(() =>
      expect(terminalServiceMock.CloseTerminal).toHaveBeenCalledWith(
        "late-host",
      ),
    );
    expect(screen.queryByRole("tab", { name: "Late host" })).toBeNull();
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
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Unable to send terminal input: terminal session closed",
    );
    expect(screen.getByRole("status")).toHaveTextContent(
      "Session is no longer available; close this tab and open a new terminal",
    );
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

    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "Unable to send terminal input: transport unavailable",
    );
    expect(dialog).toBeInTheDocument();
    expect(
      within(dialog).getByRole("button", { name: "Cancel" }),
    ).toBeEnabled();

    fireEvent.click(within(dialog).getByRole("button", { name: "Paste" }));
    await waitFor(() => expect(dialog).not.toBeInTheDocument());
    expect(screen.getByText("Clipboard pasted")).toBeInTheDocument();
    expect(terminalServiceMock.WriteTerminal).toHaveBeenCalledTimes(2);
  });

  it("replaces terminal copy success when the next attempt fails", async () => {
    terminalServiceMock.ListTerminalSessions.mockResolvedValue([
      terminalSession({ id: "alpha", title: "Alpha" }),
    ]);
    xtermMock.selection = "selected output";

    renderTerminalPage();
    await screen.findByRole("tab", { name: "Alpha" });
    fireEvent.click(screen.getByRole("button", { name: "Copy" }));

    await waitFor(() =>
      expect(runtimeMock.setClipboardText).toHaveBeenCalledWith(
        "selected output",
      ),
    );
    expect(
      await screen.findByText("Terminal selection copied"),
    ).toBeInTheDocument();

    runtimeMock.setClipboardText.mockRejectedValueOnce(new Error("denied"));
    fireEvent.click(screen.getByRole("button", { name: "Copy" }));

    expect(
      await screen.findByText("Cairn could not write to the system clipboard."),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("Terminal selection copied"),
    ).not.toBeInTheDocument();
  });

  it("replaces command copy failure when the next attempt succeeds", async () => {
    settingsServiceMock.GetCheatsheet.mockResolvedValue([
      {
        category: "Safety",
        command: "docker system prune",
        description: "Remove unused Docker objects",
        risk: "dangerous" as Risk,
        runnable: false,
      },
    ]);
    runtimeMock.setClipboardText.mockRejectedValueOnce(new Error("denied"));

    renderTerminalPage();
    const commandRow = (await screen.findByText("docker system prune")).closest(
      "div.rounded-control",
    );
    expect(commandRow).not.toBeNull();
    const copyCommandButton = within(commandRow as HTMLElement).getByRole(
      "button",
      { name: "Copy" },
    );
    fireEvent.click(copyCommandButton);

    expect(
      await screen.findByText("Cairn could not write to the system clipboard."),
    ).toBeInTheDocument();
    expect(screen.queryByText("Command copied")).not.toBeInTheDocument();

    fireEvent.click(copyCommandButton);

    expect(await screen.findByText("Command copied")).toBeInTheDocument();
    expect(
      screen.queryByText("Cairn could not write to the system clipboard."),
    ).not.toBeInTheDocument();
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

  it("rejects malformed and oversized terminal output and close events", async () => {
    terminalServiceMock.ListTerminalSessions.mockResolvedValue([
      terminalSession({ id: "alpha", title: "Alpha" }),
    ]);

    renderTerminalPage();
    expect(
      await screen.findByRole("tab", { name: "Alpha", selected: true }),
    ).toBeInTheDocument();

    act(() => {
      emit("terminal:data", {
        dataBase64: btoa("accepted output"),
        sessionID: "alpha",
      });
      emit("terminal:data", {
        dataBase64: "%%%%",
        sessionID: "alpha",
      });
      emit("terminal:data", {
        dataBase64: "A".repeat(1024 * 1024 + 4),
        sessionID: "alpha",
      });
      emit("terminal:closed", {
        exitCode: 99,
        sessionID: "x".repeat(4097),
      });
    });

    expect(xtermMock.writes).toHaveLength(1);
    expect(new TextDecoder().decode(xtermMock.writes[0])).toBe(
      "accepted output",
    );
    expect(
      screen.getByRole("tab", { name: "Alpha", selected: true }),
    ).toBeInTheDocument();
    expect(screen.queryByText("Session exited with code 99")).toBeNull();
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
    <ClipboardProvider copyText={terminalTestCopyText}>
      <TerminalPage
        containers={[]}
        initialSession={null}
        onCommandConsumed={vi.fn()}
        onInitialSessionConsumed={vi.fn()}
        projects={[]}
        queuedCommand={null}
        {...patch}
      />
    </ClipboardProvider>
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
