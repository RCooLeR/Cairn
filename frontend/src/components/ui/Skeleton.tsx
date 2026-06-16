import { cx } from "./utils";

type SkeletonProps = {
  className?: string;
};

export function Skeleton({ className }: SkeletonProps) {
  return (
    <div
      className={cx(
        "animate-pulse rounded-control bg-text-primary/10",
        className,
      )}
    />
  );
}
