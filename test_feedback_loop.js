const { addFalsePositive, getRecentFalsePositives } = require('./database');
const { getModerationSystemPrompt } = require('./moderation-prompt');

async function test() {
    console.log('--- Testando Loop de Feedback ---');

    // 1. Adicionar um falso positivo
    const comment = "Jesus te abençoe meu amigo";
    const category = "PROSELITISMO";
    console.log(`Adicionando falso positivo: "${comment}"`);
    await addFalsePositive(comment, category);

    // 2. Verificar no banco
    const recent = await getRecentFalsePositives(1);
    console.log('Recuperado do banco:', recent[0]);

    if (recent[0].comment === comment) {
        console.log('✅ Banco de dados OK');
    } else {
        console.log('❌ Falha no banco de dados');
    }

    // 3. Verificar o prompt dinâmico
    const prompt = await getModerationSystemPrompt();
    console.log('--- Início do Prompt Gerado ---');
    console.log(prompt.slice(-300)); // Mostra o final onde deve estar o feedback
    console.log('--- Fim do Prompt Gerado ---');

    if (prompt.includes(comment)) {
        console.log('✅ Prompt dinâmico OK (inclui o feedback)');
    } else {
        console.log('❌ Prompt dinâmico falhou');
    }
}

test().catch(console.error);
