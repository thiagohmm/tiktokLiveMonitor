const { ipcRenderer } = require('electron');
const Chart = require('chart.js/auto');

const usernameInput = document.getElementById('username');
const connectBtn = document.getElementById('connectBtn');
const disconnectBtn = document.getElementById('disconnectBtn');
const statusDiv = document.getElementById('status');
const userTableBody = document.getElementById('userTableBody');
const allGiftsTableBody = document.getElementById('allGiftsTableBody');
const pinnedCommentsTableBody = document.getElementById('pinnedCommentsTableBody');
const targetExpirationMinutesInput = document.getElementById('targetExpirationMinutes');
const ctx = document.getElementById('messageChart').getContext('2d');

let monitoredUsers = [];
let messageCount = 0;
let chartData = Array(60).fill(0); // Últimos 60 segundos
let autoRemoveTimers = {};
let pinnedCommentTimers = {};

const chart = new Chart(ctx, {
    type: 'line',
    data: {
        labels: Array(60).fill('').map((_, i) => 60 - i + 's atrás'),
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

// Atualizar o gráfico a cada segundo
setInterval(() => {
    chartData.push(messageCount);
    chartData.shift();
    messageCount = 0;
    chart.update();
}, 1000);

connectBtn.addEventListener('click', () => {
    const user = usernameInput.value.trim();
    if (user) {
        connectBtn.disabled = true;
        statusDiv.innerText = 'Conectando...';
        ipcRenderer.send('connect-tiktok', user);
    }
});

disconnectBtn.addEventListener('click', () => {
    ipcRenderer.send('disconnect-tiktok');
    statusDiv.innerText = 'Desconectando...';
});

targetExpirationMinutesInput.addEventListener('change', () => {
    resetTargetGiftTimers();
});

ipcRenderer.on('connection-status', (event, data) => {
    if (data.success) {
        statusDiv.innerText = `Conectado a: ${data.username}`;
        statusDiv.style.color = 'green';
        connectBtn.style.display = 'none';
        connectBtn.disabled = false; // re-enable so it's ready if we disconnect
        disconnectBtn.style.display = 'inline-block';
        usernameInput.disabled = true;
    } else {
        statusDiv.innerText = data.error === 'Desconectado pelo usuário' ? 'Desconectado' : `Erro: ${data.error}`;
        statusDiv.style.color = data.error === 'Desconectado pelo usuário' ? '#666' : 'red';
        connectBtn.style.display = 'inline-block';
        connectBtn.disabled = false; // re-enable on error
        disconnectBtn.style.display = 'none';
        usernameInput.disabled = false;
        
        // Limpar as tabelas ao desconectar
        clearTables();
    }
});

function clearTables() {
    userTableBody.innerHTML = '';
    allGiftsTableBody.innerHTML = '';
    pinnedCommentsTableBody.innerHTML = '';

    // Limpar timers de remoção automática
    for (const key in autoRemoveTimers) {
        clearTimeout(autoRemoveTimers[key]);
    }
    autoRemoveTimers = {};

    for (const key in pinnedCommentTimers) {
        clearTimeout(pinnedCommentTimers[key]);
    }
    pinnedCommentTimers = {};
}

ipcRenderer.on('new-chat-message', () => {
    messageCount++;
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

ipcRenderer.on('mark-user-red', (event, uniqueId) => {
    const targetId = String(uniqueId).toLowerCase();
    const targetRows = document.querySelectorAll('.user-row, .gift-row[data-target-gift="true"]');

    targetRows.forEach(row => {
        const rowId = String(row.getAttribute('data-id')).toLowerCase();
        if (rowId === targetId) {
            row.classList.add('red');
        }
    });
});

function addUserToList(user) {
    const timerKey = `${user.uniqueId}-${user.giftName}`;
    
    // Tentar encontrar uma linha existente para este usuário e este presente específico
    const existingRow = Array.from(userTableBody.querySelectorAll('.user-row')).find(row => {
        return String(row.getAttribute('data-id')).toLowerCase() === String(user.uniqueId).toLowerCase() && 
               row.querySelector('.gift-name-cell').innerText === user.giftName;
    });

    if (existingRow) {
        // Mover para o topo
        userTableBody.prepend(existingRow);
        
        // Se for uma atualização que deve ser vermelha
        if (user.isRed) {
            existingRow.classList.add('red');
        }
        
        // Resetar o timer de remoção automática
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
            <button class="action-btn" onclick="removeUser('${user.uniqueId}', '${user.giftName}', this)">Respondido</button>
        </td>
    `;

    userTableBody.prepend(tr);
    
    // Iniciar o timer de remoção automática
    startAutoRemoveTimer(user.uniqueId, user.giftName, tr);
}

function startAutoRemoveTimer(uniqueId, giftName, element) {
    const timerKey = `${uniqueId}-${giftName}`;
    
    // Limpar timer anterior se existir
    if (autoRemoveTimers[timerKey]) {
        clearTimeout(autoRemoveTimers[timerKey]);
    }
    
    const expirationMs = getTargetExpirationMs();
    autoRemoveTimers[timerKey] = setTimeout(() => {
        element.remove();
        delete autoRemoveTimers[timerKey];
        console.log(`Removido automaticamente por inatividade: ${uniqueId} (${giftName})`);
    }, expirationMs);
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

function addAllGiftToList(gift) {
    // Tentar encontrar uma linha existente para este usuário e este presente
    const existingRow = Array.from(allGiftsTableBody.querySelectorAll('tr')).find(row => {
        return row.getAttribute('data-user-id') === gift.uniqueId && 
               row.getAttribute('data-gift-id') === String(gift.giftId);
    });

    if (existingRow) {
        // Mover para o topo
        allGiftsTableBody.prepend(existingRow);
        if (gift.isRed) {
            existingRow.classList.add('red');
        }
        return;
    }

    const tr = document.createElement('tr');
    tr.className = 'gift-row';
    tr.setAttribute('data-id', gift.uniqueId);
    tr.setAttribute('data-user-id', gift.uniqueId);
    tr.setAttribute('data-gift-id', gift.giftId);
    tr.setAttribute('data-target-gift', gift.isTargetGift ? 'true' : 'false');

    if (gift.isRed) {
        tr.classList.add('red');
    }

    tr.innerHTML = `
        <td>
            <span class="user-name">${gift.nickname}</span>
        </td>
        <td>${gift.giftName}</td>
    `;

    // Adicionar no topo para ver os mais recentes primeiro
    allGiftsTableBody.prepend(tr);

    // Limitar a 50 entradas únicas para não sobrecarregar
    if (allGiftsTableBody.children.length > 50) {
        allGiftsTableBody.lastChild.remove();
    }
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

window.removeUser = function(uniqueId, giftName, btn) {
    const timerKey = `${uniqueId}-${giftName}`;
    if (autoRemoveTimers[timerKey]) {
        clearTimeout(autoRemoveTimers[timerKey]);
        delete autoRemoveTimers[timerKey];
    }
    const tr = btn.closest('.user-row');
    tr.remove();
};
