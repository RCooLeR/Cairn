import { act, fireEvent, render, screen } from "@testing-library/react";
import { useRef, useState } from "react";
import { describe, expect, it, vi } from "vitest";

import type { Notification } from "../../../bindings/github.com/RCooLeR/Cairn/internal/models/models.js";
import { NotificationCenter } from "./NotificationCenter";

function notification(): Notification {
  return {
    body: "Images were refreshed",
    createdAt: new Date("2026-06-16T10:00:00Z"),
    id: 1,
    level: "info",
    read: false,
    title: "Updates checked",
    topic: "update",
  };
}

describe("NotificationCenter", () => {
  it("traps focus and restores it when closed", () => {
    vi.useFakeTimers();
    const onMarkAllRead = vi.fn();
    const onNavigate = vi.fn();

    function Harness() {
      const [open, setOpen] = useState(false);
      return (
        <>
          <button onClick={() => setOpen(true)} type="button">
            Open notifications
          </button>
          <NotificationCenter
            error={null}
            loading={false}
            notifications={[notification()]}
            onClose={() => setOpen(false)}
            onMarkAllRead={onMarkAllRead}
            onNavigate={onNavigate}
            open={open}
          />
        </>
      );
    }

    render(<Harness />);
    const opener = screen.getByRole("button", {
      name: "Open notifications",
    });
    opener.focus();
    fireEvent.click(opener);

    const dialog = screen.getByRole("dialog", {
      name: "Notification center",
    });
    expect(dialog).toHaveAttribute("aria-modal", "true");
    act(() => {
      vi.advanceTimersByTime(0);
    });
    expect(screen.getByRole("button", { name: "Mark all read" })).toHaveFocus();

    fireEvent.keyDown(document, { key: "Tab", shiftKey: true });
    expect(
      screen.getByRole("button", { name: /Updates checked/ }),
    ).toHaveFocus();

    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(opener).toHaveFocus();

    vi.useRealTimers();
  });

  it("renders targetless notifications as content instead of no-op buttons", () => {
    const targetless = {
      ...notification(),
      id: 2,
      title: "Informational notice",
      topic: "unknown-topic",
    };

    render(
      <NotificationCenter
        error={null}
        loading={false}
        notifications={[targetless]}
        onClose={vi.fn()}
        onMarkAllRead={vi.fn()}
        onNavigate={vi.fn()}
        open
      />,
    );

    expect(screen.getByText("Informational notice")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /Informational notice/ }),
    ).not.toBeInTheDocument();
  });

  it("lets its trigger toggle the dialog without treating it as an outside click", () => {
    function Harness() {
      const [open, setOpen] = useState(false);
      const boundaryRef = useRef<HTMLDivElement | null>(null);
      return (
        <div ref={boundaryRef}>
          <button onClick={() => setOpen((current) => !current)} type="button">
            Notifications
          </button>
          <NotificationCenter
            error={null}
            interactionBoundaryRef={boundaryRef}
            loading={false}
            notifications={[notification()]}
            onClose={() => setOpen(false)}
            onMarkAllRead={vi.fn()}
            onNavigate={vi.fn()}
            open={open}
          />
        </div>
      );
    }

    render(<Harness />);
    const trigger = screen.getByRole("button", { name: "Notifications" });
    fireEvent.click(trigger);
    expect(screen.getByRole("dialog")).toBeInTheDocument();

    fireEvent.pointerDown(trigger);
    fireEvent.click(trigger);

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("reports the activated notification and dismisses on outside interaction", () => {
    const item = notification();
    const onClose = vi.fn();
    const onNavigate = vi.fn();

    render(
      <NotificationCenter
        error={null}
        loading={false}
        notifications={[item]}
        onClose={onClose}
        onMarkAllRead={vi.fn()}
        onNavigate={onNavigate}
        open
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: /Updates checked/ }));
    expect(onNavigate).toHaveBeenCalledWith(item, "updates");

    fireEvent.pointerDown(document.body);
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
