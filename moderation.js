const { completeModeration } = require('./ai');
const { BINARY_RELIGIOUS_MODERATION_SYSTEM, getModerationSystemPrompt } = require('./moderation-prompt');

/** Cache para evitar chamadas repetidas à IA */
const aiCache = new Map();
const AI_CACHE_MAX = 150;

let aiModerationCooldownUntil = 0;
const AI_MODERATION_COOLDOWN_MS = 30_000;

function foldChatText(text) {
    return String(text || '')
        .toLowerCase()
        .normalize('NFD')
        .replace(/\p{M}/gu, '')
        .replace(/ç/g, 'c');
}

function looksObviousAttackOnAfroBrazilianReligion(commentLower) {
    const t = foldChatText(commentLower);
    const evil = /\b(diabo|demonio|demoniac[o]?|capeta|satanas|satanico|satan|inferno)\b/;
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

function looksExplicitChristianProselytizing(commentLower) {
    const t = foldChatText(commentLower);
    const holy = /\b(jesus|cristo|deus|espirito\s+santo)\b/;

    const blatant = [
        /\bjesus\s+te\s+ama\b/, /\bdeus\s+te\s+ama\b/, /\bjesus\s+cristo\b/,
        /\bem\s+nome\s+de\s+jesus\b/, /\bgloria\s+a\s+deus\b/, /\blouvado\s+seja\b/,
        /\baleluia\b/, /\bamem\b/, /\bpaz\s+do\s+senhor\b/, /\bdeus\s+te\s+abenc\b/,
        /\bespirito\s+santo\b/, /\b(evangelho|biblia)\b/, /\bversiculo\b/,
        /\bsalmo\b/, /\bnossa\s+senhora\b/, /\bpreciso\s+de\s+(jesus|deus)\b/,
        // Slogans evangélicos (fold já tirou acento de «só» → so)
        /\b(jesus|cristo|deus)\s+salva\b/,
        /\bso\s+(jesus|cristo|deus)(\s+salva)?\b/,
        /\bunico\s+(salvador|senhor)\b.*\b(jesus|cristo)\b/,
        /\b(jesus|cristo)\b.*\bunico\s+caminho\b/
    ];
    if (blatant.some(rx => rx.test(t))) return true;
    if (holy.test(t) && /\bseja\s+entregue\s+a\s+ele\b/.test(t)) return true;
    if (/\baceita\s+(jesus|cristo|o\s+senhor)\b/.test(t) || /\baceite\s+(jesus|cristo)\b/.test(t)) return true;
    if (/\b(arrepenta|precisa\s+de\s+jesus|volta\s+para\s+jesus)\b/.test(t)) return true;
    if (/\bvenha\s+para\s+(jesus|cristo|deus)\b/.test(t)) return true;

    return false;
}

function passesChristianModerationAiGate(commentLower) {
    const t = foldChatText(commentLower);
    const jc = /\b(jesus|cristo|jeova)\b/.test(t);
    const afroCtx = /\b(candombl|umbanda|macumba|orixa[s]?|feitico[s]?|terreiro|og[aã]|vodum)\b/.test(t);

    if (jc && afroCtx) return true;

    // «Jesus salva», «só Jesus salva» — tensão de salvação (salva ≠ salvacao no fold)
    if (/\b(jesus|cristo|deus)\s+salva\b/.test(t)) return true;
    if (/\bso\s+(jesus|cristo|deus)(\s+salva)?\b/.test(t)) return true;
    if (/\b(jesus|cristo|deus)\b/.test(t) && /\b(salva[cç]ao|salva)\b/.test(t)) return true;

    const tension = /\b(converter|salvacao|entregar|arrep|pecado|cruz|inferno|pregac|culto|pregador)\b/.test(t);
    if (jc && tension) return true;

    if (/\bdeus\b/.test(t) && /\b(converter|salvacao|inferno|pecado|cruz|arrep)\b/.test(t)) return true;
    if (/\bigreja\b/.test(t) || /\bpastor\b/.test(t)) return true;

    return false;
}

function hasExternalShortlinkOrMessenger(rawComment) {
    return /bit\.ly\/|tinyurl\.com\/|cutt\.ly\/|wa\.me\/|t\.me\/|telegram\.me\//i.test(rawComment);
}

function hasNonTiktokHttpLink(rawComment) {
    if (!/https?:\/\/[^\s]+|www\.[^\s]+/i.test(rawComment)) return false;
    const tiktokish = /tiktok\.com|vm\.tiktok\.com|vt\.tiktok\.com/i;
    const urls = rawComment.match(/https?:\/\/[^\s]+|www\.[^\s]+/gi) || [];
    return urls.some((u) => !tiktokish.test(u));
}

/** Gate leve para spam/golpe — evita IA em toda mensagem, mas pega suspeitos comuns */
function passesSpamScamAiGate(rawComment, commentFolded) {
    const t = commentFolded || foldChatText(rawComment);
    if (hasExternalShortlinkOrMessenger(rawComment) || hasNonTiktokHttpLink(rawComment)) {
        return true;
    }
    if (/\b(pix\s+qrcode|pix\s+copia|mande\s+pix|clica\s+no\s+link|link\s+na\s+bio|link\s+do\s+perfil)\b/.test(t)) {
        return true;
    }
    if (/\b(ganhe\s+(dinheiro|gratis)|dinheiro\s+facil)\b/.test(t)) return true;
    if (/\bcurso\s+gratis\b/.test(t) && /https?:\/\//i.test(rawComment)) return true;
    return false;
}

/** Ofensas / provocações comuns em live BR — disparam análise mesmo sem segunda pessoa explícita */
function passesRegionalSlurAiGate(commentFolded) {
    const t = commentFolded || '';
    return /\b(testud[oa]|marmoteir[oa]|enganad[oa])\b/.test(t);
}

/** Gate para insultos/ameaças — modelo é local, podemos mandar mais casos suspeitos para a IA */
function passesPersonalAttackAiGate(commentFolded) {
    const t = commentFolded || '';
    if (passesRegionalSlurAiGate(t)) return true;

    const directed = /\b(voc[eê]|voce\b|\bvc\b|\bce\b|tu\s+t[eá]|pra\s+voce\b|pra\s+voc[eê])\b/.test(t);
    const insultCore =
        /\b(idiota|imbecil|burr[oa]|estupid[oa]|nojent[oa]|noj[o]|lixo|palha[cç][oa]|ridicul[oa]|inutil|fracassad[oa])\b/.test(t);
    const strongSlur =
        /\b(filho\s+da\s+puta|filho\s+de\s+puta|fdp\b|vsf\b|vtnc\b|vai\s+(tomar\s+no\s+cu|pro\s+inferno|a\s+merda)|se\s+fod(e|eu)|pau\s+no\s+cu|cuz[aã]o|escrot[oa])\b/.test(
            t
        );
    const threatShut =
        /\b(morre\b|apaga(\s+a\s+live)?|some(\s+daqui)?|cal[aá]\s+(a\s+)?boca|para\s+de\s+falar|cala\s+boca|te\s+arrodo|te\s+quebro)\b/.test(
            t
        );
    const familyAttack =
        /\b(sua\s+m[aã]e|teu\s+pai|tua\s+familia)\b/.test(t) &&
        /\b(puta|viad[o]|burr[o]?)\b/.test(t);

    if (strongSlur || threatShut || familyAttack) return true;
    if (directed && insultCore) return true;
    return false;
}

const { logAnomaly } = require('./database');

const CATEGORY_LABELS = {
    RELIGIAO: 'Matriz Africana',
    PROSELITISMO: 'Proselitismo Cristão',
    SPAM: 'Spam / propaganda (IA)',
    GOLPE: 'Possível golpe ou fraude (IA)',
    ODIO: 'Ódio / insulto grave (IA)',
    PERGUNTA: 'Pergunta / Dúvida (IA)',
    OUTRO: 'Conteúdo impróprio (IA)'
};

function normalizeModerationKeyword(raw) {
    const folded = foldChatText(String(raw || '')).trim();
    return folded.replace(/\s+/g, '_').replace(/[^a-z0-9_]/g, '');
}

/** Resposta esperada: NAO ou SIM_<CATEGORIA> (compat: SIM sozinho → PROSELITISMO) */
function parseBinaryReligiousAnswer(raw) {
    const key = normalizeModerationKeyword(raw);

    if (!key || key.startsWith('nao')) {
        return { flagged: false, category: 'OK' };
    }

    const prefixMap = [
        ['sim_odio', 'ODIO'],
        ['sim_proselitismo', 'PROSELITISMO'],
        ['sim_religiao', 'RELIGIAO'],
        ['sim_spam', 'SPAM'],
        ['sim_golpe', 'GOLPE'],
        ['sim_pergunta', 'PERGUNTA'],
        ['sim_outro', 'OUTRO']
    ];
    for (const [prefix, category] of prefixMap) {
        if (key === prefix || key.startsWith(prefix)) {
            // PERGUNTA não deve ser considerada infração/anomalia conforme pedido do usuário
            return { flagged: category !== 'PERGUNTA', category };
        }
    }

    const compact = foldChatText(String(raw || '')).trim().replace(/\s+/g, ' ');
    if (/^sim\b/i.test(compact)) {
        return { flagged: true, category: 'PROSELITISMO' };
    }

    return { flagged: false, category: 'OK' };
}

function recentChatBlock(chatBuffer, limit = 14) {
    if (!Array.isArray(chatBuffer) || chatBuffer.length === 0) {
        return '(nenhuma mensagem anterior no buffer)';
    }
    const slice = chatBuffer.slice(-limit);
    return slice
        .map((m) => `${String(m.nickname || m.uniqueId || '?')}: ${String(m.comment || '')}`)
        .join('\n');
}

async function analyzeMessage(comment, uniqueId, nickname, chatBuffer, liveName = 'unknown') {
    const commentLower = comment.trim().toLowerCase();
    const folded = foldChatText(commentLower);

    // 1. Filtros de Regex (Rápido) - Mantemos para agilizar flag óbvia, mas o log passará pela IA
    if (looksObviousAttackOnAfroBrazilianReligion(commentLower)) {
        const res = { flagged: true, reason: 'Ataque a matriz africana / Orixás (filtro)', category: 'RELIGIAO' };
        void logAnomaly(liveName, comment, true, res.category, uniqueId).catch(() => {});
        return res;
    }

    // 2. IA Gate (Removido o isSuspicious para que TODAS passem pela classificação de anomalias via IA)
    // No entanto, ainda evitamos perguntas ou cooldowns graves se o servidor estiver caído.
    if (Date.now() < aiModerationCooldownUntil) {
        return { flagged: false };
    }

    // 3. Cache da IA (mensagem normalizada)
    const aiCacheKey = folded.slice(0, 500);
    let result;
    
    if (aiCache.has(aiCacheKey)) {
        result = aiCache.get(aiCacheKey);
    } else {
        // 4. Chamada à IA Local (Pool distribuído no ai.js)
        try {
            const systemPrompt = await getModerationSystemPrompt();
            const userPrompt =
                `Contexto recente (mensagens anteriores na live):\n${recentChatBlock(chatBuffer)}\n\n` +
                `Autor do comentário: ${JSON.stringify(nickname || uniqueId || '')}\n` +
                `Texto para analisar (ignore menções @nome no início): ${JSON.stringify(comment)}`;

            const raw = await completeModeration(systemPrompt, userPrompt, 48);
            const { flagged, category } = parseBinaryReligiousAnswer(raw);
            const reason = flagged ? CATEGORY_LABELS[category] || CATEGORY_LABELS.OUTRO : null;

            result = { flagged, reason, category };
            
            if (flagged) {
                console.log(`[AI] ⚠️ CONTEÚDO FLAGADO: [${category}] - "${comment}"`);
            } else {
                console.log(`[AI] ✅ Conteúdo liberado: "${comment.substring(0, 50)}${comment.length > 50 ? '...' : ''}"`);
            }

            aiCache.set(aiCacheKey, result);
            if (aiCache.size > AI_CACHE_MAX) aiCache.delete(aiCache.keys().next().value);
        } catch (error) {
            aiModerationCooldownUntil = Date.now() + AI_MODERATION_COOLDOWN_MS;
            console.warn('[AI] Moderação local pausada (falha no cluster):', error.message);
            return { flagged: false };
        }
    }

    // 5. Salva no banco de dados de anomalias (requisito do usuário)
    void logAnomaly(liveName, comment, result.flagged, result.category, uniqueId).catch(err => {
        console.error('[Database] Erro ao logar anomalia:', err.message);
    });

    return result;
}

function clearModerationCache() {
    aiCache.clear();
    aiModerationCooldownUntil = 0;
}

module.exports = { analyzeMessage, clearModerationCache };
