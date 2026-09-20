import { useSyncExternalStore } from "react";

function subscribe(onChange: () => void) {
  document.addEventListener("visibilitychange", onChange);
  return () => document.removeEventListener("visibilitychange", onChange);
}

function getSnapshot() {
  return document.visibilityState !== "hidden";
}

/** Hidden and minimized windows do not need a live presentation feed. */
export function useDocumentVisible() {
  return useSyncExternalStore(subscribe, getSnapshot, () => true);
}
