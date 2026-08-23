console.log('TikTok Live Monitor Renderer Loaded');

function ensureBrowserChart() {
    if (typeof window.Chart !== 'undefined') {
        return Promise.resolve();
    }
    return new Promise((resolve, reject) => {
        const script = document.createElement('script');
        script.src = `${window.location.origin}/vendor/chart.js`;
        script.onload = () => resolve();
        script.onerror = () => reject(new Error('Não foi possível carregar Chart.js.'));
        document.head.appendChild(script);
    });
}

const usernameInput = document.getElementById('username');
const connectBtn = document.getElementById('connectBtn');
const disconnectBtn = document.getElementById('disconnectBtn');
const listenBtn = document.getElementById('listenBtn');
const statusDiv = document.getElementById('status');
const userTableBody = document.getElementById('userTableBody');
const allGiftsTableBody = document.getElementById('allGiftsTableBody');
const pinnedCommentsTableBody = document.getElementById('pinnedCommentsTableBody');
const correlationMessagesTableBody = document.getElementById('correlationMessagesTableBody');
const targetExpirationMinutesInput = document.getElementById('targetExpirationMinutes');
const chartCanvas = document.getElementById('messageChart');
const aiLedRow = document.getElementById('aiLedRow');
const aiLedDot = document.getElementById('aiLedDot');
const aiLedText = document.getElementById('aiLedText');
const modelSelectorContainer = document.getElementById('modelSelectorContainer');
const modelSelect = document.getElementById('modelSelect');
const setupProgressContainer = document.getElementById('setupProgressContainer');
const setupStatusText = document.getElementById('setupStatusText');
const setupPercentage = document.getElementById('setupPercentage');
const setupProgressBar = document.getElementById('setupProgressBar');
const targetGiftHistoryBtn = document.getElementById('targetGiftHistoryBtn');
const targetGiftsList = document.getElementById('targetGiftsList');
const availableGiftSelect = document.getElementById('availableGiftSelect');
const addTargetGiftBtn = document.getElementById('addTargetGiftBtn');
const pinnedCommentHistoryBtn = document.getElementById('pinnedCommentHistoryBtn');
const historyModalBackdrop = document.getElementById('historyModalBackdrop');
const historyModalTitle = document.getElementById('historyModalTitle');
const historyModalBody = document.getElementById('historyModalBody');
const historyModalCloseBtn = document.getElementById('historyModalCloseBtn');
const profileModalBackdrop = document.getElementById('profileModalBackdrop');
const profileModalBody = document.getElementById('profileModalBody');
const profileModalCloseBtn = document.getElementById('profileModalCloseBtn');
const giftSearchInput = document.getElementById('giftSearchInput');
const allGiftsSection = document.getElementById('allGiftsSection');
const allGiftsTableContainer = document.getElementById('allGiftsTableContainer');

// --- New feature elements ---
const rankingTableBody = document.getElementById('rankingTableBody');
const refreshRankingBtn = document.getElementById('refreshRankingBtn');
const suggestionsContainer = document.getElementById('suggestionsContainer');
const generateReportBtn = document.getElementById('generateReportBtn');
const reportWrap = document.getElementById('reportWrap');
const reportSummary = document.getElementById('reportSummary');
const reportText = document.getElementById('reportText');
const reportError = document.getElementById('reportError');
const saveAlertConfigBtn = document.getElementById('saveAlertConfigBtn');
const alertDiscord = document.getElementById('alertDiscord');
const alertTelegramChat = document.getElementById('alertTelegramChat');
const alertTelegramToken = document.getElementById('alertTelegramToken');
const alertWhatsapp = document.getElementById('alertWhatsapp');
const alertConfigStatus = document.getElementById('alertConfigStatus');

let chart;
let messageCount = 0;
let chartData = Array(60).fill(0);
let giftCount = 0;
let giftChartData = Array(60).fill(0);
let autoRemoveTimers = {};
let pinnedCommentTimers = {};
let flaggedMessageTimers = {};
let targetGiftHistory = [];
let pinnedCommentHistory = [];
let listenedMessages = [];
let listenedUserId = '';
let listenDraftValue = '';
let liveUsers = new Map();
let activeModalType = null;
let isAddingTargetGift = false;

const LIVE_USERS_MAX = 200;
let renderListenModalTimeout = null;

function throttledRenderListenModal() {
    if (renderListenModalTimeout) {
        clearTimeout(renderListenModalTimeout);
    }
    renderListenModalTimeout = setTimeout(() => {
        renderListenModal({ preserveFocus: true });
        renderListenModalTimeout = null;
    }, 150);
}

function normalizeListenUser(value) {
    return String(value || '').trim().replace(/^@+/, '').toLowerCase();
}

function normalizeFollowerFlag(value) {
    if (value === true || value === 1 || value === '1' || value === 'true') {
        return true;
    }
    if (value === false || value === 0 || value === '0' || value === 'false') {
        return false;
    }
    return null;
}

function mergeFollowerStatus(previous, next) {
    const incoming = normalizeFollowerFlag(next);
    const current = normalizeFollowerFlag(previous);
    if (incoming === true) {
        return true;
    }
    if (incoming === false && current !== true) {
        return false;
    }
    if (current != null) {
        return current;
    }
    return incoming;
}

function followerStatusForDisplay(data) {
    const key = normalizeListenUser((data && (data.uniqueId || data.nickname)) || '');
    const stored = key ? liveUsers.get(key) : null;
    if (stored && stored.isFollower != null) {
        return stored.isFollower;
    }
    return normalizeFollowerFlag(data && data.isFollower);
}

function ensureFollowerBadge(userTd, data) {
    if (!userTd) {
        return;
    }
    const badge = createFollowerBadge(followerStatusForDisplay(data));
    if (!badge) {
        return;
    }
    const existing = userTd.querySelector('.badge-follower, .badge-not-follower');
    if (existing) {
        existing.replaceWith(badge);
        return;
    }
    userTd.appendChild(badge);
}

function rememberLiveUser(data) {
    if (!data) {
        return;
    }

    const uniqueId = String(data.uniqueId || '').trim().replace(/^@+/, '');
    const nickname = String(data.nickname || uniqueId || '').trim();
    const key = normalizeListenUser(uniqueId || nickname);

    if (!key) {
        return;
    }

    const previous = liveUsers.get(key) || {};
    liveUsers.set(key, {
        uniqueId: uniqueId || previous.uniqueId || '',
        nickname: nickname || previous.nickname || uniqueId || 'Nao identificado',
        isFollower: mergeFollowerStatus(previous.isFollower, data.isFollower),
        lastSeen: Date.now()
    });

    // Limitar tamanho do Map para evitar uso excessivo de memória
    if (liveUsers.size > LIVE_USERS_MAX) {
        const entries = Array.from(liveUsers.entries());
        entries.sort((a, b) => (a[1].lastSeen || 0) - (b[1].lastSeen || 0));
        const toRemove = entries.slice(0, liveUsers.size - LIVE_USERS_MAX);
        toRemove.forEach(([key]) => liveUsers.delete(key));
    }

    if (activeModalType === 'listen') {
        throttledRenderListenModal();
    }
}

function getLiveUserMatches(query) {
    const normalizedQuery = normalizeListenUser(query);
    return Array.from(liveUsers.values())
        .filter(user => {
            if (!normalizedQuery) {
                return true;
            }

            return normalizeListenUser(user.uniqueId).includes(normalizedQuery) ||
                normalizeListenUser(user.nickname).includes(normalizedQuery);
        })
        .sort((a, b) => (b.lastSeen || 0) - (a.lastSeen || 0))
        .slice(0, 50);
}

function trimHistory(items) {
    if (items.length > 15) {
        items.length = 15;
    }
}

function appendEmptyState(parent) {
    const p = document.createElement('p');
    p.className = 'modal-empty';
    p.textContent = 'Nenhum registro ainda.';
    parent.appendChild(p);
}

function createModalList(items, renderItem) {
    const list = document.createElement('div');
    list.className = 'modal-list';

    if (!items.length) {
        appendEmptyState(list);
        return list;
    }

    items.forEach(item => {
        const row = document.createElement('div');
        row.className = 'modal-item';
        renderItem(row, item);
        list.appendChild(row);
    });

    return list;
}

function renderUserLine(row, nickname, uniqueId, isFollower) {
    const strong = document.createElement('strong');
    const userText = nickname || uniqueId || 'Nao identificado';
    strong.textContent = uniqueId ? `${userText} (@${uniqueId})` : userText;
    row.appendChild(strong);

    const badge = createFollowerBadge(isFollower);
    if (badge) {
        row.appendChild(badge);
    }
}

function createFollowerBadge(isFollower) {
    const flag = normalizeFollowerFlag(isFollower);
    if (flag === true) {
        const span = document.createElement('span');
        span.className = 'badge badge-follower';
        span.textContent = 'Segue';
        return span;
    }
    if (flag === false) {
        const span = document.createElement('span');
        span.className = 'badge badge-not-follower';
        span.textContent = 'Não Segue';
        return span;
    }
    return null;
}

