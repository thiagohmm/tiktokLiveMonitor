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
    tiktokConnection = new WebcastPushConnection(username);

    tiktokConnection.on('chat', async (data) => {
        emit('chat', data);
        if (settings.moderationEnabled) {
            const result = await analyzeMessageModeration(data.comment, data.uniqueId);
            if (result.flagged) {
                emit('moderation-alert', { ...result, uniqueId: data.uniqueId, comment: data.comment });
            }
        }
    });

    tiktokConnection.on('gift', (data) => {
        if (isTargetGift(data.giftName) && isGiftCountingSettlement(data)) {
            emit('gift', data);
        }
    });

    tiktokConnection.on('error', (err) => {
        emit('error', err.message);
    });

    try {
        await tiktokConnection.connect();
        emit('connected', { username });
    } catch (err) {
        emit('error', `Falha ao conectar: ${err.message}`);
        throw err;
    }
}

function stopMonitoring() {
    if (tiktokConnection) {
        tiktokConnection.disconnect();
        tiktokConnection = null;
        emit('disconnected', { username: currentUsername });
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
