/* Sanity test: 401 -> refresh -> retry, e URL absoluta do refresh. */
import { ApiClient } from '../src/api/client';

let stateCalls = 0;
let supabaseCalls = 0;

(globalThis as Record<string, unknown>).fetch = async (
  url: string,
  init?: RequestInit,
) => {
  const authed = (init?.headers as Record<string, string>)?.Authorization === 'Bearer new-token';
  if (url.startsWith('http://localhost:3001/api/state')) {
    stateCalls++;
    if (authed) {
      return new Response(JSON.stringify({ ok: true }), { status: 200 });
    }
    return new Response(JSON.stringify({ error: 'não autorizado' }), { status: 401 });
  }
  if (url.startsWith('https://xyz.supabase.co/auth/v1/token')) {
    supabaseCalls++;
    return new Response(
      JSON.stringify({ access_token: 'new-token', refresh_token: 'new-refresh', expires_in: 3600 }),
      { status: 200 },
    );
  }
  return new Response('not found', { status: 404 });
};

// 1) 401 -> refresh hook -> retry com token novo
const client = new ApiClient();
client.setToken('old-token');
client.setRefresh(async () => {
  const res = await fetch('https://xyz.supabase.co/auth/v1/token?grant_type=refresh_token', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ refresh_token: 'r' }),
  });
  const data = (await res.json()) as { access_token: string };
  client.setToken(data.access_token);
  return data.access_token;
});

const result = await client.get<{ ok: boolean }>('/api/state');
console.log('1) result:', result, '| state calls:', stateCalls, '| supabase calls:', supabaseCalls);
if (!result.ok || stateCalls !== 2 || supabaseCalls !== 1) {
  console.error('FAIL: 401 -> refresh -> retry');
  process.exit(1);
}

// 2) URL absoluta (refreshSession) não recebe prefixo API_BASE
const direct = await client.request<{ access_token: string }>(
  'https://xyz.supabase.co/auth/v1/token?grant_type=refresh_token',
  { noAuth: true, method: 'POST', body: { refresh_token: 'r' } },
);
console.log('2) direct absolute URL ok:', direct.access_token === 'new-token');
if (direct.access_token !== 'new-token') {
  console.error('FAIL: URL absoluta');
  process.exit(1);
}

// 3) retry cap: onUnauthorized no-op não causa loop infinito
let attempts = 0;
const client2 = new ApiClient();
client2.setToken('bad');
const p = client2.get('/api/state', {
  onUnauthorized: async () => {
    attempts++;
  },
}).catch((err) => err);
const err = await p;
console.log('3) erro esperado após 1 retry:', err?.name, err?.status, '| onUnauthorized calls:', attempts);
if (!(err?.name === 'ApiError' && err?.status === 401) || attempts !== 1) {
  console.error('FAIL: retry cap');
  process.exit(1);
}

console.log('PASS: todos os testes do client');
