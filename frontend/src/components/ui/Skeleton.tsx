import { cx } from './utils';

type SkeletonProps = {
  className?: string;
};

export function Skeleton({ className }: SkeletonProps) {
  return <div className={cx('animate-pulse rounded-control bg-white/[0.08]', className)} />;
}
