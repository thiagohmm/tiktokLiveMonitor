/**
 * Authentication store (Zustand).
 *
 * Responsibilities:
 *  - Load `/api/auth/config`; treat `enabled:false` as an anonymous admin.
 *  - Sign in via `/api/auth/login`, persisting tokens in secure storage.
 *  - Refresh the Supabase access token via `/auth/v1/token`
 *    (grant_type=refresh_token) using the supabaseUrl/supabaseAnonKey from config.
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

/** Persist tokens and keep the shared client in sync with the session. */
function syncSession(session: Session): void {
  apiClient.setToken(session.access_token);
  // Refresh hook must resolve to the new access token so the client can retry.
  apiClient.setRefresh(() =>
    useAuthStore
      .getState()
      .refresh()
      .then(() => useAuthStore.getState().token ?? ''),
  );
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
        apiClient.setToken(token);
        const user = await loadUser();
        if (user?.authenticated) {
          setState({ status: 'authenticated', user, isLoading: false });
          return;
        }
        // Stale session: attempt a refresh before giving up.
        try {
          await getState().refresh();
        } catch {
          /* refresh failed below; fall through to unauthenticated */
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
      setState({ token: session.access_token, refreshToken: session.refresh_token });
      await secureStore.set(SecureStoreKeys.accessToken, session.access_token);
      await secureStore.set(SecureStoreKeys.refreshToken, session.refresh_token);
      syncSession(session);

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
    const { refreshToken, config } = getState();
    if (!refreshToken) {
      await getState().signOut();
      throw new Error('sem token de atualização');
    }
    try {
      const next = await refreshSession(
        refreshToken,
        config?.supabaseUrl,
        config?.supabaseAnonKey,
      );
      setState({ token: next.access_token, refreshToken: next.refresh_token });
      await secureStore.set(SecureStoreKeys.accessToken, next.access_token);
      if (next.refresh_token) {
        await secureStore.set(SecureStoreKeys.refreshToken, next.refresh_token);
      }
      syncSession(next);
    } catch (err) {
      // Refresh failed (expired/revoked token, network): log out.
      await getState().signOut();
      throw err;
    }
  },

  async signOut(): Promise<void> {
    try {
      await fetchSignOut();
    } catch {
      // Ignore network/logout errors; still clear local state.
    } finally {
      apiClient.setToken(null);
      apiClient.setRefresh(undefined);
      await secureStore.delete(SecureStoreKeys.accessToken);
      await secureStore.delete(SecureStoreKeys.refreshToken);
      await secureStore.delete(SecureStoreKeys.userId);
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