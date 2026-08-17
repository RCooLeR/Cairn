import type { ReactNode } from "react";

import { X } from "lucide-react";

import { Badge } from "./Badge";
import { Button } from "./Button";

export type ToastLevel = "ok" | "warn" | "error" | "info";

export type ToastProps = {
  level: ToastLevel;
  title: string;
  body?: string;
  action?: ReactNode;
  onDismiss: () => void;
};

export function Toast({ action, body, level, onDismiss, title }: ToastProps) {
  const live = level === "error" ? "assertive" : "polite";
  return (
    <div
      aria-live={live}
      className="w-80 rounded-card border border-border bg-bg-panel p-3"
      role={level === "error" ? "alert" : "status"}
    >
      <div className="flex items-center justify-between gap-3">
        <div className="text-sm font-semibold">{title}</div>
        <div className="flex items-center gap-1">
          <Badge tone={level}>{level}</Badge>
          <Button
            aria-label={`Dismiss ${title} notification`}
            icon={<X aria-hidden="true" size={15} />}
            onClick={onDismiss}
            size="icon"
            variant="ghost"
          />
        </div>
      </div>
      {body ? <p className="mt-2 text-sm text-text-secondary">{body}</p> : null}
      {action ? <div className="mt-3">{action}</div> : null}
    </div>
  );
}
