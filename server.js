const http = require('http');
const fs = require('fs');
const path = require('path');
const { URL } = require('url');
const { WebcastPushConnection } = require('tiktok-live-connector');
const { GoogleGenerativeAI } = require('@google/generative-ai');

const HOST = process.env.HOST || '0.0.0.0';
const PORT = Number(process.env.PORT) || 3000;
const ROOT_DIR = __dirname;
const GEMINI_API_KEY = process.env.GEMINI_API_KEY;
const GEMINI_MODEL = process.env.GEMINI_MODEL || 'gemini-2.5-flash-lite';

function geminiApiKeyConfigured() {
    if (!GEMINI_API_KEY) {
        return false;
    }
    const cleaned = String(GEMINI_API_KEY).replace(/^["']|["']$/g, '').trim();
    return cleaned.length > 0;
}

function resolveGeminiApiKey() {
    if (!geminiApiKeyConfigured()) {
        return null;
    }
    return String(GEMINI_API_KEY).replace(/^["']|["']$/g, '').trim();
}

let genAI = null;
let model = null;
/** Após erro/quota, não martela a API; moderação local (regex) segue valendo */
let geminiModerationCooldownUntil = 0;
const GEMINI_MODERATION_COOLDOWN_MS = 120_000;

if (geminiApiKeyConfigured()) {
    const key = resolveGeminiApiKey();
    genAI = new GoogleGenerativeAI(key);
    model = genAI.getGenerativeModel({ model: GEMINI_MODEL });
    console.log(`[AI] Gemini configurado (${GEMINI_MODEL}).`);
} else {
    console.warn('[AI] GEMINI_API_KEY não encontrada. Análise de IA desativada.');
}

let tiktokConnection = null;
let currentUsername = null;
let chatBuffer = [];
let pinnedCommentUsers = new Set();
let processedPinnedMessages = new Set();
let repeatAlertedSequences = new Set();
const sseClients = new Set();

/** Evita chamadas repetidas à IA para o mesmo texto normalizado */
const christianModerationAiCache = new Map();
const CHRISTIAN_AI_CACHE_MAX = 150;

function christianModerationAiRecall(normalizedKey) {
    return christianModerationAiCache.get(normalizedKey);
}

function christianModerationAiRemember(normalizedKey, flaggedByAi) {
    if (christianModerationAiCache.has(normalizedKey)) {
        christianModerationAiCache.delete(normalizedKey);
    }
    christianModerationAiCache.set(normalizedKey, flaggedByAi);
    while (christianModerationAiCache.size > CHRISTIAN_AI_CACHE_MAX) {
        christianModerationAiCache.delete(christianModerationAiCache.keys().next().value);
    }
}

function repeatSequenceKey(senderKey, commentLower) {
    return JSON.stringify([senderKey, commentLower]);
}

function sendJson(response, statusCode, payload) {
    response.writeHead(statusCode, { 'Content-Type': 'application/json; charset=utf-8' });
    response.end(JSON.stringify(payload));
}

function readRequestBody(request) {
    return new Promise((resolve, reject) => {
        let data = '';

        request.on('data', chunk => {
            data += chunk;
            if (data.length > 1024 * 1024) {
                reject(new Error('Payload muito grande.'));
                request.destroy();
            }
        });

        request.on('end', () => {
            try {
                resolve(data ? JSON.parse(data) : {});
            } catch (error) {
                reject(new Error('JSON inválido.'));
            }
        });

        request.on('error', reject);
    });
}

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

/** Presentes em streak (giftType 1) geram vários eventos; só contamos quando não é o “meio” do streak. */
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

function getUserFromObject(data) {
    if (!data) return { uniqueId: null, nickname: null };

    const user = data.user || data.member || data.sender || data.author || data.owner || {};
    const uniqueId = data.uniqueId || user.uniqueId || user.secUid || user.id || null;
    const nickname = data.nickname || user.nickname || user.displayName || uniqueId || null;

    return { uniqueId, nickname };
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
        return { uniqueId: mentionMatch[1].toLowerCase(), nickname: mentionMatch[1] };
    }

    if (content) {
        const contentLower = content.toLowerCase();
        const sender = chatBuffer.find(message => {
            const commentLower = String(message.comment || '').toLowerCase();
            return contentLower.includes(commentLower) || commentLower.includes(contentLower);
        });

        if (sender) {
            return { uniqueId: sender.uniqueId, nickname: sender.nickname };
        }
    }

    return { uniqueId: null, nickname: null };
}

function getPinnedMessageKey(data) {
    return data.pinId ||
        data.msgId ||
        data.chatMessage?.common?.msgId ||
        data.chatMessage?.msgId ||
        `${data.pinTime || ''}:${data.chatMessage?.comment || ''}`;
}

function rememberPinnedUser(uniqueId) {
    const normalizedId = normalizeId(uniqueId);
    if (normalizedId) {
        pinnedCommentUsers.add(normalizedId);
    }
    return normalizedId;
}

function emitEvent(eventName, payload) {
    const message = `event: ${eventName}\ndata: ${JSON.stringify(payload)}\n\n`;

    for (const client of sseClients) {
        client.write(message);
    }
}

function emitStatus(success, extra = {}) {
    emitEvent('connection-status', {
        success,
        username: currentUsername,
        ...extra
    });
}

function logPinnedComment(content, user, data) {
    console.log('\n[PINNED COMMENT]');
    console.log(`Author: ${user.nickname || user.uniqueId || 'Nao identificado'}`);
    console.log(`Message: ${content || '[sem texto identificado]'}`);
    console.log(`Method: ${data.method || 'desconhecido'} | Action: ${data.action ?? 'desconhecida'} | Pin ID: ${data.pinId || 'n/a'}`);
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
    logPinnedComment(content, pinnedUser, data);

    emitEvent('pinned-comment', {
        uniqueId: pinnedUser.uniqueId,
        nickname: pinnedUser.nickname || pinnedUser.uniqueId || 'Nao identificado',
        comment: content || '[sem texto identificado]',
        pinId: data.pinId || data.msgId || null,
        timestamp: Date.now()
    });

    if (pinnedUser.uniqueId) {
        emitEvent('mark-user-red', rememberPinnedUser(pinnedUser.uniqueId));
    }
}

function resetLiveState() {
    chatBuffer = [];
    pinnedCommentUsers.clear();
    processedPinnedMessages.clear();
    repeatAlertedSequences.clear();
    christianModerationAiCache.clear();
}

function disconnectCurrentConnection(reason = 'Desconectado pelo usuário') {
    if (!tiktokConnection) {
        currentUsername = null;
        emitStatus(false, { error: reason });
        return;
    }

    tiktokConnection.removeAllListeners();
    tiktokConnection.disconnect();
    tiktokConnection = null;
    currentUsername = null;
    resetLiveState();
    emitStatus(false, { error: reason });
}

function foldChatText(text) {
    return String(text || '')
        .toLowerCase()
        .normalize('NFD')
        .replace(/\p{M}/gu, '')
        .replace(/ç/g, 'c');
}

/** Padrões explícitos de insulto a matriz africana — não substituem o julgamento da IA */
function looksObviousAttackOnAfroBrazilianReligion(commentLower) {
    const t = foldChatText(commentLower);
    const evil =
        /\b(diabo|demonio|demoniac[o]?|capeta|satanas|satanico|satan|inferno)\b/;
    const traditions = /\b(candomble|umbanda|macumba|orixa[s]?|feitico|feitisa[m]?)\b/;
    const patterns = [
        new RegExp(`${traditions.source}[\\s\\S]{0,140}${evil.source}`, 'i'),
        new RegExp(`${evil.source}[\\s\\S]{0,140}${traditions.source}`, 'i'),
        /\b(candomble|umbanda|macumba)\b[\s\S]{0,120}\bsatanismo\b/,
        /\bsatanismo\b[\s\S]{0,120}\b(candomble|umbanda|macumba|orixa)\b/,
        /\b(idolatr|heresia)\b[\s\S]{0,100}\b(candomble|umbanda|macumba|orixa)\b/
    ];
    return patterns.some(rx => rx.test(t));
}

/** Convite/proselitismo e marcas cristãs óbvias — resolve sem chamada à IA */
function looksExplicitChristianProselytizing(commentLower) {
    const t = foldChatText(commentLower);
    const holy = /\b(jesus|cristo|deus|espirito\s+santo)\b/;

    const blatant = [
        /\bjesus\s+te\s+ama\b/,
        /\bdeus\s+te\s+ama\b/,
        /\bjesus\s+cristo\b/,
        /\bem\s+nome\s+de\s+jesus\b/,
        /\bgloria\s+a\s+deus\b/,
        /\blouvado\s+seja\b/,
        /\baleluia\b/,
        /\bamem\b/,
        /\bpaz\s+do\s+senhor\b/,
        /\bdeus\s+te\s+abenc\b/,
        /\bespirito\s+santo\b/,
        /\b(evangelho|biblia)\b/,
        /\bversiculo\b/,
        /\bsalmo\b/,
        /\bnossa\s+senhora\b/,
        /\bpreciso\s+de\s+(jesus|deus)\b/
    ];
    if (blatant.some(rx => rx.test(t))) {
        return true;
    }

    if (holy.test(t) && /\bseja\s+entregue\s+a\s+ele\b/.test(t)) {
        return true;
    }
    if (/\baceita\s+(jesus|cristo|o\s+senhor)\b/.test(t) || /\baceite\s+(jesus|cristo)\b/.test(t)) {
        return true;
    }
    if (/\b(arrepenta|precisa\s+de\s+jesus|volta\s+para\s+jesus)\b/.test(t)) {
        return true;
    }
    if (/\bvenha\s+para\s+(jesus|cristo|deus)\b/.test(t)) {
        return true;
    }

    return false;
}

/** Pré-filtro estrito: só casos ambíguos ou com tensão (Jesus×matriz africana, doutrina...) */
function passesChristianModerationAiGate(commentLower) {
    const t = foldChatText(commentLower);
    const jc = /\b(jesus|cristo|jeova)\b/.test(t);
    const afroCtx =
        /\b(candombl|umbanda|macumba|orixa[s]?|feitico[s]?|terreiro|og[aã]|vodum)\b/.test(t);

    if (jc && afroCtx) {
        return true;
    }

    const tension =
        /\b(converter|salvacao|entregar|arrep|pecado|cruz|inferno|pregac|culto|pregador)\b/.test(t);
    if (jc && tension) {
        return true;
    }

    if (
        /\bdeus\b/.test(t) &&
        /\b(converter|salvacao|inferno|pecado|cruz|arrep)\b/.test(t)
    ) {
        return true;
    }

    if (/\bigreja\b/.test(t) || /\bpastor\b/.test(t)) {
        return true;
    }

    return false;
}

function emitOffensiveChristianContextFlag(uniqueId, nickname, comment) {
    emitEvent('flagged-message', {
        uniqueId,
        nickname,
        comment,
        reason: 'Ofensiva / proselitismo cristão'
    });
}

async function analyzeMessage(data) {
    const { uniqueId, nickname, comment } = data;
    const commentLower = comment.trim().toLowerCase();
    const senderKey = normalizeId(uniqueId);
    const repeatWindowMs = 60000;
    const repeatsRequired = 3;
    const now = Date.now();

    const priorSameUserSameText = chatBuffer.filter(msg =>
        normalizeId(msg.uniqueId) === senderKey &&
        msg.comment.trim().toLowerCase() === commentLower &&
        (now - msg.timestamp) < repeatWindowMs
    );

    const isRepeat =
        Boolean(senderKey) && priorSameUserSameText.length >= repeatsRequired - 1;

    const seqKey = repeatSequenceKey(senderKey, commentLower);

    if (isRepeat) {
        if (!repeatAlertedSequences.has(seqKey)) {
            repeatAlertedSequences.add(seqKey);
            emitEvent('flagged-message', {
                uniqueId,
                nickname,
                comment,
                reason: 'Mensagem repetida'
            });
        }
        return;
    }

    repeatAlertedSequences.delete(seqKey);
    if (senderKey) {
        for (const k of [...repeatAlertedSequences]) {
            try {
                const [uid, text] = JSON.parse(k);
                if (uid === senderKey && text !== commentLower) {
                    repeatAlertedSequences.delete(k);
                }
            } catch {
                repeatAlertedSequences.delete(k);
            }
        }
    }

    if (
        looksObviousAttackOnAfroBrazilianReligion(commentLower) ||
        looksExplicitChristianProselytizing(commentLower)
    ) {
        emitOffensiveChristianContextFlag(uniqueId, nickname, comment);
        return;
    }

    if (!passesChristianModerationAiGate(commentLower)) {
        return;
    }

    if (!model) {
        return;
    }

    if (Date.now() < geminiModerationCooldownUntil) {
        return;
    }

    const aiCacheKey = foldChatText(commentLower).slice(0, 500);
    const cachedAiVerdict = christianModerationAiRecall(aiCacheKey);
    if (cachedAiVerdict === true) {
        emitOffensiveChristianContextFlag(uniqueId, nickname, comment);
        return;
    }
    if (cachedAiVerdict === false) {
        return;
    }

    try {
        const prompt =
            'Live de CANDOMBLÉ (Orixás, axé). Responda só SIM ou NAO.\n\n' +
            'SIM = insultar/demonizar religiões de matriz africana ou Orixás; OU proselitismo cristão ao chat (conversão, entregar-se a Jesus/Deus, arrependimento).\n' +
            'NAO = neutro; Jesus sem convite; elogio à live.\n\n' +
            `Mensagem:\n${JSON.stringify(comment)}`;

        const preview =
            comment.length > 100 ? `${comment.slice(0, 100)}…` : comment;
        console.log(
            `[AI] Gemini (${GEMINI_MODEL}) chamada | ${nickname || uniqueId || 'anon'}: ${preview}`
        );

        const result = await model.generateContent({
            contents: [{ role: 'user', parts: [{ text: prompt }] }],
            generationConfig: {
                temperature: 0.1,
                maxOutputTokens: 6
            }
        });
        const responseText = result.response.text().trim().toUpperCase();
        const flaggedByAi = /\bSIM\b/.test(responseText);

        console.log(
            `[AI] Gemini resposta: "${responseText.trim()}" → ${flaggedByAi ? 'SIM (marcar)' : 'NAO'}`
        );

        christianModerationAiRemember(aiCacheKey, flaggedByAi);

        if (flaggedByAi) {
            emitOffensiveChristianContextFlag(uniqueId, nickname, comment);
        }

        geminiModerationCooldownUntil = 0;
    } catch (error) {
        geminiModerationCooldownUntil = Date.now() + GEMINI_MODERATION_COOLDOWN_MS;
        console.warn(
            '[AI] Moderação por IA pausada (~2 min):',
            error?.message || error,
            '| Regex e filtros locais continuam ativos.'
        );
    }
}

async function connectToTiktok(username) {
    if (tiktokConnection) {
        disconnectCurrentConnection('Conexão substituída');
    }

    resetLiveState();
    currentUsername = username;
    tiktokConnection = new WebcastPushConnection(username);

    tiktokConnection.on('chat', data => {
        const messageData = {
            uniqueId: data.uniqueId,
            nickname: data.nickname,
            comment: data.comment,
            timestamp: Date.now()
        };

        void analyzeMessage(messageData).catch(err => {
            console.error('[AI] Erro inesperado na análise:', err?.message || err);
        });

        chatBuffer.push(messageData);

        if (chatBuffer.length > 500) {
            chatBuffer.shift();
        }

        emitEvent('new-chat-message', { timestamp: Date.now() });
    });

    tiktokConnection.on('gift', data => {
        const uniqueId = normalizeId(data.uniqueId);
        const targetGift = isTargetGift(data.giftName);
        const isPinnedUser = pinnedCommentUsers.has(uniqueId);

        if (!isGiftCountingSettlement(data)) {
            return;
        }

        const giftType = getGiftTypeFromPayload(data);
        const repeatQty = getGiftRepeatCount(data);

        emitEvent('any-gift-received', {
            uniqueId: data.uniqueId,
            nickname: data.nickname,
            giftName: data.giftName,
            giftId: data.giftId,
            giftType,
            repeatCount: repeatQty,
            repeatEnd: data.repeatEnd,
            isTargetGift: targetGift,
            isRed: targetGift && isPinnedUser
        });

        if (targetGift) {
            emitEvent('new-gift-user', {
                uniqueId: data.uniqueId,
                nickname: data.nickname,
                giftName: data.giftName,
                isRed: isPinnedUser
            });
        }
    });

    tiktokConnection.on('roomPin', handlePinnedMessage);
    tiktokConnection.on('decodedData', (type, data) => {
        if (type === 'WebcastRoomPinMessage' || data?.method === 'WebcastRoomPinMessage') {
            handlePinnedMessage(data);
        }
    });

    try {
        await tiktokConnection.connect();
        emitStatus(true, { username });
    } catch (error) {
        tiktokConnection.removeAllListeners();
        tiktokConnection = null;
        currentUsername = null;
        resetLiveState();
        emitStatus(false, { error: error.message });
        throw error;
    }
}

function getContentType(filePath) {
    const extension = path.extname(filePath).toLowerCase();

    if (extension === '.html') return 'text/html; charset=utf-8';
    if (extension === '.js') return 'application/javascript; charset=utf-8';
    if (extension === '.css') return 'text/css; charset=utf-8';
    if (extension === '.json') return 'application/json; charset=utf-8';

    return 'application/octet-stream';
}

function serveFile(response, filePath) {
    fs.readFile(filePath, (error, content) => {
        if (error) {
            sendJson(response, 404, { error: 'Arquivo não encontrado.' });
            return;
        }

        response.writeHead(200, { 'Content-Type': getContentType(filePath) });
        response.end(content);
    });
}

async function handleApiRequest(request, response, pathname) {
    if (request.method === 'GET' && pathname === '/api/state') {
        sendJson(response, 200, {
            connected: Boolean(tiktokConnection && currentUsername),
            username: currentUsername,
            geminiConfigured: geminiApiKeyConfigured()
        });
        return true;
    }

    if (request.method === 'POST' && pathname === '/api/connect') {
        try {
            const body = await readRequestBody(request);
            const username = String(body.username || '').trim().replace(/^@/, '');

            if (!username) {
                sendJson(response, 400, { error: 'Informe um username válido.' });
                return true;
            }

            await connectToTiktok(username);
            sendJson(response, 200, { success: true, username });
        } catch (error) {
            sendJson(response, 500, { error: error.message });
        }
        return true;
    }

    if (request.method === 'POST' && pathname === '/api/disconnect') {
        disconnectCurrentConnection();
        sendJson(response, 200, { success: true });
        return true;
    }

    return false;
}

const server = http.createServer(async (request, response) => {
    const requestUrl = new URL(request.url, `http://${request.headers.host}`);
    const pathname = requestUrl.pathname;

    if (pathname === '/events') {
        response.writeHead(200, {
            'Content-Type': 'text/event-stream; charset=utf-8',
            'Cache-Control': 'no-cache, no-transform',
            Connection: 'keep-alive'
        });
        response.write('\n');
        sseClients.add(response);

        response.write(`event: server-state\ndata: ${JSON.stringify({
            connected: Boolean(tiktokConnection && currentUsername),
            username: currentUsername,
            geminiConfigured: geminiApiKeyConfigured()
        })}\n\n`);

        request.on('close', () => {
            sseClients.delete(response);
        });
        return;
    }

    if (pathname.startsWith('/api/')) {
        const handled = await handleApiRequest(request, response, pathname);
        if (!handled) {
            sendJson(response, 404, { error: 'Rota não encontrada.' });
        }
        return;
    }

    if (pathname === '/' || pathname === '/index.html') {
        serveFile(response, path.join(ROOT_DIR, 'index.html'));
        return;
    }

    if (pathname === '/renderer.js') {
        serveFile(response, path.join(ROOT_DIR, 'renderer.js'));
        return;
    }

    if (pathname === '/vendor/chart.js') {
        serveFile(response, path.join(ROOT_DIR, 'node_modules', 'chart.js', 'dist', 'chart.umd.js'));
        return;
    }

    sendJson(response, 404, { error: 'Rota não encontrada.' });
});

server.listen(PORT, HOST, () => {
    console.log(`TikTok Live Monitor disponível em http://localhost:${PORT}`);
});

function shutdownServer() {
    disconnectCurrentConnection('Servidor encerrado');

    for (const res of [...sseClients]) {
        try {
            res.end();
        } catch {
            /* ignore */
        }
    }
    sseClients.clear();

    server.close(() => process.exit(0));

    if (typeof server.closeAllConnections === 'function') {
        server.closeAllConnections();
    }

    setTimeout(() => process.exit(0), 2000);
}

process.on('SIGTERM', shutdownServer);
process.on('SIGINT', shutdownServer);
