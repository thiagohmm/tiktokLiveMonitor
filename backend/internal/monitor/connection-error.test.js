const { test } = require('node:test');
const assert = require('node:assert/strict');
const { fetchSignedWebSocketFromEulerRoute } = require('tiktok-live-connector');
const { connectionFailure } = require('./connection-error');

test('HTTP 429 preserves Retry-After through the real connector route', async () => {
    await assert.rejects(fetchSignedWebSocketFromEulerRoute({
        roomId: '123',
        webClient: {
            cookieJar: { getCookieString: async () => '' },
            clientHeaders: { 'User-Agent': 'test' }
        },
        apiClient: { rooms: { fetchWebcastURL: async () => ({
            status: 429,
            headers: { 'retry-after': '120' },
            data: Buffer.from(JSON.stringify({ message: 'Connection limit exceeded' }))
        }) } }
    }), error => {
        const failure = connectionFailure(error);
        assert.equal(error.reason, 'Rate Limited');
        assert.equal(failure.retryAfterMs, 120000);
        assert.match(failure.error, /120s/);
        return true;
    });
});

test('rate limits without a usable delay use a conservative fallback', () => {
    for (const retryAfter of [undefined, 0, -1, NaN]) {
        assert.equal(connectionFailure({ reason: 'Rate Limited', retryAfter }).retryAfterMs, 60000);
    }
});

test('ordinary connection errors retain their cause', () => {
    assert.deepEqual(connectionFailure(new Error('offline')), {
        success: false, error: 'Falha ao conectar: offline'
    });
});
