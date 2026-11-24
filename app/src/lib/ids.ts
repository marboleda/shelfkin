// The app's one source of client-generated IDs. Every write that needs an ID
// (a new entry, an event) takes it from here, so all of them are UUIDv7s
// (time-ordered, as the API requires) made from secure random numbers.

import { getRandomValues } from 'expo-crypto';
import { v7 } from 'uuid';

type RandomSource = <T extends Uint8Array>(bytes: T) => T;

/**
 * The platform's secure random source: Web Crypto where the runtime has it (web
 * browsers, Node in Jest, recent Hermes), else expo-crypto's native module.
 */
function secureRandom(): RandomSource {
  const webCrypto = (globalThis as { crypto?: { getRandomValues?: RandomSource } }).crypto;
  if (typeof webCrypto?.getRandomValues === 'function') {
    return (bytes) => webCrypto.getRandomValues!(bytes);
  }
  return (bytes) => getRandomValues(bytes);
}

/** A new UUIDv7, e.g. `0190f3a1-7c2e-7b3d-9a4f-2c1e5d6b7a80`. */
export function newId(): string {
  const random = secureRandom();
  return v7({ rng: () => random(new Uint8Array(16)) });
}