function formatSaoPauloDateTime(value) {
    if (value == null || value === '') {
        return '—';
    }
    const date = value instanceof Date ? value : new Date(value);
    if (Number.isNaN(date.getTime())) {
        return '—';
    }
    return new Intl.DateTimeFormat('pt-BR', {
        timeZone: 'America/Sao_Paulo',
        day: '2-digit',
        month: '2-digit',
        year: 'numeric',
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
        hour12: false
    }).format(date);
}

function targetGiftResponseLabel(responseType) {
    if (responseType === 'manual') {
        return 'Respondido manualmente';
    }
    if (responseType === 'automatic') {
        return 'Respondido automaticamente';
    }
    return 'Pendente';
}

function targetGiftResponseClass(responseType) {
    if (responseType === 'manual' || responseType === 'automatic') {
        return responseType;
    }
    return 'pending';
}

async function markTargetGiftAnswered(historyId, responseType) {
    const id = Number(historyId);
    if (!Number.isFinite(id) || id <= 0) {
        return;
    }
    try {
        await fetch('/api/target-gift-history/answer', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ id, responseType })
        });
    } catch (error) {
        console.error('[Frontend] Falha ao registrar resposta do presente alvo:', error);
    }
}

async function loadTargetGiftHistoryFromApi() {
    try {
        const response = await fetch('/api/target-gift-history?limit=50');
        if (!response.ok) {
            throw new Error(`status ${response.status}`);
        }
        const items = await response.json();
        return Array.isArray(items) ? items : [];
    } catch (error) {
        console.error('[Frontend] Falha ao carregar histórico de presentes alvos:', error);
        return [];
    }
}

async function renderGiftHistory() {
    historyModalTitle.textContent = 'Histórico de Presentes Alvos';
    historyModalBody.replaceChildren();

    const loading = document.createElement('p');
    loading.className = 'modal-empty';
    loading.textContent = 'Carregando histórico...';
    historyModalBody.appendChild(loading);

    const items = await loadTargetGiftHistoryFromApi();
    if (activeModalType !== 'target-gifts') {
        return;
    }

    historyModalBody.replaceChildren(createModalList(items, (row, item) => {
        renderUserLine(row, item.nickname, item.uniqueId, item.isFollower);

        const gift = document.createElement('span');
        gift.textContent = item.giftName || 'Presente Alvo';
        row.appendChild(gift);

        const meta = document.createElement('div');
        meta.className = 'modal-item-meta';

        const received = document.createElement('span');
        received.textContent = `Recebido: ${formatSaoPauloDateTime(item.receivedAt)} (SP)`;
        meta.appendChild(received);

        const answered = document.createElement('span');
        answered.textContent = `Respondido: ${formatSaoPauloDateTime(item.answeredAt)} (SP)`;
        meta.appendChild(answered);
        row.appendChild(meta);

        const status = document.createElement('span');
        status.className = `modal-item-status ${targetGiftResponseClass(item.responseType)}`;
        status.textContent = targetGiftResponseLabel(item.responseType);
        row.appendChild(status);
    }));
}

async function renderPinnedCommentHistory() {
    historyModalTitle.textContent = 'Histórico de Comentários Fixados';
    historyModalBody.replaceChildren();

    const loading = document.createElement('p');
    loading.className = 'modal-empty';
    loading.textContent = 'Carregando histórico...';
    historyModalBody.appendChild(loading);

    const items = await loadPinnedCommentsFromApi();
    if (activeModalType !== 'pinned-comments') {
        return;
    }

    historyModalBody.replaceChildren(createModalList(items, (row, item) => {
        renderUserLine(row, item.nickname, item.uniqueId, item.isFollower);

        const comment = document.createElement('span');
        comment.textContent = item.comment || '[sem texto identificado]';
        row.appendChild(comment);

        const meta = document.createElement('div');
        meta.className = 'modal-item-meta';
        const when = document.createElement('span');
        when.textContent = `${formatSaoPauloDateTime(item.timestamp)} (SP)`;
        meta.appendChild(when);
        row.appendChild(meta);
    }));
}

function setListenedUser(value) {
    const nextUserId = normalizeListenUser(value);
    if (nextUserId !== listenedUserId) {
        listenedMessages = [];
    }
    listenedUserId = nextUserId;
}

function renderLiveUserSelector(input) {
    const wrapper = document.createElement('div');
    wrapper.className = 'listen-user-panel';

    const users = getLiveUserMatches(input.value);
    if (!liveUsers.size) {
        const empty = document.createElement('p');
        empty.className = 'modal-empty';
        empty.textContent = 'Nenhum usuário visto na live ainda.';
        wrapper.appendChild(empty);
        return wrapper;
    }

    if (!users.length) {
        const empty = document.createElement('p');
        empty.className = 'modal-empty';
        empty.textContent = 'Nenhum usuário encontrado.';
        wrapper.appendChild(empty);
        return wrapper;
    }

    users.forEach(user => {
        const button = document.createElement('button');
        button.className = 'listen-user-option';
        button.type = 'button';

        const name = document.createElement('strong');
        name.textContent = user.nickname || user.uniqueId || 'Nao identificado';
        button.appendChild(name);

        const badge = createFollowerBadge(user.isFollower);
        if (badge) {
            button.appendChild(badge);
        }

        if (user.uniqueId) {
            const handle = document.createElement('span');
            handle.textContent = `@${user.uniqueId}`;
            button.appendChild(handle);
        }

        button.addEventListener('click', () => {
            listenDraftValue = user.uniqueId ? `@${user.uniqueId}` : user.nickname;
            setListenedUser(listenDraftValue);
            renderListenModal({ preserveFocus: true });
        });

        wrapper.appendChild(button);
    });

    return wrapper;
}

function renderListenModal(options = {}) {
    historyModalTitle.textContent = 'Escuta';
    historyModalBody.replaceChildren();

    const form = document.createElement('form');
    form.className = 'listen-form';

    const input = document.createElement('input');
    input.type = 'text';
    input.placeholder = '@usuario';
    input.autocomplete = 'off';
    input.value = listenDraftValue;
    input.addEventListener('input', () => {
        listenDraftValue = input.value;
        throttledRenderListenModal();
    });

    const button = document.createElement('button');
    button.type = 'submit';
    button.textContent = 'Escutar';

    form.appendChild(input);
    form.appendChild(button);
    form.addEventListener('submit', event => {
        event.preventDefault();
        setListenedUser(input.value);
        listenDraftValue = listenedUserId ? `@${listenedUserId}` : '';
        renderListenModal();
    });

    historyModalBody.appendChild(form);
    historyModalBody.appendChild(renderLiveUserSelector(input));
    historyModalBody.appendChild(createModalList(listenedMessages, (row, item) => {
        renderUserLine(row, item.nickname, item.uniqueId, item.isFollower);
        const comment = document.createElement('span');
        comment.textContent = item.comment || '';
        row.appendChild(comment);
    }));

    if (options.preserveFocus) {
        input.focus();
        input.setSelectionRange(input.value.length, input.value.length);
    }
}

function renderActiveModal() {
    if (activeModalType === 'target-gifts') {
        renderGiftHistory();
    } else if (activeModalType === 'pinned-comments') {
        renderPinnedCommentHistory();
    } else if (activeModalType === 'listen') {
        renderListenModal();
    }
}

function openHistoryModal(type) {
    activeModalType = type;
    if (type === 'listen') {
        listenDraftValue = listenedUserId ? `@${listenedUserId}` : '';
    }
    renderActiveModal();
    historyModalBackdrop.classList.add('is-open');
    historyModalBackdrop.setAttribute('aria-hidden', 'false');
}

function closeProfileModal() {
    if (!profileModalBackdrop) return;
    profileModalBackdrop.classList.remove('is-open');
    profileModalBackdrop.setAttribute('aria-hidden', 'true');
    profileModalBody.innerHTML = '';
}

function closeHistoryModal() {
    historyModalBackdrop.classList.remove('is-open');
    historyModalBackdrop.setAttribute('aria-hidden', 'true');
    activeModalType = null;
}

async function openProfile(uniqueId) {
    if (!profileModalBackdrop || !profileModalBody) return;
    profileModalBody.innerHTML = '<p style="color:var(--text-muted)">Carregando perfil...</p>';
    profileModalBackdrop.classList.add('is-open');
    profileModalBackdrop.setAttribute('aria-hidden', 'false');
    try {
        const response = await fetch('/api/profile?uid=' + encodeURIComponent(String(uniqueId)));
        const data = await response.json();
        renderProfile(data);
    } catch (error) {
        profileModalBody.innerHTML = '<p style="color:var(--pink)">Falha ao carregar o perfil do usuário.</p>';
        console.error('[Frontend] Falha ao carregar perfil:', error);
    }
}

