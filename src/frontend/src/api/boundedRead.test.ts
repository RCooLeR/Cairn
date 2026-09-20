import { afterEach, describe, expect, it, vi } from "vitest";

import { boundedRead } from "./boundedRead";

afterEach(() => vi.useRealTimers());

describe("boundedRead", () => {
  it("returns successful reads and releases their timer", async () => {
    vi.useFakeTimers();
    await expect(
      boundedRead(() => Promise.resolve("ok"), "Read"),
    ).resolves.toBe("ok");
    expect(vi.getTimerCount()).toBe(0);
  });

  it("cancels a stalled read and ignores late results", async () => {
    vi.useFakeTimers();
    let resolve!: (value: string) => void;
    const cancel = vi.fn().mockRejectedValue(new Error("bridge closed"));
    const request = Object.assign(
      new Promise<string>((done) => {
        resolve = done;
      }),
      { cancel },
    );
    const result = boundedRead(() => request, "Container list", 100);
    const rejected = expect(result).rejects.toThrow(
      "Container list timed out. Please retry.",
    );
    await vi.advanceTimersByTimeAsync(100);
    await rejected;
    expect(cancel).toHaveBeenCalledOnce();
    resolve("late");
    await Promise.resolve();
    await expect(result).rejects.toThrow("timed out");
  });

  it("handles synchronous bridge errors without leaking a timer", async () => {
    vi.useFakeTimers();
    await expect(
      boundedRead(() => {
        throw new Error("bridge unavailable");
      }, "Read"),
    ).rejects.toThrow("bridge unavailable");
    expect(vi.getTimerCount()).toBe(0);
  });

  it("does not launch work after its deadline has expired", async () => {
    const load = vi.fn();
    await expect(boundedRead(load, "Read", 0)).rejects.toThrow("timed out");
    expect(load).not.toHaveBeenCalled();
  });
});
