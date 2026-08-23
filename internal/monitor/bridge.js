function loadConnectionClass() {
    const loaders = [
        () => require('tiktok-live-connector').TikTokLiveConnection,
        () => require('tiktok-live-connector/legacy').WebcastPushConnection,
        () => require('tiktok-live-connector').WebcastPushConnection,
    ];
    const errors = [];
    for (const load of loaders) {
        try {
            const cls = load();
            if (typeof cls === 'function') return cls;
        } catch (err) {
            errors.push(err.message);
        }
    }
    throw new Error(errors.length ? errors.join(' | ') : 'tiktok-live-connector connection class not found');
}

let ConnectionClass = null;
let connectionClassError = null;
try {
    ConnectionClass = loadConnectionClass();
} catch (err) {
    connectionClassError = err;
}

let connection = null;
let currentUsername = '';
let chatBuffer = [];
let processedPinnedMessages = new Set();
const PINNED_MESSAGE_MAX = 200;
const availableGiftsById = new Map();

function errorMessage(err) {
    return (err && err.exception && err.exception.message) || (err && err.message) || String(err || '');
}

function shouldIgnoreBridgeError(message) {
    if (typeof message !== 'string') return false;
    return message.includes("reading 'map'") ||
        message.includes('eulerstream.com') ||
        message.includes('Business plan') ||
        message.includes('fetchWebcastSignatureFromEulerRoute');
}

let stdoutBroken = false;

process.stdout.on('error', (err) => {
    if (err && (err.code === 'EPIPE' || err.code === 'ERR_STREAM_DESTROYED' || err.code === 'ERR_STREAM_WRITE_AFTER_END')) {
        stdoutBroken = true;
    }
});

process.stdin.on('end', () => process.exit(0));
process.stdin.on('close', () => process.exit(0));

function send(type, data) {
    if (stdoutBroken) {
        return;
    }
    try {
        const msg = JSON.stringify({ type, data }) + '\n';
        process.stdout.write(msg);
    } catch (err) {
        stdoutBroken = true;
    }
}

const { resolveIsFollower } = require('./follower');

function getUser(data) {
    const user = data.user || data.member || data.sender || data.author || data.owner || {};
    const uniqueId = data.uniqueId || user.uniqueId || data.displayId || user.displayId ||
        data.userId || user.userId || null;
    const nickname = data.nickname || user.nickname || uniqueId || null;
    return {
        uniqueId,
        nickname,
        isFollower: resolveIsFollower(data, user)
    };
}

// tiktok-live-connector v2 (WebcastChatMessage) exposes the message text in
// `content`, while the legacy WebcastPushConnection used `comment`. Support both.
function chatContent(data) {
    if (typeof data.content === 'string' && data.content.trim()) return data.content.trim();
    if (typeof data.comment === 'string' && data.comment.trim()) return data.comment.trim();
    return '';
}

