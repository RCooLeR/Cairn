import type { ReactNode } from "react";

import { cx } from "./utils";

type CardProps = {
  children: ReactNode;
  className?: string;
};

type CardHeaderProps = {
  title: ReactNode;
  status?: ReactNode;
  actions?: ReactNode;
};

export function Card({ children, className }: CardProps) {
  return (
    <article
      className={cx(
        "min-w-0 rounded-card border border-border bg-bg-card",
        className,
      )}
    >
      {children}
    </article>
  );
}

export function CardHeader({ actions, status, title }: CardHeaderProps) {
  return (
    <div className="flex min-h-12 items-center justify-between gap-3 border-b border-border px-4 py-3">
      <div className="min-w-0 text-sm font-semibold">{title}</div>
      <div className="flex shrink-0 items-center gap-2">
        {status}
        {actions}
      </div>
    </div>
  );
}

export function CardBody({ children, className }: CardProps) {
  return <div className={cx("min-w-0 p-4", className)}>{children}</div>;
}

export function CardFooter({ children, className }: CardProps) {
  return (
    <div className={cx("border-t border-border px-4 py-3", className)}>
      {children}
    </div>
  );
}
