import { describe, expect, it } from "vitest";

import {
  isTerminalCopyShortcut,
  isTerminalPasteShortcut,
} from "./TerminalPage";
import { decodeBase64Bytes, encodeTerminalInput } from "./terminalEncoding";

describe("terminal encoding helpers", () => {
  it("round-trips UTF-8 terminal data without mojibake", () => {
    const text = "héllo Привет 你好\n";

    const bytes = decodeBase64Bytes(encodeTerminalInput(text));

    expect(new TextDecoder().decode(bytes)).toBe(text);
  });
});

describe("terminal clipboard shortcuts", () => {
  it("copies only with terminal-safe copy shortcuts", () => {
    expect(shortcutCopy({ ctrlKey: true, key: "c" })).toBe(false);
    expect(shortcutCopy({ ctrlKey: true, shiftKey: true, key: "c" })).toBe(
      true,
    );
    expect(shortcutCopy({ metaKey: true, key: "c" })).toBe(true);
    expect(shortcutCopy({ ctrlKey: true, key: "Insert" })).toBe(true);
  });

  it("pastes with terminal-safe paste shortcuts", () => {
    expect(shortcutPaste({ ctrlKey: true, key: "v" })).toBe(false);
    expect(shortcutPaste({ ctrlKey: true, shiftKey: true, key: "v" })).toBe(
      true,
    );
    expect(shortcutPaste({ metaKey: true, key: "v" })).toBe(true);
    expect(shortcutPaste({ shiftKey: true, key: "Insert" })).toBe(true);
  });
});

function shortcutCopy(patch: Partial<KeyboardEvent>) {
  return isTerminalCopyShortcut(shortcutEvent(patch));
}

function shortcutPaste(patch: Partial<KeyboardEvent>) {
  return isTerminalPasteShortcut(shortcutEvent(patch));
}

function shortcutEvent(patch: Partial<KeyboardEvent>) {
  return {
    altKey: false,
    ctrlKey: false,
    key: "",
    metaKey: false,
    shiftKey: false,
    ...patch,
  } as KeyboardEvent;
}
