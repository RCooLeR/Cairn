import { act, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
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
    expect(
      screen.getByRole("button", { name: "Mark all read" }),
    ).toHaveFocus();

    fireEvent.keyDown(document, { key: "Tab", shiftKey: true });
    expect(
      screen.getByRole("button", { name: /Updates checked/ }),
    ).toHaveFocus();

    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(opener).toHaveFocus();

    vi.useRealTimers();
  });
});
