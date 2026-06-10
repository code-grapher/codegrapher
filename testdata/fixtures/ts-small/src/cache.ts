import { Store, type Entry, describe } from './store';

/** Read-through cache with hit counting. */
export class Cache {
  private backend = new Store();
  private hits = 0;

  lookup(key: string): Entry | undefined {
    const entry = this.backend.get(key);
    if (entry !== undefined) {
      this.hits += 1;
    }
    return entry;
  }

  warm(pairs: Record<string, string>): void {
    for (const [key, value] of Object.entries(pairs)) {
      this.backend.set(key, value);
    }
  }

  /** Render cache stats for logging. */
  async report(): Promise<string> {
    const greeting = this.lookup('greeting');
    const line = greeting ? describe(greeting) : 'cold';
    return `${line} (hits=${this.hits})`;
  }
}
