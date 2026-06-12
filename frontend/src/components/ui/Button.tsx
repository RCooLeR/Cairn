import type { ButtonHTMLAttributes, ReactNode } from 'react';

import { Loader2 } from 'lucide-react';

import { cx } from './utils';

type ButtonVariant = 'primary' | 'secondary' | 'ghost' | 'danger';
type ButtonSize = 'sm' | 'md' | 'icon';

type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: ButtonVariant;
  size?: ButtonSize;
  loading?: boolean;
  disabledReason?: string;
  icon?: ReactNode;
};

const variantClasses: Record<ButtonVariant, string> = {
  primary: 'border-accent bg-accent text-bg-app hover:bg-accent/90',
  secondary: 'border-border bg-bg-inset text-text-secondary hover:border-border-strong hover:text-text-primary',
  ghost: 'border-transparent bg-transparent text-text-secondary hover:bg-bg-card hover:text-text-primary',
  danger: 'border-error bg-error/10 text-error hover:bg-error/20',
};

const sizeClasses: Record<ButtonSize, string> = {
  sm: 'h-8 px-3 text-xs',
  md: 'h-9 px-3 text-sm',
  icon: 'h-9 w-9 px-0 text-sm',
};

export function Button({
  children,
  className,
  disabled,
  disabledReason,
  icon,
  loading = false,
  size = 'md',
  type = 'button',
  variant = 'secondary',
  ...props
}: ButtonProps) {
  const isDisabled = disabled || loading;

  return (
    <button
      {...props}
      aria-disabled={isDisabled}
      className={cx(
        'inline-flex shrink-0 items-center justify-center gap-2 rounded-control border font-medium transition',
        'disabled:cursor-not-allowed disabled:opacity-50',
        variantClasses[variant],
        sizeClasses[size],
        className,
      )}
      disabled={isDisabled}
      title={isDisabled ? disabledReason : props.title}
      type={type}
    >
      {loading ? <Loader2 aria-hidden="true" className="h-4 w-4 animate-spin" /> : icon}
      {size === 'icon' ? <span className="sr-only">{props['aria-label']}</span> : children}
    </button>
  );
}