function renderProfile(profile) {
    if (!profileModalBody) return;
    profileModalBody.innerHTML = '';

    const header = document.createElement('div');
    header.style.marginBottom = '14px';
    header.innerHTML =
        '<div style="font-size:1.15em;font-weight:700;">' + escapeHtml(profile.nickname || profile.uniqueId || 'Participante') +
        ' <span style="color:var(--text-muted);font-weight:400;font-size:0.8em;">@' + escapeHtml(profile.uniqueId || '') + '</span></div>';
    const riskBadge = document.createElement('span');
    riskBadge.className = 'risk-badge ' + riskBadgeClass(profile.riskLevel);
    riskBadge.textContent = riskLabel(profile.riskLevel);
    header.appendChild(riskBadge);
    profileModalBody.appendChild(header);

    const stats = document.createElement('div');
    stats.className = 'report-summary';
    stats.style.marginBottom = '16px';
    const statItems = [
        { value: profile.totalMessages != null ? profile.totalMessages : 0, label: 'Mensagens' },
        { value: profile.totalGifts != null ? profile.totalGifts : 0, label: 'Presentes' },
        { value: (profile.lastLives || []).length, label: 'Vidas participadas' }
    ];
    statItems.forEach(stat => {
        const box = document.createElement('div');
        box.className = 'report-stat';
        box.innerHTML = '<div class="stat-value">' + escapeHtml(String(stat.value)) + '</div><div class="stat-label">' + escapeHtml(stat.label) + '</div>';
        stats.appendChild(box);
    });
    profileModalBody.appendChild(stats);

    // Últimas vidas
    const lives = profile.lastLives || [];
    if (lives.length) {
        const h = document.createElement('h4');
        h.textContent = 'Últimas vidas';
        h.style.margin = '12px 0 6px';
        h.style.fontSize = '0.9em';
        h.style.color = 'var(--text-muted)';
        profileModalBody.appendChild(h);
        lives.forEach(live => {
            const row = document.createElement('div');
            row.className = 'suggestion-card';
            row.style.borderLeftColor = 'var(--pink)';
            row.innerHTML = '<div style="font-weight:600;">' + escapeHtml(live.liveName || 'Live') + '</div>' +
                '<div style="font-size:0.8em;color:var(--text-muted);">' +
                (live.messages != null ? live.messages + ' mensagens, ' : '') +
                (live.gifts != null ? live.gifts + ' presentes. ' : '') +
                ('Primeira: ' + (live.firstSeen || '—') + ' • Última: ' + (live.lastSeen || '—')) +
                '</div>';
            profileModalBody.appendChild(row);
        });
    }

    // Alertas / infrações
    const alerts = profile.alerts || [];
    if (alerts.length) {
        const h2 = document.createElement('h4');
        h2.textContent = 'Alertas de moderação (' + alerts.length + ')';
        h2.style.margin = '12px 0 6px';
        h2.style.fontSize = '0.9em';
        h2.style.color = 'var(--text-muted)';
        profileModalBody.appendChild(h2);
        alerts.slice(0, 15).forEach(alert => {
            const row = document.createElement('div');
            row.className = 'suggestion-card';
            row.style.borderLeftColor = 'var(--pink)';
            row.innerHTML = '<div style="font-size:0.85em;">' + escapeHtml(alert.category || 'Infração') + '</div>' +
                '<div style="font-size:0.8em;color:var(--text-muted);">' + escapeHtml(alert.comment || '') + '</div>';
            profileModalBody.appendChild(row);
        });
    }

    // Mensagens recentes
    const messages = profile.messages || [];
    if (messages.length) {
        const h3 = document.createElement('h4');
        h3.textContent = 'Mensagens recentes (' + messages.length + ')';
        h3.style.margin = '12px 0 6px';
        h3.style.fontSize = '0.9em';
        h3.style.color = 'var(--text-muted)';
        profileModalBody.appendChild(h3);
        messages.slice(0, 20).forEach(msg => {
            const row = document.createElement('div');
            row.className = 'suggestion-card';
            row.style.borderLeftColor = 'var(--cyan)';
            row.textContent = (msg.username || msg.uniqueId || '') + (msg.timestamp ? ' — ' + msg.timestamp : '') + ': ' + (msg.message || '');
            profileModalBody.appendChild(row);
        });
    }
}

function addTargetGiftToHistory(user) {
    // Persistido no backend; o modal carrega de /api/target-gift-history.
    if (activeModalType === 'target-gifts') {
        renderGiftHistory();
    }
}

function addPinnedCommentToHistory() {
    if (activeModalType === 'pinned-comments') {
        renderPinnedCommentHistory();
    }
}

function handleListenedMessage(data) {
    if (!listenedUserId || !data) {
        return;
    }

    if (normalizeListenUser(data.uniqueId) !== listenedUserId) {
        return;
    }

    listenedMessages.unshift({
        uniqueId: data.uniqueId || '',
        nickname: data.nickname || data.uniqueId || 'Nao identificado',
        comment: data.comment || '',
        timestamp: data.timestamp || Date.now()
    });
    trimHistory(listenedMessages);
    if (activeModalType === 'listen') {
        throttledRenderListenModal();
    }
}

function handleNewChatMessage(data) {
    rememberLiveUser(data);
    messageCount++;
    handleListenedMessage(data);
}

function clearHistories() {
    targetGiftHistory = [];
    pinnedCommentHistory = [];
    listenedMessages = [];
    listenedUserId = '';
    listenDraftValue = '';
    liveUsers.clear();
    renderActiveModal();
}

/** Rótulo curto para coluna Categoria (payload.category do servidor) */
function infractionCategoryLabel(category) {
    const map = {
        PROSELITISMO: 'Proselitismo Cristão',
        SPAM: 'Spam',
        GOLPE: 'Golpe',
        ODIO: 'Ataque Pessoal',
        OUTRO: 'Outro',
        REPETICAO: 'Repetição',
        CORRELACAO: 'Correlação Dino/Perfume'
    };
    const key = String(category || '').trim().toUpperCase();
    if (!key) return '—';
    return map[key] || key;
}

function applyInfractionsSectionTitle(aiConfigured) {
    // Seção de infrações removida da UI.
}

function showAiLedChecking() {
    if (!aiLedRow || !aiLedDot || !aiLedText) return;
    aiLedRow.style.display = 'flex';
    aiLedDot.className = 'ai-led-dot ai-led-dot-checking';
    aiLedText.textContent = 'Verificando IA…';
}

let _aiLedPollTimer = null;

function setAiLedActive(active) {
    if (!aiLedRow || !aiLedDot || !aiLedText) return;
    aiLedRow.style.display = 'flex';
    aiLedDot.className = 'ai-led-dot ' + (active ? 'ai-led-dot-on' : 'ai-led-dot-off');
    aiLedText.textContent = active ? 'IA ativa' : 'IA inativa';
    if (active) {
        _stopAiLedPoll();
    }
}

function _stopAiLedPoll() {
    if (_aiLedPollTimer) {
        clearInterval(_aiLedPollTimer);
        _aiLedPollTimer = null;
    }
}

function _startAiLedPoll() {
    _stopAiLedPoll();
    _aiLedPollTimer = setInterval(() => {
        fetch('/api/probe-llm')
            .then((r) => r.ok ? r.json() : { llmActive: false })
            .then((data) => setAiLedActive(Boolean(data && data.llmActive)))
            .catch(() => {});
    }, 5000);
}

function hideAiLed() {
    if (!aiLedRow || !aiLedDot || !aiLedText) return;
    _stopAiLedPoll();
    aiLedRow.style.display = 'none';
    aiLedDot.className = 'ai-led-dot ai-led-dot-checking';
    aiLedText.textContent = 'Verificando IA…';
}

function createChart(ChartLib) {
    const ctx = chartCanvas.getContext('2d');
    return new ChartLib(ctx, {
        type: 'line',
        data: {
            labels: Array(60).fill('').map((_, index) => `${60 - index}s atrás`),
            datasets: [
                {
                    label: 'Mensagens/s',
                    data: chartData,
                    borderColor: '#fe2c55',
                    backgroundColor: 'rgba(254, 44, 85, 0.1)',
                    fill: true,
                    tension: 0.4
                },
                {
                    label: 'Presentes/s',
                    data: giftChartData,
                    borderColor: '#22c55e',
                    backgroundColor: 'rgba(34, 197, 94, 0.1)',
                    fill: true,
                    tension: 0.4
                }
            ]
        },
        options: {
            responsive: true,
            maintainAspectRatio: false,
            layout: {
                padding: 8
            },
            scales: {
                y: {
                    beginAtZero: true,
                    grid: {
                        color: 'rgba(255, 255, 255, 0.08)',
                        background: 'rgba(18, 21, 31, 0.6)',
                        drawBorder: false
                    },
                    ticks: {
                        stepSize: 1,
                        color: '#9aa0aa',
                        padding: 8
                    },
                    border: { display: false }
                },
                x: {
                    display: false,
                    grid: { display: false }
                }
            },
            plugins: {
                legend: {
                    display: true,
                    position: 'top',
                    labels: {
                        color: '#f2f3f5',
                        padding: 16,
                        usePointStyle: true,
                        boxWidth: 8
                    }
                }
            },
            animation: false
        }
    });
}

setInterval(() => {
    if (!chart) {
        return;
    }
    chartData.push(messageCount);
    chartData.shift();
    messageCount = 0;

    giftChartData.push(giftCount);
    giftChartData.shift();
    giftCount = 0;

    chart.update();
}, 1000);

targetGiftHistoryBtn.addEventListener('click', () => openHistoryModal('target-gifts'));
pinnedCommentHistoryBtn.addEventListener('click', () => openHistoryModal('pinned-comments'));
listenBtn.addEventListener('click', () => openHistoryModal('listen'));

