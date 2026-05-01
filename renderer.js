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
const statusDiv = document.getElementById('status');
const userTableBody = document.getElementById('userTableBody');
const allGiftsTableBody = document.getElementById('allGiftsTableBody');
const pinnedCommentsTableBody = document.getElementById('pinnedCommentsTableBody');
const flaggedMessagesTableBody = document.getElementById('flaggedMessagesTableBody');
const infractionsSectionTitle = document.getElementById('infractionsSectionTitle');
const targetExpirationMinutesInput = document.getElementById('targetExpirationMinutes');
const chartCanvas = document.getElementById('messageChart');

let chart;
let messageCount = 0;
let chartData = Array(60).fill(0);
let autoRemoveTimers = {};
let pinnedCommentTimers = {};
let flaggedMessageTimers = {};

function applyInfractionsSectionTitle(geminiConfigured) {
    if (!infractionsSectionTitle) {
        return;
    }
    infractionsSectionTitle.textContent = geminiConfigured ? 'Infrações (Análise IA)' : 'Infrações';
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

connectBtn.addEventListener('click', async () => {
    const username = usernameInput.value.trim().replace(/^@/, '');
    if (!username) {
        return;
    }

    setConnectingState();

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
});

disconnectBtn.addEventListener('click', async () => {
    statusDiv.innerText = 'Desconectando...';

    try {
        await fetch('/api/disconnect', { method: 'POST' });
    } catch (error) {
        applyDisconnectedState(error.message);
    }
});

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
}

function handleConnectionStatus(data) {
    if (data.success) {
        applyConnectedState(data.username);
        return;
    }

    applyDisconnectedState(data.error || 'Falha ao conectar.');
}

function addUserToList(user) {
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

    tr.innerHTML = `
        <td><span class="user-name">${data.nickname}</span></td>
        <td class="comment-cell">${data.comment}</td>
        <td><span style="color: #fe2c55; font-weight: bold;">${data.reason}</span></td>
    `;

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

        if (typeof payload.geminiConfigured === 'boolean') {
            applyInfractionsSectionTitle(payload.geminiConfigured);
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
        if (typeof data.geminiConfigured === 'boolean') {
            applyInfractionsSectionTitle(data.geminiConfigured);
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

    eventSource.addEventListener('new-chat-message', () => {
        messageCount++;
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

async function bootstrap() {
    try {
        await ensureBrowserChart();
        if (!window.Chart) {
            throw new Error('Chart.js indisponível.');
        }
        chart = createChart(window.Chart);
    } catch (e) {
        statusDiv.innerText = `Erro ao iniciar gráfico: ${e.message}`;
        statusDiv.style.color = 'red';
        return;
    }

    await loadInitialState();
    setupEventStream();
}

void bootstrap();
