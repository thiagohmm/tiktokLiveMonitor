/**
 * Authentication store (Zustand).
 *
 * Responsibilities:
 *  - Load `/api/auth/config`; treat `enabled:false` as an anonymous admin.
 *  - Sign in via `/api/auth/login`, persisting tokens in secure storage.
 *  - Refresh the Supabase access token via `/auth/v1/token`
 *    (grant_type=refresh_token) using the supabaseUrl/supabaseAnonKey from config.
 *    Runs reactively on 401 and proactively ahead of the token expiry;
 *    concurrent refreshes are single-flighted (Supabase rotates the token).
 *    A 4xx from Supabase revokes the session; a plain network error does not.
 *  - Sign out via `/api/auth/logout` and clear local state.
 *  - Wire the access token + refresh hook into the shared {@link apiClient}.
 */
import { create } from 'zustand';

import { apiClient, ApiError } from '@/api/client';
import {
  authConfig as fetchAuthConfig,
  authMe as fetchAuthMe,
  refreshSession,
  signIn as fetchSignIn,
  signOut as fetchSignOut,
} from '@/api/endpoints';
import type {
  AuthConfig,
  AuthMe,
  LockoutStatus,
  Session,
} from '@/api/types';
import { SecureStoreKeys, secureStore } from './secureStore';

type AuthStatus = 'idle' | 'loading' | 'authenticated' | 'unauthenticated';

interface AuthState {
  status: AuthStatus;
  config: AuthConfig | null;
  user: AuthMe | null;
  token: string | null;
  refreshToken: string | null;
  lockout: LockoutStatus | null;
  error: string | null;
  isLoading: boolean;
}

interface AuthActions {
  init: () => Promise<void>;
  loadConfig: () => Promise<void>;
  signIn: (email: string, password: string) => Promise<void>;
  refresh: () => Promise<void>;
  signOut: () => Promise<void>;
  clearError: () => void;
  setSession: (session: Session) => void;
}

export type AuthStore = AuthState & AuthActions;

/** Anonymous admin used when authentication is disabled (dev / AUTH_ENABLED=0). */
const offlineAdminUser: AuthMe = {
  authenticated: true,
  authEnabled: false,
  role: 'admin',
  active: true,
};

async function loadUser(): Promise<AuthMe | null> {
  try {
    return await fetchAuthMe();
  } catch {
    return null;
  }
}

/** Refresh this long before the access token expires. */
const PROACTIVE_REFRESH_MARGIN_MS = 60_000;

let proactiveRefreshTimer: ReturnType<typeof setTimeout> | null = null;

/** Single-flight guard: concurrent 401s share one refresh (token rotation). */
let inFlightRefresh: Promise<void> | null = null;

/** Phase 5 gate for the admin tab (plan: "rota admin só se role === 'admin'"). */
export const selectIsAdmin = (s: AuthStore): boolean => s.user?.role === 'admin';

/** Persist tokens + expiry timestamp in secure storage. */
async function persistSession(session: Session): Promise<void> {
  await secureStore.set(SecureStoreKeys.accessToken, session.access_token);
  await secureStore.set(SecureStoreKeys.refreshToken, session.refresh_token);
  if (session.expires_in > 0) {
    await secureStore.set(
      SecureStoreKeys.expiresAt,
      String(Date.now() + session.expires_in * 1000),
    );
  }
}

/** Keep the shared client in sync with the session (token + refresh hook). */
function syncSession(session: Session): void {
  apiClient.setToken(session.access_token);
  // Refresh hook must resolve to the new access token so the client can retry.
  apiClient.setRefresh(() =>
    useAuthStore
      .getState()
      .refresh()
      .then(() => useAuthStore.getState().token ?? ''),
  );
  scheduleProactiveRefresh(session.expires_in);
}

function cancelProactiveRefresh(): void {
  if (proactiveRefreshTimer) {
    clearTimeout(proactiveRefreshTimer);
    proactiveRefreshTimer = null;
  }
}

/** Refresh ahead of the access-token expiry (plan: "rodar ... em expiração"). */
function scheduleProactiveRefresh(expiresInSec: number): void {
  cancelProactiveRefresh();
  if (!Number.isFinite(expiresInSec) || expiresInSec <= 0) {
    return;
  }
  const delay = Math.max(expiresInSec * 1000 - PROACTIVE_REFRESH_MARGIN_MS, 0);
  proactiveRefreshTimer = setTimeout(() => {
    proactiveRefreshTimer = null;
    void useAuthStore.getState().refresh().catch(() => {
      /* performRefresh already signs out when the session is invalid */
    });
  }, delay);
}

/**
 * Performs the actual Supabase refresh. Single-flight: concurrent callers
 * share one in-flight promise because Supabase rotates the refresh token
 * (a second call with the same token would fail and log the user out).
 */
