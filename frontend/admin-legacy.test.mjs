import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import { runInNewContext } from 'node:vm';

const source = readFileSync(new URL('./admin.js', import.meta.url), 'utf8');
const start = source.indexOf('// ---- Lives do legado: helpers puros');
const end = source.indexOf('// ---- fim dos helpers puros ----');
assert.ok(start > 0 && end > start, 'helper markers must exist in admin.js');
const context = {};
runInNewContext(source.slice(start, end), context);
const { groupLegacyLives, legacyMoveConfirmText } = context;

test('groupLegacyLives groups sessions by live name, most recent first', () => {
    const groups = groupLegacyLives([
        { id: 's1', name: 'alpha', day: '2026-08-01', events: 3 },
        { id: 's2', name: 'beta', day: '2026-08-10', events: 1 },
        { id: 's3', name: 'alpha', day: '2026-08-05', events: '4' },
        { id: 's4', name: 'alpha', day: '2026-07-30' },
    ]);
    assert.equal(groups.length, 2);
    assert.equal(groups[0].name, 'beta');
    const alpha = groups[1];
    assert.deepEqual([...alpha.sessionIds], ['s1', 's3', 's4']);
    assert.equal(alpha.events, 7);
    assert.equal(alpha.firstDay, '2026-07-30');
    assert.equal(alpha.lastDay, '2026-08-05');
});

test('groupLegacyLives tolerates empty input', () => {
    assert.equal(groupLegacyLives(undefined).length, 0);
    assert.equal(groupLegacyLives([]).length, 0);
});

test('legacyMoveConfirmText names the destination and warns about settings', () => {
    const plain = legacyMoveConfirmText('a live "alpha"', 3, 'Cliente X', false);
    assert.match(plain, /3 sessões/);
    assert.match(plain, /"Cliente X"/);
    assert.doesNotMatch(plain, /configurações/);
    const single = legacyMoveConfirmText('a live "alpha"', 1, 'Cliente X', true);
    assert.match(single, /1 sessão\)/);
    assert.match(single, /configurações do legado vão substituir/);
});
