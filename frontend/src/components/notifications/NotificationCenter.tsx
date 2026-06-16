import { Bell } from "lucide-react";
import { useRef } from "react";

import type { Notification } from "../../../bindings/github.com/RCooLeR/Cairn/internal/models/models.js";
import { useFocusTrap } from "../../hooks/useFocusTrap";
import type { PageID } from "../../types/navigation";
import { Badge, Button, EmptyState, TableSkeleton } from "../ui";

type BadgeTone = "ok" | "warn" | "error" | "info" | "neutral" | "accent";

type NotificationCenterProps = {
  error: string | null;
  loading: boolean;
  notifications: Notification[];
  onClose: () => void;
  onMarkAllRead: () => void;
  onNavigate: (page: PageID) => void;
  open: boolean;
};

export function NotificationCenter({
  error,
  loading,
  notifications,
  onClose,
  onMarkAllRead,
  onNavigate,
  open,
}: NotificationCenterProps) {
  const panelRef = useRef<HTMLDivElement | null>(null);

  useFocusTrap(open, panelRef, onClose);

  if (!open) {
    return null;
  }

  const unread = notifications.filter(
    (notification) => !notification.read,
  ).length;

  return (
    <div
      aria-label="Notification center"
      aria-modal="true"
      className="absolute right-0 top-11 z-40 w-[min(360px,calc(100vw-2rem))] rounded-card border border-border bg-bg-panel shadow-2xl"
      ref={panelRef}
      role="dialog"
      tabIndex={-1}
    >
      <div className="flex items-center justify-between gap-3 border-b border-border px-4 py-3">
        <div>
          <div className="font-medium text-text-primary">Notifications</div>
          <div className="text-xs text-text-muted">{unread} unread</div>
        </div>
        <div className="flex gap-2">
          <Button
            disabled={unread === 0 || loading}
            disabledReason="No unread notifications"
            onClick={onMarkAllRead}
            size="sm"
            variant="secondary"
          >
            Mark all read
          </Button>
          <Button onClick={onClose} size="sm" variant="ghost">
            Close
          </Button>
        </div>
      </div>
      <div className="max-h-[420px] overflow-y-auto p-2">
        {error ? (
          <div className="rounded-control border border-error/30 bg-error/10 px-3 py-2 text-sm text-error">
            {error}
          </div>
        ) : null}
        {loading && notifications.length === 0 ? <TableSkeleton /> : null}
        {!loading && notifications.length === 0 ? (
          <EmptyState
            body="Provider, update, backup, and system messages appear here."
            icon={<Bell size={26} />}
            title="No notifications"
          />
        ) : null}
        {notifications.map((notification) => {
          const target = notificationTargetPage(notification.topic);
          return (
            <button
              className={[
                "mb-2 block w-full rounded-control border p-3 text-left text-sm transition",
                notification.read
                  ? "border-border bg-bg-inset text-text-secondary"
                  : "border-accent/30 bg-accent/10 text-text-primary",
                target ? "hover:border-border-strong" : "",
              ].join(" ")}
              key={notification.id}
              onClick={() => {
                if (target) {
                  onNavigate(target);
                }
              }}
              type="button"
            >
              <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                  <div className="flex items-center gap-2">
                    <Badge tone={notificationTone(notification.level)}>
                      {notification.level || "info"}
                    </Badge>
                    <span className="truncate font-medium">
                      {notification.title}
                    </span>
                  </div>
                  {notification.body ? (
                    <div className="mt-2 text-text-muted">
                      {notification.body}
                    </div>
                  ) : null}
                </div>
                {!notification.read ? (
                  <span className="mt-1 h-2 w-2 rounded-full bg-warn" />
                ) : null}
              </div>
              <div className="mt-2 flex items-center justify-between gap-2 text-xs text-text-muted">
                <span>{notification.topic || "system"}</span>
                <span>{relativeTime(dateMillis(notification.createdAt))}</span>
              </div>
            </button>
          );
        })}
      </div>
    </div>
  );
}

function notificationTone(level: string): BadgeTone {
  switch (level) {
    case "ok":
    case "success":
      return "ok";
    case "warn":
    case "warning":
      return "warn";
    case "error":
      return "error";
    case "info":
      return "info";
    default:
      return "neutral";
  }
}

function notificationTargetPage(topic: string): PageID | null {
  switch (topic) {
    case "app-update":
      return "settings";
    case "backup":
      return "volumes";
    case "project":
      return "projects";
    case "provider":
    case "system":
      return "overview";
    case "update":
      return "updates";
    default:
      return null;
  }
}

function dateMillis(value: unknown) {
  const date = toDate(value);
  return date?.getTime() ?? 0;
}

function relativeTime(value: number) {
  const diff = Math.max(0, Date.now() - value);
  if (diff < 60_000) {
    return "just now";
  }
  const minutes = Math.floor(diff / 60_000);
  if (minutes < 60) {
    return `${minutes}m ago`;
  }
  return `${Math.floor(minutes / 60)}h ago`;
}

function toDate(value: unknown): Date | null {
  if (!value) {
    return null;
  }
  const date = value instanceof Date ? value : new Date(String(value));
  return Number.isNaN(date.getTime()) ? null : date;
}
