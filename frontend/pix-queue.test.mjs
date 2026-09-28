import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import { runInNewContext } from 'node:vm';

const source = readFileSync(new URL('./pix.js', import.meta.url), 'utf8');
const helpers = source.slice(source.indexOf('function formatTime('), source.indexOf('// ── Estado'));
const context = {};
runInNewContext(helpers, context);
const { formatTime, receiptLabel, shouldBeaconOnPageHide, mediaState, truncate, pairingLabel, qrHint, parseMoneyBR, formatBRL, formatPhoneBR, contactLabel, contactSubline, initials, isLidJid, pairingControls } = context;

test('parseMoneyBR converts pt-BR amounts to cents', () => {
    assert.equal(parseMoneyBR('15'), 1500);
    assert.equal(parseMoneyBR('15,00'), 1500);
    assert.equal(parseMoneyBR('R$ 15,00'), 1500);
    assert.equal(parseMoneyBR('15.00'), 1500);
    assert.equal(parseMoneyBR('1.234,56'), 123456);
    assert.equal(parseMoneyBR('1.200'), 120000);
    assert.equal(parseMoneyBR(' 10,50 '), 1050);
    assert.equal(parseMoneyBR('0,01'), 1);
});

test('parseMoneyBR rejects garbage', () => {
    for (const bad of ['', 'abc', '1,2345', '-5', '15,000,5', '1e3', 'R$', '15,']) {
        assert.equal(parseMoneyBR(bad), null, `expected null for ${JSON.stringify(bad)}`);
    }
});

test('formatBRL renders cents as pt-BR currency', () => {
    assert.equal(formatBRL(1500), 'R$ 15,00');
    assert.equal(formatBRL(200), 'R$ 2,00');
    assert.equal(formatBRL(1), 'R$ 0,01');
    assert.equal(formatBRL(123456), 'R$ 1.234,56');
    assert.equal(formatBRL(0), 'R$ 0,00');
});

test('formatTime renders pt-BR short date and tolerates invalid input', () => {
    assert.equal(formatTime(''), '');
    assert.equal(formatTime('not-a-date'), '');
    const formatted = formatTime('2026-09-26T16:30:00Z');
    assert.match(formatted, /\d{2}\/\d{2}/);
});

test('receiptLabel shows a badge only with receipt', () => {
    assert.equal(receiptLabel(true), '📎');
    assert.equal(receiptLabel(false), '');
});

test('shouldBeaconOnPageHide skips reloads', () => {
    assert.equal(shouldBeaconOnPageHide('reload'), false);
    assert.equal(shouldBeaconOnPageHide('navigate'), true);
    assert.equal(shouldBeaconOnPageHide(undefined), true);
});

test('mediaState classifies text, available, gone and unsupported', () => {
    assert.equal(mediaState({ type: 'text' }), 'text');
    assert.equal(mediaState({ type: 'unsupported' }), 'unsupported');
    assert.equal(mediaState({ type: 'image', mediaMime: 'image/jpeg' }), 'available');
    assert.equal(mediaState({ type: 'image', mediaMime: 'image/jpeg', mediaDeletedAt: '2026-01-01' }), 'gone');
    assert.equal(mediaState({ type: 'document' }), 'gone');
});

test('truncate keeps small text and ellipsizes long text', () => {
    assert.equal(truncate('abc', 10), 'abc');
    assert.equal(truncate('abcdefghij', 5).length, 5);
    assert.ok(truncate('abcdefghij', 5).endsWith('…'));
});

test('pairingLabel reflects every pairing state', () => {
    assert.equal(pairingLabel('connected', '5511999990000'), 'Conectado — 5511999990000');
    assert.equal(pairingLabel('connected', ''), 'Conectado');
    assert.equal(pairingLabel('scan_qr', ''), 'Pairing por QR');
    assert.equal(pairingLabel('starting', ''), 'Conectando...');
    assert.equal(pairingLabel('failed', ''), 'Erro de WhatsApp');
    assert.equal(pairingLabel('stopped', ''), 'Desconectado');
    assert.equal(pairingLabel('disconnected', ''), 'Desconectado');
    assert.equal(pairingLabel('', ''), 'Desconectado');
});

test('qrHint separates a wait from a real failure', () => {
    assert.equal(qrHint('qr_pending').text, 'Conectando...');
    assert.equal(qrHint('qr_pending').tone, 'info');
    assert.equal(qrHint('already_connected').text, 'Conectado');
    assert.equal(qrHint('already_connected').tone, 'info');
    assert.equal(qrHint('qr_failed').text, 'Erro de WhatsApp');
    assert.equal(qrHint('qr_failed').tone, 'error');
    assert.equal(qrHint('unexpected').tone, 'error');
});

test('formatPhoneBR groups Brazilian numbers and tolerates junk', () => {
    assert.equal(formatPhoneBR('5511989592960'), '+55 11 98959-2960');
    assert.equal(formatPhoneBR('551198959296'), '+55 11 9895-9296');
    assert.equal(formatPhoneBR('552134567890'), '+55 21 3456-7890');
    assert.equal(formatPhoneBR('123'), '+123');
    assert.equal(formatPhoneBR(''), '');
});

test('contactLabel never exposes a raw JID', () => {
    assert.equal(contactLabel({ pushName: 'Fulano' }), 'Fulano');
    assert.equal(contactLabel({ phoneE164: '5511989592960' }), '+55 11 98959-2960');
    assert.equal(contactLabel({ whatsappJid: '37048606048491@lid' }), 'Cliente não identificado');
    assert.equal(contactLabel(null), 'Cliente');
});

test('contactSubline shows phone when named and a LID tail when private', () => {
    assert.equal(contactSubline({ pushName: 'Fulano', phoneE164: '5511989592960' }), '+55 11 98959-2960');
    const sub = contactSubline({ whatsappJid: '37048606048491@lid' });
    assert.match(sub, /WhatsApp privado · ID …048491$/);
    assert.equal(contactSubline({ pushName: 'Fulano', phoneE164: '5511999999999', whatsappJid: '5511999999999@c.us' }), '+55 11 99999-9999');
});

test('initials and isLidJid', () => {
    assert.equal(initials('Thiago Henrique'), 'T');
    assert.equal(initials('  '), '?');
    assert.equal(initials(''), '?');
    assert.equal(isLidJid('37048606048491@lid'), true);
    assert.equal(isLidJid('5511989592960@c.us'), false);
    assert.equal(isLidJid(''), false);
});

test('pairingControls shows connect/QR/disconnect only to the organization owner', () => {
    const plain = value => JSON.parse(JSON.stringify(value));
    assert.deepEqual(plain(pairingControls(true, false)), { connect: true, disconnect: false, qr: true, ownerOnlyNotice: false });
    assert.deepEqual(plain(pairingControls(true, true)), { connect: false, disconnect: true, qr: false, ownerOnlyNotice: false });
    assert.deepEqual(plain(pairingControls(false, false)), { connect: false, disconnect: false, qr: false, ownerOnlyNotice: true });
    assert.deepEqual(plain(pairingControls(false, true)), { connect: false, disconnect: false, qr: false, ownerOnlyNotice: false });
});
