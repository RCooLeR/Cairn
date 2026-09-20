export const DEFAULT_READ_TIMEOUT_MS = 30_000;

type CancellableRead<T> = PromiseLike<T> & {
  cancel?: (cause?: unknown) => PromiseLike<void> | void;
};

/** Bound read-only RPCs so a lost bridge response cannot latch a loading state. */
export function boundedRead<T>(
  load: () => CancellableRead<T>,
  operation: string,
  timeoutMS = DEFAULT_READ_TIMEOUT_MS,
): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    if (timeoutMS <= 0) {
      reject(new Error(`${operation} timed out. Please retry.`));
      return;
    }

    let request: CancellableRead<T>;
    try {
      request = load();
    } catch (error) {
      reject(error);
      return;
    }

    const timeout = globalThis.setTimeout(() => {
      const error = new Error(`${operation} timed out. Please retry.`);
      // Settle before cancellation: some bridges reject synchronously when
      // cancelled, but callers should still receive the useful timeout error.
      reject(error);
      try {
        void Promise.resolve(request.cancel?.(error)).catch(() => undefined);
      } catch {
        // A late response is ignored even if the bridge cannot cancel it.
      }
    }, timeoutMS);

    void Promise.resolve(request).then(
      (value) => {
        globalThis.clearTimeout(timeout);
        resolve(value);
      },
      (error: unknown) => {
        globalThis.clearTimeout(timeout);
        reject(error);
      },
    );
  });
}
