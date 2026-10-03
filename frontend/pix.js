// Fila PIX (WhatsApp/WAHA + MinIO) — frontend da tela principal.
//
// Requer que auth.js e renderer.js já tenham carregado: usa window.TLMAuth
// (Bearer/cookie) e assina os eventos SSE no stream criado em renderer.js.
(function () {
    'use strict';

    // ── Helpers puros (exportados para teste) ────────────────────────────

    function formatTime(iso) {
        if (!iso) return '';
        const date = new Date(iso);
        if (Number.isNaN(date.getTime())) return '';
        return date.toLocaleString('pt-BR', { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' });
    }

    function receiptLabel(hasReceipt) {
        return hasReceipt ? '📎' : '';
    }

    function shouldBeaconOnPageHide(navigationType) {
        return navigationType !== 'reload';
    }

    function mediaState(message) {
        if (!message || !message.type) return 'text';
        if (message.type !== 'image' && message.type !== 'document') return message.type;
        if (message.mediaDeletedAt) return 'gone';
        if (!message.mediaMime) return 'gone';
        return 'available';
    }

    function truncate(text, max) {
        const value = String(text || '');
        if (value.length <= max) return value;
        return value.slice(0, max - 1) + '…';
    }

    function pairingLabel(status, phone) {
        const labels = {
            connected: 'Conectado',
            scan_qr: 'Pairing por QR',
            starting: 'Conectando...',
            failed: 'Erro de WhatsApp',
            stopped: 'Desconectado',
            disconnected: 'Desconectado',
        };
        const text = labels[status] || labels.disconnected;
        return status === 'connected' && phone ? text + ' — ' + phone : text;
    }

    // qrHint translates why there is no QR yet. WAHA booting the session is a
    // wait, not a failure, and only a real error deserves the error style.
    function qrHint(state) {
        const hints = {
            qr_pending: { text: 'Conectando...', tone: 'info' },
            already_connected: { text: 'Conectado', tone: 'info' },
            qr_failed: { text: 'Erro de WhatsApp', tone: 'error' },
        };
        return hints[state] || hints.qr_failed;
    }

    // parseMoneyBR converts user input ("15", "15,00", "R$ 1.234,56") to
    // cents. Mirrors receipt.ParseBRLCents on the backend; null = invalid.
    function parseMoneyBR(text) {
        let value = String(text || '').toLowerCase().replace(/r\$/, '').trim();
        if (!value || !/^\d{1,3}(\.\d{3})*([.,]\d{1,2})?$/.test(value)) return null;
        let intPart = value;
        let decPart = '';
        const dec = value.match(/[.,](\d{1,2})$/);
        if (dec) {
            intPart = value.slice(0, dec.index);
            decPart = dec[1];
        }
        intPart = intPart.replace(/\./g, '');
        const units = parseInt(intPart || '0', 10);
        const cents = decPart ? parseInt(decPart.length === 1 ? decPart + '0' : decPart, 10) : 0;
        const total = units * 100 + cents;
        if (!Number.isFinite(total) || total <= 0 || total > 999999999) return null;
        return total;
    }

    function formatBRL(cents) {
        const value = Math.max(0, Math.floor(Number(cents) || 0));
        const units = Math.floor(value / 100);
        const frac = value % 100;
        const grouped = String(units).replace(/\B(?=(\d{3})+(?!\d))/g, '.');
        return 'R$ ' + grouped + ',' + String(frac).padStart(2, '0');
    }

    // formatPhoneBR groups raw WhatsApp digits as a Brazilian number:
    // "5511989592960" -> "+55 11 98959-2960". Anything unrecognized keeps "+digits".
    function formatPhoneBR(digits) {
        const d = String(digits || '').replace(/\D/g, '');
        if (!d) return '';
        let local = d;
        if (d.startsWith('55') && (d.length === 12 || d.length === 13)) {
            local = d.slice(2);
        }
        if (local.length === 10 || local.length === 11) {
            const area = local.slice(0, 2);
            const rest = local.slice(2);
            const body = local.length === 11
                ? rest.slice(0, 5) + '-' + rest.slice(5)
                : rest.slice(0, 4) + '-' + rest.slice(4);
            return '+55 ' + area + ' ' + body;
        }
        return '+' + d;
    }

    function isLidJid(jid) {
        return String(jid || '').toLowerCase().endsWith('@lid');
    }

    // contactLabel never shows a raw JID: name > phone > generic label.
    function contactLabel(contact) {
        if (!contact) return 'Cliente';
        const name = String(contact.pushName || '').trim();
        if (name) return name;
        const phone = formatPhoneBR(contact.phoneE164);
        if (phone) return phone;
        return 'Cliente não identificado';
    }

    // contactSubline is the muted second row: phone when named, and a short
    // tail of the privacy LID so anonymous clients stay distinguishable.
    function contactSubline(contact) {
        if (!contact) return '';
        const name = String(contact.pushName || '').trim();
        const phone = formatPhoneBR(contact.phoneE164);
        if (name && phone) return phone;
        if (isLidJid(contact.whatsappJid)) {
            const id = String(contact.whatsappJid).split('@')[0];
            const tail = id.length > 6 ? '…' + id.slice(-6) : id;
            return 'WhatsApp privado · ID ' + tail;
        }
        return '';
    }

    function initials(name) {
        const clean = String(name || '').trim();
        if (!clean) return '?';
        return clean.split(/\s+/)[0].slice(0, 1).toUpperCase();
    }

    // pairingControls decides which WhatsApp pairing controls are visible.
    // Only the organization's owner (canPair) registers or removes the number;
    // operators see the connection state and keep working the queue.
    function pairingControls(canPair, connected) {
        return {
            connect: !!canPair && !connected,
            disconnect: !!canPair && !!connected,
            qr: !!canPair && !connected,
            ownerOnlyNotice: !canPair && !connected,
        };
    }

    const OWNER_ONLY_HINT = 'Apenas o dono da organização conecta o WhatsApp.';

    // ── Estado ───────────────────────────────────────────────────────────

    const state = {
        status: '',
        connected: false,
        qrLoading: false,
        canPair: false,
        disabled: false,
        selected: null,
        readonly: false,
        pollTimer: null,
        objectURLs: [],
        values: [],
        lastTickets: [],
    };

    const el = id => document.getElementById(id);

    function toggle(node, visible) {
        if (!node) return;
        node.classList.toggle('pix-hidden', !visible);
    }

    async function api(path, init) {
        return window.TLMAuth.authFetch(path, init);
    }

    function releaseObjectURLs() {
        for (const url of state.objectURLs) {
            try { URL.revokeObjectURL(url); } catch (_) { /* noop */ }
        }
        state.objectURLs = [];
    }

    function setStatusText(text) {
        const status = el('pixConnStatus');
        if (!status) return;
        status.textContent = text;
        status.classList.toggle('pix-status-on', state.connected);
    }

    // setHint explains what is happening with the QR: pending reads as progress,
    // only a real failure gets the error style.
    function setHint(text, tone) {
        const hint = el('pixQrHint');
        if (!hint) return;
        hint.textContent = text || '';
        hint.classList.toggle('pix-error', tone === 'error');
        toggle(hint, !!text);
    }

    // ── Conexão WhatsApp ─────────────────────────────────────────────────

    async function refreshStatus(first) {
        try {
            const response = await api('/api/pix/whatsapp/status');
            if (response.status === 503) {
                markDisabled();
                return;
            }
            if (!response.ok) {
                if (first) markDisabled();
                return;
            }
            const data = await response.json();
            state.disabled = false;
            state.status = data.status || '';
            state.connected = !!data.connected;
            setStatusText(pairingLabel(state.status, data.mePhone));
            const controls = pairingControls(state.canPair, state.connected);
            toggle(el('pixConnectBtn'), controls.connect);
            toggle(el('pixDisconnectBtn'), controls.disconnect);
            if (controls.ownerOnlyNotice) {
                hideQR();
                setHint(OWNER_ONLY_HINT, 'info');
            }
            if (state.connected) {
                setHint('');
                hideQR();
                stopStatusPoll();
            } else {
                scheduleStatusPoll();
            }
        } catch (_) {
            if (first) markDisabled();
        }
    }

    function markDisabled() {
        state.disabled = true;
        state.connected = false;
        hideQR();
        stopStatusPoll();
        setStatusText('Desconectado');
        toggle(el('pixContent'), false);
        toggle(el('pixUnavailable'), true);
    }

    function scheduleStatusPoll() {
        if (state.pollTimer || state.disabled) return;
        state.pollTimer = setInterval(() => {
            if (!state.connected) {
                refreshStatus(false);
                if (state.canPair) loadQR();
            }
        }, 3000);
    }

    function stopStatusPoll() {
        if (state.pollTimer) {
            clearInterval(state.pollTimer);
            state.pollTimer = null;
        }
    }

    function hideQR() {
        const img = el('pixQr');
        if (!img) return;
        if (img.dataset.url) {
            try { URL.revokeObjectURL(img.dataset.url); } catch (_) { /* noop */ }
            delete img.dataset.url;
        }
        img.removeAttribute('src');
        toggle(img, false);
    }

    async function loadQR() {
        if (state.disabled || state.connected || state.qrLoading || !state.canPair) return;
        state.qrLoading = true;
        try {
            const response = await api('/api/pix/whatsapp/qr');
            // WAHA only has a valid QR while it waits in SCAN_QR_CODE: whenever the
            // answer is not an image the old one must go, or the user scans it and
            // pairing fails without a word.
            if (response.status === 202) {
                hideQR();
                setHint(qrHint('qr_pending').text, qrHint('qr_pending').tone);
                return;
            }
            if (response.status === 409) {
                hideQR();
                setHint(qrHint('already_connected').text, qrHint('already_connected').tone);
                await refreshStatus(false);
                return;
            }
            if (!response.ok) {
                hideQR();
                setHint(qrHint('qr_failed').text, qrHint('qr_failed').tone);
                return;
            }
            const blob = await response.blob();
            const img = el('pixQr');
            if (!img) return;
            hideQR();
            const url = URL.createObjectURL(blob);
            img.dataset.url = url;
            img.src = url;
            toggle(img, true);
            setHint('QR code do WhatsApp', 'info');
        } catch (err) {
            console.error('[Pix] qr', err);
            setHint(qrHint('qr_failed').text, qrHint('qr_failed').tone);
        } finally {
            state.qrLoading = false;
        }
    }

    async function connectWhatsApp() {
        if (!state.canPair) return;
        const button = el('pixConnectBtn');
        if (button) button.disabled = true;
        setHint('Conectando...', 'info');
        try {
            const response = await api('/api/pix/whatsapp/connect', { method: 'POST' });
            if (response.status === 503) {
                markDisabled();
                return;
            }
            if (!response.ok) {
                setHint(qrHint('qr_failed').text, qrHint('qr_failed').tone);
                toast('Não foi possível iniciar a conexão com o WhatsApp.', 'error');
                return;
            }
            const data = await response.json().catch(() => ({}));
            setStatusText(pairingLabel(data.status, ''));
            await loadQR();
        } finally {
            if (button) button.disabled = false;
        }
    }

    async function disconnectWhatsApp() {
        if (!state.canPair) return;
        const response = await api('/api/pix/whatsapp/disconnect', { method: 'POST' });
        if (!response.ok) {
            toast('Não foi possível desconectar o WhatsApp.', 'error');
            return;
        }
        state.connected = false;
        hideQR();
        await refreshStatus(false);
    }

    // ── Valores PIX aceitos ──────────────────────────────────────────────

    function setValuesHint(text, tone) {
        const hint = el('pixValuesHint');
        if (!hint) return;
        hint.textContent = text || '';
        hint.classList.toggle('pix-error', tone === 'error');
    }

    async function loadValues() {
        try {
            const response = await api('/api/pix/values');
            if (!response.ok) return;
            const data = await response.json();
            state.values = Array.isArray(data.values) ? data.values : [];
            renderValueRows();
        } catch (err) {
            console.error('[Pix] loadValues', err);
        }
    }

    function renderValueRows() {
        const wrap = el('pixValueRows');
        if (!wrap) return;
        wrap.textContent = '';
        const list = state.values.length ? state.values : [null];
        for (const cents of list) appendValueRow(wrap, cents);
    }

    function appendValueRow(wrap, cents) {
        const row = document.createElement('div');
        row.className = 'pix-value-row';
        const prefix = document.createElement('span');
        prefix.className = 'pix-value-prefix';
        prefix.textContent = 'R$';
        const input = document.createElement('input');
        input.type = 'text';
        input.inputMode = 'decimal';
        input.maxLength = 20;
        input.placeholder = 'Ex.: 15,00';
        input.value = cents ? formatBRL(cents) : '';
        const remove = document.createElement('button');
        remove.type = 'button';
        remove.className = 'small-btn secondary-btn';
        remove.textContent = '✕';
        remove.title = 'Remover valor';
        remove.addEventListener('click', () => row.remove());
        row.appendChild(prefix);
        row.appendChild(input);
        row.appendChild(remove);
        wrap.appendChild(row);
    }

    function addValueRow() {
        const wrap = el('pixValueRows');
        if (wrap) appendValueRow(wrap, null);
    }

    function collectValues() {
        const inputs = document.querySelectorAll('#pixValueRows input');
        const values = [];
        for (const input of inputs) {
            const text = input.value.trim();
            if (!text) continue;
            const cents = parseMoneyBR(text);
            if (cents === null) return { error: 'Valor inválido: ' + text };
            if (!values.includes(cents)) values.push(cents);
        }
        if (values.length > 20) return { error: 'Máximo de 20 valores.' };
        return { values };
    }

    async function saveValues() {
        if (!state.canPair) return;
        const button = el('pixSaveValuesBtn');
        const collected = collectValues();
        if (collected.error) {
            setValuesHint(collected.error, 'error');
            return;
        }
        if (button) button.disabled = true;
        try {
            const response = await api('/api/pix/values', {
                method: 'PUT',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ values: collected.values.map(cents => formatBRL(cents)) }),
            });
            if (!response.ok) {
                const payload = await response.json().catch(() => ({}));
                setValuesHint(payload.error || 'Não foi possível salvar os valores.', 'error');
                return;
            }
            await loadValues();
            setValuesHint('Valores salvos.', 'info');
            loadTickets();
        } catch (err) {
            console.error('[Pix] saveValues', err);
            setValuesHint('Não foi possível salvar os valores.', 'error');
        } finally {
            if (button) button.disabled = false;
        }
    }

    // ── Fila e conversa ──────────────────────────────────────────────────

    async function loadTickets() {
        if (state.disabled) return;
        try {
            const response = await api('/api/pix/tickets?status=pending&limit=100');
            if (!response.ok) return;
            const tickets = await response.json();
            state.lastTickets = Array.isArray(tickets) ? tickets : [];
            renderTickets(state.lastTickets);
        } catch (err) {
            console.error('[Pix] loadTickets', err);
        }
    }

    function renderTickets(tickets) {
        const body = el('pixTicketsBody');
        if (!body) return;
        body.textContent = '';
        for (const ticket of tickets) {
            body.appendChild(ticketRow(ticket));
        }
        if (tickets.length === 0) {
            const empty = document.createElement('div');
            empty.className = 'pix-empty';
            empty.textContent = 'Nenhum atendimento pendente.';
            body.appendChild(empty);
        }
        const count = el('pixQueueCount');
        if (count) {
            count.textContent = String(tickets.length);
            toggle(count, tickets.length > 0);
        }
    }

    function ticketRow(ticket) {
        const row = document.createElement('div');
        row.className = 'pix-ticket-row' + (state.selected && state.selected.id === ticket.id ? ' active' : '');
        row.setAttribute('role', 'button');
        row.tabIndex = 0;

        const label = contactLabel(ticket.contact);
        const avatar = document.createElement('span');
        avatar.className = 'pix-avatar';
        avatar.textContent = initials(label);

        const main = document.createElement('div');
        main.className = 'pix-ticket-main';

        const top = document.createElement('div');
        top.className = 'pix-ticket-top';
        const name = document.createElement('span');
        name.className = 'pix-ticket-name';
        name.textContent = label;
        if (ticket.hasReceipt) {
            const receipt = document.createElement('span');
            receipt.className = 'pix-badge';
            receipt.textContent = '📎';
            receipt.title = 'Tem comprovante';
            name.appendChild(receipt);
        }
        const received = document.createElement('span');
        received.className = 'pix-ticket-time';
        received.textContent = formatTime(ticket.receivedAt);
        top.appendChild(name);
        top.appendChild(received);
        main.appendChild(top);

        const subline = contactSubline(ticket.contact);
        if (subline) {
            const sub = document.createElement('div');
            sub.className = 'pix-ticket-sub';
            sub.textContent = subline;
            main.appendChild(sub);
        }
        if (ticket.paidTotalCents > 0) {
            const paid = document.createElement('div');
            paid.className = 'pix-ticket-meta';
            paid.textContent = formatBRL(ticket.paidTotalCents);
            paid.title = 'Soma dos comprovantes';
            main.appendChild(paid);
        }

        const preview = document.createElement('div');
        preview.className = 'pix-ticket-preview';
        if (ticket.lastMessagePreview) {
            const isReceipt = ticket.lastMessageType === 'image' || ticket.lastMessageType === 'document';
            preview.textContent = (isReceipt ? '🧾 ' : '') + truncate(ticket.lastMessagePreview, 60);
        } else {
            preview.textContent = '—';
        }
        main.appendChild(preview);

        row.appendChild(avatar);
        row.appendChild(main);
        row.addEventListener('click', () => openTicket(ticket));
        row.addEventListener('keydown', event => {
            if (event.key === 'Enter' || event.key === ' ') {
                event.preventDefault();
                openTicket(ticket);
            }
        });
        return row;
    }

    async function openTicket(ticket, readonly) {
        state.selected = ticket;
        state.readonly = !!readonly || !state.canPair;
        releaseObjectURLs();
        renderConversationHeader(ticket);
        toggle(el('pixAnswerBtn'), !state.readonly && ticket.status === 'pending');
        toggle(el('pixComposer'), !state.readonly);
        await reloadMessages();
        renderTickets(state.lastTickets || []);
    }

    function renderConversationHeader(ticket) {
        const title = el('pixConversationTitle');
        if (title) title.textContent = contactLabel(ticket.contact);
        const sub = el('pixConversationSub');
        if (sub) sub.textContent = contactSubline(ticket.contact);
        const chip = el('pixConversationStatus');
        if (chip) {
            const answered = ticket.status === 'answered';
            chip.textContent = answered ? 'Encerrado' : 'Pendente';
            chip.className = 'pix-chip' + (answered ? ' pix-chip-done' : ' pix-chip-open');
        }
    }

    async function reloadMessages() {
        const ticket = state.selected;
        if (!ticket) return;
        try {
            const response = await api('/api/pix/tickets/' + ticket.id + '/messages?limit=200');
            if (!response.ok) return;
            const messages = await response.json();
            await renderMessages(Array.isArray(messages) ? messages : []);
        } catch (err) {
            console.error('[Pix] reloadMessages', err);
        }
    }

    async function renderMessages(messages) {
        const wrap = el('pixMessages');
        if (!wrap) return;
        releaseObjectURLs();
        wrap.textContent = '';
        for (const message of messages) {
            const div = document.createElement('div');
            div.className = 'pix-msg ' + (message.direction === 'outbound' ? 'outbound' : 'inbound');

            const stateMedia = mediaState(message);
            if (message.type === 'text') {
                div.appendChild(document.createTextNode(message.body || ''));
            } else if (stateMedia === 'available' && message.type === 'image') {
                div.appendChild(document.createTextNode(message.mediaFilename || 'Comprovante'));
                const img = document.createElement('img');
                img.alt = 'Comprovante PIX';
                div.appendChild(img);
                attachMedia(img, message, 'image');
            } else if (stateMedia === 'available' && message.type === 'document') {
                const link = document.createElement('a');
                link.textContent = '📄 ' + (message.mediaFilename || 'Comprovante PDF');
                link.href = '#';
                div.appendChild(link);
                attachMedia(link, message, 'download');
            } else if (message.type === 'unsupported') {
                div.appendChild(document.createTextNode('⚠️ Mensagem não suportada neste atendimento.'));
            } else {
                div.appendChild(document.createTextNode('Comprovante removido ao desconectar a live.'));
            }
            if ((message.type === 'image' || message.type === 'document') && message.mediaValueCents > 0) {
                const tag = document.createElement('span');
                tag.className = 'pix-value-tag';
                tag.textContent = formatBRL(message.mediaValueCents);
                div.appendChild(tag);
            }

            const time = document.createElement('span');
            time.className = 'pix-time';
            time.textContent = formatTime(message.createdAt);
            div.appendChild(time);
            wrap.appendChild(div);
        }
        wrap.scrollTop = wrap.scrollHeight;
    }

    async function attachMedia(node, message, mode) {
        try {
            const response = await api('/api/pix/media/' + message.id);
            if (!response.ok) {
                if (response.status === 410) {
                    node.textContent = 'Comprovante removido ao desconectar a live.';
                }
                return;
            }
            const blob = await response.blob();
            const url = URL.createObjectURL(blob);
            state.objectURLs.push(url);
            if (mode === 'image') {
                node.src = url;
            } else {
                node.href = url;
                node.download = message.mediaFilename || 'comprovante.pdf';
            }
        } catch (err) {
            console.error('[Pix] media', err);
        }
    }

    async function sendMessage() {
        const input = el('pixInput');
        if (!input || !state.selected || state.readonly) return;
        const body = input.value.trim();
        if (!body) return;
        const button = el('pixSendBtn');
        if (button) button.disabled = true;
        try {
            const response = await api('/api/pix/tickets/' + state.selected.id + '/messages', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ body }),
            });
            if (!response.ok) {
                const payload = await response.json().catch(() => ({}));
                toast(payload.error || 'Não foi possível enviar a mensagem.', 'error');
                return;
            }
            input.value = '';
            await reloadMessages();
        } finally {
            if (button) button.disabled = false;
        }
    }

    async function answerTicket() {
        if (!state.selected || state.readonly) return;
        const button = el('pixAnswerBtn');
        if (button) button.disabled = true;
        try {
            const response = await api('/api/pix/tickets/' + state.selected.id + '/answer', { method: 'POST' });
            if (!response.ok) {
                toast('Não foi possível encerrar o atendimento.', 'error');
                return;
            }
            // Keep the conversation open (read-only) so the operator can still
            // review it; the ticket just leaves the pending queue.
            state.selected = Object.assign({}, state.selected, { status: 'answered' });
            state.readonly = true;
            renderConversationHeader(state.selected);
            toggle(el('pixAnswerBtn'), false);
            toggle(el('pixComposer'), false);
            toast('Atendimento encerrado ✓', 'success');
            await loadTickets();
        } finally {
            if (button) button.disabled = false;
        }
    }

    let toastTimer = null;

    function toast(text, tone) {
        const node = el('pixToast');
        if (!node) return;
        node.textContent = text;
        node.className = 'pix-toast show ' + (tone || '');
        clearTimeout(toastTimer);
        toastTimer = setTimeout(() => node.classList.remove('show'), 2600);
    }

    // O histórico da fila abre no pop-up compartilhado (mesmo da presente alvo).
    function openPixHistoryModal() {
        if (typeof openHistoryModal === 'function') {
            openHistoryModal('pix-history');
        }
    }

    // ── SSE ──────────────────────────────────────────────────────────────

    function subscribeSSE() {
        const stream = window.__tlmEventStream;
        if (!stream) {
            setTimeout(subscribeSSE, 500);
            return;
        }
        const handler = event => {
            // O backend só entrega eventos da organização do usuário.
            let data;
            try { data = JSON.parse(event.data || '{}'); } catch (_) { return; }
            loadTickets();
            if (typeof renderActiveModal === 'function'
                && typeof activeModalType !== 'undefined'
                && activeModalType === 'pix-history') {
                renderActiveModal();
            }
            if (state.selected && data.ticketId === state.selected.id) {
                reloadMessages();
            }
        };
        stream.addEventListener('pix-ticket-update', handler);
        stream.addEventListener('pix-new-message', handler);
    }

    // ── Beacon de fechamento de página ───────────────────────────────────

    function installPageHideBeacon() {
        window.addEventListener('pagehide', () => {
            let navType = 'navigate';
            try {
                const entries = performance.getEntriesByType('navigation');
                if (entries && entries[0] && entries[0].type) navType = entries[0].type;
            } catch (_) { /* noop */ }
            if (!shouldBeaconOnPageHide(navType)) return;
            const username = (el('username') && el('username').value.trim()) || '';
            void window.TLMAuth.authFetch('/api/monitoring/beacon-disconnect', {method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({username}),keepalive:true});
        });
    }

    // ── Init ─────────────────────────────────────────────────────────────

    const PIX_SIDE_MIN = 220;
    const PIX_CHAT_MIN = 300;
    const PIX_SIDE_KEY = 'pixSidebarW';

    function pixSideLimits(grid) {
        const total = grid.clientWidth || 0;
        const gaps = 28; // 2 × 14px do .pix-grid
        const max = Math.max(PIX_SIDE_MIN, total - 8 - gaps - PIX_CHAT_MIN);
        return { min: PIX_SIDE_MIN, max };
    }

    function applyPixSideWidth(grid, width) {
        const limits = pixSideLimits(grid);
        const clamped = Math.min(limits.max, Math.max(limits.min, Math.round(width)));
        grid.style.setProperty('--pix-side-w', clamped + 'px');
        return clamped;
    }

    // Alça entre a lista e o chat: arrastar redimensiona a lateral.
    function initPixResize() {
        const handle = el('pixResizeHandle');
        if (!handle || handle.dataset.bound) return;
        handle.dataset.bound = '1';
        const grid = handle.closest('.pix-grid');
        if (!grid) return;

        const sidebar = grid.querySelector('.pix-sidebar');
        const currentWidth = () => (sidebar && sidebar.getBoundingClientRect().width) || PIX_SIDE_MIN;

        let saved = 0;
        try {
            saved = Number(localStorage.getItem(PIX_SIDE_KEY));
            if (!Number.isFinite(saved) || saved <= 0) saved = 0;
        } catch (_) { saved = 0; }
        if (saved > 0) {
            // A seção pode estar oculta (largura 0) até a sessão carregar:
            // espera ela aparecer para não grampear no mínimo.
            let attempts = 0;
            const applySaved = () => {
                if (grid.clientWidth === 0 && attempts++ < 300) {
                    requestAnimationFrame(applySaved);
                    return;
                }
                applyPixSideWidth(grid, saved);
            };
            applySaved();
        }

        let startX = 0;
        let startW = 0;
        handle.addEventListener('pointerdown', event => {
            if (event.button !== undefined && event.button !== 0) return;
            startX = event.clientX;
            startW = currentWidth();
            handle.classList.add('pix-resizing');
            try { handle.setPointerCapture(event.pointerId); } catch (_) { /* noop */ }
            event.preventDefault();
        });
        handle.addEventListener('pointermove', event => {
            if (!handle.classList.contains('pix-resizing')) return;
            applyPixSideWidth(grid, startW + (event.clientX - startX));
        });
        const stop = () => {
            if (!handle.classList.contains('pix-resizing')) return;
            handle.classList.remove('pix-resizing');
            try { localStorage.setItem(PIX_SIDE_KEY, String(Math.round(currentWidth()))); } catch (_) { /* noop */ }
        };
        handle.addEventListener('pointerup', stop);
        handle.addEventListener('pointercancel', stop);
        handle.addEventListener('dblclick', () => {
            grid.style.removeProperty('--pix-side-w');
            try { localStorage.removeItem(PIX_SIDE_KEY); } catch (_) { /* noop */ }
        });
        handle.addEventListener('keydown', event => {
            if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return;
            event.preventDefault();
            const next = applyPixSideWidth(grid, currentWidth() + (event.key === 'ArrowRight' ? 24 : -24));
            try { localStorage.setItem(PIX_SIDE_KEY, String(next)); } catch (_) { /* noop */ }
        });
        window.addEventListener('resize', () => {
            if (grid.style.getPropertyValue('--pix-side-w')) applyPixSideWidth(grid, currentWidth());
        });
    }

    function bind() {
        initPixResize();
        const connect = el('pixConnectBtn');
        if (connect) connect.addEventListener('click', connectWhatsApp);
        const disconnect = el('pixDisconnectBtn');
        if (disconnect) disconnect.addEventListener('click', disconnectWhatsApp);
        const send = el('pixSendBtn');
        if (send) send.addEventListener('click', sendMessage);
        const input = el('pixInput');
        if (input) {
            input.addEventListener('keydown', event => {
                if (event.key === 'Enter') {
                    event.preventDefault();
                    sendMessage();
                }
            });
        }
        const answer = el('pixAnswerBtn');
        if (answer) answer.addEventListener('click', answerTicket);
        const history = el('pixHistoryBtn');
        if (history) history.addEventListener('click', openPixHistoryModal);
        const addValue = el('pixAddValueBtn');
        if (addValue) addValue.addEventListener('click', addValueRow);
        const saveValuesBtn = el('pixSaveValuesBtn');
        if (saveValuesBtn) saveValuesBtn.addEventListener('click', saveValues);
    }

    async function init() {
        if (!window.TLMAuth || !el('pixSection')) return;
        const user = await window.TLMAuth.requireSession();
        if (!user) return;
        state.canPair = !!(window.TLMAuth.canManageOrg && window.TLMAuth.canManageOrg());
        bind();
        if (state.canPair) installPageHideBeacon();
        subscribeSSE();
        await refreshStatus(true);
        if (!state.disabled) {
            toggle(el('pixContent'), true);
            await loadTickets();
            loadValues();
            if (state.canPair) loadQR();
        }
    }

    window.PixQueue = {
        // exported for tests
        formatTime,
        receiptLabel,
        shouldBeaconOnPageHide,
        mediaState,
        truncate,
        pairingLabel,
        pairingControls,
        qrHint,
        parseMoneyBR,
        formatBRL,
        formatPhoneBR,
        contactLabel,
        contactSubline,
        initials,
        isLidJid,
        init,
    };

    // Inicializa ao carregar o script (a página já está parseada neste ponto).
    // init() é idempotente via requireSession + marcador abaixo.
    let started = false;
    async function start() {
        if (started) return;
        started = true;
        try {
            await init();
        } catch (err) {
            console.error('[Pix] init', err);
        }
    }
    void start();
})();
