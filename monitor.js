const { WebcastPushConnection } = require('tiktok-live-connector');
const { aiConfigured, probeLlamaReady } = require('./ai');
const { analyzeMessage: analyzeMessageModeration } = require('./moderation');
const { addFeedback } = require('./database');

// Variáveis de estado
let tiktokConnection;
let currentUsername;
let chatBuffer = []; // Ultimas 500 mensagens
let pinnedCommentUsers = new Set();
let processedPinnedMessages = new Set();
let repeatAlertedSequences = new Set();
let eventHandlers = [];

function repeatSequenceKey(senderKey, commentLower) {
    return JSON.stringify([senderKey, commentLower]);
}

function getUserFromObject(data) {
    if (!data) return { uniqueId: null, nickname: null, isFollower: null };

    const user = data.user || data.member || data.sender || data.author || data.owner || {};
    const uniqueId = data.uniqueId || user.uniqueId || user.secUid || user.id || null;
    const nickname = data.nickname || user.nickname || user.displayName || uniqueId || null;

    let isFollower = null;
    if (user.followInfo && typeof user.followInfo.followStatus === 'number') {
        isFollower = user.followInfo.followStatus >= 1;
    } else if (data.followInfo && typeof data.followInfo.followStatus === 'number') {
        isFollower = data.followInfo.followStatus >= 1;
    } else if (data.isFollower !== undefined) {
        isFollower = Boolean(data.isFollower);
    } else if (user.isFollower !== undefined) {
        isFollower = Boolean(user.isFollower);
    }

    return { uniqueId, nickname, isFollower };
}

function textFromDisplayText(displayText) {
    if (!displayText) return null;
    if (typeof displayText === 'string') return displayText;
    if (displayText.defaultPattern) return displayText.defaultPattern;
    if (displayText.format) return displayText.format;
    if (displayText.displayText) return textFromDisplayText(displayText.displayText);
    const pieces = displayText.pieces || displayText.piecesList;
    if (Array.isArray(pieces)) {
        const text = pieces
            .map(piece => piece.stringValue || piece.text || piece.userValue?.nickname || piece.userValue?.uniqueId || '')
            .join('')
            .trim();
        return text || null;
    }
    return null;
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
        data.content,
        data.comment,
        data.text,
        data.message,
        data.description,
        data.pinnedText,
        data.pinnedComment,
        typeof data.pinnedMessage === 'string' ? data.pinnedMessage : null,
        pinnedSource.comment,
        pinnedSource.content,
        pinnedSource.text,
        pinnedSource.message,
        pinnedSource.actionDescription,
        textFromDisplayText(pinnedSource.common?.displayText),
        textFromDisplayText(pinnedSource.publicAreaMessageCommon?.displayText),
        textFromDisplayText(pinnedSource.publicAreaCommon?.userLabel),
        textFromDisplayText(pinnedSource.trayDisplayText),
        textFromDisplayText(pinnedSource.displayTextForAnchor),
        textFromDisplayText(pinnedSource.displayTextForAudience)
    ];

    const content = candidates.find(value => typeof value === 'string' && value.trim());
    return content ? content.trim() : null;
}

function getPinnedUser(data, content) {
    const sources = [
        data.chatMessage,
        data.pinMessage,
        data.pinnedMessage,
        data.socialMessage,
        data.giftMessage,
        data.memberMessage,
        data.likeMessage,
        data.user,
        data
    ].filter(source => source && typeof source === 'object');

    for (const source of sources) {
        const user = getUserFromObject(source);
        if (user.uniqueId) {
            return user;
        }
    }

    const mentionMatch = content && content.match(/@([a-zA-Z0-9._]+)/);
    if (mentionMatch) {
        return { uniqueId: mentionMatch[1].toLowerCase(), nickname: mentionMatch[1], isFollower: null };
    }

    if (content) {
        const contentLower = content.toLowerCase();
        const sender = chatBuffer.find(m => {
            const commentLower = String(m.comment || '').toLowerCase();
            return contentLower.includes(commentLower) || commentLower.includes(contentLower);
        });
        if (sender) {
            return { uniqueId: sender.uniqueId, nickname: sender.nickname, isFollower: sender.isFollower };
        }
    }

    return { uniqueId: null, nickname: null, isFollower: null };
}

function getPinnedMessageKey(data) {
    return data.pinId ||
        data.msgId ||
        data.chatMessage?.common?.msgId ||
        data.chatMessage?.msgId ||
        `${data.pinTime || ''}:${data.chatMessage?.comment || ''}`;
}

function handlePinnedMessage(data) {
    if (!data || data.method === 'unpin' || data.action === 2) {
        return;
    }

    const messageKey = getPinnedMessageKey(data);
    if (messageKey && processedPinnedMessages.has(messageKey)) {
        return;
    }
    if (messageKey) {
        processedPinnedMessages.add(messageKey);
        if (processedPinnedMessages.size > 200) {
            processedPinnedMessages = new Set(Array.from(processedPinnedMessages).slice(-100));
        }
    }

    const content = getPinnedContent(data);
    const pinnedUser = getPinnedUser(data, content);

    emit('pinned-comment', {
        uniqueId: pinnedUser.uniqueId,
        nickname: pinnedUser.nickname || pinnedUser.uniqueId || 'Nao identificado',
        comment: content || '[sem texto identificado]',
        pinId: data.pinId || data.msgId || null,
        timestamp: Date.now(),
        isFollower: pinnedUser.isFollower
    });

    if (pinnedUser.uniqueId) {
        const pinnedUniqueId = normalizeId(pinnedUser.uniqueId);
        pinnedCommentUsers.add(pinnedUniqueId);
        emit('mark-user-red', pinnedUniqueId);
    }
}

