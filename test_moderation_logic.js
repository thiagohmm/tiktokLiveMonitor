const { analyzeMessage } = require('./moderation');

async function test() {
    console.log('--- Iniciando Testes de Moderação ---');

    const tests = [
        { msg: 'Olá, como vai?', expected: false, note: 'Mensagem comum (não suspeita)' },
        { msg: 'Jesus te ama', expected: true, note: 'Proselitismo (filtro regex)' },
        { msg: 'Você é um idiota', expected: true, note: 'Ataque pessoal (AI gate -> deve passar para IA)' },
        { msg: 'Clica aqui no meu link bit.ly/123', expected: true, note: 'Spam/Golpe (AI gate -> deve passar para IA)' },
        { msg: 'Orixá é coisa do demônio', expected: true, note: 'Ataque religioso (filtro regex)' },
        { msg: 'Qual o seu nome?', expected: false, note: 'Pergunta (não passa pela IA)' }
    ];

    for (const t of tests) {
        // Mocking chatBuffer
        const result = await analyzeMessage(t.msg, 'user1', 'nickname1', []);
        console.log(`Mensagem: "${t.msg}"`);
        console.log(`  Resultado: ${result.flagged ? 'FLAGGED' : 'OK'}`);
        console.log(`  Esperado: ${t.expected ? 'FLAGGED' : 'OK'}`);
        console.log(`  Nota: ${t.note}`);
        console.log('---');
    }
}

test().catch(console.error);
