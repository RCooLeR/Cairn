import { useEffect, useEffectEvent } from "react";

import { Events } from "@wailsio/runtime";

export function useDebouncedRuntimeEvent<E extends Events.WailsEventName>(
  name: E,
  delayMs: number,
  callback: (
    event: Parameters<Events.WailsEventCallback<E>>[0],
  ) => void | PromiseLike<unknown>,
  merge?: (
    previous: Parameters<Events.WailsEventCallback<E>>[0],
    next: Parameters<Events.WailsEventCallback<E>>[0],
  ) => Parameters<Events.WailsEventCallback<E>>[0],
  { foregroundOnly = false }: { foregroundOnly?: boolean } = {},
) {
  const emitLatest = useEffectEvent(callback);
  const mergeLatest = useEffectEvent(
    (
      previous: Parameters<Events.WailsEventCallback<E>>[0],
      next: Parameters<Events.WailsEventCallback<E>>[0],
    ) => (merge ? merge(previous, next) : next),
  );

  useEffect(() => {
    let timer: number | undefined;
    let maximumWaitTimer: number | undefined;
    let active = true;
    let running = false;
    let ready = false;
    let pending: Parameters<Events.WailsEventCallback<E>>[0] | undefined;
    const runPending = () => {
      if (
        !active ||
        running ||
        !ready ||
        !pending ||
        (foregroundOnly && document.visibilityState === "hidden")
      ) {
        return;
      }
      const next = pending;
      pending = undefined;
      ready = false;
      window.clearTimeout(timer);
      window.clearTimeout(maximumWaitTimer);
      timer = undefined;
      maximumWaitTimer = undefined;
      running = true;
      const finish = () => {
        running = false;
        runPending();
      };
      // Slow reads must finish before another event invalidates them. Keep one
      // merged trailing event instead of piling up requests under daemon load.
      try {
        const result = emitLatest(next);
        if (result) {
          void Promise.resolve(result).then(finish, finish);
        } else {
          finish();
        }
      } catch {
        finish();
      }
    };
    const flush = () => {
      window.clearTimeout(timer);
      window.clearTimeout(maximumWaitTimer);
      timer = undefined;
      maximumWaitTimer = undefined;
      ready = true;
      runPending();
    };
    const off = Events.On(name, (event) => {
      pending = pending ? mergeLatest(pending, event) : event;
      if (foregroundOnly && document.visibilityState === "hidden") {
        // Keep just the merged invalidation while in the tray. A busy daemon
        // must not wake the webview or issue background inventory RPCs.
        ready = true;
        return;
      }
      window.clearTimeout(timer);
      timer = window.setTimeout(flush, delayMs);
      // A busy daemon can emit continuously. Keep a bound on staleness even
      // if its event stream never provides a full debounce interval of quiet.
      maximumWaitTimer ??= window.setTimeout(flush, delayMs * 4);
    });
    const onVisibilityChange = () => {
      if (document.visibilityState !== "hidden") {
        flush();
      } else {
        window.clearTimeout(timer);
        window.clearTimeout(maximumWaitTimer);
        timer = undefined;
        maximumWaitTimer = undefined;
        ready = true;
      }
    };
    if (foregroundOnly) {
      document.addEventListener("visibilitychange", onVisibilityChange);
    }
    return () => {
      active = false;
      pending = undefined;
      window.clearTimeout(timer);
      window.clearTimeout(maximumWaitTimer);
      document.removeEventListener("visibilitychange", onVisibilityChange);
      off();
    };
  }, [delayMs, foregroundOnly, name]);
}
