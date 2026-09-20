import { Toast } from "./Toast";

import type { ToastQueueItem } from "../../hooks/useToastQueue";

type ToastViewportProps = {
  onDismiss: (id: string) => void;
  toasts: ToastQueueItem[];
};

export function ToastViewport({ onDismiss, toasts }: ToastViewportProps) {
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
          onDismiss={() => onDismiss(toast.id)}
          title={toast.title}
        />
      ))}
    </div>
  );
}
