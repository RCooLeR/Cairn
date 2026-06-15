import { useEffect } from "react";

import { Events } from "@wailsio/runtime";

export function useDebouncedRuntimeEvent<E extends Events.WailsEventName>(
  name: E,
  delayMs: number,
  callback: Events.WailsEventCallback<E>,
) {
  useEffect(() => {
    let timer: number | undefined;
    const off = Events.On(name, (event) => {
      window.clearTimeout(timer);
      timer = window.setTimeout(() => {
        callback(event);
      }, delayMs);
    });
    return () => {
      window.clearTimeout(timer);
      off();
    };
  }, [callback, delayMs, name]);
}
