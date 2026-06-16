import type { ReactNode } from "react";

import { X } from "lucide-react";
import { useEffect, useId, useRef } from "react";

import { Button } from "./Button";
import { cx } from "./utils";

type ModalProps = {
  open: boolean;
  title: string;
  children: ReactNode;
  footer?: ReactNode;
  busy?: boolean;
  danger?: boolean;
  onClose: () => void;
  size?: "sm" | "md" | "lg";
};

const focusableSelector = [
  "a[href]",
  "button:not([disabled])",
  "input:not([disabled])",
  "select:not([disabled])",
  "textarea:not([disabled])",
  '[tabindex]:not([tabindex="-1"])',
].join(",");

const sizeClasses: Record<NonNullable<ModalProps["size"]>, string> = {
  sm: "max-w-sm",
  md: "max-w-lg",
  lg: "max-w-3xl",
};

export function Modal({
  busy = false,
  children,
  danger = false,
  footer,
  onClose,
  open,
  size = "md",
  title,
}: ModalProps) {
  const panelRef = useRef<HTMLElement>(null);
  const titleID = useId();

  useEffect(() => {
    if (!open) {
      return undefined;
    }
    const previousFocus =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;
    const firstFocusable =
      panelRef.current?.querySelector<HTMLElement>(focusableSelector);
    (firstFocusable ?? panelRef.current)?.focus();

    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !busy) {
        onClose();
      }
      if (event.key !== "Tab" || !panelRef.current) {
        return;
      }

      const focusable = Array.from(
        panelRef.current.querySelectorAll<HTMLElement>(focusableSelector),
      );
      if (focusable.length === 0) {
        event.preventDefault();
        panelRef.current.focus();
        return;
      }

      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => {
      window.removeEventListener("keydown", onKeyDown);
      previousFocus?.focus();
    };
  }, [busy, onClose, open]);

  if (!open) {
    return null;
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-6">
      <section
        aria-labelledby={titleID}
        aria-modal="true"
        className={cx(
          "w-full rounded-card border border-border bg-bg-panel",
          sizeClasses[size],
        )}
        ref={panelRef}
        role="dialog"
        tabIndex={-1}
      >
        <header
          className={cx(
            "flex items-center justify-between border-b px-4 py-3",
            danger ? "border-error/40 text-error" : "border-border",
          )}
        >
          <h2 className="text-base font-semibold" id={titleID}>
            {title}
          </h2>
          <Button
            aria-label="Close"
            disabled={busy}
            disabledReason="Action is running"
            icon={<X size={16} />}
            onClick={onClose}
            size="icon"
            variant="ghost"
          />
        </header>
        <div className="p-4 text-sm text-text-secondary">{children}</div>
        {footer ? (
          <footer className="border-t border-border px-4 py-3">{footer}</footer>
        ) : null}
      </section>
    </div>
  );
}
