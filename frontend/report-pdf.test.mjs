import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import { runInNewContext } from 'node:vm';

const source = readFileSync(new URL('./renderer.js', import.meta.url), 'utf8');
const start = source.indexOf('// --- Relatório Pós-Live ---');
const end = source.indexOf('// Garante a visibilidade da seção "todos os presentes".');
const reportScript = source.slice(start, end);

class Element {
    constructor() {
        this.children = [];
        this.style = {};
        this.disabled = false;
        this.textContent = '';
        this.className = '';
        this._innerHTML = '';
    }

    set innerHTML(value) {
        this._innerHTML = value;
        if (value === '') this.children = [];
    }

    get innerHTML() { return this._innerHTML; }

    appendChild(child) {
        this.children.push(child);
        child.parent = this;
        return child;
    }

    querySelectorAll() { return []; }

    remove() {
        if (this.parent) {
            this.parent.children = this.parent.children.filter(child => child !== this);
        }
    }
}

function setup(fetch) {
    const elements = {
        generateReportBtn: new Element(),
        downloadPdfBtn: new Element(),
        reportWrap: new Element(),
        reportSummary: new Element(),
        reportText: new Element(),
        reportError: new Element(),
        participantsSection: new Element(),
        participantsTableWrap: new Element(),
        participantsCount: new Element(),
    };
    elements.generateReportBtn.textContent = 'Gerar Relatório';
    const context = {
        ...elements,
        document: { createElement: () => new Element() },
        window: { jspdf: { jsPDF: function FakePDF() {} } },
        fetch,
        console: { error() {} },
    };
    runInNewContext(reportScript, context);
    return { context, elements };
}

const report = {
    liveName: 'Live Teste',
    startedAt: '2026-09-11T10:00:00-03:00',
    endedAt: '2026-09-11T11:00:00-03:00',
    durationMinutes: 60,
    participantCount: 1,
    messageCount: 3,
    giftCount: 2,
};

test('keeps the report usable when the participant request fails', async () => {
    const calls = [];
    const { context, elements } = setup(async url => {
        calls.push(url);
        if (url.startsWith('/api/ranking')) throw new Error('offline');
        return { ok: true, json: async () => report };
    });

    await context.loadReport();

    assert.deepEqual(calls, ['/api/ranking?mode=engagement', '/api/report']);
    assert.equal(elements.reportError.style.display, 'none');
    assert.equal(elements.downloadPdfBtn.disabled, false);
    assert.match(elements.participantsTableWrap.children[0].textContent, /não foi possível carregar/);
});

test('renders participant names as text instead of executable HTML', () => {
    const { context, elements } = setup(async () => ({ ok: true, json: async () => report }));
    context.renderParticipants([{
        nickname: '<img src=x onerror=alert(1)>',
        score: 1,
        firstSeen: '2026-09-11T10:00:00Z',
        lastSeen: '2026-09-11T10:01:00Z',
    }]);

    const table = elements.participantsTableWrap.children[0];
    const tbody = table.children[1];
    assert.equal(tbody.children[0].children[1].textContent, '<img src=x onerror=alert(1)>');
    assert.equal(tbody.children[0].children.length, 6);
});

test('sanitizes the PDF filename and HTML interpolations', () => {
    const { context } = setup(async () => ({ ok: true, json: async () => report }));
    assert.equal(context.reportPdfFilename('../../Líve da Ana 🌹'), 'resumo-live-Live-da-Ana.pdf');
    assert.equal(
        context.escapeHtml(`<script data-x="1">'&</script>`),
        '&lt;script data-x=&quot;1&quot;&gt;&#39;&amp;&lt;/script&gt;'
    );
});
