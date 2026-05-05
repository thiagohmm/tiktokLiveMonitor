const { app, BrowserWindow, ipcMain, dialog } = require('electron');
const path = require('path');
const { spawn } = require('child_process');
const { WebcastPushConnection } = require('tiktok-live-connector');
const { aiConfigured, probeLlamaReady } = require('./ai');
const { analyzeMessage: analyzeMessageModeration } = require('./moderation');

let mainWindow;
let tiktokConnection;
let currentUsername;
let chatBuffer = []; // Ultimas 500 mensagens
let pinnedCommentUsers = new Set();
let processedPinnedMessages = new Set();
let repeatAlertedSequences = new Set();

function repeatSequenceKey(senderKey, commentLower) {
    return JSON.stringify([senderKey, commentLower]);
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

function aiConfiguredLocal() {
    return true;
}

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

ipcMain.handle('get-ui-config', () => ({
    geminiConfigured: aiConfigured()
}));

ipcMain.handle('probe-llm', async () => ({
    llmActive: await probeLlamaReady()
}));

ipcMain.on('open-bot-window', async (event) => {
    // REMOVIDO a pedido do usuário
});

function getTargetGiftLabel(giftName) {
    const normalizedGiftName = String(giftName || '').toLowerCase();
    const compactGiftName = normalizedGiftName.replace(/[^a-z0-9]/g, '');

    if (
        normalizedGiftName.includes('tiny dyny') ||
        normalizedGiftName.includes('tiny diny') ||
        compactGiftName.includes('tinydyny') ||
        compactGiftName.includes('tinydiny')
    ) {
        return 'Tiny Dyny';
    }

    if (normalizedGiftName.includes('perfume')) {
        return 'Perfume';
    }

    return 'Presente Alvo';
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

    // Sempre mostra o evento de fixado no console, mesmo se a lib mudar o formato.
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

    mainWindow.webContents.send('pinned-comment', {
        uniqueId: pinnedUser.uniqueId,
        nickname: pinnedUser.nickname || pinnedUser.uniqueId || 'Nao identificado',
        comment: content || '[sem texto identificado]',
        pinId: data.pinId || data.msgId || null,
        timestamp: Date.now(),
        isFollower: isFollower
    });

    if (pinnedUser.uniqueId) {
        const pinnedUniqueId = rememberPinnedUser(pinnedUser.uniqueId);
        mainWindow.webContents.send('mark-user-red', pinnedUniqueId);
    }

    // Comentario fixado nao e presente. Ele apenas marca o usuario como prioritario;
    // a entrada na fila de presentes deve acontecer somente no evento real de gift.
}

function rememberPinnedUser(uniqueId) {
    const normalizedId = normalizeId(uniqueId);
    if (normalizedId) {
        pinnedCommentUsers.add(normalizedId);
    }
    return normalizedId;
}

function reportFatalError(context, error) {
    const message = `${context}\n\n${error?.stack || error?.message || String(error)}`;
    console.error(message);

    if (app.isReady()) {
        dialog.showErrorBox('TikTok Live Monitor', message);
    }
}

process.on('uncaughtException', (error) => {
    reportFatalError('Erro não tratado no processo principal.', error);
});

process.on('unhandledRejection', (error) => {
    reportFatalError('Promise rejeitada sem tratamento.', error);
});

function createWindow() {
    mainWindow = new BrowserWindow({
        width: 1000,
        height: 800,
        webPreferences: {
            nodeIntegration: true,
            contextIsolation: false
        }
    });

    mainWindow.webContents.on('render-process-gone', (event, details) => {
        reportFatalError(`Renderer encerrado inesperadamente (${details.reason}).`, new Error(details.exitCode ? `Exit code: ${details.exitCode}` : 'Sem exit code'));
    });

    mainWindow.webContents.on('did-fail-load', (event, errorCode, errorDescription) => {
        reportFatalError(`Falha ao carregar a interface (${errorCode}).`, new Error(errorDescription));
    });

    mainWindow.loadFile(path.join(__dirname, 'index.html')).catch((error) => {
        reportFatalError('Falha ao abrir index.html.', error);
    });

    mainWindow.on('closed', () => {
        mainWindow = null;
    });
}

app.whenReady().then(createWindow);

app.on('window-all-closed', () => {
    if (process.platform !== 'darwin') app.quit();
});

ipcMain.on('connect-tiktok', (event, username) => {
    if (tiktokConnection) {
        tiktokConnection.removeAllListeners();
        tiktokConnection.disconnect();
    }

    currentUsername = username;
    chatBuffer = [];
    pinnedCommentUsers.clear();
    processedPinnedMessages.clear();
    repeatAlertedSequences.clear();
    tiktokConnection = createTikTokConnection(username);

    tiktokConnection.connect().then(state => {
        const roomId = state.roomId || tiktokConnection.roomId;
        console.log(`Conectado à live de ${username}. Room ID: ${roomId}`);
        event.reply('connection-status', { success: true, username, roomId: roomId });
    }).catch(err => {
        console.error('Falha ao conectar:', err);
        event.reply('connection-status', { success: false, error: formatTikTokConnectionError(err) });
    });

    // Monitorar chat para o buffer de 500 mensagens
    tiktokConnection.on('chat', (data) => {
        const comment = String(data.comment || '').trim();
        if (!comment) {
            return;
        }

        const user = getUserFromObject(data);
        const isFollower = user.isFollower;


        const msg = {
            uniqueId: data.uniqueId,
            nickname: data.nickname,
            comment,
            timestamp: Date.now(),
            isFollower: isFollower
        };

        const commentLower = comment.toLowerCase();
        const senderKey = normalizeId(data.uniqueId);
        const repeatWindowMs = 60000;
        const repeatsRequired = 3;
        const now = Date.now();

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
                // Removido a pedido do usuário: não alertar mensagem repetida como flag
            }
        } else {
            repeatAlertedSequences.delete(seqKey);

            // Realiza análise de moderação (Regex + IA Local) apenas se não for repetida
            // para evitar "falsos positivos" ou ruído excessivo.
            analyzeMessageModeration(comment, data.uniqueId, data.nickname, chatBuffer)
                .then(result => {
                    if (result.flagged) {
                        mainWindow.webContents.send('flagged-message', {
                            uniqueId: data.uniqueId,
                            nickname: data.nickname,
                            comment,
                            reason: result.reason,
                            category: result.category || null
                        });
                    }
                })
                .catch(err => {
                    console.error('[AI] Erro na moderação:', err.message);
                });
        }

        chatBuffer.push(msg);
        if (chatBuffer.length > 500) {
            chatBuffer.shift();
        }
        // Notificar renderer para o gráfico
        mainWindow.webContents.send('new-chat-message', msg);
    });

    tiktokConnection.on('member', (data) => {
        mainWindow.webContents.send('live-user-connected', {
            uniqueId: data.uniqueId,
            nickname: data.nickname,
            timestamp: Date.now()
        });
    });

    // Monitorar presentes (Gifts)
    tiktokConnection.on('gift', (data) => {
        const uniqueId = normalizeId(data.uniqueId);
        const targetGift = isTargetGift(data.giftName);
        const isPinnedUser = pinnedCommentUsers.has(uniqueId);

        if (!isGiftCountingSettlement(data)) {
            return;
        }

        console.log('\n🎁 PRESENTE RECEBIDO:');
        console.log(`👤 Usuário: ${data.uniqueId}`);
        console.log(`🎁 Presente: ${data.giftName} (ID: ${data.giftId})`);
        console.log(`🎯 Alvo: ${targetGift ? 'Sim' : 'Não'}`);
        console.log(`⭐ Prioritário: ${isPinnedUser ? 'Sim' : 'Não'}`);
        console.log('----------------------------\n');

        const giftType = getGiftTypeFromPayload(data);
        const repeatQty = getGiftRepeatCount(data);

        mainWindow.webContents.send('any-gift-received', {
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
            mainWindow.webContents.send('new-gift-user', {
                uniqueId: data.uniqueId,
                nickname: data.nickname,
                giftName: data.giftName,
                isRed: isPinnedUser
            });
        }
    });

    // Monitorar mensagens fixadas
    tiktokConnection.on('roomPin', (data) => {
        handlePinnedMessage(data);
    });

    // Nesta versão da lib, algumas lives recebem o fixado em decodedData,
    // mas não emitem o alias roomPin.
    tiktokConnection.on('decodedData', (type, data) => {
        if (type === 'WebcastRoomPinMessage' || data?.method === 'WebcastRoomPinMessage') {
            handlePinnedMessage(data);
        }
    });

    tiktokConnection.on('roomUser', (data) => {
        // Algumas versões da lib mandam info de fixado aqui
        if (data && data.displayType === 'pm_mt_guidance_share') {
             // Pode ser um aviso de fixação
        }
    });
});

ipcMain.on('disconnect-tiktok', (event) => {
    if (tiktokConnection) {
        tiktokConnection.removeAllListeners();
        tiktokConnection.disconnect();
        tiktokConnection = null;
    }
    
    currentUsername = null;

    chatBuffer = [];
    pinnedCommentUsers.clear();
    processedPinnedMessages.clear();
    repeatAlertedSequences.clear();
    event.reply('connection-status', { success: false, error: 'Desconectado pelo usuário' });
});