// Configurações padrão
let settings = {
    moderationEnabled: true,
    aiModerationEnabled: true,
    logLevel: 'info'
};

function normalizeId(value) {
    return String(value || '').toLowerCase();
}

function isTargetGift(giftName) {
    const normalizedGiftName = String(giftName || '').toLowerCase();
    const compactGiftName = normalizedGiftName.replace(/[^a-z0-9]/g, '');

    return normalizedGiftName.includes('perfume') ||
        normalizedGiftName.includes('tiny dyny') ||
        normalizedGiftName.includes('tiny diny') ||
        compactGiftName.includes('tinydyny') ||
        compactGiftName.includes('tinydiny');
}

function getGiftTypeFromPayload(data) {
    return data.giftType ?? data.giftDetails?.giftType;
}

function isGiftCountingSettlement(data) {
    const giftType = getGiftTypeFromPayload(data);
    if (Number(giftType) === 1 && data.repeatEnd === false) {
        return false;
    }
    return true;
}

function getGiftRepeatCount(data) {
    const rc = Number(data.repeatCount);
    return Number.isFinite(rc) && rc > 0 ? rc : 1;
}

function emit(type, data) {
    eventHandlers.forEach(handler => handler(type, data));
}

async function startMonitoring(username) {
    if (tiktokConnection) {
        stopMonitoring();
    }

    currentUsername = username;
    chatBuffer = [];
    pinnedCommentUsers.clear();
    processedPinnedMessages.clear();
    repeatAlertedSequences.clear();

    tiktokConnection = new WebcastPushConnection(username);

    tiktokConnection.on('chat', async (data) => {
        const comment = String(data.comment || '').trim();
        if (!comment) return;

        emit('new-chat-message', data);
        
        const commentLower = comment.toLowerCase();
        const senderKey = normalizeId(data.uniqueId);
        const now = Date.now();
        const repeatWindowMs = 60000;
        const repeatsRequired = 3;

        const priorSameUserSameText = chatBuffer.filter(m =>
            normalizeId(m.uniqueId) === senderKey &&
            m.comment.trim().toLowerCase() === commentLower &&
            (now - m.timestamp) < repeatWindowMs
        );

        const isRepeat = Boolean(senderKey) && priorSameUserSameText.length >= repeatsRequired - 1;
        const seqKey = repeatSequenceKey(senderKey, commentLower);

        if (isRepeat) {
            if (!repeatAlertedSequences.has(seqKey)) {
                repeatAlertedSequences.add(seqKey);
                emit('flagged-message', {
                    uniqueId: data.uniqueId,
                    nickname: data.nickname,
                    comment,
                    reason: 'Mensagem repetida',
                    category: 'REPETICAO'
                });
            }
        } else {
            repeatAlertedSequences.delete(seqKey);
        }

        const msgData = {
            uniqueId: data.uniqueId,
            nickname: data.nickname,
            comment,
            timestamp: now,
            isFollower: data.isFollower
        };
        chatBuffer.push(msgData);
        if (chatBuffer.length > 500) chatBuffer.shift();

        if (settings.moderationEnabled && !isRepeat) {
            const result = await analyzeMessageModeration(comment, data.uniqueId, data.nickname, chatBuffer, currentUsername);
            if (result.flagged) {
                emit('flagged-message', { ...result, uniqueId: data.uniqueId, comment: data.comment, nickname: data.nickname });
            }
        }
    });

    tiktokConnection.on('gift', (data) => {
        const uniqueId = normalizeId(data.uniqueId);
        const isPinnedUser = pinnedCommentUsers.has(uniqueId);
        const targetGift = isTargetGift(data.giftName);

        // Emitir para todos os presentes
        emit('any-gift-received', { ...data, isRed: targetGift && isPinnedUser });

        // Emitir apenas para presentes alvos
        if (targetGift && isGiftCountingSettlement(data)) {
            emit('new-gift-user', { ...data, isRed: isPinnedUser });
        }
    });

    tiktokConnection.on('roomPin', (data) => {
        handlePinnedMessage(data);
    });

    tiktokConnection.on('decodedData', (type, data) => {
        if (type === 'WebcastRoomPinMessage' || data?.method === 'WebcastRoomPinMessage') {
            handlePinnedMessage(data);
        }
    });

    tiktokConnection.on('member', (data) => {
        emit('live-user-connected', data);
    });

    tiktokConnection.on('error', (err) => {
        emit('connection-status', { success: false, error: err.message });
    });

    try {
        await tiktokConnection.connect();
        emit('connection-status', { success: true, username });
    } catch (err) {
        emit('connection-status', { success: false, error: `Falha ao conectar: ${err.message}` });
        throw err;
    }
}

function stopMonitoring() {
    if (tiktokConnection) {
        tiktokConnection.disconnect();
        tiktokConnection = null;
        emit('connection-status', { success: false, error: 'Desconectado pelo usuário' });
    }
}

function getState() {
    return {
        connected: !!tiktokConnection,
        username: currentUsername,
        settings
    };
}

function onEvent(handler) {
    eventHandlers.push(handler);
}

function setSettings(newSettings) {
    settings = { ...settings, ...newSettings };
}

function getSettings() {
    return settings;
}

async function forceCheck() {
    // Implementação simplificada para o headless
    return { success: true };
}

module.exports = {
    startMonitoring,
    stopMonitoring,
    getState,
    onEvent,
    setSettings,
    getSettings,
    forceCheck
};
