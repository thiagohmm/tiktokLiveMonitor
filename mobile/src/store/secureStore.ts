/**
 * Thin wrapper around `expo-secure-store` for persisting sensitive values
 * (access/refresh tokens) that must not live in plain Zustand state or
 * AsyncStorage. Falls back to a plain object in-memory cache when secure
 * storage is unavailable (e.g. web / some simulators) so the app still runs.
 */
import * as SecureStore from 'expo-secure-store';

const memory: Record<string, string> = {};

async function isAvailable(): Promise<boolean> {
  try {
    // `getItemAsync` on an empty key resolves to null; a successful call means
    // the native backend is present. Throws on unsupported platforms.
    await SecureStore.getItemAsync('__probe__');
    return true;
  } catch {
    return false;
  }
}

let probePromise: Promise<boolean> | null = null;
async function isAvailableOnce(): Promise<boolean> {
  if (!probePromise) {
    probePromise = isAvailable();
  }
  return probePromise;
}

export const secureStore = {
  async get(key: string): Promise<string | null> {
    if (await isAvailableOnce()) {
      return SecureStore.getItemAsync(key);
    }
    return memory[key] ?? null;
  },

  async set(key: string, value: string): Promise<void> {
    if (await isAvailableOnce()) {
      await SecureStore.setItemAsync(key, value);
    } else {
      memory[key] = value;
    }
  },

  async delete(key: string): Promise<void> {
    if (await isAvailableOnce()) {
      await SecureStore.deleteItemAsync(key);
    } else {
      delete memory[key];
    }
  },
};

export const SecureStoreKeys = {
  accessToken: 'auth.accessToken',
  refreshToken: 'auth.refreshToken',
  userId: 'auth.userId',
} as const;