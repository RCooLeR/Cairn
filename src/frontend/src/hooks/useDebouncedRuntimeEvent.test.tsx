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
    vi.restoreAllMocks();
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

  it("merges a burst without losing earlier payloads", () => {
    const callback = vi.fn();
    const merge = vi.fn((previous, next) => ({
      ...next,
      data: [...previous.data, ...next.data],
    }));
    renderHook(() =>
      useDebouncedRuntimeEvent("objects:changed", 250, callback, merge),
    );
    act(() => {
      latestEventCallback()({ name: "objects:changed", data: ["image"] });
      latestEventCallback()({ name: "objects:changed", data: ["container"] });
      vi.advanceTimersByTime(250);
    });
    expect(callback).toHaveBeenCalledWith({
      name: "objects:changed",
      data: ["image", "container"],
    });
  });

  it("flushes during a continuous event stream instead of starving refresh", () => {
    const callback = vi.fn();
    renderHook(() =>
      useDebouncedRuntimeEvent("objects:changed", 250, callback),
    );
    act(() => {
      for (let index = 0; index < 10; index += 1) {
        latestEventCallback()({ name: "objects:changed", data: { index } });
        vi.advanceTimersByTime(100);
      }
    });
    expect(callback).toHaveBeenCalledOnce();
    expect(callback).toHaveBeenCalledWith({
      name: "objects:changed",
      data: { index: 9 },
    });
  });

  it("finishes a slow refresh before processing one merged trailing event", async () => {
    let finish!: () => void;
    const callback = vi.fn().mockImplementationOnce(
      () =>
        new Promise<void>((resolve) => {
          finish = resolve;
        }),
    );
    const merge = vi.fn((previous, next) => ({
      ...next,
      data: [...previous.data, ...next.data],
    }));
    renderHook(() =>
      useDebouncedRuntimeEvent("objects:changed", 250, callback, merge),
    );
    act(() => {
      latestEventCallback()({ name: "objects:changed", data: ["first"] });
      vi.advanceTimersByTime(250);
      latestEventCallback()({ name: "objects:changed", data: ["container"] });
      vi.advanceTimersByTime(1000);
      latestEventCallback()({ name: "objects:changed", data: ["image"] });
      vi.advanceTimersByTime(1000);
    });
    expect(callback).toHaveBeenCalledTimes(1);

    await act(async () => finish());
    expect(callback).toHaveBeenCalledTimes(2);
    expect(callback).toHaveBeenLastCalledWith({
      name: "objects:changed",
      data: ["container", "image"],
    });
    expect(vi.getTimerCount()).toBe(0);
  });

  it("releases the refresh queue after an asynchronous rejection", async () => {
    let fail!: (error: Error) => void;
    const callback = vi.fn().mockImplementationOnce(
      () =>
        new Promise<void>((_, reject) => {
          fail = reject;
        }),
    );
    renderHook(() =>
      useDebouncedRuntimeEvent("objects:changed", 250, callback),
    );
    act(() => {
      latestEventCallback()({ name: "objects:changed", data: { id: 1 } });
      vi.advanceTimersByTime(250);
      latestEventCallback()({ name: "objects:changed", data: { id: 2 } });
      vi.advanceTimersByTime(250);
    });
    await act(async () => fail(new Error("read failed")));
    expect(callback).toHaveBeenCalledTimes(2);
  });

  it("does not start queued work after an active refresh settles on unmount", async () => {
    let finish!: () => void;
    const callback = vi.fn().mockImplementationOnce(
      () =>
        new Promise<void>((resolve) => {
          finish = resolve;
        }),
    );
    const { unmount } = renderHook(() =>
      useDebouncedRuntimeEvent("objects:changed", 250, callback),
    );
    act(() => {
      latestEventCallback()({ name: "objects:changed", data: { id: 1 } });
      vi.advanceTimersByTime(250);
      latestEventCallback()({ name: "objects:changed", data: { id: 2 } });
      vi.advanceTimersByTime(250);
    });
    unmount();
    await act(async () => finish());
    expect(callback).toHaveBeenCalledTimes(1);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("merges hidden-window invalidations without timers and refreshes once on show", () => {
    const visibility = vi.spyOn(document, "visibilityState", "get");
    visibility.mockReturnValue("hidden");
    const callback = vi.fn();
    const merge = vi.fn((previous, next) => ({
      ...next,
      data: [...previous.data, ...next.data],
    }));
    renderHook(() =>
      useDebouncedRuntimeEvent("objects:changed", 250, callback, merge, {
        foregroundOnly: true,
      }),
    );

    act(() => {
      latestEventCallback()({ name: "objects:changed", data: ["container"] });
      latestEventCallback()({ name: "objects:changed", data: ["image"] });
      vi.advanceTimersByTime(10_000);
    });
    expect(callback).not.toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);

    act(() => {
      visibility.mockReturnValue("visible");
      document.dispatchEvent(new Event("visibilitychange"));
    });
    expect(callback).toHaveBeenCalledExactlyOnceWith({
      name: "objects:changed",
      data: ["container", "image"],
    });
  });

  it("holds queued work if the window hides during an in-flight refresh", async () => {
    const visibility = vi.spyOn(document, "visibilityState", "get");
    visibility.mockReturnValue("visible");
    let finish!: () => void;
    const callback = vi.fn().mockImplementationOnce(
      () =>
        new Promise<void>((resolve) => {
          finish = resolve;
        }),
    );
    renderHook(() =>
      useDebouncedRuntimeEvent("objects:changed", 250, callback, undefined, {
        foregroundOnly: true,
      }),
    );
    act(() => {
      latestEventCallback()({ name: "objects:changed", data: { id: 1 } });
      vi.advanceTimersByTime(250);
      latestEventCallback()({ name: "objects:changed", data: { id: 2 } });
      visibility.mockReturnValue("hidden");
      document.dispatchEvent(new Event("visibilitychange"));
    });
    await act(async () => finish());
    expect(callback).toHaveBeenCalledTimes(1);
    expect(vi.getTimerCount()).toBe(0);

    act(() => {
      visibility.mockReturnValue("visible");
      document.dispatchEvent(new Event("visibilitychange"));
    });
    expect(callback).toHaveBeenCalledTimes(2);
    expect(callback).toHaveBeenLastCalledWith({
      name: "objects:changed",
      data: { id: 2 },
    });
  });
});

function latestEventCallback() {
  const calls = runtimeMock.on.mock.calls;
  const callback = calls[calls.length - 1]?.[1];
  expect(callback).toEqual(expect.any(Function));
  return callback as (event: unknown) => void;
}
