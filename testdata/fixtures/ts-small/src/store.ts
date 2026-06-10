/** A key-value entry. */
export interface Entry {
  key: string;
  value: string;
}

/** Result kind for store operations. */
export type LookupResult = Entry | undefined;

/** In-memory key-value store. */
export class Store {
  private items = new Map<string, string>();

  /** Get a value by key. */
  get(key: string): LookupResult {
    const value = this.items.get(key);
    return value === undefined ? undefined : { key, value };
  }

  /** Store a value under a key. */
  set(key: string, value: string): void {
    this.items.set(key, normalize(value));
  }

  get size(): number {
    return this.items.size;
  }
}

export function normalize(value: string): string {
  return value === '' ? '<empty>' : value;
}

export const describe = (entry: Entry): string => `${entry.key}=${entry.value}`;
