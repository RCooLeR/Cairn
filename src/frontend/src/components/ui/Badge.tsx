import type { ReactNode } from "react";

import { cx } from "./utils";

type BadgeTone = "ok" | "warn" | "error" | "info" | "neutral" | "accent";

type BadgeProps = {
  children: ReactNode;
  tone?: BadgeTone;
  pulse?: boolean;
};

const toneClasses: Record<BadgeTone, string> = {
  ok: "border-ok/25 bg-ok/10 text-ok",
  warn: "border-warn/25 bg-warn/10 text-warn",
  error: "border-error/25 bg-error/10 text-error",
  info: "border-info/25 bg-info/10 text-info",
  neutral: "border-border bg-bg-inset text-text-muted",
  accent: "border-accent/25 bg-accent/10 text-accent",
};

export function Badge({
  children,
  pulse = false,
  tone = "neutral",
}: BadgeProps) {
  return (
    <span
      className={cx(
        "inline-flex h-6 items-center rounded-full border px-2 text-xs font-medium",
        toneClasses[tone],
        pulse && "animate-pulse",
      )}
    >
      {children}
    </span>
  );
}
