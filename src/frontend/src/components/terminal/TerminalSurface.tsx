import type { Ref } from "react";
import { useEffect, useEffectEvent, useImperativeHandle, useRef } from "react";
import type { TerminalSessionInfo } from "../../../bindings/github.com/RCooLeR/Cairn/internal/models/models.js";
import { Terminal as XTerm } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import { subscribeTerminalOutput } from "./terminalOutput";
import {
  isTerminalCopyShortcut,
  isTerminalPasteShortcut,
} from "./terminalShortcuts";

export type TerminalSurfaceHandle = {
  focus: () => void;
  getSelection: () => string;
};

type TerminalSurfaceProps = {
  active: boolean;
  onCopyShortcut: (session: TerminalSessionInfo) => Promise<void>;
  onInput: (session: TerminalSessionInfo, data: string) => Promise<unknown>;
  onPasteShortcut: (session: TerminalSessionInfo) => Promise<void>;
  onResize: (
    session: TerminalSessionInfo,
    cols: number,
    rows: number,
  ) => Promise<unknown>;
  ref?: Ref<TerminalSurfaceHandle>;
  session: TerminalSessionInfo;
};

export default function TerminalSurface({
  active,
  onCopyShortcut,
  onInput,
  onPasteShortcut,
  onResize,
  ref,
  session,
}: TerminalSurfaceProps) {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const terminalRef = useRef<XTerm | null>(null);
  const resizeTimer = useRef<number | null>(null);
  const copyShortcut = useEffectEvent(() => onCopyShortcut(session));
  const input = useEffectEvent((data: string) => onInput(session, data));
  const pasteShortcut = useEffectEvent(() => onPasteShortcut(session));
  const reportResize = useEffectEvent((cols: number, rows: number) =>
    onResize(session, cols, rows),
  );

  useImperativeHandle(
    ref,
    () => ({
      focus: () => terminalRef.current?.focus(),
      getSelection: () => terminalRef.current?.getSelection() ?? "",
    }),
    [],
  );

  useEffect(() => {
    if (!hostRef.current) {
      return undefined;
    }
    const terminal = new XTerm({
      allowProposedApi: false,
      convertEol: true,
      cursorBlink: true,
      fontFamily:
        "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace",
      fontSize: 13,
      scrollback: 10000,
      theme: terminalThemeFromCSS(),
    });
    terminal.attachCustomKeyEventHandler((event) => {
      if (event.type !== "keydown") {
        return true;
      }
      if (isTerminalCopyShortcut(event)) {
        event.preventDefault();
        void copyShortcut();
        return false;
      }
      if (isTerminalPasteShortcut(event)) {
        event.preventDefault();
        void pasteShortcut();
        return false;
      }
      return true;
    });
    terminal.open(hostRef.current);
    terminalRef.current = terminal;
    const disposable = terminal.onData((data) => {
      void input(data);
    });
    const resize = () => {
      if (!hostRef.current) {
        return;
      }
      const rect = hostRef.current.getBoundingClientRect();
      const cols = Math.max(40, Math.floor(rect.width / 8.2));
      const rows = Math.max(10, Math.floor(rect.height / 17.5));
      terminal.resize(cols, rows);
      if (resizeTimer.current !== null) {
        window.clearTimeout(resizeTimer.current);
      }
      resizeTimer.current = window.setTimeout(() => {
        void reportResize(cols, rows);
      }, 100);
    };
    resize();
    let observer: ResizeObserver | null = null;
    if (typeof ResizeObserver !== "undefined") {
      observer = new ResizeObserver(resize);
      observer.observe(hostRef.current);
    }
    let themeObserver: MutationObserver | null = null;
    const applyTheme = () => {
      if (terminal.options) {
        terminal.options.theme = terminalThemeFromCSS();
      }
    };
    if (typeof MutationObserver !== "undefined") {
      themeObserver = new MutationObserver(applyTheme);
      themeObserver.observe(document.documentElement, {
        attributeFilter: ["data-theme", "style"],
        attributes: true,
      });
    }
    return () => {
      if (resizeTimer.current !== null) {
        window.clearTimeout(resizeTimer.current);
      }
      observer?.disconnect();
      themeObserver?.disconnect();
      disposable.dispose();
      terminal.dispose();
      terminalRef.current = null;
    };
  }, [session.id]);

  useEffect(() => {
    return subscribeTerminalOutput(session.id, (data) => {
      terminalRef.current?.write(data);
    });
  }, [session.id]);

  return (
    <div
      aria-labelledby={`terminal-tab-${session.id}`}
      className={active ? "absolute inset-0 p-2" : "hidden"}
      data-terminal-session={session.id}
      id={`terminal-panel-${session.id}`}
      ref={hostRef}
      role="tabpanel"
    />
  );
}

function terminalThemeFromCSS() {
  return {
    background: rgbVariable("--terminal-bg", "rgb(7, 10, 15)"),
    foreground: rgbVariable("--terminal-fg", "rgb(214, 222, 235)"),
    cursor: rgbVariable("--terminal-cursor", "rgb(45, 212, 167)"),
    selectionBackground: rgbVariable(
      "--terminal-selection",
      "rgba(45, 212, 167, 0.27)",
      0.27,
    ),
  };
}

function rgbVariable(name: string, fallback: string, alpha?: number) {
  if (typeof window === "undefined") {
    return fallback;
  }
  const raw = window
    .getComputedStyle(document.documentElement)
    .getPropertyValue(name)
    .trim();
  const channels = raw.split(/\s+/).map(Number);
  if (
    channels.length !== 3 ||
    channels.some((channel) => !Number.isFinite(channel))
  ) {
    return fallback;
  }
  const [red, green, blue] = channels;
  if (alpha === undefined) {
    return `rgb(${red}, ${green}, ${blue})`;
  }
  return `rgba(${red}, ${green}, ${blue}, ${alpha})`;
}
