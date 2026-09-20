import type { ComponentPropsWithoutRef } from "react";

export type LiveMessageLevel = "error" | "status";

export type LiveMessageProps = Omit<
  ComponentPropsWithoutRef<"div">,
  "aria-live" | "role"
> & {
  level: LiveMessageLevel;
};

/**
 * Announces discrete operation progress, completion, and failures.
 *
 * Keep high-frequency output such as logs and terminal streams outside this
 * component so assistive technology is not flooded with incremental updates.
 */
export function LiveMessage({ level, ...props }: LiveMessageProps) {
  const isError = level === "error";

  return (
    <div
      {...props}
      aria-atomic="true"
      aria-live={isError ? "assertive" : "polite"}
      role={isError ? "alert" : "status"}
    />
  );
}
