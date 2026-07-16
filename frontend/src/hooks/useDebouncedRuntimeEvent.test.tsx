import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useDebouncedRuntimeEvent } from "./useDebouncedRuntimeEvent";

const runtimeMock = vi.hoisted(() => ({
  on: vi.fn(),
}));

vi.mock("@wailsio/runtime", () => ({
  Events: {
    On: runtimeMock.on,
  },
}));

describe("useDebouncedRuntimeEvent", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    runtimeMock.on.mockReset();
    runtimeMock.on.mockImplementation(() => vi.fn());
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("cancels pending work when the consumer unmounts", () => {
    const callback = vi.fn();
    const { unmount } = renderHook(() =>
      useDebouncedRuntimeEvent("objects:changed", 250, callback),
    );
    const onEvent = latestEventCallback();

    act(() => {
      onEvent({ name: "objects:changed", data: { kind: "container" } });
    });
    unmount();
    act(() => {
      vi.advanceTimersByTime(250);
    });

    expect(callback).not.toHaveBeenCalled();
  });

  it("cancels the old timer when event dependencies change", () => {
    const callback = vi.fn();
    const { rerender } = renderHook(
      ({ delayMs }) =>
        useDebouncedRuntimeEvent("objects:changed", delayMs, callback),
      { initialProps: { delayMs: 250 } },
    );
    const firstOnEvent = latestEventCallback();

    act(() => {
      firstOnEvent({ name: "objects:changed", data: { kind: "image" } });
    });
    rerender({ delayMs: 500 });
    act(() => {
      vi.advanceTimersByTime(250);
    });
    expect(callback).not.toHaveBeenCalled();

    const nextEvent = { name: "objects:changed", data: { kind: "volume" } };
    act(() => {
      latestEventCallback()(nextEvent);
      vi.advanceTimersByTime(500);
    });

    expect(callback).toHaveBeenCalledTimes(1);
    expect(callback).toHaveBeenCalledWith(nextEvent);
  });
});

function latestEventCallback() {
  const calls = runtimeMock.on.mock.calls;
  const callback = calls[calls.length - 1]?.[1];
  expect(callback).toEqual(expect.any(Function));
  return callback as (event: unknown) => void;
}
