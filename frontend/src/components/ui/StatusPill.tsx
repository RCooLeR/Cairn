import { StatusDot } from "./StatusDot";

type StatusPillProps = {
  label: string;
  ok: boolean;
  value?: string;
};

export function StatusPill({ label, ok, value }: StatusPillProps) {
  return (
    <div className="rounded-control border border-border bg-bg-inset p-3">
      <div className="flex items-center gap-2">
        <StatusDot tone={ok ? "ok" : "neutral"} />
        <span>{label}</span>
      </div>
      {value ? (
        <div className="mt-1 truncate text-xs text-text-muted">{value}</div>
      ) : null}
    </div>
  );
}