if (refreshRankingBtn) {
    refreshRankingBtn.addEventListener('click', () => loadRanking());
}
if (generateReportBtn) {
    generateReportBtn.addEventListener('click', () => loadReport());
}
if (saveAlertConfigBtn) {
    saveAlertConfigBtn.addEventListener('click', () => saveAlertConfig());
}

historyModalCloseBtn.addEventListener('click', closeHistoryModal);
historyModalBackdrop.addEventListener('click', event => {
    if (event.target === historyModalBackdrop) {
        closeHistoryModal();
    }
});

if (profileModalCloseBtn) {
    profileModalCloseBtn.addEventListener('click', closeProfileModal);
}
if (profileModalBackdrop) {
    profileModalBackdrop.addEventListener('click', event => {
        if (event.target === profileModalBackdrop) {
            closeProfileModal();
        }
    });
}
document.addEventListener('keydown', event => {
    if (event.key === 'Escape' && activeModalType) {
        closeHistoryModal();
    }
});

{
    connectBtn.addEventListener('click', async () => {
        const username = usernameInput.value.trim().replace(/^@/, '');
        if (!username) {
            return;
        }

        setConnectingState();
        showAiLedChecking();
        const probePromise = fetch('/api/probe-llm')
            .then(async (r) => {
                if (!r.ok) return { llmActive: false };
                return r.json();
            })
            .catch(() => ({ llmActive: false }));

        try {
            const response = await fetch('/api/connect', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json'
                },
                body: JSON.stringify({ username })
            });

            if (!response.ok) {
                const payload = await response.json();
                throw new Error(payload.error || 'Falha ao conectar.');
            }
        } catch (error) {
            applyDisconnectedState(error.message);
        }

        try {
            const probeData = await probePromise;
            const active = Boolean(probeData.llmActive);
            setAiLedActive(active);
            if (!active) _startAiLedPoll();
        } catch {
            setAiLedActive(false);
            _startAiLedPoll();
        }
    });

    disconnectBtn.addEventListener('click', async () => {
        hideAiLed();
        statusDiv.innerText = 'Desconectando...';

        try {
            await fetch('/api/disconnect', { method: 'POST' });
        } catch (error) {
            applyDisconnectedState(error.message);
        }
    });
}

const EXPIRATION_STORAGE_KEY = 'targetExpirationMinutes';

function loadTargetExpirationMinutes() {
    if (!targetExpirationMinutesInput) {
        return;
    }
    try {
        const stored = Number(localStorage.getItem(EXPIRATION_STORAGE_KEY));
        if (Number.isFinite(stored) && stored > 0) {
            targetExpirationMinutesInput.value = String(Math.floor(stored));
        }
    } catch {
        // ignore storage errors
    }
}

function persistTargetExpirationMinutes() {
    if (!targetExpirationMinutesInput) {
        return;
    }
    const minutes = getTargetExpirationMinutes();
    targetExpirationMinutesInput.value = String(minutes);
    try {
        localStorage.setItem(EXPIRATION_STORAGE_KEY, String(minutes));
    } catch {
        // ignore storage errors
    }
}

function onExpirationMinutesChanged(shouldPersist) {
    const minutes = Number(targetExpirationMinutesInput?.value);
    if (!Number.isFinite(minutes) || minutes <= 0) {
        return;
    }
    if (shouldPersist) {
        persistTargetExpirationMinutes();
    }
    resetTargetGiftTimers();
}

loadTargetExpirationMinutes();
if (targetExpirationMinutesInput) {
    targetExpirationMinutesInput.addEventListener('input', () => onExpirationMinutesChanged(false));
    targetExpirationMinutesInput.addEventListener('change', () => onExpirationMinutesChanged(true));
}

function setStatus(text, state) {
    statusDiv.innerText = text;
    statusDiv.classList.remove('connected', 'connecting', 'reconnecting', 'error');
    if (state) {
        statusDiv.classList.add(state);
    }
}

function setConnectingState() {
    connectBtn.disabled = true;
    disconnectBtn.disabled = true;
    setStatus('Conectando...', 'connecting');
}

function applyConnectedState(username) {
    setStatus(`Conectado a: ${username}`, 'connected');
    connectBtn.style.display = 'none';
    connectBtn.disabled = false;
    disconnectBtn.style.display = 'inline-block';
    disconnectBtn.disabled = false;
    usernameInput.disabled = true;
}

function applyDisconnectedState(error) {
    const isUserDisconnect = error === 'Desconectado pelo usuário' || error === 'Servidor encerrado';
    setStatus(isUserDisconnect ? 'Desconectado' : `Erro: ${error}`, isUserDisconnect ? '' : 'error');
    connectBtn.style.display = 'inline-block';
    connectBtn.disabled = false;
    disconnectBtn.style.display = 'none';
    disconnectBtn.disabled = false;
    usernameInput.disabled = false;
    clearTables();
}

function clearTables() {
    userTableBody.innerHTML = '';
    allGiftsTableBody.innerHTML = '';
    pinnedCommentsTableBody.innerHTML = '';
    if (correlationMessagesTableBody) {
        correlationMessagesTableBody.innerHTML = '';
    }

    for (const key in autoRemoveTimers) {
        clearTimeout(autoRemoveTimers[key]);
    }
    autoRemoveTimers = {};

    for (const key in pinnedCommentTimers) {
        clearTimeout(pinnedCommentTimers[key]);
    }
    pinnedCommentTimers = {};

    for (const key in flaggedMessageTimers) {
        clearTimeout(flaggedMessageTimers[key]);
    }
    flaggedMessageTimers = {};
    clearHistories();
}

function handleConnectionStatus(data) {
    console.log('[Frontend] handleConnectionStatus chamado:', data);
    if (data.success) {
        applyConnectedState(data.username);
        loadAvailableGifts();
        console.log('[Frontend] handleConnectionStatus: restaurando históricos...');
        loadAllGifts();
        loadPendingTargetGifts();
        loadPinnedComments();
        return;
    }

    // Reconexão automática em andamento: mantém o estado atual visível
    // sem limpar as tabelas.
    if (data.retries) {
        applyReconnectingState(data.retries, data.nextRetryInMs);
        return;
    }

    applyDisconnectedState(data.error || 'Falha ao conectar.');
}

function applyReconnectingState(retries, nextRetryInMs) {
    const secs = Math.max(0, Math.round((nextRetryInMs || 0) / 1000));
    setStatus(`Reconectando (tentativa ${retries}, em ${secs}s)...`, 'reconnecting');
    // Mantém os botões no estado conectado; o usuário ainda pode parar.
    connectBtn.style.display = 'none';
    disconnectBtn.style.display = 'inline-block';
    disconnectBtn.disabled = false;
}

function addUserToList(user, options = {}) {
    rememberLiveUser(user);
    if (!options.fromHistory) {
        addTargetGiftToHistory(user);
    }

    const historyId = user.historyId != null ? String(user.historyId) : '';

    if (historyId) {
        const existingByHistory = Array.from(userTableBody.querySelectorAll('.user-row')).find(row => {
            return row.dataset.historyId === historyId;
        });
        if (existingByHistory) {
            applyTargetGiftReceivedAt(existingByHistory, user.receivedAt, options.fromHistory);
            startAutoRemoveTimer(user.uniqueId, user.giftName, existingByHistory, {
                refreshStart: !options.fromHistory
            });
            return;
        }
    }

    const existingRow = Array.from(userTableBody.querySelectorAll('.user-row')).find(row => {
        return String(row.getAttribute('data-id')).toLowerCase() === String(user.uniqueId).toLowerCase() &&
            row.querySelector('.gift-name-cell').innerText === user.giftName;
    });

    if (existingRow) {
        if (!options.fromHistory) {
            const previousHistoryId = existingRow.dataset.historyId;
            if (previousHistoryId && previousHistoryId !== historyId) {
                markTargetGiftAnswered(previousHistoryId, 'automatic');
            }
        }
        if (historyId) {
            existingRow.dataset.historyId = historyId;
        }
        userTableBody.prepend(existingRow);
        if (user.isRed) {
            existingRow.classList.add('red');
        }
        applyTargetGiftReceivedAt(existingRow, user.receivedAt, options.fromHistory);
        startAutoRemoveTimer(user.uniqueId, user.giftName, existingRow, {
            refreshStart: !options.fromHistory
        });
        return;
    }

    const tr = document.createElement('tr');
    tr.className = 'user-row';
    tr.setAttribute('data-id', user.uniqueId);
    if (historyId) {
        tr.dataset.historyId = historyId;
    }

    if (user.isRed) {
        tr.classList.add('red');
    }

    const userTd = document.createElement('td');
    const userSpan = document.createElement('span');
    userSpan.className = 'user-name';
    userSpan.textContent = user.nickname;
    if (user.uniqueId) {
        userSpan.style.cursor = 'pointer';
        userSpan.title = 'Ver perfil';
        userSpan.addEventListener('click', () => openProfile(user.uniqueId));
    }
    userTd.appendChild(userSpan);

    const badge = createFollowerBadge(followerStatusForDisplay(user));
    if (badge) {
        userTd.appendChild(badge);
    }

    tr.appendChild(userTd);

    const giftTd = document.createElement('td');
    giftTd.className = 'gift-name-cell';
    giftTd.textContent = user.giftName;
    tr.appendChild(giftTd);

    const actionTd = document.createElement('td');
    const actionBtn = document.createElement('button');
    actionBtn.className = 'action-btn';
    actionBtn.dataset.uniqueId = user.uniqueId;
    actionBtn.dataset.giftName = user.giftName;
    actionBtn.textContent = 'Respondido';
    actionBtn.addEventListener('click', event => {
        removeUser(event.currentTarget.dataset.uniqueId, event.currentTarget.dataset.giftName, event.currentTarget);
    });
    actionTd.appendChild(actionBtn);
    tr.appendChild(actionTd);

    applyTargetGiftReceivedAt(tr, user.receivedAt, options.fromHistory);
    userTableBody.prepend(tr);
    startAutoRemoveTimer(user.uniqueId, user.giftName, tr, {
        refreshStart: !options.fromHistory
    });
}

