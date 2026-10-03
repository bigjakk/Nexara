/**
 * A lock manager as a browser's navigator.locks, for tests: jsdom has none.
 *
 * Exclusive locks by name, granted in the order they were asked for and released
 * when the callback's promise settles. A request whose signal aborts before it
 * is granted rejects with an AbortError and its callback never runs, as in a
 * browser; one that is granted already is not affected by an abort. A request
 * with `ifAvailable` is never queued: it is granted at once when nothing holds
 * the lock and nothing waits for it, and otherwise its callback is called with
 * null instead, which is the whole of what ifAvailable promises.
 *
 * A browser answers a request after a round trip, not in the same turn.
 * pauseDecisions() holds every ifAvailable request's answer until
 * resumeDecisions(), so that a test can have a refresh in flight that has not
 * yet been told whether it has the lock, and see what else does meanwhile.
 *
 * installFakeLocks() puts one on the page's navigator, and removeFakeLocks()
 * takes it off again: a page without navigator.locks is what jsdom, and a
 * plain-HTTP origin, is.
 */

function abortError(): DOMException {
  return new DOMException("The operation was aborted.", "AbortError");
}

export class FakeLockManager {
  /** The names asked for, in the order the locks were granted. */
  readonly granted: string[] = [];
  /** How many ifAvailable requests were told the lock was taken. */
  skipped = 0;
  private held = false;
  private queue: (() => void)[] = [];
  private paused = false;
  private decisions: (() => void)[] = [];

  /** How many requests are queued behind the lock now. */
  get waiting(): number {
    return this.queue.length;
  }

  /** How many ifAvailable requests are held back, waiting for resumeDecisions(). */
  get undecided(): number {
    return this.decisions.length;
  }

  /** Holds every ifAvailable request's answer back until resumeDecisions(). */
  pauseDecisions(): void {
    this.paused = true;
  }

  /** Answers the ifAvailable requests held back, in the order they were made. */
  resumeDecisions(): void {
    this.paused = false;
    const held = this.decisions;
    this.decisions = [];
    for (const decide of held) decide();
  }

  request(
    name: string,
    options: { signal?: AbortSignal; ifAvailable?: boolean },
    callback: (lock: { name: string } | null) => Promise<unknown>,
  ): Promise<unknown> {
    return new Promise((resolve, reject) => {
      const run = () => {
        this.held = true;
        this.granted.push(name);
        void Promise.resolve()
          .then(() => callback({ name }))
          .then(resolve, reject)
          .finally(() => {
            this.held = false;
            this.queue.shift()?.();
          });
      };
      if (options.ifAvailable === true) {
        const decide = () => {
          if (!this.held && this.queue.length === 0) {
            run();
            return;
          }
          this.skipped += 1;
          void Promise.resolve()
            .then(() => callback(null))
            .then(resolve, reject);
        };
        if (this.paused) this.decisions.push(decide);
        else decide();
        return;
      }
      if (options.signal?.aborted) {
        reject(abortError());
        return;
      }
      if (!this.held && this.queue.length === 0) {
        run();
        return;
      }
      this.queue.push(run);
      options.signal?.addEventListener(
        "abort",
        () => {
          const at = this.queue.indexOf(run);
          if (at === -1) return; // already granted: too late to matter
          this.queue.splice(at, 1);
          reject(abortError());
        },
        { once: true },
      );
    });
  }
}

/** Gives the page the lock manager of a browser in a secure context. */
export function installFakeLocks(): FakeLockManager {
  const locks = new FakeLockManager();
  Object.defineProperty(navigator, "locks", {
    configurable: true,
    value: locks,
  });
  return locks;
}

/** Takes it off again. */
export function removeFakeLocks(): void {
  Reflect.deleteProperty(navigator, "locks");
}