async function performRefresh(): Promise<void> {
  const { refreshToken, config } = useAuthStore.getState();
  if (!refreshToken) {
    await useAuthStore.getState().signOut();
    throw new Error('sem token de atualização');
  }
  try {
    const next = await refreshSession(
      refreshToken,
      config?.supabaseUrl,
      config?.supabaseAnonKey,
    );
    const session: Session = {
      access_token: next.access_token,
      // Supabase rotates the refresh token; keep the old one if none returned.
      refresh_token: next.refresh_token || refreshToken,
      expires_in: next.expires_in,
      token_type: next.token_type,
    };
    await persistSession(session);
    useAuthStore.getState().setSession(session);
  } catch (err) {
    // Revoke the session only when the refresh token itself is invalid
    // (Supabase 4xx); a plain network error must not destroy a valid session.
    if (err instanceof ApiError && err.status >= 400 && err.status < 500) {
      await useAuthStore.getState().signOut();
    }
    throw err;
  }
}

const initialState: AuthState = {
  status: 'idle',
  config: null,
  user: null,
  token: null,
  refreshToken: null,
  lockout: null,
  error: null,
  isLoading: false,
};

export const useAuthStore = create<AuthStore>((setState, getState) => ({
  ...initialState,

  setSession(session: Session): void {
    setState({ token: session.access_token, refreshToken: session.refresh_token });
    syncSession(session);
  },

  async loadConfig(): Promise<void> {
    const config = await fetchAuthConfig();
    setState({ config });
  },

  async init(): Promise<void> {
    setState({ isLoading: true, status: 'loading' });
    try {
      await getState().loadConfig();

      const token = await secureStore.get(SecureStoreKeys.accessToken);
      const refreshToken = await secureStore.get(SecureStoreKeys.refreshToken);
      if (token && refreshToken) {
        setState({ token, refreshToken });
        // Wire the refresh hook before the first request so a 401 on
        // /api/auth/me self-heals via refresh + retry inside the client.
        syncSession({ access_token: token, refresh_token: refreshToken, expires_in: 0 });

        // Restore the proactive refresh for the (possibly still valid) session.
        const expiresAtRaw = await secureStore.get(SecureStoreKeys.expiresAt);
        if (expiresAtRaw) {
          const expiresAt = Number(expiresAtRaw);
          if (Number.isFinite(expiresAt) && expiresAt > Date.now()) {
            scheduleProactiveRefresh(Math.floor((expiresAt - Date.now()) / 1000));
          }
        }

        const user = await loadUser();
        if (user?.authenticated) {
          setState({ status: 'authenticated', user, isLoading: false });
          return;
        }
        // Stale session: attempt a refresh and re-validate before giving up.
        try {
          await getState().refresh();
          const refreshedUser = await loadUser();
          if (refreshedUser?.authenticated) {
            setState({ status: 'authenticated', user: refreshedUser, isLoading: false });
            return;
          }
        } catch {
          /* refresh failed; fall through to unauthenticated */
        }
      }

      if (!getState().config?.enabled && !getState().user) {
        setState({ status: 'authenticated', user: offlineAdminUser, isLoading: false });
        return;
      }

      setState({ status: 'unauthenticated', isLoading: false });
    } catch (err) {
      setState({
        error: err instanceof Error ? err.message : 'Falha ao iniciar sessão',
        status: 'unauthenticated',
        isLoading: false,
      });
    }
  },

  async signIn(email: string, password: string): Promise<void> {
    setState({ isLoading: true, error: null, lockout: null });
    try {
      const { session } = await fetchSignIn(email, password);
      await persistSession(session);
      getState().setSession(session);

      const user = await loadUser();
      setState({
        status: 'authenticated',
        user: user ?? { authenticated: true, authEnabled: true },
        lockout: null,
        error: null,
        isLoading: false,
      });
    } catch (err) {
      if (err instanceof ApiError) {
        const body =
          err.body && typeof err.body === 'object'
            ? (err.body as Record<string, unknown>)
            : {};
        setState({
          lockout: {
            locked: Boolean(body.locked),
            retryAfterSec:
              typeof body.retryAfterSec === 'number' ? body.retryAfterSec : undefined,
            remainingAttempts:
              typeof body.remainingAttempts === 'number' ? body.remainingAttempts : undefined,
          },
          error: err.message,
          isLoading: false,
        });
        return;
      }
      setState({
        error: err instanceof Error ? err.message : 'Falha na autenticação',
        isLoading: false,
      });
    }
  },

  async refresh(): Promise<void> {
    if (inFlightRefresh) {
      return inFlightRefresh;
    }
    inFlightRefresh = performRefresh().finally(() => {
      inFlightRefresh = null;
    });
    return inFlightRefresh;
  },

  async signOut(): Promise<void> {
    // Detach the refresh hook first so a 401 on the logout request cannot
    // re-enter refresh() (recursion / deadlock).
    apiClient.setRefresh(undefined);
    try {
      await fetchSignOut();
    } catch {
      // Ignore network/logout errors; still clear local state.
    } finally {
      apiClient.setToken(null);
      cancelProactiveRefresh();
      await secureStore.delete(SecureStoreKeys.accessToken);
      await secureStore.delete(SecureStoreKeys.refreshToken);
      await secureStore.delete(SecureStoreKeys.expiresAt);
      setState({
        ...initialState,
        status: 'unauthenticated',
        config: getState().config,
      });
    }
  },

  clearError(): void {
    setState({ error: null });
  },
}));

export default useAuthStore;