function applyTargetGiftReceivedAt(element, receivedAt, fromHistory) {
    if (!fromHistory || !receivedAt || element.dataset.addedAt) {
        return;
    }
    const ts = new Date(receivedAt).getTime();
    if (Number.isFinite(ts)) {
        element.dataset.addedAt = String(ts);
    }
}

function startAutoRemoveTimer(uniqueId, giftName, element, options = {}) {
    const refreshStart = options.refreshStart !== false;
    const timerKey = `${uniqueId}-${giftName}`;

    if (autoRemoveTimers[timerKey]) {
        clearTimeout(autoRemoveTimers[timerKey]);
        delete autoRemoveTimers[timerKey];
    }

    if (refreshStart || !element.dataset.addedAt) {
        element.dataset.addedAt = String(Date.now());
    }

    const addedAt = Number(element.dataset.addedAt) || Date.now();
    const remainingMs = getTargetExpirationMs() - (Date.now() - addedAt);

    if (remainingMs <= 0) {
        markTargetGiftAnswered(element.dataset.historyId, 'automatic');
        element.remove();
        if (activeModalType === 'target-gifts') {
            renderGiftHistory();
        }
        return;
    }

    autoRemoveTimers[timerKey] = setTimeout(() => {
        markTargetGiftAnswered(element.dataset.historyId, 'automatic');
        element.remove();
        delete autoRemoveTimers[timerKey];
        if (activeModalType === 'target-gifts') {
            renderGiftHistory();
        }
    }, remainingMs);
}

function getTargetExpirationMinutes() {
    const minutes = Number(targetExpirationMinutesInput?.value);
    return Number.isFinite(minutes) && minutes > 0 ? Math.floor(minutes) : 4;
}

function getTargetExpirationMs() {
    return getTargetExpirationMinutes() * 60 * 1000;
}

function resetTargetGiftTimers() {
    Array.from(userTableBody.querySelectorAll('.user-row')).forEach(row => {
        const uniqueId = row.getAttribute('data-id');
        const giftName = row.querySelector('.gift-name-cell')?.innerText;
        if (uniqueId && giftName) {
            // Mantém o horário original do presente e só reaplica o prazo atual.
            startAutoRemoveTimer(uniqueId, giftName, row, { refreshStart: false });
        }
    });
}

function normalizeUserIdForGift(uniqueId) {
    return String(uniqueId || '').toLowerCase();
}

function normalizedGiftNameInTable(row) {
    return (row.querySelector('.gift-name-cell')?.innerText || '').trim().toLowerCase();
}

function normalizedGiftNameFromPayload(gift) {
    return String(gift.giftName || '').trim().toLowerCase();
}

function findAllGiftsRowForGift(gift) {
    const uid = normalizeUserIdForGift(gift.uniqueId);
    const giftId = gift.giftId != null && gift.giftId !== '' ? String(gift.giftId) : '';
    const name = normalizedGiftNameFromPayload(gift);
    return Array.from(allGiftsTableBody.querySelectorAll('tr')).find(row => {
        if (normalizeUserIdForGift(row.getAttribute('data-user-id')) !== uid) {
            return false;
        }
        const rowGiftId = row.getAttribute('data-gift-id') || '';
        if (giftId && rowGiftId) {
            return rowGiftId === giftId;
        }
        return normalizedGiftNameInTable(row) === name;
    });
}

function getGiftCountFromTableRow(row) {
    const cell = row.querySelector('.gift-count-cell');
    if (!cell) {
        return 0;
    }
    const n = parseInt(String(cell.textContent).trim(), 10);
    return Number.isFinite(n) && n >= 0 ? n : 0;
}

function isGiftStreakInProgress(gift) {
    const v = gift ? gift.repeatEnd : undefined;
    return v === false || v === 0 || v === 'false' || v === '0';
}

function committedGiftCountFromRow(row) {
    const raw = row.getAttribute('data-committed');
    if (raw == null || raw === '') {
        return getGiftCountFromTableRow(row);
    }
    const n = Number(raw);
    return Number.isFinite(n) && n >= 0 ? n : 0;
}

function reorderAllGiftsTableByCount() {
    const rows = Array.from(allGiftsTableBody.children);
    rows.sort((a, b) => (Number(b.getAttribute('data-count')) || 0) - (Number(a.getAttribute('data-count')) || 0));
    rows.forEach(row => allGiftsTableBody.appendChild(row));
}

function trimAllGiftsTable(maxRows) {
    while (allGiftsTableBody.children.length > maxRows) {
        allGiftsTableBody.lastElementChild.remove();
    }
}

function applyGiftFilter() {
    if (!giftSearchInput) return;
    const filterText = giftSearchInput.value.trim().toLowerCase();
    const rows = Array.from(allGiftsTableBody.querySelectorAll('tr'));
    
    rows.forEach(row => {
        const giftName = (row.getAttribute('data-gift-name') || '').toLowerCase();
        if (!filterText || giftName.includes(filterText)) {
            row.style.display = '';
        } else {
            row.style.display = 'none';
        }
    });
}

if (giftSearchInput) {
    giftSearchInput.addEventListener('input', applyGiftFilter);
}

function addAllGiftToList(gift) {
    giftCount++;
    rememberLiveUser(gift);

    const quantity = Math.max(1, Number(gift.repeatCount) || 1);
    const inProgress = isGiftStreakInProgress(gift);
    const existingRow = findAllGiftsRowForGift(gift);

    if (existingRow) {
        const committed = committedGiftCountFromRow(existingRow);
        // Combo em andamento: mostra committed + repeatCount. No fim, soma só o combo (não o total já exibido).
        const nextCommitted = inProgress ? committed : committed + quantity;
        const nextPending = inProgress ? quantity : 0;
        const next = nextCommitted + nextPending;
        existingRow.setAttribute('data-committed', String(nextCommitted));
        existingRow.setAttribute('data-count', String(next));
        const countCell = existingRow.querySelector('.gift-count-cell');
        if (countCell) {
            countCell.textContent = String(next);
        }
        if (gift.isRed) {
            existingRow.classList.add('red');
        }
        ensureFollowerBadge(existingRow.querySelector('td'), gift);
        reorderAllGiftsTableByCount();
        trimAllGiftsTable(200);
        applyGiftFilter();
        return;
    }

    const committed = inProgress ? 0 : quantity;
    const pending = inProgress ? quantity : 0;
    const total = committed + pending;

    const tr = document.createElement('tr');
    tr.className = 'gift-row';
    tr.setAttribute('data-id', gift.uniqueId);
    tr.setAttribute('data-user-id', gift.uniqueId);
    tr.setAttribute('data-gift-id', gift.giftId != null && gift.giftId !== '' ? String(gift.giftId) : '');
    tr.setAttribute('data-gift-name', gift.giftName || '');
    tr.setAttribute('data-committed', String(committed));
    tr.setAttribute('data-count', String(total));
    tr.setAttribute('data-target-gift', gift.isTargetGift ? 'true' : 'false');

    if (gift.isRed) {
        tr.classList.add('red');
    }

    const userTd = document.createElement('td');
    const userSpan = document.createElement('span');
    userSpan.className = 'user-name';
    userSpan.textContent = gift.nickname;
    if (gift.uniqueId) {
        userSpan.style.cursor = 'pointer';
        userSpan.title = 'Ver perfil';
        userSpan.addEventListener('click', () => openProfile(gift.uniqueId));
    }
    userTd.appendChild(userSpan);

    const badge = createFollowerBadge(followerStatusForDisplay(gift));
    if (badge) {
        userTd.appendChild(badge);
    }
    tr.appendChild(userTd);

    const giftTd = document.createElement('td');
    giftTd.className = 'gift-name-cell';
    giftTd.textContent = gift.giftName;
    tr.appendChild(giftTd);

    const countTd = document.createElement('td');
    countTd.className = 'gift-count-cell';
    countTd.textContent = String(total);
    tr.appendChild(countTd);

    allGiftsTableBody.appendChild(tr);
    reorderAllGiftsTableByCount();
    trimAllGiftsTable(200);
    applyGiftFilter();
}

