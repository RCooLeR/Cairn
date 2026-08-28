import { useEffect, useEffectEvent } from "react";

import { Events } from "@wailsio/runtime";

export function useDebouncedRuntimeEvent<E extends Events.WailsEventName>(
  name: E,
  delayMs: number,
  callback: Events.WailsEventCallback<E>,
) {
  const emitLatest = useEffectEvent(callback);

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
          emitLatest(next);
        }
      }, delayMs);
    });
    return () => {
      pending = undefined;
      window.clearTimeout(timer);
      off();
    };
  }, [delayMs, name]);
}
