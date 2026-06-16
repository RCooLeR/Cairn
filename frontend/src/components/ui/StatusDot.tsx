import { cx } from "./utils";

type StatusTone = "ok" | "warn" | "error" | "info" | "neutral";

type StatusDotProps = {
  tone?: StatusTone;
  pulse?: boolean;
  label?: string;
};

const toneClasses: Record<StatusTone, string> = {
  ok: "bg-ok",
  warn: "bg-warn",
  error: "bg-error",
  info: "bg-info",
  neutral: "bg-neutral",
};

export function StatusDot({
  label,
  pulse = false,
  tone = "neutral",
}: StatusDotProps) {
  return (
    <span className="inline-flex items-center gap-2">
      <span
        aria-hidden={label ? undefined : "true"}
        className={cx(
          "h-2 w-2 rounded-full",
          toneClasses[tone],
          pulse && "animate-pulse",
        )}
      />
      {label ? <span>{label}</span> : null}
    </span>
  );
}
