import type { ReactNode } from 'react';

type EmptyStateProps = {
  icon: ReactNode;
  title: string;
  body: string;
  action?: ReactNode;
};

export function EmptyState({ action, body, icon, title }: EmptyStateProps) {
  return (
    <div className="flex min-h-48 flex-col items-center justify-center rounded-card border border-dashed border-border bg-bg-inset p-6 text-center">
      <div className="text-text-muted">{icon}</div>
      <h2 className="mt-3 text-base font-semibold">{title}</h2>
      <p className="mt-2 max-w-sm text-sm text-text-secondary">{body}</p>
      {action ? <div className="mt-4">{action}</div> : null}
    </div>
  );
}
