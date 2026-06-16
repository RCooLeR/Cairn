import { Toast } from "./Toast";

import type { ToastQueueItem } from "../../hooks/useToastQueue";

type ToastViewportProps = {
  toasts: ToastQueueItem[];
};

export function ToastViewport({ toasts }: ToastViewportProps) {
  if (toasts.length === 0) {
    return null;
  }
  return (
    <div className="fixed bottom-5 right-5 z-50 flex flex-col gap-2">
      {toasts.map((toast) => (
        <Toast
          action={toast.action}
          body={toast.body}
          key={toast.id}
          level={toast.level}
          title={toast.title}
        />
      ))}
    </div>
  );
}
