const http = require('http');
const fs = require('fs');
const path = require('path');
const { spawn } = require('child_process');
const { URL } = require('url');
const { WebcastPushConnection } = require('tiktok-live-connector');
const { analyzeMessage: analyzeMessageModeration, clearModerationCache } = require('./moderation');
const { probeLlamaReady } = require('./ai');
const { addFalsePositive } = require('./database');

const HOST = process.env.HOST || '0.0.0.0';
const PORT = Number(process.env.PORT) || 3000;
const ROOT_DIR = __dirname;

function aiConfigured() {
    return true;
}

console.log(`[AI] LLM local (Gemma 4 E2B IT GGUF) configurado.`);

let tiktokConnection = null;
let currentUsername = null;
let chatBuffer = [];
let pinnedCommentUsers = new Set();
let processedPinnedMessages = new Set();
let repeatAlertedSequences = new Set();
const sseClients = new Set();

function createTikTokConnection(username) {
    return new WebcastPushConnection(username, {
        logFetchFallbackErrors: true
    });
}

function formatTikTokConnectionError(error) {
    const message = String(error?.message || error || 'Falha desconhecida ao conectar.');

    if (message.includes("isn't online")) {
        return 'Esse usuário não está ao vivo agora. Abra uma live ativa e tente novamente.';
    }

    if (message.includes('ENOTFOUND') || message.includes('EAI_AGAIN')) {
        return 'Falha de DNS/rede ao acessar TikTok/Euler. Verifique internet, VPN, proxy ou bloqueio de DNS.';
    }

    if (message.includes('SIGN SERVER') || message.includes('sign server') || message.includes('Euler')) {
        return `Falha no serviço de assinatura usado pela conexão do TikTok: ${message}`;
    }

    return message;
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
        const sender = chatBuffer.find(message => {
            const commentLower = String(message.comment || '').toLowerCase();
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
    console.log('\n📌 COMENTÁRIO FIXADO:');
    if (user.uniqueId) {
        const followStr = user.isFollower === true ? ' (Seguidor)' : (user.isFollower === false ? ' (Não Segue)' : '');
        console.log(`👤 Autor: ${user.nickname || user.uniqueId}${followStr} (@${user.uniqueId})`);
    } else {
        console.log('👤 Autor: Não identificado');
    }
    console.log(`💬 Mensagem: ${content || '[sem texto identificado]'}`);
    console.log(`📎 Método: ${data.method || 'desconhecido'} | Ação: ${data.action ?? 'desconhecida'} | Pin ID: ${data.pinId || 'n/a'}`);
    console.log('----------------------------\n');
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

    let isFollower = pinnedUser.isFollower;

    // Se ainda nulo, tenta buscar no buffer pelo uniqueId do autor identificado
    if (isFollower === null && pinnedUser.uniqueId) {
        const targetId = normalizeId(pinnedUser.uniqueId);
        const lastKnown = chatBuffer.slice().reverse().find(m => normalizeId(m.uniqueId) === targetId);
        if (lastKnown && lastKnown.isFollower !== undefined) {
            isFollower = lastKnown.isFollower;
        }
    }

    emitEvent('pinned-comment', {
        uniqueId: pinnedUser.uniqueId,
        nickname: pinnedUser.nickname || pinnedUser.uniqueId || 'Nao identificado',
        comment: content || '[sem texto identificado]',
        pinId: data.pinId || data.msgId || null,
        timestamp: Date.now(),
        isFollower: isFollower
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
    clearModerationCache();
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

    const isRepeat = Boolean(senderKey) && priorSameUserSameText.length >= repeatsRequired - 1;
    const seqKey = repeatSequenceKey(senderKey, commentLower);

    if (isRepeat) {
        if (!repeatAlertedSequences.has(seqKey)) {
            repeatAlertedSequences.add(seqKey);
            // Removido a pedido do usuário: não alertar mensagem repetida como flag
        }
        return;
    }

    repeatAlertedSequences.delete(seqKey);

    // Usa o novo módulo de moderação consolidado
    const result = await analyzeMessageModeration(comment, uniqueId, nickname, chatBuffer);
    if (result.flagged) {
        emitEvent('flagged-message', {
            uniqueId,
            nickname,
            comment,
            reason: result.reason,
            category: result.category || null
        });
    }
}

async function connectToTiktok(username) {
    if (tiktokConnection) {
        disconnectCurrentConnection('Conexão substituída');
    }

    resetLiveState();
    currentUsername = username;
    tiktokConnection = createTikTokConnection(username);

    tiktokConnection.on('chat', data => {
        const user = getUserFromObject(data);
        const isFollower = user.isFollower;


        const messageData = {
            uniqueId: data.uniqueId,
            nickname: data.nickname,
            comment: data.comment,
            timestamp: Date.now(),
            isFollower: isFollower
        };

        void analyzeMessage(messageData).catch(err => {
            console.error('[AI] Erro inesperado na análise:', err?.message || err);
        });

        chatBuffer.push(messageData);

        if (chatBuffer.length > 500) {
            chatBuffer.shift();
        }

        emitEvent('new-chat-message', messageData);
    });

    tiktokConnection.on('member', data => {
        emitEvent('live-user-connected', {
            uniqueId: data.uniqueId,
            nickname: data.nickname,
            timestamp: Date.now()
        });
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
        emitStatus(false, { error: formatTikTokConnectionError(error) });
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
    if (request.method === 'GET' && pathname === '/api/probe-llm') {
        const llmActive = await probeLlamaReady();
        sendJson(response, 200, { llmActive });
        return true;
    }

    if (request.method === 'GET' && pathname === '/api/state') {
        sendJson(response, 200, {
            connected: Boolean(tiktokConnection && currentUsername),
            username: currentUsername,
            aiConfigured: aiConfigured()
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
            sendJson(response, 500, { error: formatTikTokConnectionError(error) });
        }
        return true;
    }

    if (request.method === 'POST' && pathname === '/api/disconnect') {
        disconnectCurrentConnection();
        sendJson(response, 200, { success: true });
        return true;
    }

    if (request.method === 'POST' && pathname === '/api/feedback') {
        try {
            const body = await readRequestBody(request);
            const { comment, category } = body;
            if (!comment || !category) {
                sendJson(response, 400, { error: 'Comment e category são obrigatórios.' });
                return true;
            }
            await addFalsePositive(comment, category);
            sendJson(response, 200, { success: true });
        } catch (error) {
            sendJson(response, 500, { error: error.message });
        }
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
            aiConfigured: aiConfigured()
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
    const base = `http://${HOST === '0.0.0.0' ? 'localhost' : HOST}:${PORT}`;
    console.log(`TikTok Live Monitor (modo navegador): ${base}`);
    if (HOST === '0.0.0.0') {
        console.log(`  Na LAN/Raspberry Pi: abra no celular ou PC http://<IP-desta-maquina>:${PORT}`);
    }
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
