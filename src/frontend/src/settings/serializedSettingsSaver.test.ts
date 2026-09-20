import { describe, expect, it, vi } from "vitest";

import { SerializedSettingsSaver } from "./serializedSettingsSaver";

function deferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, reject, resolve };
}

describe("SerializedSettingsSaver", () => {
  it("coalesces duplicate values and keeps writes serialized", async () => {
    const pendingChanges: number[] = [];
    const saver = new SerializedSettingsSaver((count) =>
      pendingChanges.push(count),
    );
    const first = deferred<void>();
    const order: string[] = [];
    const saveFirst = vi.fn(async () => {
      order.push("first:start");
      await first.promise;
      order.push("first:end");
    });
    const saveSecond = vi.fn(async () => {
      order.push("second");
    });

    const firstRequest = saver.enqueue("terminal.shell", "/bin/zsh", saveFirst);
    const duplicateRequest = saver.enqueue(
      "terminal.shell",
      "/bin/zsh",
      saveFirst,
    );
    const secondRequest = saver.enqueue("general.theme", "light", saveSecond);

    expect(duplicateRequest).toBe(firstRequest);
    await Promise.resolve();
    expect(saveFirst).toHaveBeenCalledTimes(1);
    expect(saveSecond).not.toHaveBeenCalled();
    expect(order).toEqual(["first:start"]);

    first.resolve();
    await Promise.all([firstRequest, duplicateRequest, secondRequest]);

    expect(saveSecond).toHaveBeenCalledTimes(1);
    expect(order).toEqual(["first:start", "first:end", "second"]);
    expect(pendingChanges).toEqual([1, 2, 1, 0]);
  });

  it("correlates each completion with the newest overall and keyed intent", async () => {
    const saver = new SerializedSettingsSaver(() => undefined);
    const first = deferred<void>();
    const contexts: Array<{
      isLatest: boolean;
      isLatestForKey: boolean;
      label: string;
    }> = [];

    const firstRequest = saver.enqueue(
      "general.theme",
      "light",
      async (ctx) => {
        await first.promise;
        contexts.push({
          isLatest: ctx.isLatest(),
          isLatestForKey: ctx.isLatestForKey(),
          label: "first",
        });
      },
    );
    const secondRequest = saver.enqueue(
      "general.theme",
      "system",
      async (ctx) => {
        contexts.push({
          isLatest: ctx.isLatest(),
          isLatestForKey: ctx.isLatestForKey(),
          label: "second",
        });
      },
    );
    const thirdRequest = saver.enqueue("updates.notify", false, async (ctx) => {
      contexts.push({
        isLatest: ctx.isLatest(),
        isLatestForKey: ctx.isLatestForKey(),
        label: "third",
      });
    });

    first.resolve();
    await Promise.all([firstRequest, secondRequest, thirdRequest]);

    expect(contexts).toEqual([
      { isLatest: false, isLatestForKey: false, label: "first" },
      { isLatest: false, isLatestForKey: true, label: "second" },
      { isLatest: true, isLatestForKey: true, label: "third" },
    ]);
  });

  it("continues the queue after a rejected write", async () => {
    const saver = new SerializedSettingsSaver(() => undefined);
    const failure = new Error("disk full");
    const second = vi.fn(async () => "saved");

    const firstRequest = saver.enqueue("general.theme", "light", async () => {
      throw failure;
    });
    const secondRequest = saver.enqueue("general.theme", "dark", second);

    await expect(firstRequest).rejects.toBe(failure);
    await expect(secondRequest).resolves.toBe("saved");
    expect(second).toHaveBeenCalledTimes(1);
  });

  it("stops publishing pending changes after a subscriber unmounts", async () => {
    const saver = new SerializedSettingsSaver();
    const pendingChanges: number[] = [];
    const unsubscribe = saver.subscribePendingCount((count) =>
      pendingChanges.push(count),
    );
    const save = deferred<void>();

    const request = saver.enqueue("general.theme", "light", () => save.promise);
    expect(pendingChanges).toEqual([0, 1]);
    unsubscribe();
    save.resolve();
    await request;

    expect(pendingChanges).toEqual([0, 1]);
  });
});
