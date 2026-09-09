import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import { runInNewContext } from 'node:vm';

const source = readFileSync(new URL('./renderer.js', import.meta.url), 'utf8');
const section = (start, end) => source.slice(source.indexOf(start), source.indexOf(end));
const script = [
    section('function findTargetGiftRowByHistoryId(', 'async function loadTargetGiftHistoryFromApi('),
    section('function giftQueueSortKey(', 'function getTargetExpirationMinutes('),
    section('function removeUser(', 'async function loadInitialState(')
].join('\n');

// Minimal DOM supporting the queue's actual rendering and event handlers.
class Element {
    constructor() {
        this.dataset = {};
        this.children = [];
        this.attributes = {};
        this.style = {};
        this.className = '';
        this.classList = {
            contains: name => this.className.split(' ').includes(name),
            add: name => this.classList.toggle(name, true),
            toggle: (name, enabled) => {
                const classes = new Set(this.className.split(' ').filter(Boolean));
                enabled ? classes.add(name) : classes.delete(name);
                this.className = [...classes].join(' ');
            }
        };
    }
    get innerText() { return this.textContent; }
    setAttribute(name, value) { this.attributes[name] = value; }
    getAttribute(name) { return this.attributes[name]; }
    addEventListener(name, handler) { this[name] = handler; }
    appendChild(child) { child.remove(); this.children.push(child); child.parent = this; }
    remove() {
        if (this.parent) this.parent.children = this.parent.children.filter(child => child !== this);
        this.parent = null;
    }
    querySelectorAll(selector) {
        const names = selector.split(',').map(part => part.trim().slice(1));
        return this.children.flatMap(child => [
            ...(names.some(name => child.classList.contains(name)) ? [child] : []),
            ...child.querySelectorAll(selector)
        ]);
    }
    querySelector(selector) { return this.querySelectorAll(selector)[0] || null; }
    closest(selector) { return this.classList.contains(selector.slice(1)) ? this : this.parent?.closest(selector); }
}

function setup(fetch = async () => ({ ok: true })) {
    const timers = new Map();
    const answered = [];
    let timerID = 0;
    const context = {
        document: { createElement: () => new Element() },
        userTableBody: new Element(),
        autoRemoveTimers: {},
        fetch,
        console: { error() {} },
        rememberLiveUser() {}, addTargetGiftToHistory() {},
        createFollowerBadge() { return null; }, followerStatusForDisplay() {},
        getTargetExpirationMs: () => 240000,
        markTargetGiftAnswered: (id, type) => answered.push([id, type]),
        loadPendingTargetGifts: async () => {},
        activeModalType: null,
        setTimeout: callback => { timers.set(++timerID, callback); return timerID; },
        clearTimeout: id => timers.delete(id)
    };
    runInNewContext(script, context);
    const add = (id, extra = {}) => context.addUserToList({
        historyId: id, uniqueId: 'ana', nickname: 'Ana', giftName: 'Rosa',
        receivedAt: new Date().toISOString(), ...extra
    }, { fromHistory: true });
    return { context, add, timers, answered, rows: () => context.userTableBody.children };
}

test('restores separate pending IDs for the same user/gift, with independent timers', () => {
    const { context, add, rows, timers, answered } = setup();
    add(1, { isPriority: true, priorityAt: new Date().toISOString() });
    add(2);
    assert.deepEqual(rows().map(row => row.dataset.historyId), ['1', '2']);
    assert.equal(rows()[0].dataset.priority, 'true');
    assert.equal(timers.size, 2);
    add(1, { isPriority: true, priorityAt: new Date().toISOString() });
    assert.equal(rows().length, 2);
    assert.equal(timers.size, 2);
    const first = rows()[0];
    context.removeUser('ana', 'Rosa', first.querySelector('.action-btn'));
    assert.deepEqual(answered, [['1', 'manual']]);
    assert.equal(timers.size, 1);
    [...timers.values()][0]();
    assert.deepEqual(answered, [['1', 'manual'], ['2', 'automatic']]);
    assert.equal(rows().length, 0);
});

test('blocks overlapping changes and leaves the confirmed state after failures', async () => {
    const pending = [];
    const { context, add, rows } = setup(() => new Promise(resolve => pending.push(resolve)));
    add(1);
    const row = rows()[0];
    const first = context.markTargetGiftPriority(1, true);
    await context.markTargetGiftPriority(1, false);
    assert.equal(pending.length, 1);
    assert.equal(row.dataset.priorityPending, 'true');
    pending[0]({ ok: false, status: 500 });
    await first;
    const retry = context.markTargetGiftPriority(1, true);
    pending[1]({ ok: false, status: 500 });
    await retry;
    assert.notEqual(row.dataset.priority, 'true');
    assert.equal(row.querySelector('.priority-badge'), null);
    assert.equal(row.dataset.priorityPending, undefined);
});

test('uses server promotion time even when the browser clock is behind', async () => {
    const earlier = new Date(Date.now() + 60000).toISOString();
    const later = new Date(Date.now() + 61000).toISOString();
    const { context, add, rows } = setup(async () => ({
        ok: true, json: async () => ({ success: true, isPriority: true, priorityAt: later })
    }));
    add(1, { isPriority: true, priorityAt: earlier });
    add(2);
    await context.markTargetGiftPriority(2, true);
    assert.deepEqual(rows().map(row => row.dataset.historyId), ['1', '2']);
    assert.equal(rows()[1].dataset.priorityAt, later);
});

test('demotion clears the promotion timestamp and restores normal FIFO order', async () => {
    const { context, add, rows } = setup(async () => ({
        ok: true, json: async () => ({ success: true, isPriority: false, priorityAt: null })
    }));
    add(1, { receivedAt: new Date(Date.now() - 2000).toISOString() });
    add(2, { isPriority: true, priorityAt: new Date().toISOString() });
    assert.equal(rows()[0].dataset.historyId, '2');
    await context.markTargetGiftPriority(2, false);
    assert.deepEqual(rows().map(row => row.dataset.historyId), ['1', '2']);
    assert.equal(rows()[1].dataset.priorityAt, undefined);
});

test('reconciles a request failure with a promotion committed by the server', async () => {
    const { context, add, rows } = setup(async () => { throw new Error('connection lost'); });
    add(1);
    const stamp = new Date().toISOString();
    context.loadPendingTargetGifts = async () => add(1, { isPriority: true, priorityAt: stamp });
    await context.markTargetGiftPriority(1, true);
    assert.equal(rows()[0].dataset.priority, 'true');
    assert.equal(rows()[0].dataset.priorityAt, stamp);
    assert.equal(rows()[0].dataset.priorityPending, undefined);
});

test('live events with separate history IDs remain distinct and sort by server receipt time', () => {
    const { context, rows } = setup();
    const receivedAt = new Date().toISOString();
    context.addUserToList({ historyId: 2, uniqueId: 'ana', giftName: 'Rosa', receivedAt });
    context.addUserToList({ historyId: 1, uniqueId: 'ana', giftName: 'Rosa', receivedAt: new Date(Date.now() - 1000).toISOString() });
    assert.deepEqual(rows().map(row => row.dataset.historyId), ['1', '2']);
});
