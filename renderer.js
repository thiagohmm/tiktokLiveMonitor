let ipcRenderer = null;
try {
    if (typeof require !== 'undefined') {
        ipcRenderer = require('electron').ipcRenderer;
    }
} catch {
    ipcRenderer = null;
}

const isElectron = Boolean(ipcRenderer);

function ensureBrowserChart() {
    if (isElectron || typeof window.Chart !== 'undefined') {
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
const botLoginBtn = document.getElementById('botLoginBtn');
const botStatusDiv = document.getElementById('botStatus');
const statusDiv = document.getElementById('status');
const userTableBody = document.getElementById('userTableBody');
const allGiftsTableBody = document.getElementById('allGiftsTableBody');
const pinnedCommentsTableBody = document.getElementById('pinnedCommentsTableBody');
const flaggedMessagesTableBody = document.getElementById('flaggedMessagesTableBody');
const infractionsSectionTitle = document.getElementById('infractionsSectionTitle');
const targetExpirationMinutesInput = document.getElementById('targetExpirationMinutes');
const chartCanvas = document.getElementById('messageChart');
const aiLedRow = document.getElementById('aiLedRow');
const aiLedDot = document.getElementById('aiLedDot');
const aiLedText = document.getElementById('aiLedText');
const targetGiftHistoryBtn = document.getElementById('targetGiftHistoryBtn');
const pinnedCommentHistoryBtn = document.getElementById('pinnedCommentHistoryBtn');
const historyModalBackdrop = document.getElementById('historyModalBackdrop');
const historyModalTitle = document.getElementById('historyModalTitle');
const historyModalBody = document.getElementById('historyModalBody');
const historyModalCloseBtn = document.getElementById('historyModalCloseBtn');

let chart;
let messageCount = 0;
let chartData = Array(60).fill(0);
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

function normalizeListenUser(value) {
    return String(value || '').trim().replace(/^@+/, '').toLowerCase();
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
        lastSeen: Date.now()
    });

    if (activeModalType === 'listen') {
        renderListenModal({ preserveFocus: true });
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

function renderUserLine(row, nickname, uniqueId) {
    const strong = document.createElement('strong');
    const userText = nickname || uniqueId || 'Nao identificado';
    strong.textContent = uniqueId ? `${userText} (@${uniqueId})` : userText;
    row.appendChild(strong);
}

function renderGiftHistory() {
    historyModalTitle.textContent = 'Últimos 15 Presentes Alvos';
    historyModalBody.replaceChildren(createModalList(targetGiftHistory, (row, item) => {
        renderUserLine(row, item.nickname, item.uniqueId);
        const gift = document.createElement('span');
        gift.textContent = item.giftName || 'Presente Alvo';
        row.appendChild(gift);
    }));
}

function renderPinnedCommentHistory() {
    historyModalTitle.textContent = 'Últimos 15 Comentários Fixados';
    historyModalBody.replaceChildren(createModalList(pinnedCommentHistory, (row, item) => {
        renderUserLine(row, item.nickname, item.uniqueId);
        const comment = document.createElement('span');
        comment.textContent = item.comment || '[sem texto identificado]';
        row.appendChild(comment);
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
        renderListenModal({ preserveFocus: true });
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
        renderUserLine(row, item.nickname, item.uniqueId);
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
    } else if (activeModalType === 'bot-config') {
        renderBotConfigModal();
    }
}

async function updateBotStatusBrowser() {
    if (isElectron) return;
    try {
        const res = await fetch('/api/bot-status');
        if (!res.ok) return;
        const data = await res.json();
        if (!botStatusDiv) return;
        botStatusDiv.style.display = 'block';
        botStatusDiv.textContent = `Bot: ${data.text}`;
        botStatusDiv.style.color = data.active ? '#22c55e' : '#666';
    } catch (e) {}
}

function renderBotConfigModal() {
    historyModalTitle.textContent = 'Configuração do Bot';
    historyModalBody.replaceChildren();

    const container = document.createElement('div');
    container.style.display = 'flex';
    container.style.flexDirection = 'column';
    container.style.gap = '14px';

    const help = document.createElement('p');
    help.style.fontSize = '0.92em';
    help.style.color = '#555';
    help.style.margin = '0 0 4px 0';
    help.textContent = 'No Docker/Navegador, o login automático não é possível. Insira os cookies da sua conta manualmente.';
    container.appendChild(help);

    const tiktokLink = document.createElement('button');
    tiktokLink.className = 'secondary-btn small-btn';
    tiktokLink.textContent = '1. Abrir TikTok para Login';
    tiktokLink.style.alignSelf = 'flex-start';
    tiktokLink.onclick = () => window.open('https://www.tiktok.com', '_blank');
    container.appendChild(tiktokLink);

    const form = document.createElement('div');
    form.style.display = 'flex';
    form.style.flexDirection = 'column';
    form.style.gap = '10px';
    form.style.padding = '12px';
    form.style.background = '#f9f9f9';
    form.style.borderRadius = '6px';
    form.style.border = '1px solid #eee';

    const sessionLabel = document.createElement('label');
    sessionLabel.textContent = '2. Cookie sessionid:';
    sessionLabel.style.fontWeight = 'bold';
    sessionLabel.style.fontSize = '0.85em';
    form.appendChild(sessionLabel);

    const sessionInput = document.createElement('input');
    sessionInput.type = 'text';
    sessionInput.placeholder = 'Cole o valor do cookie sessionid aqui...';
    sessionInput.style.padding = '10px';
    sessionInput.style.border = '1px solid #ccc';
    sessionInput.style.borderRadius = '4px';
    form.appendChild(sessionInput);

    const idcLabel = document.createElement('label');
    idcLabel.textContent = '3. Cookie tt-target-idc (opcional):';
    idcLabel.style.fontWeight = 'bold';
    idcLabel.style.fontSize = '0.85em';
    form.appendChild(idcLabel);

    const idcInput = document.createElement('input');
    idcInput.type = 'text';
    idcInput.placeholder = 'Ex: useast2a';
    idcInput.style.padding = '10px';
    idcInput.style.border = '1px solid #ccc';
    idcInput.style.borderRadius = '4px';
    form.appendChild(idcInput);

    const saveBtn = document.createElement('button');
    saveBtn.textContent = 'Salvar e Ativar Bot';
    saveBtn.style.marginTop = '6px';
    saveBtn.onclick = async () => {
        const sid = sessionInput.value.trim();
        if (!sid) {
            alert('Por favor, insira o sessionid.');
            return;
        }
        saveBtn.disabled = true;
        saveBtn.textContent = 'Salvando...';
        try {
            const res = await fetch('/api/bot-config', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    sessionId: sid,
                    ttTargetIdc: idcInput.value.trim()
                })
            });
            if (res.ok) {
                closeHistoryModal();
                updateBotStatusBrowser();
            } else {
                alert('Erro ao salvar configuração no servidor.');
            }
        } catch (e) {
            alert('Falha de conexão com o servidor.');
        } finally {
            saveBtn.disabled = false;
            saveBtn.textContent = 'Salvar e Ativar Bot';
        }
    };
    form.appendChild(saveBtn);

    container.appendChild(form);
    historyModalBody.appendChild(container);
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

function closeHistoryModal() {
    historyModalBackdrop.classList.remove('is-open');
    historyModalBackdrop.setAttribute('aria-hidden', 'true');
    activeModalType = null;
}

function addTargetGiftToHistory(user) {
    targetGiftHistory.unshift({
        uniqueId: user.uniqueId || '',
        nickname: user.nickname || user.uniqueId || 'Nao identificado',
        giftName: user.giftName || 'Presente Alvo',
        timestamp: user.timestamp || Date.now()
    });
    trimHistory(targetGiftHistory);
    if (activeModalType === 'target-gifts') {
        renderGiftHistory();
    }
}

function addPinnedCommentToHistory(pinnedComment) {
    pinnedCommentHistory.unshift({
        uniqueId: pinnedComment.uniqueId || '',
        nickname: pinnedComment.nickname || pinnedComment.uniqueId || 'Nao identificado',
        comment: pinnedComment.comment || '[sem texto identificado]',
        timestamp: pinnedComment.timestamp || Date.now()
    });
    trimHistory(pinnedCommentHistory);
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
        renderListenModal();
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
        RELIGIAO: 'Matriz africana',
        PROSELITISMO: 'Proselitismo',
        SPAM: 'Spam',
        GOLPE: 'Golpe',
        ODIO: 'Ódio',
        OUTRO: 'Outro',
        REPETICAO: 'Repetição'
    };
    const key = String(category || '').trim().toUpperCase();
    if (!key) return '—';
    return map[key] || key;
}

function applyInfractionsSectionTitle(aiConfigured) {
    if (!infractionsSectionTitle) {
        return;
    }
    infractionsSectionTitle.textContent = aiConfigured ? 'Infrações (Análise IA Local)' : 'Infrações';
}

function showAiLedChecking() {
    if (!aiLedRow || !aiLedDot || !aiLedText) return;
    aiLedRow.style.display = 'flex';
    aiLedDot.className = 'ai-led-dot ai-led-dot-checking';
    aiLedText.textContent = 'Verificando IA…';
}

function setAiLedActive(active) {
    if (!aiLedRow || !aiLedDot || !aiLedText) return;
    aiLedRow.style.display = 'flex';
    aiLedDot.className = 'ai-led-dot ' + (active ? 'ai-led-dot-on' : 'ai-led-dot-off');
    aiLedText.textContent = active ? 'IA ativa' : 'IA inativa';
}

function hideAiLed() {
    if (!aiLedRow || !aiLedDot || !aiLedText) return;
    aiLedRow.style.display = 'none';
    aiLedDot.className = 'ai-led-dot ai-led-dot-checking';
    aiLedText.textContent = 'Verificando IA…';
}

function runLlmProbeElectron() {
    showAiLedChecking();
    ipcRenderer
        .invoke('probe-llm')
        .then((data) => setAiLedActive(Boolean(data && data.llmActive)))
        .catch(() => setAiLedActive(false));
}

function createChart(ChartLib) {
    const ctx = chartCanvas.getContext('2d');
    return new ChartLib(ctx, {
        type: 'line',
        data: {
            labels: Array(60).fill('').map((_, index) => `${60 - index}s atrás`),
            datasets: [{
                label: 'Mensagens por segundo',
                data: chartData,
                borderColor: '#fe2c55',
                backgroundColor: 'rgba(254, 44, 85, 0.1)',
                fill: true,
                tension: 0.4
            }]
        },
        options: {
            responsive: true,
            maintainAspectRatio: false,
            scales: {
                y: {
                    beginAtZero: true,
                    ticks: { stepSize: 1 }
                },
                x: {
                    display: false
                }
            },
            plugins: {
                legend: { display: false }
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
    chart.update();
}, 1000);

targetGiftHistoryBtn.addEventListener('click', () => openHistoryModal('target-gifts'));
pinnedCommentHistoryBtn.addEventListener('click', () => openHistoryModal('pinned-comments'));
listenBtn.addEventListener('click', () => openHistoryModal('listen'));

botLoginBtn.style.display = 'inline-block';

if (isElectron) {
    botLoginBtn.addEventListener('click', () => {
        ipcRenderer.send('open-bot-window');
    });

    ipcRenderer.on('bot-status', (event, data) => {
        if (!botStatusDiv) return;
        botStatusDiv.style.display = 'block';
        botStatusDiv.textContent = `Bot: ${data.text}`;
        botStatusDiv.style.color = data.active ? '#22c55e' : '#666';
    });
} else {
    botLoginBtn.addEventListener('click', () => {
        openHistoryModal('bot-config');
    });
}

historyModalCloseBtn.addEventListener('click', closeHistoryModal);
historyModalBackdrop.addEventListener('click', event => {
    if (event.target === historyModalBackdrop) {
        closeHistoryModal();
    }
});
document.addEventListener('keydown', event => {
    if (event.key === 'Escape' && activeModalType) {
        closeHistoryModal();
    }
});

if (isElectron) {
    connectBtn.addEventListener('click', () => {
        const user = usernameInput.value.trim().replace(/^@/, '');
        if (!user) {
            return;
        }
        connectBtn.disabled = true;
        statusDiv.innerText = 'Conectando...';
        statusDiv.style.color = '#666';
        runLlmProbeElectron();
        ipcRenderer.send('connect-tiktok', user);
    });

    disconnectBtn.addEventListener('click', () => {
        hideAiLed();
        ipcRenderer.send('disconnect-tiktok');
        statusDiv.innerText = 'Desconectando...';
    });
} else {
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
            setAiLedActive(Boolean(probeData.llmActive));
        } catch {
            setAiLedActive(false);
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

targetExpirationMinutesInput.addEventListener('change', () => {
    resetTargetGiftTimers();
});

function setConnectingState() {
    connectBtn.disabled = true;
    disconnectBtn.disabled = true;
    statusDiv.innerText = 'Conectando...';
    statusDiv.style.color = '#666';
}

function applyConnectedState(username) {
    statusDiv.innerText = `Conectado a: ${username}`;
    statusDiv.style.color = 'green';
    connectBtn.style.display = 'none';
    connectBtn.disabled = false;
    disconnectBtn.style.display = 'inline-block';
    disconnectBtn.disabled = false;
    usernameInput.disabled = true;
}

function applyDisconnectedState(error) {
    statusDiv.innerText = error === 'Desconectado pelo usuário' || error === 'Servidor encerrado'
        ? 'Desconectado'
        : `Erro: ${error}`;
    statusDiv.style.color = error === 'Desconectado pelo usuário' || error === 'Servidor encerrado' ? '#666' : 'red';
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
    if (flaggedMessagesTableBody) {
        flaggedMessagesTableBody.innerHTML = '';
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
    if (data.success) {
        applyConnectedState(data.username);
        return;
    }

    applyDisconnectedState(data.error || 'Falha ao conectar.');
}

function addUserToList(user) {
    rememberLiveUser(user);
    addTargetGiftToHistory(user);

    const existingRow = Array.from(userTableBody.querySelectorAll('.user-row')).find(row => {
        return String(row.getAttribute('data-id')).toLowerCase() === String(user.uniqueId).toLowerCase() &&
            row.querySelector('.gift-name-cell').innerText === user.giftName;
    });

    if (existingRow) {
        userTableBody.prepend(existingRow);
        if (user.isRed) {
            existingRow.classList.add('red');
        }
        startAutoRemoveTimer(user.uniqueId, user.giftName, existingRow);
        return;
    }

    const tr = document.createElement('tr');
    tr.className = 'user-row';
    tr.setAttribute('data-id', user.uniqueId);

    if (user.isRed) {
        tr.classList.add('red');
    }

    tr.innerHTML = `
        <td>
            <span class="user-name">${user.nickname}</span>
        </td>
        <td class="gift-name-cell">${user.giftName}</td>
        <td>
            <button class="action-btn" data-unique-id="${user.uniqueId}" data-gift-name="${user.giftName}">Respondido</button>
        </td>
    `;

    tr.querySelector('.action-btn').addEventListener('click', event => {
        removeUser(event.currentTarget.dataset.uniqueId, event.currentTarget.dataset.giftName, event.currentTarget);
    });

    userTableBody.prepend(tr);
    startAutoRemoveTimer(user.uniqueId, user.giftName, tr);
}

function startAutoRemoveTimer(uniqueId, giftName, element) {
    const timerKey = `${uniqueId}-${giftName}`;

    if (autoRemoveTimers[timerKey]) {
        clearTimeout(autoRemoveTimers[timerKey]);
    }

    autoRemoveTimers[timerKey] = setTimeout(() => {
        element.remove();
        delete autoRemoveTimers[timerKey];
    }, getTargetExpirationMs());
}

function getTargetExpirationMs() {
    const minutes = Number(targetExpirationMinutesInput.value);
    const validMinutes = Number.isFinite(minutes) && minutes > 0 ? minutes : 4;
    return validMinutes * 60 * 1000;
}

function resetTargetGiftTimers() {
    Array.from(userTableBody.querySelectorAll('.user-row')).forEach(row => {
        const uniqueId = row.getAttribute('data-id');
        const giftName = row.querySelector('.gift-name-cell')?.innerText;
        if (uniqueId && giftName) {
            startAutoRemoveTimer(uniqueId, giftName, row);
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
    const name = normalizedGiftNameFromPayload(gift);
    return Array.from(allGiftsTableBody.querySelectorAll('tr')).find(row => {
        if (normalizeUserIdForGift(row.getAttribute('data-user-id')) !== uid) {
            return false;
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

function shouldSkipIntermediateStreakGift(gift) {
    return Number(gift.giftType) === 1 && gift.repeatEnd === false;
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

function addAllGiftToList(gift) {
    if (shouldSkipIntermediateStreakGift(gift)) {
        return;
    }

    rememberLiveUser(gift);

    const quantity = Math.max(1, Number(gift.repeatCount) || 1);
    const existingRow = findAllGiftsRowForGift(gift);

    if (existingRow) {
        const current = getGiftCountFromTableRow(existingRow);
        const next = current + quantity;
        existingRow.setAttribute('data-count', String(next));
        const countCell = existingRow.querySelector('.gift-count-cell');
        if (countCell) {
            countCell.textContent = String(next);
        }
        if (gift.isRed) {
            existingRow.classList.add('red');
        }
        reorderAllGiftsTableByCount();
        trimAllGiftsTable(50);
        return;
    }

    const tr = document.createElement('tr');
    tr.className = 'gift-row';
    tr.setAttribute('data-id', gift.uniqueId);
    tr.setAttribute('data-user-id', gift.uniqueId);
    tr.setAttribute('data-gift-id', gift.giftId != null && gift.giftId !== '' ? String(gift.giftId) : '');
    tr.setAttribute('data-gift-name', gift.giftName || '');
    tr.setAttribute('data-count', String(quantity));
    tr.setAttribute('data-target-gift', gift.isTargetGift ? 'true' : 'false');

    if (gift.isRed) {
        tr.classList.add('red');
    }

    tr.innerHTML = `
        <td>
            <span class="user-name">${gift.nickname}</span>
        </td>
        <td class="gift-name-cell">${gift.giftName}</td>
        <td class="gift-count-cell">${quantity}</td>
    `;

    allGiftsTableBody.appendChild(tr);
    reorderAllGiftsTableByCount();
    trimAllGiftsTable(50);
}

function addPinnedCommentToList(pinnedComment) {
    rememberLiveUser(pinnedComment);
    addPinnedCommentToHistory(pinnedComment);

    const timerKey = `${pinnedComment.pinId || pinnedComment.timestamp || Date.now()}-${Math.random()}`;
    const tr = document.createElement('tr');
    tr.className = 'pinned-comment-row';
    tr.setAttribute('data-id', pinnedComment.uniqueId || '');

    const userTd = document.createElement('td');
    const userSpan = document.createElement('span');
    userSpan.className = 'user-name';
    userSpan.innerText = pinnedComment.nickname || pinnedComment.uniqueId || 'Nao identificado';
    userTd.appendChild(userSpan);

    const commentTd = document.createElement('td');
    commentTd.className = 'comment-cell';
    commentTd.innerText = pinnedComment.comment || '[sem texto identificado]';

    tr.appendChild(userTd);
    tr.appendChild(commentTd);
    pinnedCommentsTableBody.prepend(tr);

    pinnedCommentTimers[timerKey] = setTimeout(() => {
        tr.remove();
        delete pinnedCommentTimers[timerKey];
    }, 50 * 1000);

    if (pinnedCommentsTableBody.children.length > 50) {
        pinnedCommentsTableBody.lastChild.remove();
    }
}

function addFlaggedMessageToList(data) {
    if (!flaggedMessagesTableBody) {
        return;
    }
    const timerKey = `flagged-${Date.now()}-${Math.random()}`;
    const tr = document.createElement('tr');
    tr.className = 'flagged-message-row';

    const tdUser = document.createElement('td');
    const spanUser = document.createElement('span');
    spanUser.className = 'user-name';
    spanUser.textContent = data.nickname != null ? String(data.nickname) : '';
    tdUser.appendChild(spanUser);

    const tdMsg = document.createElement('td');
    tdMsg.className = 'comment-cell';
    tdMsg.textContent = data.comment != null ? String(data.comment) : '';

    const tdCat = document.createElement('td');
    const spanCat = document.createElement('span');
    spanCat.className = 'infraction-category';
    spanCat.textContent = infractionCategoryLabel(data.category);
    if (data.category) spanCat.title = String(data.category);
    tdCat.appendChild(spanCat);

    const tdReason = document.createElement('td');
    const spanReason = document.createElement('span');
    spanReason.style.color = '#fe2c55';
    spanReason.style.fontWeight = 'bold';
    spanReason.textContent = data.reason != null ? String(data.reason) : '';
    tdReason.appendChild(spanReason);

    tr.appendChild(tdUser);
    tr.appendChild(tdMsg);
    tr.appendChild(tdCat);
    tr.appendChild(tdReason);

    flaggedMessagesTableBody.prepend(tr);

    flaggedMessageTimers[timerKey] = setTimeout(() => {
        tr.remove();
        delete flaggedMessageTimers[timerKey];
    }, 30 * 1000);

    if (flaggedMessagesTableBody.children.length > 50) {
        flaggedMessagesTableBody.lastChild.remove();
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
        tr.remove();
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
            usernameInput.value = payload.username;
            applyConnectedState(payload.username);
        }
    } catch (error) {
        statusDiv.innerText = 'Servidor indisponível';
        statusDiv.style.color = 'red';
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

    eventSource.addEventListener('new-gift-user', event => {
        addUserToList(JSON.parse(event.data));
    });

    eventSource.addEventListener('any-gift-received', event => {
        addAllGiftToList(JSON.parse(event.data));
    });

    eventSource.addEventListener('pinned-comment', event => {
        addPinnedCommentToList(JSON.parse(event.data));
    });

    eventSource.addEventListener('flagged-message', event => {
        addFlaggedMessageToList(JSON.parse(event.data));
    });

    eventSource.addEventListener('mark-user-red', event => {
        markUserRed(JSON.parse(event.data));
    });

    eventSource.onerror = () => {
        statusDiv.innerText = 'Reconectando ao servidor...';
        statusDiv.style.color = '#666';
    };
}

function setupElectronIpc() {
    ipcRenderer.on('connection-status', (event, data) => {
        if (data.success) {
            statusDiv.innerText = `Conectado a: ${data.username}`;
            statusDiv.style.color = 'green';
            connectBtn.style.display = 'none';
            connectBtn.disabled = false;
            disconnectBtn.style.display = 'inline-block';
            usernameInput.disabled = true;
        } else {
            statusDiv.innerText = data.error === 'Desconectado pelo usuário' ? 'Desconectado' : `Erro: ${data.error}`;
            statusDiv.style.color = data.error === 'Desconectado pelo usuário' ? '#666' : 'red';
            connectBtn.style.display = 'inline-block';
            connectBtn.disabled = false;
            disconnectBtn.style.display = 'none';
            usernameInput.disabled = false;
            clearTables();
        }
    });

    ipcRenderer.on('new-chat-message', (event, data) => {
        handleNewChatMessage(data);
    });

    ipcRenderer.on('live-user-connected', (event, data) => {
        rememberLiveUser(data);
    });

    ipcRenderer.on('new-gift-user', (event, user) => {
        addUserToList(user);
    });

    ipcRenderer.on('any-gift-received', (event, gift) => {
        addAllGiftToList(gift);
    });

    ipcRenderer.on('pinned-comment', (event, pinnedComment) => {
        addPinnedCommentToList(pinnedComment);
    });

    ipcRenderer.on('flagged-message', (event, data) => {
        addFlaggedMessageToList(data);
    });

    ipcRenderer.on('mark-user-red', (event, uniqueId) => {
        markUserRed(uniqueId);
    });
}

async function bootstrap() {
    try {
        await ensureBrowserChart();
        const ChartLib = isElectron ? require('chart.js/auto') : window.Chart;
        if (!ChartLib) {
            throw new Error('Chart.js indisponível.');
        }
        chart = createChart(ChartLib);
    } catch (e) {
        statusDiv.innerText = `Erro ao iniciar gráfico: ${e.message}`;
        statusDiv.style.color = 'red';
        return;
    }

    applyInfractionsSectionTitle(false);

    if (isElectron) {
        try {
            const cfg = await ipcRenderer.invoke('get-ui-config');
            applyInfractionsSectionTitle(Boolean(cfg && cfg.geminiConfigured));
        } catch {
            applyInfractionsSectionTitle(false);
        }
        setupElectronIpc();
    } else {
        await loadInitialState();
        setupEventStream();
        updateBotStatusBrowser();
    }
}

void bootstrap();
