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
    '- SIM_PROSELITISMO: Pregação, tentativa de conversão, "Jesus te ama", "Aceite a Cristo", condenação religiosa ou imposição de dogmas.',
    '- SIM_ODIO: Ofensa, ataque pessoal, xingamento, humilhação, racismo ou preconceito.',
    '- SIM_RELIGIAO: Ataque específico a religiões de matriz africana.',
    '',
    'Responda com EXATAMENTE UMA destas palavras-chave (maiúsculas):',
    '- NAO',
    '- SIM_PERGUNTA',
    '- SIM_PROSELITISMO',
    '- SIM_ODIO',
    '- SIM_RELIGIAO',
    '- SIM_SPAM',
    '- SIM_GOLPE',
    '- SIM_OUTRO',
    '',
    'Regra de saída: uma única token, sem explicações.'
].join('\n');

async function getModerationSystemPrompt() {
    try {
        const feedbacks = await getRecentFeedbacks(12);
        if (feedbacks.length === 0) {
            return BASE_SYSTEM_PROMPT;
        }

        let feedbackSection = '\n\nO usuário humano revisou as seguintes mensagens. Siga estas classificações como exemplos (Few-Shot):\n';
        feedbacks.forEach(f => {
            feedbackSection += `- Texto: "${f.comment}" -> Classificar como: ${f.expected}\n`;
        });

        return BASE_SYSTEM_PROMPT + feedbackSection;
    } catch (error) {
        console.error('Erro ao buscar feedbacks para o prompt:', error);
        return BASE_SYSTEM_PROMPT;
    }
}

// Mantendo a constante para compatibilidade onde o carregamento síncrono é exigido, 
// mas o ideal é usar a função async.
module.exports = {
    BINARY_RELIGIOUS_MODERATION_SYSTEM: BASE_SYSTEM_PROMPT,
    getModerationSystemPrompt
};
