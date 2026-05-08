const { WebcastPushConnection } = require('tiktok-live-connector');
const { correlateGiftQuestionWithLlm } = require('./ai');

// Variáveis de estado
let tiktokConnection;
let currentUsername;
let chatBuffer = []; // Ultimas 500 mensagens
let questionBuffer = []; // Perguntas recentes para correlacionar com presentes alvo
let pinnedCommentUsers = new Set();
let processedPinnedMessages = new Set();
let repeatAlertedSequences = new Set();
let eventHandlers = [];

const QUESTION_BUFFER_MAX = 300;
const QUESTION_CORRELATION_WINDOW_MS = 3 * 60 * 1000;
const CORRELATION_FORWARD_LOOKAHEAD_COUNT = 2;
const CORRELATION_FORWARD_REVIEW_DELAY_MS = 4000;

function repeatSequenceKey(senderKey, commentLower) {
    return JSON.stringify([senderKey, commentLower]);
}

function getUserFromObject(data) {
    if (!data) return { uniqueId: null, nickname: null, isFollower: null };

    const user = data.user || data.member || data.sender || data.author || data.owner || {};
    const uniqueId = data.uniqueId || user.uniqueId || user.secUid || user.id || null;
    const nickname = data.nickname || user.nickname || user.displayName || uniqueId || null;

    let isFollower = null;
    
    // Tenta extrair status de seguidor de múltiplas fontes possíveis na estrutura do TikTok
    const followInfo = user.followInfo || data.followInfo || {};
    const followStatus = followInfo.followStatus !== undefined ? followInfo.followStatus : 
                         (followInfo.followerStatus !== undefined ? followInfo.followerStatus : 
                         (data.followStatus !== undefined ? data.followStatus : 
                         (user.followStatus !== undefined ? user.followStatus : 
                         (data.followerStatus !== undefined ? data.followerStatus : 
                         (user.followerStatus !== undefined ? user.followerStatus : undefined)))));
    
    const followRole = data.followRole !== undefined ? data.followRole : (user.followRole !== undefined ? user.followRole : null);
    const badges = data.userBadges || user.userBadges || [];
    const userIdentity = data.userIdentity || user.userIdentity || {};

    // Prioridade para userIdentity se disponível (mais confiável em versões recentes)
    if (userIdentity.isFollowerOfAnchor !== undefined) {
        isFollower = Boolean(userIdentity.isFollowerOfAnchor);
    } else if (userIdentity.isMutualFollowingWithAnchor !== undefined) {
        isFollower = Boolean(userIdentity.isMutualFollowingWithAnchor);
    } 
    
    // Se ainda não determinado, tenta via followStatus (pode ser número ou string)
    if (isFollower === null && followStatus !== undefined && followStatus !== null) {
        const fs = Number(followStatus);
        if (!isNaN(fs)) {
            isFollower = fs >= 1;
        }
    }

    // Outras verificações secundárias
    if (isFollower === null) {
        if (typeof followRole === 'number' && followRole > 0) {
            isFollower = true;
        } else if (Array.isArray(badges) && badges.some(b => b.type === 'follower' || b.name === 'Follower' || b.badgeId === 'follower')) {
            isFollower = true;
        } else if (data.isFollower !== undefined) {
            isFollower = Boolean(data.isFollower);
        } else if (user.isFollower !== undefined) {
            isFollower = Boolean(user.isFollower);
        }
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

function foldChatText(text) {
    return String(text || '')
        .toLowerCase()
        .normalize('NFD')
        .replace(/\p{M}/gu, '')
        .replace(/ç/g, 'c');
}

function looksLikeQuestion(comment) {
    const raw = String(comment || '').trim();
    if (!raw) return false;
    if (/[?¿]/.test(raw)) return true;

    const t = foldChatText(raw);
    if (/^(pq|pk|por\s+que|porque|como|quando|onde|aonde|quem|qual|quais|sera\s+que|duvida\b|duvida[:\-])\b/.test(t)) {
        return true;
    }
    return /\b(tem\s+como|da\s+pra|d[aá]\s+pra|alguem\s+sabe|algm\s+sabe|me\s+tira\s+uma\s+duvida|qual\s+o|qual\s+a)\b/.test(t);
}

function pruneQuestionBuffer(now = Date.now()) {
    questionBuffer = questionBuffer.filter((q) => (now - q.timestamp) <= QUESTION_CORRELATION_WINDOW_MS);
    if (questionBuffer.length > QUESTION_BUFFER_MAX) {
        questionBuffer = questionBuffer.slice(-QUESTION_BUFFER_MAX);
    }
}

function trackQuestionMessage(msgData) {
    if (!msgData || !looksLikeQuestion(msgData.comment)) return;
    questionBuffer.push({
        uniqueId: normalizeId(msgData.uniqueId),
        nickname: msgData.nickname,
        comment: msgData.comment,
        timestamp: msgData.timestamp,
        isFollower: msgData.isFollower
    });
    pruneQuestionBuffer(msgData.timestamp);
}

function getRecentChatCandidates(now = Date.now()) {
    return [...chatBuffer]
        .filter((m) => (now - Number(m.timestamp || 0)) <= QUESTION_CORRELATION_WINDOW_MS)
        .slice(-40)
        .map((m) => ({
            uniqueId: normalizeId(m.uniqueId),
            nickname: m.nickname,
            comment: m.comment,
            timestamp: m.timestamp,
            isFollower: m.isFollower
        }));
}

function chooseQuestionHeuristic(giftPayload) {
    const now = Date.now();
    const giftUid = normalizeId(giftPayload?.uniqueId);
    const giftNickFold = foldChatText(giftPayload?.nickname || '');
    const questionCandidates = [...questionBuffer].reverse().filter((q) => (now - q.timestamp) <= QUESTION_CORRELATION_WINDOW_MS);
    const recentChatCandidates = getRecentChatCandidates(now).reverse();
    if (!questionCandidates.length && !recentChatCandidates.length) return null;

    // Melhor caso: mesmo usuário enviou a pergunta e depois enviou o presente.
    if (giftUid) {
        const directQuestion = questionCandidates.find((q) => q.uniqueId && q.uniqueId === giftUid);
        if (directQuestion) return { match: directQuestion, method: 'same-user-question', confidence: 'high' };

        const directRecentMessage = recentChatCandidates.find((m) => m.uniqueId && m.uniqueId === giftUid);
        if (directRecentMessage) return { match: directRecentMessage, method: 'same-user-recent-message', confidence: 'high' };
    }

    if (giftNickFold) {
        const directNick = recentChatCandidates.find((m) => foldChatText(m.nickname || '').includes(giftNickFold));
        if (directNick) return { match: directNick, method: 'same-nickname-recent-message', confidence: 'medium' };
    }

    // Fallback: pergunta menciona o nickname de quem enviou o presente.
    if (giftNickFold) {
        const byMention = questionCandidates.find((q) => foldChatText(q.comment).includes(giftNickFold));
        if (byMention) return { match: byMention, method: 'nickname-mention', confidence: 'medium' };
    }

    return null;
}

function correlationLog(event, payload = {}) {
    const shortQuestion = String(payload.question || '')
        .replace(/\s+/g, ' ')
        .trim()
        .slice(0, 120);

    console.log(
        `[Correlation] ${event} | gift=${payload.giftName || '-'} | giftUser=${payload.giftNickname || payload.giftUserId || '-'} | method=${payload.method || '-'} | confidence=${payload.confidence || '-'} | questionUser=${payload.questionNickname || payload.questionUserId || '-'} | question="${shortQuestion}"`
    );
}

function correlationIdFor(giftPayload, now = Date.now()) {
    const giftUser = normalizeId(giftPayload?.uniqueId) || foldChatText(giftPayload?.nickname || 'anon');
    const nonce = Math.random().toString(36).slice(2, 8);
    return `corr-${giftUser}-${now}-${nonce}`;
}

function sameMessageIdentity(a, b) {
    if (!a || !b) return false;
    const aUid = normalizeId(a.uniqueId);
    const bUid = normalizeId(b.uniqueId);
    if (aUid && bUid) return aUid === bUid;

    const aNick = foldChatText(a.nickname || '');
    const bNick = foldChatText(b.nickname || '');
    return Boolean(aNick && bNick && aNick === bNick);
}

function scoreCorrelationCandidate(candidate) {
    const text = String(candidate?.comment || '').trim();
    if (!text) return 0;

    let score = 0;
    if (looksLikeQuestion(text)) score += 3;
    if (/[?¿]/.test(text)) score += 1;
    if (/\b(pq|pk|por\s+que|porque|como|quando|onde|aonde|quem|qual|duvida|tem\s+como|da\s+pra|d[aá]\s+pra)\b/i.test(text)) {
        score += 1;
    }

    const len = text.length;
    if (len >= 8 && len <= 220) score += 0.5;

    return score;
}

function getForwardMessages(baseMatch, limit = CORRELATION_FORWARD_LOOKAHEAD_COUNT) {
    const baseTs = Number(baseMatch?.timestamp || 0);
    if (!baseTs) return [];

    const forward = [...chatBuffer]
        .filter((m) => Number(m.timestamp || 0) > baseTs)
        .sort((a, b) => Number(a.timestamp || 0) - Number(b.timestamp || 0));

    if (!forward.length) return [];

    const sameAuthor = forward.filter((m) => sameMessageIdentity(baseMatch, m));
    const source = sameAuthor.length ? sameAuthor : forward;

    return source.slice(0, Math.max(1, limit)).map((m) => ({
        uniqueId: normalizeId(m.uniqueId),
        nickname: m.nickname,
        comment: m.comment,
        timestamp: m.timestamp,
        isFollower: m.isFollower
    }));
}

function emitCorrelationEvent({ correlationId, giftPayload, pick, method, confidence, replacement = false }) {
    emit('gift-question-correlation', {
        correlationId,
        giftName: giftPayload.giftName,
        giftUserId: giftPayload.uniqueId,
        giftNickname: giftPayload.nickname,
        questionUserId: pick.uniqueId,
        questionNickname: pick.nickname,
        question: pick.comment,
        method,
        confidence,
        replacement,
        timestamp: Date.now()
    });
}

function scheduleForwardCorrelationReview({ correlationId, giftPayload, baseMatch, baseMethod, baseConfidence }) {
    setTimeout(() => {
        const forwardMessages = getForwardMessages(baseMatch, CORRELATION_FORWARD_LOOKAHEAD_COUNT);
        if (!forwardMessages.length) {
            correlationLog('FORWARD_NO_MESSAGES', {
                giftName: giftPayload.giftName,
                giftUserId: giftPayload.uniqueId,
                giftNickname: giftPayload.nickname,
                method: `${baseMethod}+forward-${CORRELATION_FORWARD_LOOKAHEAD_COUNT}`,
                confidence: baseConfidence
            });
            return;
        }

        const baseScore = scoreCorrelationCandidate(baseMatch);
        let bestPick = baseMatch;
        let bestScore = baseScore;

        for (const msg of forwardMessages) {
            const score = scoreCorrelationCandidate(msg);
            if (score > bestScore) {
                bestPick = msg;
                bestScore = score;
            }
        }

        const changed = String(bestPick.comment || '') !== String(baseMatch.comment || '') ||
            normalizeId(bestPick.uniqueId) !== normalizeId(baseMatch.uniqueId);

        if (!changed || bestScore < (baseScore + 0.5)) {
            correlationLog('FORWARD_KEEP_ORIGINAL', {
                giftName: giftPayload.giftName,
                giftUserId: giftPayload.uniqueId,
                giftNickname: giftPayload.nickname,
                questionUserId: baseMatch.uniqueId,
                questionNickname: baseMatch.nickname,
                question: baseMatch.comment,
                method: baseMethod,
                confidence: baseConfidence
            });
            return;
        }

        correlationLog('FORWARD_REPLACED', {
            giftName: giftPayload.giftName,
            giftUserId: giftPayload.uniqueId,
            giftNickname: giftPayload.nickname,
            questionUserId: bestPick.uniqueId,
            questionNickname: bestPick.nickname,
            question: bestPick.comment,
            method: `${baseMethod}+forward-${CORRELATION_FORWARD_LOOKAHEAD_COUNT}`,
            confidence: baseConfidence
        });

        emitCorrelationEvent({
            correlationId,
            giftPayload,
            pick: bestPick,
            method: `${baseMethod}+forward-${CORRELATION_FORWARD_LOOKAHEAD_COUNT}`,
            confidence: baseConfidence,
            replacement: true
        });
    }, CORRELATION_FORWARD_REVIEW_DELAY_MS);
}

async function correlateGiftWithQuestion(giftPayload) {
    const now = Date.now();
    const correlationId = correlationIdFor(giftPayload, now);
    pruneQuestionBuffer(now);
    const recentChatCandidates = getRecentChatCandidates(now);
    if (!questionBuffer.length && !recentChatCandidates.length) {
        correlationLog('NO_CANDIDATES', {
            giftName: giftPayload.giftName,
            giftUserId: giftPayload.uniqueId,
            giftNickname: giftPayload.nickname,
            method: 'none',
            confidence: 'none'
        });
        return;
    }

    const heuristic = chooseQuestionHeuristic(giftPayload);
    if (heuristic?.match) {
        correlationLog('HEURISTIC_MATCH', {
            giftName: giftPayload.giftName,
            giftUserId: giftPayload.uniqueId,
            giftNickname: giftPayload.nickname,
            questionUserId: heuristic.match.uniqueId,
            questionNickname: heuristic.match.nickname,
            question: heuristic.match.comment,
            method: heuristic.method,
            confidence: heuristic.confidence
        });

        emitCorrelationEvent({
            correlationId,
            giftPayload,
            pick: heuristic.match,
            method: heuristic.method,
            confidence: heuristic.confidence
        });
        scheduleForwardCorrelationReview({
            correlationId,
            giftPayload,
            baseMatch: heuristic.match,
            baseMethod: heuristic.method,
            baseConfidence: heuristic.confidence
        });
        return;
    }

    // Fallback via LLM quando heurística não encontrou vínculo claro.
    const llmCandidates = [...questionBuffer]
        .filter((q) => (now - q.timestamp) <= QUESTION_CORRELATION_WINDOW_MS)
        .concat(recentChatCandidates)
        .filter((entry, index, arr) => arr.findIndex((x) =>
            x.uniqueId === entry.uniqueId &&
            String(x.comment || '') === String(entry.comment || '') &&
            Number(x.timestamp || 0) === Number(entry.timestamp || 0)
        ) === index)
        .slice(-8)
        .map((q) => ({
            ...q,
            ageMs: now - q.timestamp
        }));

    const llmPick = await correlateGiftQuestionWithLlm({
        giftName: giftPayload.giftName,
        giftUser: { uniqueId: giftPayload.uniqueId, nickname: giftPayload.nickname },
        candidates: llmCandidates
    });

    if (!llmPick) {
        correlationLog('LLM_NO_MATCH', {
            giftName: giftPayload.giftName,
            giftUserId: giftPayload.uniqueId,
            giftNickname: giftPayload.nickname,
            method: 'llm-fallback',
            confidence: 'none'
        });
        return;
    }

    correlationLog('LLM_MATCH', {
        giftName: giftPayload.giftName,
        giftUserId: giftPayload.uniqueId,
        giftNickname: giftPayload.nickname,
        questionUserId: llmPick.uniqueId,
        questionNickname: llmPick.nickname,
        question: llmPick.comment,
        method: 'llm-fallback',
        confidence: 'medium'
    });

    emitCorrelationEvent({
        correlationId,
        giftPayload,
        pick: llmPick,
        method: 'llm-fallback',
        confidence: 'medium'
    });
    scheduleForwardCorrelationReview({
        correlationId,
        giftPayload,
        baseMatch: llmPick,
        baseMethod: 'llm-fallback',
        baseConfidence: 'medium'
    });
}

function isTargetGift(giftName) {
    const normalizedGiftName = String(giftName || '').toLowerCase();
    const compactGiftName = normalizedGiftName.replace(/[^a-z0-9]/g, '');

    return normalizedGiftName.includes('perfume') ||
        normalizedGiftName.includes('dino') ||
        normalizedGiftName.includes('tiny dyny') ||
        normalizedGiftName.includes('tiny diny') ||
        compactGiftName.includes('dino') ||
        compactGiftName.includes('tinydyny') ||
        compactGiftName.includes('tinydiny');
}

function detectKeywordMention(comment) {
    const normalized = String(comment || '').toLowerCase();
    if (normalized.includes('dino')) return 'dino';
    if (normalized.includes('perfume')) return 'perfume';
    return null;
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
    questionBuffer = [];
    pinnedCommentUsers.clear();
    processedPinnedMessages.clear();
    repeatAlertedSequences.clear();

    tiktokConnection = new WebcastPushConnection(username);

    tiktokConnection.on('chat', async (data) => {
        const comment = String(data.comment || '').trim();
        if (!comment) return;

        const user = getUserFromObject(data);
        emit('new-chat-message', { ...data, ...user });
        
        const commentLower = comment.toLowerCase();
        const senderKey = normalizeId(user.uniqueId);
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
                    uniqueId: user.uniqueId,
                    nickname: user.nickname,
                    isFollower: user.isFollower,
                    comment,
                    reason: 'Mensagem repetida',
                    category: 'REPETICAO'
                });
            }
        } else {
            repeatAlertedSequences.delete(seqKey);
        }

        const msgData = {
            uniqueId: user.uniqueId,
            nickname: user.nickname,
            comment,
            timestamp: now,
            isFollower: user.isFollower
        };
        chatBuffer.push(msgData);
        if (chatBuffer.length > 500) chatBuffer.shift();
        trackQuestionMessage(msgData);

        const keyword = detectKeywordMention(comment);
        if (keyword) {
            if (senderKey) {
                pinnedCommentUsers.add(senderKey);
                emit('mark-user-red', senderKey);
            }

            emit('keyword-mention', {
                uniqueId: user.uniqueId,
                nickname: user.nickname,
                comment,
                keyword,
                timestamp: now,
                isFollower: user.isFollower
            });
        }
    });

    tiktokConnection.on('gift', (data) => {
        const user = getUserFromObject(data);
        const uniqueId = normalizeId(user.uniqueId);
        const isPinnedUser = pinnedCommentUsers.has(uniqueId);
        const targetGift = isTargetGift(data.giftName);

        const payload = { ...data, ...user, isRed: targetGift && isPinnedUser };

        // Emitir para todos os presentes
        emit('any-gift-received', payload);

        // Emitir apenas para presentes alvos
        if (targetGift && isGiftCountingSettlement(data)) {
            emit('new-gift-user', payload);
            void correlateGiftWithQuestion(payload).catch((err) => {
                console.warn('[Correlation] Falha ao correlacionar presente com pergunta:', err.message);
            });
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
        const user = getUserFromObject(data);
        emit('live-user-connected', user);
    });

    tiktokConnection.on('social', (data) => {
        if (data.displayType === 'pm_mt_guidance_share') return; // Ignorar share se necessário
        const user = getUserFromObject(data);
        emit('new-social-event', { ...data, ...user });
    });

    tiktokConnection.on('follow', (data) => {
        const user = getUserFromObject(data);
        // Quando alguém segue, forçamos isFollower para true se não estiver identificado
        if (user.isFollower === null) user.isFollower = true;
        emit('new-follower', user);
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
