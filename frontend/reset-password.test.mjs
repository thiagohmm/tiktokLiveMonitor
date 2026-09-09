import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import { runInNewContext } from 'node:vm';

const html = readFileSync(new URL('./reset-password.html', import.meta.url), 'utf8');
const script = [...html.matchAll(/<script>([\s\S]*?)<\/script>/g)].map(match => match[1]).join('\n');

async function loadForm({ hash = '#access_token=test-token&type=recovery', failure } = {}) {
    const elements = new Map();
    const calls = [];
    const document = {
        getElementById(id) {
            if (!elements.has(id)) {
                const classes = new Set();
                elements.set(id, {
                    value: 'test-password',
                    disabled: false,
                    classList: {
                        add: name => classes.add(name),
                        remove: name => classes.delete(name),
                        contains: name => classes.has(name),
                    },
                    addEventListener(event, handler) { this[event] = handler; },
                });
            }
            return elements.get(id);
        },
    };
    const window = {
        location: { hash },
        TLMAuth: {
            async loadAuthConfig() {},
            async resetPassword(token, password) {
                calls.push({ token, password });
                if (failure) throw new Error(failure);
            },
        },
    };
    runInNewContext(script, { document, window, URLSearchParams, setTimeout() {} });
    await new Promise(resolve => setImmediate(resolve));
    return { elements, calls, submit: () => elements.get('resetForm').submit({ preventDefault() {} }) };
}

test('submits a valid password and displays success', async () => {
    const form = await loadForm();
    await form.submit();
    assert.deepEqual(form.calls, [{ token: 'test-token', password: 'test-password' }]);
    assert.equal(form.elements.get('resetSuccess').classList.contains('visible'), true);
    assert.equal(form.elements.get('resetBtn').disabled, true);
});

test('rejects mismatched passwords without calling the API', async () => {
    const form = await loadForm();
    form.elements.get('confirmPassword').value = 'different-password';
    await form.submit();
    assert.equal(form.calls.length, 0);
    assert.equal(form.elements.get('resetError').classList.contains('visible'), true);
});

test('displays API errors and enables retry', async () => {
    const form = await loadForm({ failure: 'Token expirado' });
    await form.submit();
    assert.equal(form.elements.get('resetError').textContent, 'Token expirado');
    assert.equal(form.elements.get('resetBtn').disabled, false);
});

test('disables the form when the recovery token is missing', async () => {
    const form = await loadForm({ hash: '' });
    assert.equal(form.elements.get('resetBtn').disabled, true);
    assert.equal(form.elements.get('newPassword').disabled, true);
    assert.equal(form.calls.length, 0);
});
