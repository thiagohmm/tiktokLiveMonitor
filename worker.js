const { spawn } = require('child_process');
const path = require('path');
const fs = require('fs');
const http = require('http');
const os = require('os');
const { GGUF_FILENAME } = require('./llm-model');

const MASTER_URL = process.env.MASTER_URL || 'http://localhost:3000';
const WORKER_PORT = process.env.WORKER_PORT || 8081;
const WORKER_HOST = process.env.WORKER_HOST || os.hostname();

function getPaths() {
    let baseDir = __dirname;
    const platform = process.platform === 'win32' ? 'win' : process.platform === 'darwin' ? 'mac' : 'linux';
    const arch = process.arch;
    const binName = platform === 'win' ? 'llama-server.exe' : 'llama-server';
    const archDir = path.join(baseDir, 'bin', platform, arch);
    
    // Resolve path (simplificado para o worker)
    let binPath = path.join(archDir, binName);
    if (!fs.existsSync(binPath)) binPath = path.join(archDir, 'build', 'bin', binName);
    
    const modelPath = path.join(baseDir, 'models', GGUF_FILENAME);
    return { binPath, modelPath };
}

async function startWorker() {
    const { binPath, modelPath } = getPaths();
    if (!fs.existsSync(binPath) || !fs.existsSync(modelPath)) {
        console.error('Binários ou modelo não encontrados. Execute setup-llm.js primeiro.');
        process.exit(1);
    }

    const threads = String(Math.max(1, os.cpus().length || 2));
    const args = [
        '-m', modelPath,
        '--host', '0.0.0.0',
        '--port', WORKER_PORT.toString(),
        '--threads', threads,
        '--n-gpu-layers', '0'
    ];

    console.log(`[Worker] Iniciando llama-server na porta ${WORKER_PORT}...`);
    const proc = spawn(binPath, args, { cwd: path.dirname(binPath), stdio: 'inherit' });

    proc.on('exit', () => process.exit(1));

    // Loop de registro no Master
    setInterval(() => {
        const data = JSON.stringify({ host: WORKER_HOST, port: Number(WORKER_PORT) });
        const req = http.request(`${MASTER_URL}/api/worker/register`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(data) }
        }, (res) => {
            if (res.statusCode === 200) {
                // console.log('[Worker] Registrado no Master com sucesso.');
            }
        });
        req.on('error', (err) => console.warn('[Worker] Falha ao conectar no Master:', err.message));
        req.write(data);
        req.end();
    }, 10000);
}

startWorker();
