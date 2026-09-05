/* Sanity test do auth store: init c/ token expirado, single-flight, signOut. */
import { apiClient } from '../src/api/client';
import { secureStore } from '../src/store/secureStore';
import { useAuthStore } from '../src/store/auth';

let supabaseCalls = 0;

(globalThis as Record<string, unknown>).fetch = async (
  url: string,
  init?: RequestInit,
) => {
  const auth = (init?.headers as Record<string, string>)?.Authorization ?? '';
  const ok = (body: unknown, status = 200) =>
    new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

  if (url.endsWith('/api/auth/config')) {
    return ok({
      enabled: true,
      supabaseUrl: 'https://xyz.supabase.co',
      supabaseAnonKey: 'anon-key',
      maxLoginAttempts: 5,
      lockoutMinutes: 15,
    });
  }
  if (url.endsWith('/api/auth/me')) {
    if (auth === 'Bearer new-token') {
      return ok({
        authenticated: true,
        authEnabled: true,
        email: 'user@example.com',
        role: 'user',
        active: true,
      });
    }
    return ok({ error: 'não autorizado' }, 401);
  }
  if (url.endsWith('/api/auth/logout')) {
    if (auth) return ok({ success: true });
    return ok({ error: 'não autorizado' }, 401);
  }
  if (url.startsWith('https://xyz.supabase.co/auth/v1/token')) {
    supabaseCalls++;
    return ok({ access_token: 'new-token', refresh_token: 'new-refresh', expires_in: 3600 });
  }
  if (url.endsWith('/api/state')) {
    if (auth === 'Bearer new-token') return ok({ ok: true });
    return ok({ error: 'não autorizado' }, 401);
  }
  return ok({ error: 'not found' }, 404);
};

function assert(cond: unknown, msg: string): void {
  if (!cond) {
    console.error(`FAIL: ${msg}`);
    process.exit(1);
  }
  console.log(`ok: ${msg}`);
}

// --- 1) Cold start com access token expirado -> refresh -> authenticated ---
await secureStore.set('auth.accessToken', 'expired-token');
await secureStore.set('auth.refreshToken', 'refresh-1');
await secureStore.set('auth.expiresAt', String(Date.now() - 1000)); // já expirou

await useAuthStore.getState().init();
let s = useAuthStore.getState();
assert(s.status === 'authenticated', `init restaura sessão expirada (status=${s.status})`);
assert(s.token === 'new-token', `token renovado (token=${s.token})`);
assert(s.user?.email === 'user@example.com', `user carregado (${s.user?.email})`);
assert(supabaseCalls === 1, `refresh via supabase 1x (calls=${supabaseCalls})`);

// --- 2) Single-flight: dois 401s paralelos -> um único refresh ---
apiClient.setToken('expired-token'); // força 401 nos dois requests
supabaseCalls = 0;
const [a, b] = await Promise.all([
  apiClient.get<{ ok: boolean }>('/api/state'),
  apiClient.get<{ ok: boolean }>('/api/state'),
]);
assert(a.ok && b.ok, `requests paralelos recuperam via refresh (${JSON.stringify(a)} ${JSON.stringify(b)})`);
assert(supabaseCalls === 1, `single-flight: 1 refresh para 2 401s (calls=${supabaseCalls})`);

// --- 3) signOut: logout + limpeza local ---
await useAuthStore.getState().signOut();
s = useAuthStore.getState();
assert(s.status === 'unauthenticated', `signOut desloga (status=${s.status})`);
assert(s.token === null, 'token limpo');
assert((await secureStore.get('auth.accessToken')) === null, 'secure store limpo');

console.log('PASS: todos os testes do auth store');
