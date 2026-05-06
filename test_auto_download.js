const { probeLlamaReady } = require('./ai');

async function test() {
    console.log('Iniciando probeLlamaReady...');
    try {
        const ready = await probeLlamaReady();
        console.log('Llama pronto:', ready);
    } catch (err) {
        console.error('Erro no teste:', err);
    }
}

test();
