import { useId, useRef, type KeyboardEvent, type ReactNode } from "react";

import { cx } from "./utils";

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
  const instanceID = useId();
  const panelID = `${instanceID}-panel`;
  const tabRefs = useRef(new Map<string, HTMLButtonElement>());
  const enabledItems = items.filter((item) => !item.disabled);
  const activeEnabled = enabledItems.some((item) => item.id === activeID);
  const focusID = activeEnabled ? activeID : enabledItems[0]?.id;
  const labelledItem =
    items.find((item) => item.id === activeID) ??
    items.find((item) => item.id === focusID);
  const tabDOMID = (itemID: string) =>
    `${instanceID}-tab-${items.findIndex((item) => item.id === itemID)}`;
  const selectAndFocus = (itemID: string) => {
    onChange(itemID);
    tabRefs.current.get(itemID)?.focus();
  };
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (!focusID || enabledItems.length === 0) {
      return;
    }
    const eventTargetID =
      event.target instanceof HTMLElement
        ? event.target.dataset.tabId
        : undefined;
    const activeIndex = enabledItems.findIndex(
      (item) => item.id === (eventTargetID || focusID),
    );
    if (activeIndex === -1) {
      return;
    }

    if (event.key === "ArrowRight") {
      event.preventDefault();
      selectAndFocus(enabledItems[(activeIndex + 1) % enabledItems.length].id);
    } else if (event.key === "ArrowLeft") {
      event.preventDefault();
      selectAndFocus(
        enabledItems[
          (activeIndex - 1 + enabledItems.length) % enabledItems.length
        ].id,
      );
    } else if (event.key === "Home") {
      event.preventDefault();
      selectAndFocus(enabledItems[0].id);
    } else if (event.key === "End") {
      event.preventDefault();
      selectAndFocus(enabledItems[enabledItems.length - 1].id);
    }
  };

  return (
    <div>
      <div
        className="flex border-b border-border"
        onKeyDown={onKeyDown}
        role="tablist"
      >
        {items.map((item) => (
          <button
            aria-controls={panelID}
            aria-selected={item.id === activeID}
            className={cx(
              "h-10 border-b-2 px-3 text-sm transition",
              item.id === activeID
                ? "border-accent text-accent"
                : "border-transparent text-text-secondary hover:text-text-primary",
              item.disabled && "cursor-not-allowed opacity-50",
            )}
            data-tab-id={item.id}
            disabled={item.disabled}
            id={tabDOMID(item.id)}
            key={item.id}
            onClick={() => onChange(item.id)}
            ref={(element) => {
              if (element) {
                tabRefs.current.set(item.id, element);
              } else {
                tabRefs.current.delete(item.id);
              }
            }}
            role="tab"
            tabIndex={item.id === focusID ? 0 : -1}
            type="button"
          >
            {item.label}
          </button>
        ))}
      </div>
      <div
        aria-labelledby={labelledItem ? tabDOMID(labelledItem.id) : undefined}
        id={panelID}
        role="tabpanel"
      >
        {children}
      </div>
    </div>
  );
}
