import { useCallback, useEffect, useRef, useState } from "react";

import type { ReactNode } from "react";
import type { ToastLevel } from "../components/ui/Toast";

export type ToastQueueItem = {
  id: string;
  level: ToastLevel;
  title: string;
  body?: string;
  action?: ReactNode;
};

export type ToastInput = Omit<ToastQueueItem, "id"> & {
  id?: string;
  ttlMS?: number;
};

const defaultToastTTL = 3200;
const maxToastCount = 4;

export function useToastQueue() {
  const [toasts, setToasts] = useState<ToastQueueItem[]>([]);
  const nextID = useRef(0);
  const timers = useRef(new Map<string, number>());

  const dismissToast = useCallback((id: string) => {
    const timer = timers.current.get(id);
    if (timer !== undefined) {
      window.clearTimeout(timer);
      timers.current.delete(id);
    }
    setToasts((current) => current.filter((toast) => toast.id !== id));
  }, []);

  const pushToast = useCallback(
    (toast: ToastInput) => {
      const id = toast.id ?? `toast-${++nextID.current}`;
      const ttlMS = toast.ttlMS ?? defaultToastTTL;
      setToasts((current) => {
        const next = current
          .filter((queued) => queued.id !== id)
          .concat({ ...toast, id })
          .slice(-maxToastCount);
        const nextIDs = new Set(next.map((queued) => queued.id));
        for (const queued of current) {
          if (!nextIDs.has(queued.id)) {
            const timer = timers.current.get(queued.id);
            if (timer !== undefined) {
              window.clearTimeout(timer);
              timers.current.delete(queued.id);
            }
          }
        }
        return next;
      });
      const existingTimer = timers.current.get(id);
      if (existingTimer !== undefined) {
        window.clearTimeout(existingTimer);
      }
      timers.current.set(
        id,
        window.setTimeout(() => dismissToast(id), ttlMS),
      );
      return id;
    },
    [dismissToast],
  );

  useEffect(
    () => () => {
      for (const timer of timers.current.values()) {
        window.clearTimeout(timer);
      }
      timers.current.clear();
    },
    [],
  );

  return { dismissToast, pushToast, toasts };
}
