const { getRecentFeedbacks } = require('./database');

/**
 * Prompt do llama-server: moderação multi-rótulo (live BR); o texto vai no turno "user".
 * Saída estrita para parser em moderation.js / askLlama em ai.js.
 */
const BASE_SYSTEM_PROMPT = [
    'Você é moderador de chat de live em português do Brasil. Avalie o comentário isolado e o contexto recente.',
    '',
    'ATENÇÃO: É comum usuários responderem a outros citando o nome no início (ex: "@JesusTeAma qual sua religião?"). IGNORE nomes de usuário ou arrobas (@) na avaliação. Foque apenas na intenção da mensagem real.',
    '',
    'DIRETRIZES DE CLASSIFICAÇÃO:',
    '- NAO: Mensagem comum, saudação, elogio neutro ou comentário irrelevante.',
    '- SIM_PERGUNTA: Qualquer pergunta direta ou dúvida enviada ao streamer ou sobre o tema da live.',
    '- PRIORIDADE: Se a mensagem for uma pergunta e NÃO tiver xingamento/ofensa explícita, classifique como SIM_PERGUNTA (nunca SIM_ODIO).',
    '- SIM_PROSELITISMO: Pregação, tentativa de conversão, "Jesus te ama", "Aceite a Cristo", condenação religiosa ou imposição de dogmas.',
    '- SIM_ODIO: Ofensa, ataque pessoal, xingamento, humilhação, racismo ou preconceito.',
    '',
    'Responda com EXATAMENTE UMA destas palavras-chave (maiúsculas):',
    '- NAO',
    '- SIM_PERGUNTA',
    '- SIM_PROSELITISMO',
    '- SIM_ODIO',
    '- SIM_SPAM',
    '- SIM_GOLPE',
    '- SIM_OUTRO',
    '',
    'Regra de saída: uma única token, sem explicações.'
].join('\n');

async function getModerationSystemPrompt() {
    try {
        const { prompt } = await getModerationPromptContext(12);
        return prompt;
    } catch (error) {
        console.error('Erro ao buscar feedbacks para o prompt:', error);
        return BASE_SYSTEM_PROMPT;
    }
}

async function getModerationPromptContext(limit = 12) {
    const feedbacks = await getRecentFeedbacks(limit);
    if (feedbacks.length === 0) {
        return {
            prompt: BASE_SYSTEM_PROMPT,
            feedbackCount: 0
        };
    }

    let feedbackSection = '\n\nO usuário humano revisou as seguintes mensagens. Siga estas classificações como exemplos (Few-Shot):\n';
    feedbacks.forEach(f => {
        feedbackSection += `- Texto: "${f.comment}" -> Classificar como: ${f.expected}\n`;
    });

    return {
        prompt: BASE_SYSTEM_PROMPT + feedbackSection,
        feedbackCount: feedbacks.length
    };
}

// Mantendo a constante para compatibilidade onde o carregamento síncrono é exigido, 
// mas o ideal é usar a função async.
module.exports = {
    MODERATION_SYSTEM_PROMPT: BASE_SYSTEM_PROMPT,
    getModerationSystemPrompt,
    getModerationPromptContext
};
