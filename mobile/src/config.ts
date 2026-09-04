import Constants from 'expo-constants';

/**
 * Central app configuration.
 *
 * The API base URL is provided through the `EXPO_PUBLIC_API_BASE` environment
 * variable. In development it defaults to the local backend; EAS production
 * builds override it to point at the Railway deployment.
 */

const DEFAULT_API_BASE = 'http://localhost:3001';

function resolveApiBase(): string {
  // Prefer the public env var (set via `--env` / CI / local `.env`).
  const fromEnv =
    typeof process !== 'undefined' ? process.env.EXPO_PUBLIC_API_BASE : undefined;
  if (fromEnv && fromEnv.trim().length > 0) {
    return fromEnv.trim().replace(/\/+$/, '');
  }

  // Fall back to EAS profile `extra` (app.json -> expo.extra).
  const extra = Constants?.expoConstants?.extra;
  if (extra && typeof extra.API_BASE === 'string' && extra.API_BASE.trim()) {
    return extra.API_BASE.trim().replace(/\/+$/, '');
  }

  return DEFAULT_API_BASE;
}

export const config = {
  /** Base URL of the TikTok Live Monitor backend API. */
  API_BASE: resolveApiBase(),
  /** Default SSE connection timeout in milliseconds. */
  SSE_TIMEOUT_MS: 30_000,
  /** Base backoff for SSE reconnection attempts in milliseconds. */
  SSE_BACKOFF_BASE_MS: 1_000,
  /** Maximum SSE reconnection backoff in milliseconds. */
  SSE_BACKOFF_MAX_MS: 30_000,
} as const;

export default config;