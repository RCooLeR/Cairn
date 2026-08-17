import { act, fireEvent, render, screen } from "@testing-library/react";
import { useRef } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ToastViewport } from "../components/ui/ToastViewport";
import { useToastQueue } from "./useToastQueue";

function ToastHarness({ onAction }: { onAction: () => void }) {
  const { dismissToast, pushToast, toasts } = useToastQueue();
  const nextAction = useRef(0);

  return (
    <>
      <button
        onClick={() =>
          pushToast({
            body: "Saved to disk",
            level: "ok",
            title: "Setting saved",
          })
        }
        type="button"
      >
        Push passive
      </button>
      <button
        onClick={() =>
          pushToast({
            action: (
              <button onClick={onAction} type="button">
                Copy path
              </button>
            ),
            body: "42 lines saved.",
            level: "ok",
            title: "Logs exported",
            ttlMS: 1,
          })
        }
        type="button"
      >
        Push actionable
      </button>
      <button
        onClick={() => {
          const index = ++nextAction.current;
          pushToast({
            action: <button type="button">Review {index}</button>,
            level: "info",
            title: `Action ${index}`,
          });
        }}
        type="button"
      >
        Push bounded action
      </button>
      <ToastViewport onDismiss={dismissToast} toasts={toasts} />
    </>
  );
}

describe("useToastQueue", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("keeps actionable toasts available until their accessible dismiss control is used", () => {
    vi.useFakeTimers();
    const onAction = vi.fn();
    render(<ToastHarness onAction={onAction} />);

    fireEvent.click(screen.getByRole("button", { name: "Push actionable" }));
    const action = screen.getByRole("button", { name: "Copy path" });
    action.focus();

    act(() => {
      vi.advanceTimersByTime(60_000);
    });

    expect(screen.getByRole("status")).toHaveTextContent("Logs exported");
    expect(action).toHaveFocus();
    fireEvent.click(action);
    expect(onAction).toHaveBeenCalledTimes(1);

    const dismiss = screen.getByRole("button", {
      name: "Dismiss Logs exported notification",
    });
    dismiss.focus();
    expect(dismiss).toHaveFocus();
    fireEvent.click(dismiss);
    expect(screen.queryByText("Logs exported")).not.toBeInTheDocument();
  });

  it("continues to auto-dismiss passive toasts", () => {
    vi.useFakeTimers();
    render(<ToastHarness onAction={vi.fn()} />);

    fireEvent.click(screen.getByRole("button", { name: "Push passive" }));
    expect(screen.getByRole("status")).toHaveTextContent("Setting saved");

    act(() => {
      vi.advanceTimersByTime(3200);
    });

    expect(screen.queryByText("Setting saved")).not.toBeInTheDocument();
  });

  it("keeps the visible queue bounded when actionable toasts persist", () => {
    render(<ToastHarness onAction={vi.fn()} />);
    const push = screen.getByRole("button", { name: "Push bounded action" });

    for (let index = 0; index < 5; index += 1) {
      fireEvent.click(push);
    }

    expect(screen.queryByText("Action 1")).not.toBeInTheDocument();
    for (let index = 2; index <= 5; index += 1) {
      expect(screen.getByText(`Action ${index}`)).toBeInTheDocument();
    }
    expect(screen.getAllByRole("status")).toHaveLength(4);
  });
});
