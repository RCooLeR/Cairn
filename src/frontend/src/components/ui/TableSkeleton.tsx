import { Skeleton } from "./Skeleton";

export function TableSkeleton() {
  return (
    <div className="space-y-3">
      <Skeleton className="h-8 w-80" />
      <Skeleton className="h-[420px] w-full" />
    </div>
  );
}
