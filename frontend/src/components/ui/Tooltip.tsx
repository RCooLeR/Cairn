import type { ReactNode } from "react";

type TooltipProps = {
  label: string;
  children: ReactNode;
};

export function Tooltip({ children, label }: TooltipProps) {
  return (
    <span className="group relative inline-flex">
      {children}
      <span
        role="tooltip"
        className="pointer-events-none absolute bottom-full left-1/2 z-20 mb-2 hidden max-w-64 -translate-x-1/2 whitespace-nowrap rounded-control border border-border bg-bg-inset px-2 py-1 text-xs text-text-secondary shadow-none group-hover:block group-focus-within:block"
      >
        {label}
      </span>
    </span>
  );
}
