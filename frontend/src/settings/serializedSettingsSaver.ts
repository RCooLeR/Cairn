export type SerializedSettingsSaveContext = {
  isLatest: () => boolean;
  isLatestForKey: () => boolean;
  sequence: number;
};

type PendingRequest<T> = {
  promise: Promise<T>;
  value: unknown;
};

export class SerializedSettingsSaver {
  private latestSequence = 0;
  private readonly latestSequenceByKey = new Map<string, number>();
  private readonly latestRequestByKey = new Map<
    string,
    PendingRequest<unknown>
  >();
  private nextSequence = 0;
  private onPendingCountChange: ((pendingCount: number) => void) | null;
  private pendingCount = 0;
  private tail: Promise<void> = Promise.resolve();

  constructor(onPendingCountChange?: (pendingCount: number) => void) {
    this.onPendingCountChange = onPendingCountChange ?? null;
  }

  subscribePendingCount(listener: (pendingCount: number) => void): () => void {
    this.onPendingCountChange = listener;
    listener(this.pendingCount);
    return () => {
      if (this.onPendingCountChange === listener) {
        this.onPendingCountChange = null;
      }
    };
  }

  enqueue<T>(
    key: string,
    value: unknown,
    save: (context: SerializedSettingsSaveContext) => Promise<T>,
  ): Promise<T> {
    const existing = this.latestRequestByKey.get(key);
    if (existing && Object.is(existing.value, value)) {
      return existing.promise as Promise<T>;
    }

    const sequence = this.nextSequence + 1;
    this.nextSequence = sequence;
    this.latestSequence = sequence;
    this.latestSequenceByKey.set(key, sequence);
    this.pendingCount += 1;
    this.onPendingCountChange?.(this.pendingCount);

    const context: SerializedSettingsSaveContext = {
      isLatest: () => this.latestSequence === sequence,
      isLatestForKey: () => this.latestSequenceByKey.get(key) === sequence,
      sequence,
    };
    const operation = this.tail.then(() => save(context));
    this.tail = operation.then(
      () => undefined,
      () => undefined,
    );

    const settled = operation.finally(() => {
      const current = this.latestRequestByKey.get(key);
      if (current?.promise === settled) {
        this.latestRequestByKey.delete(key);
      }
      this.pendingCount = Math.max(0, this.pendingCount - 1);
      this.onPendingCountChange?.(this.pendingCount);
    });
    this.latestRequestByKey.set(key, { promise: settled, value });
    return settled;
  }
}
