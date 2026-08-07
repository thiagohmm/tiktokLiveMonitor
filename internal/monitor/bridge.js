const { WebcastPushConnection } = require('tiktok-live-connector');

let connection = null;
let currentUsername = '';
let chatBuffer = [];
let processedPinnedMessages = new Set();
const PINNED_MESSAGE_MAX = 200;

function send(type, data) {
    const msg = JSON.stringify({ type, data }) + '\n';
    process.stdout.write(msg);
}

function getUser(data) {
    const user = data.user || data.member || data.sender || data.author || data.owner || {};
    return {
        uniqueId: data.uniqueId || user.uniqueId || null,
        nickname: data.nickname || user.nickname || data.uniqueId || user.uniqueId || null,
        isFollower: typeof data.isFollower === 'boolean' ? data.isFollower :
                    typeof user.isFollower === 'boolean' ? user.isFollower : null
    };
}

function textFromDisplayText(displayText) {
    if (!displayText) return null;
    if (typeof displayText === 'string') return displayText;
    return displayText.defaultPattern || null;
}

function getPinnedContent(data) {
    const pinnedSource = data.chatMessage ||
        data.pinMessage ||
        data.pinnedMessage ||
        data.socialMessage ||
        data.giftMessage ||
        data.memberMessage ||
        data.likeMessage ||
        data;
    const candidates = [
        data.content, data.comment, data.text, data.message, data.description,
        data.pinnedText, data.pinnedComment,
        typeof data.pinnedMessage === 'string' ? data.pinnedMessage : null,
        pinnedSource.comment, pinnedSource.content, pinnedSource.text, pinnedSource.message,
        pinnedSource.actionDescription,
        textFromDisplayText(pinnedSource.common?.displayText),
        textFromDisplayText(pinnedSource.publicAreaMessageCommon?.displayText),
        textFromDisplayText(pinnedSource.publicAreaCommon?.userLabel),
        textFromDisplayText(pinnedSource.trayDisplayText),
        textFromDisplayText(pinnedSource.displayTextForAnchor),
        textFromDisplayText(pinnedSource.displayTextForAudience)
    ];
    const content = candidates.find(v => typeof v === 'string' && v.trim());
    return content ? content.trim() : null;
}

function getPinnedUser(data, content) {
    const sources = [
        data.chatMessage, data.pinMessage, data.pinnedMessage,
        data.socialMessage, data.giftMessage, data.memberMessage, data.likeMessage,
        data.user, data
    ].filter(s => s && typeof s === 'object');
    for (const s of sources) {
        const u = getUser(s);
        if (u.uniqueId) return u;
    }
    const mentionMatch = content && content.match(/@([a-zA-Z0-9._]+)/);
    if (mentionMatch) {
        return { uniqueId: mentionMatch[1].toLowerCase(), nickname: mentionMatch[1], isFollower: null };
    }
    if (content) {
        const contentLower = content.toLowerCase();
        const sender = chatBuffer.find(m => {
            const c = String(m.comment || '').toLowerCase();
            return contentLower.includes(c) || c.includes(contentLower);
        });
        if (sender) return { uniqueId: sender.uniqueId, nickname: sender.nickname, isFollower: sender.isFollower };
    }
    return { uniqueId: null, nickname: null, isFollower: null };
}

function getPinnedMessageKey(data) {
    return data.pinId || data.msgId ||
        data.chatMessage?.common?.msgId || data.chatMessage?.msgId ||
        `${data.pinTime || ''}:${data.chatMessage?.comment || ''}`;
}

function handlePinnedMessage(data) {
    if (!data || data.method === 'unpin' || data.action === 2) return;
    const messageKey = getPinnedMessageKey(data);
    if (messageKey && processedPinnedMessages.has(messageKey)) return;
    if (messageKey) {
        processedPinnedMessages.add(messageKey);
        if (processedPinnedMessages.size > PINNED_MESSAGE_MAX) {
            processedPinnedMessages = new Set(Array.from(processedPinnedMessages).slice(-100));
        }
    }
    const content = getPinnedContent(data);
    const pinnedUser = getPinnedUser(data, content);
    send('pinned-comment', {
        uniqueId: pinnedUser.uniqueId,
        nickname: pinnedUser.nickname || pinnedUser.uniqueId || 'Nao identificado',
        comment: content || '[sem texto identificado]',
        pinId: data.pinId || data.msgId || null,
        timestamp: Date.now(),
        isFollower: pinnedUser.isFollower
    });
    if (pinnedUser.uniqueId) {
        send('mark-user-red', pinnedUser.uniqueId.toLowerCase());
    }
}

process.stdin.on('data', async (chunk) => {
    const lines = chunk.toString().trim().split('\n');
    for (const line of lines) {
        try {
            const cmd = JSON.parse(line);
            await handleCommand(cmd);
        } catch (e) {
            send('error', { message: e.message });
        }
    }
});