function pinnedCommentKey(pinnedComment) {
    if (pinnedComment.pinId) {
        return `pin:${pinnedComment.pinId}`;
    }
    if (pinnedComment.id) {
        return `id:${pinnedComment.id}`;
    }
    return `${String(pinnedComment.uniqueId || '').toLowerCase()}|${pinnedComment.comment || ''}|${pinnedComment.timestamp || ''}`;
}

function addPinnedCommentToList(pinnedComment, options = {}) {
    rememberLiveUser(pinnedComment);
    if (!options.fromHistory) {
        addPinnedCommentToHistory();
    }

    const key = pinnedCommentKey(pinnedComment);
    const existing = Array.from(pinnedCommentsTableBody.querySelectorAll('.pinned-comment-row')).find(row => {
        return row.dataset.pinKey === key;
    });
    if (existing) {
        return;
    }

    const timerKey = `${pinnedComment.pinId || pinnedComment.timestamp || Date.now()}-${Math.random()}`;
    const tr = document.createElement('tr');
    tr.className = 'pinned-comment-row';
    tr.setAttribute('data-id', pinnedComment.uniqueId || '');
    tr.dataset.pinKey = key;

    const userTd = document.createElement('td');
    const userSpan = document.createElement('span');
    userSpan.className = 'user-name';
    userSpan.innerText = pinnedComment.nickname || pinnedComment.uniqueId || 'Nao identificado';
    if (pinnedComment.uniqueId) {
        userSpan.style.cursor = 'pointer';
        userSpan.title = 'Ver perfil';
        userSpan.addEventListener('click', () => openProfile(pinnedComment.uniqueId));
    }
    userTd.appendChild(userSpan);

    const badge = createFollowerBadge(followerStatusForDisplay(pinnedComment));
    if (badge) {
        userTd.appendChild(badge);
    }

    const commentTd = document.createElement('td');
    commentTd.className = 'comment-cell';
    commentTd.innerText = pinnedComment.comment || '[sem texto identificado]';

    tr.appendChild(userTd);
    tr.appendChild(commentTd);
    pinnedCommentsTableBody.prepend(tr);

    if (!options.fromHistory) {
        pinnedCommentTimers[timerKey] = setTimeout(() => {
            tr.remove();
            delete pinnedCommentTimers[timerKey];
        }, 50 * 1000);
    }

    if (pinnedCommentsTableBody.children.length > 50) {
        pinnedCommentsTableBody.lastChild.remove();
    }
}

function addFlaggedMessageToList(data) {
    if (!correlationMessagesTableBody) {
        return;
    }

    rememberLiveUser(data);

    const category = String(data.category || '').toUpperCase();
    if (!['REPETICAO', 'CORRELACAO', 'SPAM', 'GOLPE', 'PROSELITISMO', 'ODIO', 'OUTRO'].includes(category)) {
        return;
    }

    const messageKey = `alert-${category}-${String(data.uniqueId || '').toLowerCase()}-${String(data.comment || '').toLowerCase()}`;
    const existingRow = Array.from(correlationMessagesTableBody.children).find(row => row.dataset.messageKey === messageKey);
    if (existingRow) {
        existingRow.classList.add('blink-row');
        setTimeout(() => existingRow.classList.remove('blink-row'), 2000);
        return;
    }

    const timerKey = `flagged-${Date.now()}-${Math.random()}`;
    const tr = document.createElement('tr');
    tr.className = 'flagged-message-row blink-row';
    tr.dataset.messageKey = messageKey;

    const tdUser = document.createElement('td');
    const spanUser = document.createElement('span');
    spanUser.className = 'user-name';
    spanUser.textContent = data.nickname != null ? String(data.nickname) : '';
    if (data.uniqueId) {
        spanUser.style.cursor = 'pointer';
        spanUser.title = 'Ver perfil';
        spanUser.addEventListener('click', () => openProfile(data.uniqueId));
    }
    tdUser.appendChild(spanUser);

    const badge = createFollowerBadge(followerStatusForDisplay(data));
    if (badge) {
        tdUser.appendChild(badge);
    }

    const tdMsg = document.createElement('td');
    tdMsg.className = 'comment-cell';
    tdMsg.textContent = data.comment != null ? String(data.comment) : '';

    const tdCat = document.createElement('td');
    const spanCat = document.createElement('span');
    spanCat.className = 'infraction-category';
    spanCat.textContent = infractionCategoryLabel(category);
    if (category) spanCat.title = category;
    tdCat.appendChild(spanCat);

    const tdReason = document.createElement('td');
    tdReason.textContent = data.reason != null ? String(data.reason) : '';

    tr.appendChild(tdUser);
    tr.appendChild(tdMsg);
    tr.appendChild(tdCat);
    tr.appendChild(tdReason);

    correlationMessagesTableBody.prepend(tr);

    flaggedMessageTimers[timerKey] = setTimeout(() => {
        tr.remove();
        delete flaggedMessageTimers[timerKey];
    }, 60 * 1000);

    if (correlationMessagesTableBody.children.length > 50) {
        correlationMessagesTableBody.lastChild.remove();
    }
}

function handleKeywordMention(data) {
    if (!data) {
        return;
    }

    rememberLiveUser(data);
    markUserRed(data.uniqueId || '');

    addPinnedCommentToList({
        uniqueId: data.uniqueId,
        nickname: data.nickname,
        isFollower: data.isFollower,
        comment: data.comment,
        pinId: `keyword-${data.keyword || 'target'}-${data.uniqueId || 'anon'}-${data.timestamp || Date.now()}`,
        timestamp: data.timestamp || Date.now()
    });
}

function addCorrelationMessageToList(data) {
    if (!correlationMessagesTableBody) {
        return;
    }

    const correlationId = String(data.correlationId || '').trim();
    if (correlationId) {
        const existing = Array.from(correlationMessagesTableBody.children).find((row) => row.dataset.correlationId === correlationId);
        if (existing) {
            existing.remove();
        }
    }

    const tr = document.createElement('tr');
    tr.className = 'flagged-message-row';
    if (correlationId) {
        tr.dataset.correlationId = correlationId;
    }
    if (data.replacement) {
        tr.classList.add('blink-row');
        setTimeout(() => tr.classList.remove('blink-row'), 1800);
    }

    const tdGiftUser = document.createElement('td');
    const spanGiftUser = document.createElement('span');
    spanGiftUser.className = 'user-name';
    const userLabel = data.giftNickname || data.giftUserId || 'Nao identificado';
    spanGiftUser.textContent = data.giftUserId
        ? `${userLabel} (@${data.giftUserId})`
        : userLabel;
    tdGiftUser.appendChild(spanGiftUser);

    const tdQuestion = document.createElement('td');
    tdQuestion.className = 'comment-cell';
    tdQuestion.textContent = data.question || '[pergunta não encontrada]';

    const tdConfidence = document.createElement('td');
    const confidenceBadge = document.createElement('span');
    confidenceBadge.className = 'infraction-category';
    confidenceBadge.textContent = String(data.confidence || 'medium').toUpperCase();
    tdConfidence.appendChild(confidenceBadge);

    const tdMethod = document.createElement('td');
    const methodLabel = String(data.method || 'heuristica');
    tdMethod.textContent = data.replacement ? `${methodLabel} (ajustada)` : methodLabel;

    tr.appendChild(tdGiftUser);
    tr.appendChild(tdQuestion);
    tr.appendChild(tdConfidence);
    tr.appendChild(tdMethod);

    correlationMessagesTableBody.prepend(tr);
    if (correlationMessagesTableBody.children.length > 50) {
        correlationMessagesTableBody.lastChild.remove();
    }
}

function markUserRed(uniqueId) {
    const targetId = String(uniqueId).toLowerCase();
    const targetRows = document.querySelectorAll('.user-row, .gift-row[data-target-gift="true"]');

    targetRows.forEach(row => {
        const rowId = String(row.getAttribute('data-id')).toLowerCase();
        if (rowId === targetId) {
            row.classList.add('red');
        }
    });
}

function removeUser(uniqueId, giftName, button) {
    const timerKey = `${uniqueId}-${giftName}`;
    if (autoRemoveTimers[timerKey]) {
        clearTimeout(autoRemoveTimers[timerKey]);
        delete autoRemoveTimers[timerKey];
    }

    const tr = button.closest('.user-row');
    if (tr) {
        markTargetGiftAnswered(tr.dataset.historyId, 'manual');
        tr.remove();
        if (activeModalType === 'target-gifts') {
            renderGiftHistory();
        }
    }
}

async function loadInitialState() {
    try {
        const response = await fetch('/api/state');
        const payload = await response.json();

        if (typeof payload.aiConfigured === 'boolean') {
            applyInfractionsSectionTitle(payload.aiConfigured);
        }

        if (payload.connected && payload.username) {
            console.log('[Frontend] loadInitialState: já conectado a', payload.username, '- carregando presentes...');
            usernameInput.value = payload.username;
            applyConnectedState(payload.username);
            await Promise.all([
                loadAllGifts(),
                loadPendingTargetGifts(),
                loadPinnedComments(),
                loadRanking()
            ]);
        }

        // Carrega config de alertas e ranking mesmo desconectado.
        loadAlertConfig();
        loadRanking();
    } catch (error) {
        setStatus('Servidor indisponível', 'error');
    }
}

