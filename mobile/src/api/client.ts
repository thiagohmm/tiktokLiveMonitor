import { config } from '@/config';

/**
 * Optional hook used to refresh the access token when the backend answers a
 * request with 401. Provided by the auth store (Fase 3); when absent the
 * client simply rejects with the error.
 */
export type RefreshTokenFn = () => Promise<string>;

export interface RequestOptions {
  method?: string;
  body?: unknown;
  /** Per-request timeout in ms (defaults to config default). */
  timeoutMs?: number;
  /** Bypass auth header (e.g. for public endpoints). */
  noAuth?: boolean;
  /** Extra headers merged over the defaults (e.g. Supabase `apikey`). */
  headers?: Record<string, string>;
  /** Called when a 401 is received; may throw to abort the retry. */
  onUnauthorized?: () => void | Promise<void>;
}

export class ApiError extends Error {
  status: number;
  body: unknown;

  constructor(status: number, message: string, body?: unknown) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.body = body;
  }
}

async function readJson(response: Response): Promise<unknown> {
  const text = await response.text();
  if (!text) {
    return undefined;
  }
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

/**
 * Central HTTP client for the TikTok Live Monitor backend.
 *
 * Adds the `Authorization: Bearer` header, a timeout, and JSON encoding on
 * request bodies. On a 401 it invokes the optional refresh hook and retries
 * the request once.
 */
export class ApiClient {
  private token: string | null = null;
  private refresh?: RefreshTokenFn;

  constructor(refresh?: RefreshTokenFn) {
    this.refresh = refresh;
  }

  setToken(token: string | null): void {
    this.token = token;
  }

  getToken(): string | null {
    return this.token;
  }

  setRefresh(fn: RefreshTokenFn | undefined): void {
    this.refresh = fn;
  }

  async request<T = unknown>(
    path: string,
    options: RequestOptions = {},
  ): Promise<T> {
    const {
      method = 'GET',
      body,
      timeoutMs = 15_000,
      noAuth = false,
      headers: extraHeaders,
      onUnauthorized,
    } = options;

    // Absolute URLs (e.g. the Supabase token refresh) bypass the API base.
    const url = /^https?:\/\//i.test(path) ? path : `${config.API_BASE}${path}`;

    const doRequest = async (retried: boolean): Promise<T> => {
      // Built per attempt so the post-refresh retry carries the new token.
      const headers: Record<string, string> = {
        'Content-Type': 'application/json',
        Accept: 'application/json',
        ...extraHeaders,
      };
      if (!noAuth && this.token) {
        headers.Authorization = `Bearer ${this.token}`;
      }

      const controller = new AbortController();
      const timer = setTimeout(() => controller.abort(), timeoutMs);
      try {
        const res = await fetch(url, {
          method,
          headers,
          body: body !== undefined ? JSON.stringify(body) : undefined,
          signal: controller.signal,
        });

        const data = await readJson(res);

        if (res.ok) {
          return data as T;
        }

        if (res.status === 401 && !retried) {
          // Prefer a per-request onUnauthorized hook; otherwise fall back to
          // the client-wide refresh fn set via setRefresh(). Once the refresh
          // hook runs, retry a single time with a fresh token.
          const refreshFn = onUnauthorized ?? this.refresh;
          if (refreshFn) {
            await refreshFn();
            return doRequest(true);
          }
        }

        const message =
          (data && typeof data === 'object' && 'error' in data
            ? String((data as Record<string, unknown>).error)
            : undefined) || res.statusText || `HTTP ${res.status}`;
        throw new ApiError(res.status, message, data);
      } finally {
        clearTimeout(timer);
      }
    };

    return doRequest(false);
  }

  get<T = unknown>(path: string, options: RequestOptions = {}): Promise<T> {
    return this.request<T>(path, { ...options, method: 'GET' });
  }

  post<T = unknown>(path: string, body?: unknown, options: RequestOptions = {}): Promise<T> {
    return this.request<T>(path, { ...options, method: 'POST', body });
  }

  delete<T = unknown>(path: string, options: RequestOptions = {}): Promise<T> {
    return this.request<T>(path, { ...options, method: 'DELETE' });
  }
}

/** Shared client instance. Token/refresh wiring happens in the auth store. */
export const apiClient = new ApiClient();

export default apiClient;