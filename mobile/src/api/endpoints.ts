/**
 * Typed wrappers around the backend REST routes (see backend/internal/view).
 * All functions use the shared {@link apiClient}.
 */
import { apiClient } from './client';
import type {
  AuthConfig,
  AuthLoginResponse,
  AuthMe,
  Gift,
  GoalsState,
  LiveRanking,
  PinnedComment,
  Settings,
  State,
  SupabaseRefreshResponse,
  TargetGiftHistory,
  TargetGiftResponse,
} from './types';

// --- Auth -------------------------------------------------------------------

export const authConfig = () =>
  apiClient.get<AuthConfig>('/api/auth/config', { noAuth: true });

export const authMe = () => apiClient.get<AuthMe>('/api/auth/me');

export const signIn = (email: string, password: string) =>
  apiClient.post<AuthLoginResponse>(
    '/api/auth/login',
    { email, password },
    { noAuth: true },
  );

/**
 * Direct Supabase token refresh (grant_type=refresh_token). Uses the
 * supabaseUrl/supabaseAnonKey from GET /api/auth/config so the app never
 * needs the service role key.
 */
export const refreshSession = (
  refreshToken: string,
  supabaseUrl: string | undefined,
  supabaseAnonKey: string | undefined,
): Promise<SupabaseRefreshResponse> => {
  if (!supabaseUrl || !supabaseAnonKey) {
    return Promise.reject(new Error('supabase not configured'));
  }
  return apiClient.request<SupabaseRefreshResponse>(
    `${supabaseUrl}/auth/v1/token?grant_type=refresh_token`,
    {
      noAuth: true,
      method: 'POST',
      body: { refresh_token: refreshToken },
      headers: { apikey: supabaseAnonKey },
    },
  );
};

export const signOut = () => apiClient.post<void>('/api/auth/logout');

// --- Monitor ----------------------------------------------------------------

export const getState = () => apiClient.get<State>('/api/state');

export const connect = (username: string) =>
  apiClient.post<void>('/api/connect', { username });

export const disconnect = () => apiClient.post<void>('/api/disconnect');

export const getSettings = () => apiClient.get<Settings>('/api/settings');

export const setSettings = (settings: Settings) =>
  apiClient.post<void>('/api/settings', settings);

export const getGoals = () => apiClient.get<GoalsState>('/api/goals');

export const completeGoal = (id: number) =>
  apiClient.post<void>(`/api/goals/complete?id=${id}`);

export const cancelGoal = (id: number) =>
  apiClient.post<void>(`/api/goals/cancel?id=${id}`);

export const getRanking = (mode?: string, live?: string) => {
  const params = new URLSearchParams();
  if (mode) params.set('mode', mode);
  if (live) params.set('live', live);
  const query = params.toString();
  return apiClient.get<LiveRanking>(`/api/ranking${query ? `?${query}` : ''}`);
};

export const getPinnedComments = (limit = 15) =>
  apiClient.get<PinnedComment>(`/api/pinned-comments?limit=${limit}`);

export const getTargetGiftHistory = (
  limit = 50,
  pending = false,
) => {
  const query = pending ? `?pending=1&limit=${limit}` : `?limit=${limit}`;
  return apiClient.get<TargetGiftHistory>(
    `/api/target-gift-history${query}`,
  );
};

export const answerTargetGift = (id: number, responseType: TargetGiftResponse) =>
  apiClient.post<void>('/api/target-gift-history/answer', { id, responseType });

export const getGifts = (limit = 200, user?: string) => {
  const params = new URLSearchParams();
  params.set('limit', String(limit));
  if (user) params.set('user', user);
  return apiClient.get<Gift>(`/api/gifts?${params.toString()}`);
};

export const getAvailableGifts = () =>
  apiClient.get<string[]>('/api/available-gifts');

// --- Admin (role admin only) ------------------------------------------------

export const getLives = (limit = 100) =>
  apiClient.get<{ lives: unknown[] }>(`/api/admin/lives?limit=${limit}`);

export const deleteLive = (live: string) =>
  apiClient.post<void>(`/api/admin/lives/delete?live=${encodeURIComponent(live)}`);

export const getUsers = () => apiClient.get<unknown[]>('/api/admin/users');

export const deleteUser = (id: number) =>
  apiClient.post<void>(`/api/admin/users/delete?id=${id}`);