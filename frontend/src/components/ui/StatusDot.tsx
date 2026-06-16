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

const toneLabels: Record<StatusTone, string> = {
  ok: "Status ok",
  warn: "Status warning",
  error: "Status error",
  info: "Status information",
  neutral: "Status neutral",
};

export function StatusDot({
  label,
  pulse = false,
  tone = "neutral",
}: StatusDotProps) {
  const accessibleProps = label
    ? {}
    : { "aria-label": toneLabels[tone], role: "img" };
  return (
    <span className="inline-flex items-center gap-2" {...accessibleProps}>
      <span
        aria-hidden="true"
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
