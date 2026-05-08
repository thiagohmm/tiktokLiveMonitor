const { completeModeration } = require('./ai');
const { getModerationSystemPrompt, getModerationPromptContext } = require('./moderation-prompt');

/** Cache para evitar chamadas repetidas à IA */
const aiCache = new Map();
const AI_CACHE_MAX = 150;

let aiModerationCooldownUntil = 0;
const AI_MODERATION_COOLDOWN_MS = 30_000;
const MODERATION_AUDIT_RECLASSIFY = ['1', 'true', 'yes', 'y'].includes(
    String(process.env.MODERATION_AUDIT_RECLASSIFY || '').trim().toLowerCase()
);

let moderationStartupStatus = {
    ready: false,
    feedbackCount: 0,
    warmedAt: null,
    lastError: null
};

let warmupInFlight = null;

function logModerationAudit(event, payload) {
    if (!MODERATION_AUDIT_RECLASSIFY) return;
    try {
        console.log(`[MOD-AUDIT] ${event}: ${JSON.stringify(payload)}`);
    } catch {
        console.log(`[MOD-AUDIT] ${event}`);
    }
}

function foldChatText(text) {
    return String(text || '')
        .toLowerCase()
        .normalize('NFD')
        .replace(/\p{M}/gu, '')
        .replace(/ç/g, 'c');
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

function looksQuestion(comment) {
    const raw = String(comment || '').trim();
    if (!raw) return false;

    const folded = foldChatText(raw);
    if (raw.includes('?') || raw.includes('¿')) return true;

    const startsLikeQuestion =
        /^(pq|pk|por\s+que|porque|como|quando|onde|aonde|quem|qual|quais|q\b|sera\s+que|pode|poderia|tem\s+como|da\s+pra|d[aá]\s+pra|isso\s+e|isso\s+eh|v[oô]ce\s+sabe|alguem\s+sabe|algm\s+sabe|me\s+tira\s+uma\s+duvida|duvida\b|duvida:|duvida\s*[-:])\b/.test(
            folded
        );

    if (startsLikeQuestion) return true;

    const containsQuestionCue =
        /\b(pq|pk|por\s+que|como\s+assim|quem\s+sabe|alguem\s+sabe|algm\s+sabe|tem\s+como|da\s+pra|d[aá]\s+pra|sera\s+que|qual\s+o|qual\s+a)\b/.test(
            folded
        );

    return containsQuestionCue;
}

function hasClearPersonalAttackSignal(comment) {
    const folded = foldChatText(comment);
    if (passesPersonalAttackAiGate(folded)) return true;

    // Cobre ataques em formato de pergunta que nem sempre passam no gate base.
    return /\b(vc|voce|voces|tu|ce|c\b)\b[\s\S]{0,20}\b(e\s+)?(burro|idiota|imbecil|retardad[oa]|ridicul[oa]|otari[oa]|troux[ae]|lixo)\b/.test(
        folded
    );
}

/**
 * Detecta linguagem afetiva/romântica sobre terceiros.
 * Ex: "ela gosta dele", "vai atrás", "tem sentimentos", "está apaixonado".
 * Essas frases NÃO são ataques pessoais e nunca devem ser classificadas como ODIO.
 */
function looksAffectiveOrRomantic(comment) {
    const t = foldChatText(comment);
    if (/\b(gosta\s+d[eio]|gostar\s+d[eio]|gostou\s+d[eio])\b/.test(t)) return true;
    if (/\b(vai\s+atras|vai\s+atr[aá]s|foi\s+atras|correr\s+atras)\b/.test(t)) return true;
    if (/\b(tem\s+sentimentos?|tinha\s+sentimentos?|ter\s+sentimentos?)\b/.test(t)) return true;
    if (/\b(esta\s+apaixonad[oa]|ficou\s+apaixonad[oa]|apaixonou)\b/.test(t)) return true;
    if (/\b(tem\s+interesse|demonstrou?\s+interesse|esta\s+interessad[oa])\b/.test(t)) return true;
    if (/\b(curte|curtiu|se\s+apaixonou|quer\s+fic[ao]r?|quer\s+namorar)\b/.test(t)) return true;
    if (/\b(esta\s+(gostando|querendo)|sempre\s+gostou)\b/.test(t)) return true;
    return false;
}

const { logAnomaly } = require('./database');

const CATEGORY_LABELS = {
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

    // 1. IA Gate (Removido o isSuspicious para que TODAS passem pela classificação de anomalias via IA)
    // No entanto, ainda evitamos perguntas ou cooldowns graves se o servidor estiver caído.
    if (Date.now() < aiModerationCooldownUntil) {
        return { flagged: false };
    }

    // 2. Cache da IA (mensagem normalizada)
    const aiCacheKey = folded.slice(0, 500);
    let result;
    
    if (aiCache.has(aiCacheKey)) {
        result = aiCache.get(aiCacheKey);
    } else {
        // 3. Chamada à IA Local (Pool distribuído no ai.js)
        try {
            const systemPrompt = await getModerationSystemPrompt();
            const userPrompt =
                `Contexto recente (mensagens anteriores na live):\n${recentChatBlock(chatBuffer)}\n\n` +
                `Autor do comentário: ${JSON.stringify(nickname || uniqueId || '')}\n` +
                `Texto para analisar (ignore menções @nome no início): ${JSON.stringify(comment)}`;

            const raw = await completeModeration(systemPrompt, userPrompt, 48);
            const { flagged, category } = parseBinaryReligiousAnswer(raw);
            let finalCategory = category;
            let finalFlagged = flagged;

            // Evita falso positivo: perguntas sem sinal claro de ataque não devem virar "ODIO".
            if (finalCategory === 'ODIO' && looksQuestion(comment) && !hasClearPersonalAttackSignal(comment)) {
                logModerationAudit('reclassified_odio_to_pergunta', {
                    liveName,
                    uniqueId,
                    nickname,
                    rawModelOutput: raw,
                    originalCategory: finalCategory,
                    finalCategory: 'PERGUNTA',
                    message: comment
                });
                finalCategory = 'PERGUNTA';
                finalFlagged = false;
            }

            // Evita falso positivo: linguagem afetiva/romântica (ex: "ela gosta dele", "vai atrás",
            // "tem sentimentos") não deve ser classificada como ODIO.
            if (finalCategory === 'ODIO' && finalFlagged && looksAffectiveOrRomantic(comment)) {
                logModerationAudit('reclassified_odio_to_nao_affective', {
                    liveName,
                    uniqueId,
                    nickname,
                    rawModelOutput: raw,
                    originalCategory: finalCategory,
                    finalCategory: 'NAO',
                    message: comment
                });
                finalCategory = 'OK';
                finalFlagged = false;
            }

            // Evita falso positivo: gossip/drama sobre terceiros (ex: "X está traindo Y") sem
            // sinal verificável de ataque pessoal não devem ser flagados como ODIO.
            if (finalCategory === 'ODIO' && finalFlagged && !hasClearPersonalAttackSignal(comment)) {
                logModerationAudit('reclassified_odio_to_nao_no_signal', {
                    liveName,
                    uniqueId,
                    nickname,
                    rawModelOutput: raw,
                    originalCategory: finalCategory,
                    finalCategory: 'NAO',
                    message: comment
                });
                finalCategory = 'OK';
                finalFlagged = false;
            }

            const reason = finalFlagged ? CATEGORY_LABELS[finalCategory] || CATEGORY_LABELS.OUTRO : null;

            result = { flagged: finalFlagged, reason, category: finalCategory };
            
            if (result.flagged) {
                console.log(`[AI] ⚠️ CONTEÚDO FLAGADO: [${result.category}] - "${comment}"`);
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

    // 4. Salva no banco de dados de anomalias (requisito do usuário)
    void logAnomaly(liveName, comment, result.flagged, result.category, uniqueId).catch(err => {
        console.error('[Database] Erro ao logar anomalia:', err.message);
    });

    return result;
}

function clearModerationCache() {
    aiCache.clear();
    aiModerationCooldownUntil = 0;
}

async function warmupModerationLearning(options = {}) {
    const { touchLlm = false, force = false } = options;

    if (warmupInFlight && !force) {
        return warmupInFlight;
    }

    warmupInFlight = (async () => {
        try {
            const { prompt, feedbackCount } = await getModerationPromptContext(12);

            if (touchLlm) {
                // Warmup para reduzir latência da primeira classificação após startup.
                await completeModeration(
                    prompt,
                    'Contexto recente (mensagens anteriores na live): (nenhuma mensagem anterior no buffer)\n\nAutor do comentário: "system"\nTexto para analisar (ignore menções @nome no início): "mensagem de aquecimento"',
                    8
                );
            }

            moderationStartupStatus = {
                ready: true,
                feedbackCount,
                warmedAt: new Date().toISOString(),
                lastError: null
            };

            return { ...moderationStartupStatus };
        } catch (error) {
            moderationStartupStatus = {
                ready: false,
                feedbackCount: 0,
                warmedAt: null,
                lastError: error.message
            };
            throw error;
        } finally {
            warmupInFlight = null;
        }
    })();

    return warmupInFlight;
}

function getModerationStartupStatus() {
    return { ...moderationStartupStatus };
}

module.exports = {
    analyzeMessage,
    clearModerationCache,
    warmupModerationLearning,
    getModerationStartupStatus
};
