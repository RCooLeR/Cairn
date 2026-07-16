import { useEffect, useRef } from "react";

import { Events } from "@wailsio/runtime";

export function useDebouncedRuntimeEvent<E extends Events.WailsEventName>(
  name: E,
  delayMs: number,
  callback: Events.WailsEventCallback<E>,
) {
  const callbackRef = useRef(callback);
  useEffect(() => {
    callbackRef.current = callback;
  }, [callback]);

  useEffect(() => {
    let timer: number | undefined;
    let pending: Parameters<Events.WailsEventCallback<E>>[0] | undefined;
    const off = Events.On(name, (event) => {
      pending = event;
      window.clearTimeout(timer);
      timer = window.setTimeout(() => {
        const next = pending;
        pending = undefined;
        if (next) {
          callbackRef.current(next);
        }
      }, delayMs);
    });
    return () => {
      if (timer !== undefined && pending) {
        callbackRef.current(pending);
        pending = undefined;
      }
      window.clearTimeout(timer);
      off();
    };
  }, [delayMs, name]);
}