function asBool(value, fallback = true) {
    if (value === undefined || value === null) return fallback;
    if (typeof value === 'boolean') return value;
    if (typeof value === 'number') return value !== 0;
    if (typeof value === 'string') {
        const v = value.trim().toLowerCase();
        if (v === 'false' || v === '0' || v === '') return false;
        return true;
    }
    return Boolean(value);
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

function looksLikeGift(item) {
    if (!item || typeof item !== 'object' || Array.isArray(item)) return false;
    if (item.giftName || item.describe || item.diamond_count != null || item.diamondCount != null) return true;
    if ((item.name || item.displayName) && (item.image || item.icon || item.gift_id != null || item.type != null)) return true;
    return false;
}

function extractGiftArray(raw) {
    const out = [];
    const visit = (value, depth, assumeGifts) => {
        if (value == null || depth > 4) return;
        if (Array.isArray(value)) {
            for (const item of value) {
                if (item && typeof item === 'object' && Array.isArray(item.gifts)) {
                    visit(item.gifts, depth + 1, true);
                    continue;
                }
                if (assumeGifts || looksLikeGift(item)) {
                    out.push(item);
                }
            }
            return;
        }
        if (typeof value !== 'object') return;
        if (Array.isArray(value.gifts)) visit(value.gifts, depth + 1, true);
        if (Array.isArray(value.pages)) visit(value.pages, depth + 1, false);
        if (value.data && typeof value.data === 'object') visit(value.data, depth + 1, false);
    };
    visit(raw, 0, Array.isArray(raw));
    if (out.length) return out;
    if (raw && typeof raw === 'object' && !Array.isArray(raw)) {
        for (const value of Object.values(raw)) {
            if (Array.isArray(value) && value.some(looksLikeGift)) {
                return value.filter(looksLikeGift);
            }
        }
    }
    return [];
}

function giftDisplayName(gift) {
    if (typeof gift === 'string') return gift.trim();
    if (!gift || typeof gift !== 'object') return '';
    const candidates = [gift.giftName, gift.name, gift.describe, gift.displayName];
    for (const candidate of candidates) {
        if (typeof candidate === 'string' && candidate.trim()) return candidate.trim();
        if (candidate && typeof candidate === 'object') {
            const nested = candidate.name || candidate.defaultPattern || candidate.default || '';
            if (typeof nested === 'string' && nested.trim()) return nested.trim();
        }
    }
    return '';
}

function currentGiftNames() {
    const names = [];
    const seen = new Set();
    for (const name of availableGiftsById.values()) {
        if (name && !seen.has(name)) {
            seen.add(name);
            names.push(name);
        }
    }
    return names;
}

function rememberAvailableGifts(raw) {
    for (const gift of extractGiftArray(raw)) {
        const name = giftDisplayName(gift);
        const id = gift?.id ?? gift?.gift_id ?? gift?.giftId;
        if (name && id != null) {
            availableGiftsById.set(Number(id), name);
        } else if (name) {
            availableGiftsById.set(name, name);
        }
    }
    return currentGiftNames();
}

async function fetchGiftCatalogUnsigned() {
    const webClient = connection && connection.webClient;
    const roomId = connection && connection.roomId;
    if (!webClient || !roomId || typeof webClient.getJsonObjectFromWebcastApi !== 'function') {
        return connection && connection.roomInfo ? connection.roomInfo : null;
    }
    try {
        // signRequest=false: não usa Euler Stream.
        return await webClient.getJsonObjectFromWebcastApi('gift/list/', {
            ...webClient.clientParams,
            room_id: roomId
        }, false);
    } catch {
        return connection.roomInfo || null;
    }
}

let giftsPublishInFlight = false;

async function publishAvailableGifts() {
    if (!connection || giftsPublishInFlight) {
        return;
    }
    giftsPublishInFlight = true;
    try {
        let giftNames = rememberAvailableGifts(connection.availableGifts);
        if (!giftNames.length) {
            giftNames = rememberAvailableGifts(await fetchGiftCatalogUnsigned());
        }
        send('gifts-list', { gifts: giftNames });
    } catch {
        const cached = currentGiftNames();
        send('gifts-list', { gifts: cached });
    } finally {
        giftsPublishInFlight = false;
    }
}

function firstNonEmptyString(...values) {
    for (const value of values) {
        if (typeof value === 'string' && value.trim()) return value.trim();
        if (value && typeof value === 'object') {
            const nested = value.giftName || value.name || value.describe || value.defaultPattern;
            if (typeof nested === 'string' && nested.trim()) return nested.trim();
        }
    }
    return '';
}

function resolveGiftName(data) {
    const fromPayload = firstNonEmptyString(
        data.giftDetails?.giftName,
        data.giftDetails?.name,
        data.giftName,
        data.name,
        data.describe,
        data.extendedGiftInfo?.giftName,
        data.extendedGiftInfo?.name,
        data.gift?.giftName,
        data.gift?.name
    );
    if (fromPayload) return fromPayload;
    const giftId = data.giftId ?? data.gift?.gift_id ?? data.gift?.giftId;
    if (giftId != null && availableGiftsById.has(Number(giftId))) {
        return availableGiftsById.get(Number(giftId));
    }
    return giftId != null && String(giftId) !== '' ? `Presente ${giftId}` : 'Presente';
}

function resolveGiftType(data) {
    const value = data.giftDetails?.giftType ?? data.giftType ?? data.gift?.gift_type ?? 0;
    const n = Number(value);
    return Number.isFinite(n) ? n : 0;
}

async function handleFetchGifts() {
    if (!connection) {
        send('gifts-list', { gifts: [] });
        return;
    }
    await publishAvailableGifts();
}

async function doConnect(username) {
    if (!ConnectionClass) {
        send('connection-status', {
            success: false,
            error: `Falha ao iniciar o conector TikTok: ${connectionClassError && connectionClassError.message}`
        });
        return;
    }
    if (connection) {
        doDisconnect();
    }

    chatBuffer = [];
    processedPinnedMessages = new Set();
    availableGiftsById.clear();
    currentUsername = username;
    connection = new ConnectionClass(username, {
        processInitialData: false,
        fetchRoomInfoOnConnect: true,
        enableExtendedGiftInfo: false
    });

    connection.on('connected', async () => {
        send('connection-status', { success: true, username });
        await publishAvailableGifts();
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
            comment: chatContent(data),
            timestamp: Date.now(),
            isFollower: user.isFollower
        });
    });

    connection.on('gift', (data) => {
        try {
            const user = getUser(data);
            const giftType = resolveGiftType(data);
            const repeatEnd = asBool(data.repeatEnd ?? data.repeat_end, true);
            const repeatCount = Number(data.repeatCount ?? data.repeat_count ?? 1) || 1;
            const giftName = resolveGiftName(data);
            const giftId = data.giftId ?? data.gift?.gift_id ?? data.gift?.giftId ?? null;

            const payload = {
                uniqueId: user.uniqueId || String(data.userId || ''),
                nickname: user.nickname || user.uniqueId || String(data.userId || 'Nao identificado'),
                giftName,
                giftId,
                repeatCount,
                repeatEnd,
                giftType,
                isFollower: user.isFollower,
                timestamp: Date.now()
            };

            send('any-gift-received', payload);

            if (giftName && !String(giftName).startsWith('Presente ')) {
                const key = giftId != null ? Number(giftId) : giftName;
                if (!availableGiftsById.has(key)) {
                    availableGiftsById.set(key, giftName);
                    send('gifts-list', { gifts: currentGiftNames() });
                }
            }

            if (repeatEnd) {
                send('new-gift-user', payload);
            }
        } catch (err) {
            send('error', { message: `gift handler: ${err.message}` });
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

    connection.on('giftPanelUpdate', (data) => {
        const names = rememberAvailableGifts(data);
        if (names.length) {
            send('gifts-list', { gifts: names });
        }
    });

    connection.on('roomPin', (data) => {
        handlePinnedMessage(data);
    });

    connection.on('decodedData', (type, payload) => {
        const data = payload && payload.data ? payload.data : payload;
        if (type === 'WebcastRoomPinMessage' || data?.method === 'WebcastRoomPinMessage') {
            handlePinnedMessage(data);
        }
    });

    connection.on('chat', (data) => {
        const user = getUser(data);
        chatBuffer.push({
            uniqueId: user.uniqueId,
            nickname: user.nickname,
            comment: chatContent(data),
            timestamp: Date.now(),
            isFollower: user.isFollower
        });
        if (chatBuffer.length > 500) chatBuffer = chatBuffer.slice(-500);
    });

    connection.on('error', (err) => {
        const message = errorMessage(err);
        if (shouldIgnoreBridgeError(message)) {
            return;
        }
        send('error', { message });
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
    const message = errorMessage(err);
    if (shouldIgnoreBridgeError(message)) {
        return;
    }
    send('error', { message });
});

process.on('unhandledRejection', (err) => {
    const message = errorMessage(err);
    if (shouldIgnoreBridgeError(message)) {
        return;
    }
    send('error', { message });
});
