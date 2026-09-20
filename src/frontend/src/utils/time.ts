export function dateMillis(value: unknown) {
  const date = toDate(value);
  return date?.getTime() ?? 0;
}

export function formatDate(value: unknown) {
  const date = toDate(value);
  if (!date) {
    return "-";
  }
  return date.toLocaleString(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  });
}

export function relativeTime(value: number) {
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

export function toDate(value: unknown): Date | null {
  if (!value) {
    return null;
  }
  const date = value instanceof Date ? value : new Date(String(value));
  return Number.isNaN(date.getTime()) ? null : date;
}
