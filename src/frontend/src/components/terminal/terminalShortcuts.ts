type TerminalShortcutEvent = Pick<
  KeyboardEvent,
  "altKey" | "ctrlKey" | "key" | "metaKey" | "shiftKey"
>;

export function isTerminalCopyShortcut(event: TerminalShortcutEvent) {
  if (event.altKey) {
    return false;
  }
  const key = event.key.toLowerCase();
  return (
    (event.ctrlKey && event.shiftKey && !event.metaKey && key === "c") ||
    (event.metaKey && !event.ctrlKey && key === "c") ||
    (event.ctrlKey && !event.metaKey && event.key === "Insert")
  );
}

export function isTerminalPasteShortcut(event: TerminalShortcutEvent) {
  if (event.altKey) {
    return false;
  }
  const key = event.key.toLowerCase();
  return (
    (event.ctrlKey && event.shiftKey && !event.metaKey && key === "v") ||
    (event.metaKey && !event.ctrlKey && key === "v") ||
    (event.shiftKey && !event.metaKey && event.key === "Insert")
  );
}