function setupEventStream() {
    const eventSource = new EventSource('/events');

    eventSource.addEventListener('server-state', event => {
        const data = JSON.parse(event.data);
        if (typeof data.aiConfigured === 'boolean') {
            applyInfractionsSectionTitle(data.aiConfigured);
        }
        if (data.connected && data.username) {
            usernameInput.value = data.username;
            applyConnectedState(data.username);
        } else {
            applyDisconnectedState('Desconectado pelo usuário');
        }
    });

    eventSource.addEventListener('connection-status', event => {
        handleConnectionStatus(JSON.parse(event.data));
    });

    eventSource.addEventListener('new-chat-message', event => {
        handleNewChatMessage(JSON.parse(event.data));
    });

    eventSource.addEventListener('live-user-connected', event => {
        rememberLiveUser(JSON.parse(event.data));
    });

    eventSource.addEventListener('new-follower', event => {
        rememberLiveUser(JSON.parse(event.data));
    });

    eventSource.addEventListener('new-social-event', event => {
        rememberLiveUser(JSON.parse(event.data));
    });

    eventSource.addEventListener('new-gift-user', event => {
        try {
            addUserToList(JSON.parse(event.data));
        } catch (error) {
            console.error('[Frontend] Falha ao registrar presente alvo:', error, event.data);
        }
    });

    eventSource.addEventListener('any-gift-received', event => {
        try {
            addAllGiftToList(JSON.parse(event.data));
        } catch (error) {
            console.error('[Frontend] Falha ao registrar presente:', error, event.data);
        }
    });

    eventSource.addEventListener('gifts-list', event => {
        const data = JSON.parse(event.data);
        populateAvailableGifts(data.gifts || data);
    });

    eventSource.addEventListener('pinned-comment', event => {
        try {
            addPinnedCommentToList(JSON.parse(event.data));
        } catch (error) {
            console.error('[Frontend] Falha ao registrar comentário fixado:', error, event.data);
        }
    });

    eventSource.addEventListener('flagged-message', event => {
        try {
            addFlaggedMessageToList(JSON.parse(event.data));
        } catch (error) {
            console.error('[Frontend] Falha ao registrar alerta:', error, event.data);
        }
    });

    eventSource.addEventListener('gift-question-correlation', event => {
        try {
            addCorrelationMessageToList(JSON.parse(event.data));
        } catch (error) {
            console.error('[Frontend] Falha ao registrar correlação:', error, event.data);
        }
    });

    eventSource.addEventListener('keyword-mention', event => {
        handleKeywordMention(JSON.parse(event.data));
    });

    eventSource.addEventListener('mark-user-red', event => {
        markUserRed(JSON.parse(event.data));
    });

    eventSource.addEventListener('suggested-response', event => {
        try {
            addSuggestion(JSON.parse(event.data));
        } catch (error) {
            console.error('[Frontend] Falha ao registrar resposta sugerida:', error, event.data);
        }
    });

    eventSource.addEventListener('settings-update', event => {
        renderTargetGifts();
    });

    eventSource.onerror = () => {
        setStatus('Reconectando ao servidor...', 'reconnecting');
    };
}

// --- Ranking Inteligente ---
function riskBadgeClass(level) {
    const map = {
        'none': 'risk-none',
        'low': 'risk-low',
        'medium': 'risk-medium',
        'high': 'risk-high',
        'critical': 'risk-critical'
    };
    return map[level] || 'risk-none';
}

function riskLabel(level) {
    const map = {
        'none': 'Nenhum',
        'low': 'Baixo',
        'medium': 'Médio',
        'high': 'Alto',
        'critical': 'Crítico'
    };
    return map[level] || (level || 'Nenhum');
}

async function loadRanking() {
    if (!rankingTableBody) return;
    try {
        const response = await fetch('/api/ranking');
        const data = await response.json();
        renderRanking(data);
    } catch (error) {
        console.error('[Frontend] Falha ao carregar ranking:', error);
    }
}

function renderRanking(ranking) {
    if (!rankingTableBody) return;
    rankingTableBody.innerHTML = '';
    const rows = ranking.userRanks || ranking.userRanks || [];
    if (!rows.length) {
        const tr = document.createElement('tr');
        const td = document.createElement('td');
        td.colSpan = 8;
        td.style.textAlign = 'center';
        td.style.color = 'var(--text-muted)';
        td.textContent = 'Sem dados de engajamento ainda.';
        tr.appendChild(td);
        rankingTableBody.appendChild(tr);
        return;
    }
    rows.forEach((user, index) => {
        const tr = document.createElement('tr');
        tr.className = 'user-row';
        tr.dataset.uniqueId = user.uniqueId || '';

        const tdRank = document.createElement('td');
        tdRank.className = 'ranking-rank';
        tdRank.textContent = String(index + 1);
        tr.appendChild(tdRank);

        const tdUser = document.createElement('td');
        const spanUser = document.createElement('span');
        spanUser.className = 'user-name';
        spanUser.style.cursor = 'pointer';
        spanUser.textContent = (user.nickname || user.uniqueId || 'Nao identificado');
        spanUser.addEventListener('click', () => {
            if (user.uniqueId) {
                openProfile(user.uniqueId);
            }
        });
        tdUser.appendChild(spanUser);
        tr.appendChild(tdUser);

        const tdScore = document.createElement('td');
        tdScore.textContent = (user.score != null ? user.score.toFixed(1) : '0');
        tr.appendChild(tdScore);

        const tdGifts = document.createElement('td');
        tdGifts.textContent = String(user.giftCount || 0);
        tr.appendChild(tdGifts);

        const tdShares = document.createElement('td');
        tdShares.textContent = String(user.shareCount || 0);
        tr.appendChild(tdShares);

        const tdMessages = document.createElement('td');
        tdMessages.textContent = String(user.messageCount || 0);
        tr.appendChild(tdMessages);

        const tdQuestions = document.createElement('td');
        tdQuestions.textContent = String(user.questionCount || 0);
        tr.appendChild(tdQuestions);

        const tdRisk = document.createElement('td');
        const badge = document.createElement('span');
        badge.className = 'risk-badge ' + riskBadgeClass(user.riskLevel);
        badge.textContent = riskLabel(user.riskLevel);
        tdRisk.appendChild(badge);
        tr.appendChild(tdRisk);

        rankingTableBody.appendChild(tr);
    });
}

// --- Respostas Sugeridas ---
function addSuggestion(data) {
    if (!suggestionsContainer) return;
    const card = document.createElement('div');
    card.className = 'suggestion-card';

    const q = document.createElement('div');
    q.className = 'suggestion-q';
    q.textContent = 'P: ' + (data.question || '(pergunta)');
    card.appendChild(q);

    const a = document.createElement('div');
    a.className = 'suggestion-a';
    a.textContent = data.suggested || '';
    card.appendChild(a);

    if (data.reason) {
        const reason = document.createElement('div');
        reason.className = 'suggestion-reason';
        reason.textContent = '💡 ' + data.reason;
        card.appendChild(reason);
    }

    suggestionsContainer.prepend(card);
    while (suggestionsContainer.children.length > 25) {
        suggestionsContainer.lastChild.remove();
    }
}

// --- Relatório Pós-Live ---
async function loadReport() {
    if (!generateReportBtn) return;
    generateReportBtn.disabled = true;
    const originalText = generateReportBtn.textContent;
    generateReportBtn.textContent = 'Gerando...';
    reportWrap.style.display = 'block';
    reportError.style.display = 'none';
    reportSummary.innerHTML = '';
    reportText.textContent = 'Gerando relatório com a IA, aguarê...';
    try {
        const response = await fetch('/api/report');
        const data = await response.json();
        if (data.error) {
            reportText.textContent = '';
            reportError.textContent = 'Erro: ' + data.error;
            reportError.style.display = 'block';
        } else {
            renderReport(data);
        }
    } catch (error) {
        reportText.textContent = '';
        reportError.textContent = 'Falha ao conectar com o servidor.';
        reportError.style.display = 'block';
        console.error('[Frontend] Falha ao gerar relatório:', error);
    } finally {
        generateReportBtn.disabled = false;
        generateReportBtn.textContent = originalText;
    }
}

function renderReport(report) {
    if (!reportSummary) return;
    reportSummary.innerHTML = '';
    const stats = [
        { value: report.durationMinutes != null ? report.durationMinutes + ' min' : '—', label: 'Duração' },
        { value: report.messageCount || 0, label: 'Mensagens' },
        { value: report.participantCount || 0, label: 'Participantes' },
        { value: report.giftCount || 0, label: 'Presentes' },
        { value: report.giftTotal || 0, label: 'Total presentes' }
    ];
    stats.forEach(stat => {
        const box = document.createElement('div');
        box.className = 'report-stat';
        box.innerHTML = '<div class="stat-value">' + escapeHtml(String(stat.value)) + '</div><div class="stat-label">' + escapeHtml(stat.label) + '</div>';
        reportSummary.appendChild(box);
    });
    reportText.textContent = report.summary || 'Relatório indisponível.';
}

function escapeHtml(value) {
    return String(value)
        .replace(/&/g, '&')
        .replace(/</g, '<')
        .replace(/>/g, '>');
}

