import { Events } from "@wailsio/runtime";
import { useEffect } from "react";

import { decodeBase64Bytes } from "./terminalEncoding";

const maxBufferedSessions = 32;
const maxSessionBytes = 128 * 1024;
const maxSessionChunks = 512;
const maxEventBase64Length = 1024 * 1024;
const base64Pattern =
  /^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/;

type OutputListener = (data: Uint8Array) => void;
type SessionOutput = {
  bytes: number;
  chunks: Uint8Array[];
  listeners: Set<OutputListener>;
};

const outputs = new Map<string, SessionOutput>();
const closedSessions = new Set<string>();
let captureOwners = 0;
let stopCapture: (() => void) | null = null;

// App owns capture for its full lifetime; TerminalPage also owns it so isolated
// previews/tests work. Buffering before a tab mounts preserves the shell prompt
// emitted before OpenTerminal resolves, and preserves output during navigation.
export function useTerminalOutputCapture() {
  useEffect(() => {
    captureOwners += 1;
    if (!stopCapture) {
      const offData = Events.On("terminal:data", (event) => {
        const payload = eventPayload(event);
        if (
          !payload ||
          !validSessionID(payload.sessionID) ||
          closedSessions.has(payload.sessionID) ||
          typeof payload.dataBase64 !== "string" ||
          payload.dataBase64.length > maxEventBase64Length ||
          payload.dataBase64.length % 4 !== 0 ||
          !base64Pattern.test(payload.dataBase64)
        ) {
          return;
        }
        const data = decodeBase64Bytes(payload.dataBase64);
        if (data.length === 0) {
          return;
        }
        const output = sessionOutput(payload.sessionID);
        if (!output) {
          return;
        }
        output.chunks.push(data);
        output.bytes += data.length;
        while (
          output.bytes > maxSessionBytes ||
          output.chunks.length > maxSessionChunks
        ) {
          const first = output.chunks[0];
          const excess = output.bytes - maxSessionBytes;
          if (
            first.length <= excess ||
            output.chunks.length > maxSessionChunks
          ) {
            output.chunks.shift();
            output.bytes -= first.length;
          } else {
            output.chunks[0] = first.slice(excess);
            output.bytes -= excess;
          }
        }
        for (const listener of output.listeners) {
          listener(data);
        }
      });
      const offClosed = Events.On("terminal:closed", (event) => {
        const payload = eventPayload(event);
        if (
          payload &&
          validSessionID(payload.sessionID) &&
          typeof payload.exitCode === "number" &&
          Number.isFinite(payload.exitCode)
        ) {
          outputs.delete(payload.sessionID);
          closedSessions.add(payload.sessionID);
          if (closedSessions.size > 1024) {
            const oldest = closedSessions.values().next().value;
            if (oldest) {
              closedSessions.delete(oldest);
            }
          }
        }
      });
      stopCapture = () => {
        offData();
        offClosed();
      };
    }
    return () => {
      captureOwners -= 1;
      if (captureOwners === 0) {
        stopCapture?.();
        stopCapture = null;
        outputs.clear();
        closedSessions.clear();
      }
    };
  }, []);
}

export function isTerminalSessionClosed(sessionID: string) {
  return closedSessions.has(sessionID);
}

export function subscribeTerminalOutput(
  sessionID: string,
  listener: OutputListener,
) {
  const output = sessionOutput(sessionID);
  if (!output) {
    return () => {};
  }
  output.listeners.add(listener);
  for (const chunk of output.chunks) {
    listener(chunk);
  }
  return () => {
    output.listeners.delete(listener);
  };
}

function sessionOutput(sessionID: string) {
  const existing = outputs.get(sessionID);
  if (existing) {
    return existing;
  }
  if (outputs.size >= maxBufferedSessions) {
    const oldest = Array.from(outputs).find(
      ([, output]) => output.listeners.size === 0,
    );
    if (!oldest) {
      return null;
    }
    outputs.delete(oldest[0]);
  }
  const output: SessionOutput = {
    bytes: 0,
    chunks: [],
    listeners: new Set(),
  };
  outputs.set(sessionID, output);
  return output;
}

function eventPayload(event: unknown): Record<string, unknown> | null {
  if (!event || typeof event !== "object" || !("data" in event)) {
    return null;
  }
  const payload = event.data;
  return payload && typeof payload === "object" && !Array.isArray(payload)
    ? (payload as Record<string, unknown>)
    : null;
}

function validSessionID(value: unknown): value is string {
  return (
    typeof value === "string" && value.length <= 4096 && Boolean(value.trim())
  );
}
