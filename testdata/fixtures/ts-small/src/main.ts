import { Cache } from './cache';

export async function main(): Promise<void> {
  const cache = new Cache();
  cache.warm({ greeting: 'hello' });
  const report = await cache.report();
  console.log(report);
}

void main();
