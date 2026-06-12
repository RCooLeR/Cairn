import type { KeyboardEvent, ReactNode } from 'react';

import { cx } from './utils';

type TabItem = {
  id: string;
  label: string;
  disabled?: boolean;
};

type TabsProps = {
  items: TabItem[];
  activeID: string;
  onChange: (id: string) => void;
  children: ReactNode;
};

export function Tabs({ activeID, children, items, onChange }: TabsProps) {
  const enabledItems = items.filter((item) => !item.disabled);
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const activeIndex = enabledItems.findIndex((item) => item.id === activeID);
    if (activeIndex === -1) {
      return;
    }

    if (event.key === 'ArrowRight') {
      event.preventDefault();
      onChange(enabledItems[(activeIndex + 1) % enabledItems.length].id);
    } else if (event.key === 'ArrowLeft') {
      event.preventDefault();
      onChange(enabledItems[(activeIndex - 1 + enabledItems.length) % enabledItems.length].id);
    } else if (event.key === 'Home') {
      event.preventDefault();
      onChange(enabledItems[0].id);
    } else if (event.key === 'End') {
      event.preventDefault();
      onChange(enabledItems[enabledItems.length - 1].id);
    }
  };

  return (
    <div>
      <div className="flex border-b border-border" onKeyDown={onKeyDown} role="tablist">
        {items.map((item) => (
          <button
            aria-selected={item.id === activeID}
            className={cx(
              'h-10 border-b-2 px-3 text-sm transition',
              item.id === activeID
                ? 'border-accent text-accent'
                : 'border-transparent text-text-secondary hover:text-text-primary',
              item.disabled && 'cursor-not-allowed opacity-50',
            )}
            disabled={item.disabled}
            key={item.id}
            onClick={() => onChange(item.id)}
            role="tab"
            tabIndex={item.id === activeID ? 0 : -1}
            type="button"
          >
            {item.label}
          </button>
        ))}
      </div>
      <div role="tabpanel">{children}</div>
    </div>
  );
}