// --- Alertas Externos ---
async function loadAlertConfig() {
    try {
        const response = await fetch('/api/alert-config');
        const config = await response.json();
        if (config.discordWebhook) alertDiscord.value = config.discordWebhook;
        if (config.telegramChatId) alertTelegramChat.value = config.telegramChatId;
        if (config.telegramToken) alertTelegramToken.value = config.telegramToken;
        if (config.whatsappUrl) alertWhatsapp.value = config.whatsappUrl;
    } catch (error) {
        console.error('[Frontend] Falha ao carregar config de alertas:', error);
    }
}

async function saveAlertConfig() {
    const payload = {
        discordWebhook: alertDiscord.value.trim(),
        telegramChatId: alertTelegramChat.value.trim(),
        telegramToken: alertTelegramToken.value.trim(),
        whatsappUrl: alertWhatsapp.value.trim()
    };
    alertConfigStatus.textContent = '';
    alertConfigStatus.className = 'alert-config-status';
    try {
        const response = await fetch('/api/alert-config', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(payload)
        });
        if (response.ok) {
            alertConfigStatus.textContent = 'Configuração salva.';
            alertConfigStatus.className = 'alert-config-status ok';
        } else {
            throw new Error('Falha no servidor');
        }
    } catch (error) {
        alertConfigStatus.textContent = 'Falha ao salvar configuração.';
        alertConfigStatus.className = 'alert-config-status err';
        console.error('[Frontend] Falha ao salvar config de alertas:', error);
    }
}

function updateAllGiftsVisibility() {
    if (!allGiftsSection || !allGiftsTableContainer) return;
    // Sempre manter a tabela de todos os presentes visível,
    // mesmo quando há presentes alvos configurados.
    allGiftsSection.style.display = '';
    allGiftsTableContainer.style.display = '';
}

function renderTargetGifts() {
    if (!targetGiftsList) return;
    targetGiftsList.innerHTML = '';

    fetch('/api/settings')
        .then(r => r.json())
        .then(settings => {
            const gifts = settings.targetGifts || [];
            gifts.forEach(giftName => {
                const span = document.createElement('span');
                span.className = 'target-gift-chip';
                const label = document.createElement('span');
                label.textContent = giftName;
                span.appendChild(label);
                const btn = document.createElement('button');
                btn.type = 'button';
                btn.textContent = '×';
                btn.setAttribute('aria-label', `Remover ${giftName}`);
                btn.addEventListener('click', () => removeTargetGift(giftName));
                span.appendChild(btn);
                targetGiftsList.appendChild(span);
            });
            updateAllGiftsVisibility();
        })
        .catch(() => {});
}

async function removeTargetGift(giftToRemove) {
    console.log('Removing target gift:', giftToRemove);
    try {
        const response = await fetch('/api/settings');
        const settings = await response.json();
        const gifts = settings.targetGifts || [];
        const updatedGifts = gifts.filter(g => g !== giftToRemove);

        const res = await fetch('/api/settings', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ ...settings, targetGifts: updatedGifts })
        });
        if (res.ok) {
            console.log('Successfully removed target gift:', giftToRemove);
            renderTargetGifts();
        } else {
            console.error('Failed to remove target gift:', await res.text());
        }
    } catch (e) {
        console.error('Erro ao remover presente alvo:', e);
    }
}

async function loadAvailableGifts() {
    if (!availableGiftSelect) return;
    try {
        const response = await fetch('/api/available-gifts');
        if (!response.ok) return;
        const gifts = await response.json();
        populateAvailableGifts(gifts);
    } catch (e) {
        console.error('Erro ao carregar presentes disponíveis:', e);
    }
}

function populateAvailableGifts(gifts) {
    if (!availableGiftSelect || !Array.isArray(gifts) || gifts.length === 0) {
        return;
    }
    const current = availableGiftSelect.value;
    const unique = [...new Set(gifts.map(gift => String(gift || '').trim()).filter(Boolean))];
    unique.sort((a, b) => a.localeCompare(b, 'pt'));
    availableGiftSelect.innerHTML = '<option value="">Selecione um presente...</option>';
    unique.forEach(gift => {
        const option = document.createElement('option');
        option.value = gift;
        option.textContent = gift;
        availableGiftSelect.appendChild(option);
    });
    if (current && unique.includes(current)) {
        availableGiftSelect.value = current;
    }
}

// Carrega presentes históricos do banco no frontend (pós-reconexão ou estado inicial).
async function loadAllGifts() {
    if (!allGiftsTableBody) {
        console.error('[Frontend] loadAllGifts: allGiftsTableBody não encontrado.');
        return;
    }
    if (allGiftsTableBody.children.length > 0) {
        return;
    }
    try {
        console.log('[Frontend] loadAllGifts: buscando presentes...');
        const response = await fetch('/api/gifts');
        console.log(`[Frontend] loadAllGifts: status=${response.status}`);
        if (!response.ok) {
            console.error('[Frontend] loadAllGifts: response não ok');
            return;
        }
        const gifts = await response.json();
        if (!Array.isArray(gifts)) {
            console.error('[Frontend] loadAllGifts: payload inválido', gifts);
            return;
        }
        if (allGiftsTableBody.children.length > 0) {
            return;
        }
        console.log(`[Frontend] loadAllGifts: ${gifts.length} presentes recebidos. Exemplo:`, gifts[0]);
        allGiftsTableBody.innerHTML = '';
        gifts.forEach(gift => {
            try {
                addAllGiftToList(gift);
            } catch (e) {
                console.error('[Frontend] loadAllGifts: erro ao adicionar gift:', e, gift);
            }
        });
        console.log(`[Frontend] loadAllGifts: ${gifts.length} presentes renderizados.`);
    } catch (e) {
        console.error('[Frontend] loadAllGifts: erro:', e);
    }
}

async function loadPendingTargetGifts() {
    if (!userTableBody) {
        return;
    }
    try {
        const response = await fetch('/api/target-gift-history?pending=1&limit=50');
        if (!response.ok) {
            console.error('[Frontend] loadPendingTargetGifts: status', response.status);
            return;
        }
        const items = await response.json();
        if (!Array.isArray(items)) {
            return;
        }
        items.slice().reverse().forEach(item => {
            addUserToList({
                uniqueId: item.uniqueId,
                nickname: item.nickname,
                giftName: item.giftName,
                historyId: item.id,
                receivedAt: item.receivedAt
            }, { fromHistory: true });
        });
        console.log(`[Frontend] loadPendingTargetGifts: ${items.length} pendentes restaurados.`);
    } catch (e) {
        console.error('[Frontend] loadPendingTargetGifts: erro:', e);
    }
}

async function loadPinnedCommentsFromApi() {
    try {
        const response = await fetch('/api/pinned-comments?limit=50');
        if (!response.ok) {
            throw new Error(`status ${response.status}`);
        }
        const items = await response.json();
        return Array.isArray(items) ? items : [];
    } catch (error) {
        console.error('[Frontend] Falha ao carregar histórico de comentários fixados:', error);
        return [];
    }
}

async function loadPinnedComments() {
    if (!pinnedCommentsTableBody) {
        return;
    }
    try {
        const items = await loadPinnedCommentsFromApi();
        items.slice().reverse().forEach(item => {
            addPinnedCommentToList(item, { fromHistory: true });
        });
        console.log(`[Frontend] loadPinnedComments: ${items.length} comentários restaurados.`);
    } catch (e) {
        console.error('[Frontend] loadPinnedComments: erro:', e);
    }
}

async function addTargetGift() {
    if (isAddingTargetGift) return;
    isAddingTargetGift = true;
    try {
        const value = availableGiftSelect.value.trim();
        if (!value) return;
        console.log('Adding target gift:', value);

        const response = await fetch('/api/settings');
        const settings = await response.json();
        const gifts = settings.targetGifts || [];
        if (gifts.includes(value)) {
            console.log('Gift already exists in targets:', value);
            return;
        }

        const updatedGifts = [...gifts, value];
        const res = await fetch('/api/settings', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ ...settings, targetGifts: updatedGifts })
        });
        if (res.ok) {
            console.log('Successfully added target gift:', value);
            availableGiftSelect.value = '';
            // renderTargetGifts() is triggered by the SSE 'settings-update' event
            // sent by the server after the POST, avoiding a race that duplicates tags.
        } else {
            console.error('Failed to add target gift:', await res.text());
        }
    } catch (e) {
        console.error('Erro ao adicionar presente alvo:', e);
    } finally {
        isAddingTargetGift = false;
    }
}

addTargetGiftBtn.addEventListener('click', addTargetGift);

async function bootstrap() {
    renderTargetGifts();
    applyInfractionsSectionTitle(false);

    try {
        await ensureBrowserChart();
        const ChartLib = window.Chart;
        if (!ChartLib) {
            throw new Error('Chart.js indisponível.');
        }
        chart = createChart(ChartLib);
    } catch (e) {
        console.error('Chart.js init error:', e);
        setStatus(`Erro ao iniciar gráfico: ${e.message}`, 'error');
        // Don't return; let the rest of bootstrap run.
    }

    await loadInitialState();
    setupEventStream();
}

void bootstrap();
