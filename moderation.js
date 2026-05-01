const { completeModeration } = require('./ai');

/** Cache para evitar chamadas repetidas à IA */
const aiCache = new Map();
const AI_CACHE_MAX = 150;

let aiModerationCooldownUntil = 0;
const AI_MODERATION_COOLDOWN_MS = 120_000;

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
        /\bsalmo\b/, /\bnossa\s+senhora\b/, /\bpreciso\s+de\s+(jesus|deus)\b/
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

const MODERATION_SYSTEM = [
    'Moderador de chat de live de CANDOMBLÉ (Orixás, axé).',
    'Responda com UMA única palavra em MAIÚSCULAS, escolha só entre:',
    'OK — mensagem aceitável (neutro, elogio, dúvida respeitosa, Jesus mencionado sem convite).',
    'RELIGIAO — insultar, demonizar ou zombar de religiões de matriz africana, terreiro ou Orixás.',
    'PROSELITISMO — convite religioso cristão ao chat (converter, aceitar Jesus/Deus, arrependimento, salvação).',
    'SPAM — propaganda repetitiva, pedido para seguir/clicar/link genérico sem contexto da live.',
    'GOLPE — possível fraude (PIX suspeito, “ganhe dinheiro”, esquema, dados bancários).',
    'ODIO — insultos graves, ameaça, discriminação explícita não coberta só por RELIGIAO.',
    'OUTRO — conteúdo claramente inadequado para o chat que não se encaixa acima.',
    'Se mais de um se aplica, escolha o mais grave (ex.: GOLPE > SPAM).'
].join('\n');

const CATEGORY_LABELS = {
    RELIGIAO: 'Ataque a matriz africana / Orixás (IA)',
    PROSELITISMO: 'Proselitismo cristão (IA)',
    SPAM: 'Spam / propaganda (IA)',
    GOLPE: 'Possível golpe ou fraude (IA)',
    ODIO: 'Ódio / insulto grave (IA)',
    OUTRO: 'Conteúdo impróprio (IA)'
};

function parseCategoryToken(raw) {
    const folded = foldChatText(raw || '');
    const token = folded.replace(/[^a-z]+/g, ' ').trim().split(/\s+/)[0] || '';

    const aliases = {
        ok: 'OK',
        nao: 'OK',
        sim: 'OUTRO',
        religiao: 'RELIGIAO',
        proselitismo: 'PROSELITISMO',
        spam: 'SPAM',
        golpe: 'GOLPE',
        odio: 'ODIO',
        outro: 'OUTRO'
    };

    const cat = aliases[token] || null;
    if (cat) return cat;

    if (folded.includes('relig')) return 'RELIGIAO';
    if (folded.includes('proselit')) return 'PROSELITISMO';
    if (folded.includes('golpe') || folded.includes('fraude')) return 'GOLPE';
    if (folded.includes('spam')) return 'SPAM';
    if (folded.includes('odio') || folded.includes('dio')) return 'ODIO';

    return 'OK';
}

function recentChatBlock(chatBuffer, limit = 6) {
    if (!Array.isArray(chatBuffer) || chatBuffer.length === 0) {
        return '(nenhuma mensagem anterior no buffer)';
    }
    const slice = chatBuffer.slice(-limit);
    return slice
        .map((m) => `${String(m.nickname || m.uniqueId || '?')}: ${String(m.comment || '')}`)
        .join('\n');
}

async function analyzeMessage(comment, uniqueId, nickname, chatBuffer) {
    const commentLower = comment.trim().toLowerCase();
    
    // 1. Verificação de Repetição (opcional, pode ser movida para aqui se quiser)
    // Para simplificar, focaremos no conteúdo ofensivo
    
    const folded = foldChatText(commentLower);

    // 2. Filtros de Regex (Rápido)
    if (looksObviousAttackOnAfroBrazilianReligion(commentLower)) {
        return { flagged: true, reason: 'Ataque a matriz africana / Orixás (filtro)', category: 'RELIGIAO' };
    }
    if (looksExplicitChristianProselytizing(commentLower)) {
        return { flagged: true, reason: 'Proselitismo cristão (filtro)', category: 'PROSELITISMO' };
    }

    // 3. Gate da IA (religião/proselitismo OU suspeita spam/golpe)
    const christianGate = passesChristianModerationAiGate(commentLower);
    const spamGate = passesSpamScamAiGate(comment, folded);
    if (!christianGate && !spamGate) {
        return { flagged: false };
    }

    if (Date.now() < aiModerationCooldownUntil) {
        return { flagged: false };
    }

    // 4. Cache da IA (mensagem normalizada; contexto não entra na chave para hit-rate)
    const aiCacheKey = folded.slice(0, 500);
    if (aiCache.has(aiCacheKey)) {
        return aiCache.get(aiCacheKey);
    }

    // 5. Chamada à IA Local (fila serial em ai.js)
    try {
        const userPrompt =
            `Contexto recente (mensagens anteriores na live):\n${recentChatBlock(chatBuffer)}\n\n` +
            `Mensagem a avaliar:\nAutor: ${JSON.stringify(nickname || uniqueId || '')}\n` +
            `Texto: ${JSON.stringify(comment)}`;

        const raw = await completeModeration(MODERATION_SYSTEM, userPrompt, 32);
        const category = parseCategoryToken(raw);
        const flagged = category !== 'OK';
        const reason = flagged ? CATEGORY_LABELS[category] || CATEGORY_LABELS.OUTRO : null;

        const result = { flagged, reason, category };

        aiCache.set(aiCacheKey, result);
        if (aiCache.size > AI_CACHE_MAX) aiCache.delete(aiCache.keys().next().value);

        return result;
    } catch (error) {
        aiModerationCooldownUntil = Date.now() + AI_MODERATION_COOLDOWN_MS;
        console.warn('[AI] Moderação local pausada:', error.message);
        return { flagged: false };
    }
}

function clearModerationCache() {
    aiCache.clear();
    aiModerationCooldownUntil = 0;
}

module.exports = { analyzeMessage, clearModerationCache };