async function handleCommand(cmd) {
    switch (cmd.action) {
        case 'connect':
            await doConnect(cmd.username);
            break;
        case 'disconnect':
            doDisconnect();
            break;
        case 'get-state':
            send('state', { connected: !!connection, username: currentUsername });
            break;
        case 'fetch-gifts':
            await handleFetchGifts();
            break;
    }
}

async function handleFetchGifts() {
    if (!connection) {
        send('gifts-list', { gifts: [] });
        return;
    }
    try {
        const gifts = await connection.fetchAvailableGifts();
        const giftNames = [];
        const seen = new Set();
        if (gifts && Array.isArray(gifts)) {
            for (const gift of gifts) {
                const name = gift.giftName || gift.name || '';
                if (name && !seen.has(name)) {
                    seen.add(name);
                    giftNames.push(name);
                }
            }
        }
        send('gifts-list', { gifts: giftNames });
    } catch (err) {
        send('gifts-list', { gifts: [] });
    }
}

async function doConnect(username) {
    if (connection) {
        doDisconnect();
    }

    chatBuffer = [];
    processedPinnedMessages = new Set();
    currentUsername = username;
    connection = new WebcastPushConnection(username, {
        processInitialData: false,
        fetchRoomInfoOnConnect: true,
        enableExtendedGiftInfo: false
    });

    connection.on('connected', async () => {
        send('connection-status', { success: true, username });
        // Buscar presentes disponíveis automaticamente após conectar
        try {
            const gifts = await connection.fetchAvailableGifts();
            const giftNames = [];
            const seen = new Set();
            if (gifts && Array.isArray(gifts)) {
                for (const gift of gifts) {
                    const name = gift.giftName || gift.name || '';
                    if (name && !seen.has(name)) {
                        seen.add(name);
                        giftNames.push(name);
                    }
                }
            }
            send('gifts-list', { gifts: giftNames });
        } catch (err) {
            send('gifts-list', { gifts: [] });
        }
    });

    connection.on('disconnected', () => {
        send('connection-status', { success: false, error: 'Desconectado' });
        connection = null;
    });

    connection.on('chat', (data) => {
        const user = getUser(data);
        send('new-chat-message', {
            uniqueId: user.uniqueId,
            nickname: user.nickname,
            comment: data.comment || '',
            timestamp: Date.now(),
            isFollower: user.isFollower
        });
    });

    connection.on('gift', (data) => {
        const user = getUser(data);
        const giftType = data.giftDetails?.giftType ?? data.giftType ?? 0;
        const repeatEnd = data.repeatEnd ?? true;
        const repeatCount = data.repeatCount ?? 1;
        const giftName = data.giftDetails?.giftName || data.giftName || '';

        const payload = {
            uniqueId: user.uniqueId,
            nickname: user.nickname,
            giftName,
            repeatCount,
            repeatEnd,
            giftType,
            isFollower: user.isFollower,
            timestamp: Date.now()
        };

        send('any-gift-received', payload);

        const isTarget = giftType === 1 ? repeatEnd : true;
        if (isTarget) {
            send('new-gift-user', payload);
        }
    });

    connection.on('member', (data) => {
        const user = getUser(data);
        send('live-user-connected', {
            uniqueId: user.uniqueId,
            nickname: user.nickname,
            isFollower: user.isFollower
        });
    });

    connection.on('follow', (data) => {
        const user = getUser(data);
        send('new-follower', {
            uniqueId: user.uniqueId,
            nickname: user.nickname,
            isFollower: true
        });
    });

    connection.on('share', (data) => {
        const user = getUser(data);
        send('new-social-event', {
            uniqueId: user.uniqueId,
            nickname: user.nickname,
            isFollower: user.isFollower
        });
    });

    connection.on('roomPin', (data) => {
        handlePinnedMessage(data);
    });

    connection.on('decodedData', (type, data) => {
        if (type === 'WebcastRoomPinMessage' || data?.method === 'WebcastRoomPinMessage') {
            handlePinnedMessage(data);
        }
    });

    connection.on('chat', (data) => {
        chatBuffer.push({
            uniqueId: getUser(data).uniqueId,
            nickname: getUser(data).nickname,
            comment: data.comment || '',
            timestamp: Date.now()
        });
        if (chatBuffer.length > 500) chatBuffer = chatBuffer.slice(-500);
    });

    connection.on('error', (err) => {
        send('error', { message: err.message || String(err) });
    });

    try {
        await connection.connect();
    } catch (err) {
        send('connection-status', {
            success: false,
            error: `Falha ao conectar: ${err.message}`
        });
        connection = null;
    }
}

function doDisconnect() {
    if (connection) {
        try { connection.disconnect(); } catch (e) {}
        connection = null;
    }
    currentUsername = '';
}

process.on('uncaughtException', (err) => {
    send('error', { message: err.message });
});

process.on('unhandledRejection', (err) => {
    send('error', { message: err.message || String(err) });
});
