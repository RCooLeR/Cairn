import type { ReactNode } from 'react';

import { Badge } from './Badge';

type ToastLevel = 'ok' | 'warn' | 'error' | 'info';

type ToastProps = {
  level: ToastLevel;
  title: string;
  body?: string;
  action?: ReactNode;
};

export function Toast({ action, body, level, title }: ToastProps) {
  const live = level === 'error' ? 'assertive' : 'polite';
  return (
    <div
      aria-live={live}
      className="w-80 rounded-card border border-border bg-bg-panel p-3"
      role={level === 'error' ? 'alert' : 'status'}
    >
      <div className="flex items-center justify-between gap-3">
        <div className="text-sm font-semibold">{title}</div>
        <Badge tone={level}>{level}</Badge>
      </div>
      {body ? <p className="mt-2 text-sm text-text-secondary">{body}</p> : null}
      {action ? <div className="mt-3">{action}</div> : null}
    </div>
  );
}
