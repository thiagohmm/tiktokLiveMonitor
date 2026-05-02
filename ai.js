const { spawn } = require('child_process');
const path = require('path');
const fs = require('fs');
const http = require('http');
const os = require('os');
const { BINARY_RELIGIOUS_MODERATION_SYSTEM } = require('./moderation-prompt');
const { GGUF_FILENAME } = require('./llm-model');

let llamaProcess = null;
const LLAMA_PORT = 8080;

/** Serializa chamadas ao llama-server para evitar picos de CPU com várias mensagens simultâneas */
let llamaRequestChain = Promise.resolve();

function enqueueLlama(task) {
    const next = llamaRequestChain.then(() => task());
    llamaRequestChain = next.then(
        () => {},
        () => {}
    );
    return next;
}

function getPaths() {
    // Se estiver rodando no Electron empacotado, os arquivos estarão em Resources/resources
    // Se estiver em desenvolvimento ou Node puro, estarão na raiz do projeto
    
    let baseDir = __dirname;
    
    // Tenta detectar se estamos no Electron empacotado
    if (process.resourcesPath) {
        // No Mac, Resources fica acima do app.asar. No Win fica no mesmo nível.
        // extraResources vai para o diretório de recursos da aplicação.
        if (fs.existsSync(path.join(process.resourcesPath, 'bin'))) {
            baseDir = process.resourcesPath;
        }
    }

    const platform = process.platform === 'win32' ? 'win' : process.platform === 'darwin' ? 'mac' : 'linux';
    const arch = process.arch;

    const binName = platform === 'win' ? 'llama-server.exe' : 'llama-server';
    const archDir = path.join(baseDir, 'bin', platform, arch);

    const binPath = resolveLlamaServerPath(archDir, binName);
    const modelPath = path.join(baseDir, 'models', GGUF_FILENAME);

    return { binPath, modelPath };
}

/** Zip Windows (flat), zip mac antigo em build/bin/, tarball llama-bNNNN/ em mac/linux novos */
function resolveLlamaServerPath(archDir, binName) {
    const ordered = [
        path.join(archDir, binName),
        path.join(archDir, 'build', 'bin', binName)
    ];
    for (const p of ordered) {
        if (fs.existsSync(p)) return p;
    }
    if (!fs.existsSync(archDir)) return ordered[0];
    try {
        const entries = fs.readdirSync(archDir, { withFileTypes: true });
        for (const e of entries) {
            if (!e.isDirectory()) continue;
            if (/^llama-b\d+$/i.test(e.name)) {
                const nested = path.join(archDir, e.name, binName);
                if (fs.existsSync(nested)) return nested;
            }
        }
    } catch {
        /* ignore */
    }
    return ordered[0];
}

async function startLlamaServer() {
    if (llamaProcess) return;

    const { binPath, modelPath } = getPaths();
    
    if (!fs.existsSync(binPath)) {
        throw new Error(`Binário do llama-server não encontrado em: ${binPath}. Execute 'npm install' ou 'npm run setup-llm'.`);
    }
    if (!fs.existsSync(modelPath)) {
        throw new Error(`Modelo GGUF não encontrado em: ${modelPath}. Execute 'npm install' ou 'npm run setup-llm'.`);
    }
    const ms = fs.statSync(modelPath).size;
    if (ms < 4096) {
        throw new Error(`Modelo GGUF parece inválido ou vazio (${ms} bytes). Execute: npm run setup-llm`);
    }

    const binDir = path.dirname(binPath);

    /** 127.0.0.1 quebra porta publicada pelo Docker (curl no host → resposta vazia). */
    let bindHost = process.env.LLAMA_SERVER_BIND;
    if (!bindHost) {
        try {
            bindHost = fs.existsSync('/.dockerenv') ? '0.0.0.0' : '127.0.0.1';
        } catch {
            bindHost = '127.0.0.1';
        }
    }

    console.log(`[AI] Iniciando llama-server em http://${bindHost}:${LLAMA_PORT}...`);
    console.log(`[AI] Bin: ${binPath}`);
    console.log(`[AI] Model: ${modelPath}`);

    const cores = Math.max(1, (os.cpus() && os.cpus().length) || 2);
    const cap = process.platform === 'linux' && process.arch === 'arm64' ? 4 : 8;
    const threads = String(Math.min(cap, cores));

    /** Distribuições tarball colocam libggml*.so ao lado do binário */
    const spawnEnv = { ...process.env };
    if (process.platform === 'linux') {
        const sep = ':';
        spawnEnv.LD_LIBRARY_PATH = spawnEnv.LD_LIBRARY_PATH
            ? `${binDir}${sep}${spawnEnv.LD_LIBRARY_PATH}`
            : binDir;
    }

    const llamaArgs = [
        '-m', modelPath,
        '--host', bindHost,
        '--port', LLAMA_PORT.toString(),
        '--n-gpu-layers', '0',
        '--threads', threads
    ];
    // Docker/overlay: mmap costuma funcionar melhor; desktop com pouca RAM pode usar --no-mmap
    if (process.env.LLAMA_USE_MMAP !== '1') {
        llamaArgs.push('--no-mmap');
    }

    let stderrTail = '';
    llamaProcess = spawn(binPath, llamaArgs, {
        cwd: binDir,
        stdio: ['ignore', 'pipe', 'pipe'],
        env: spawnEnv
    });

    llamaProcess.stderr.on('data', (chunk) => {
        const text = chunk.toString();
        stderrTail = (stderrTail + text).slice(-8000);
        const line = text.trim();
        if (/error|fatal|fail|cannot load|not found|permission denied/i.test(line)) {
            console.error('[AI] llama-server:', line.slice(0, 800));
        }
    });

    llamaProcess.on('error', (err) => {
        console.error(`[AI] Falha ao iniciar processo:`, err);
    });
    llamaProcess.on('exit', (code, signal) => {
        if ((code !== 0 && code !== null) || signal) {
            console.error(`[AI] llama-server encerrou (code=${code} signal=${signal})`);
            if (stderrTail.trim()) {
                console.error('[AI] stderr (final):\n', stderrTail.trim().slice(-4000));
            }
        }
    });

    const maxAttempts = Math.min(
        600,
        Math.max(30, Number(process.env.LLAMA_HEALTH_MAX_ATTEMPTS) || 90)
    );

    // /health devolve 503 {"Loading model"...} até o GGUF ficar pronto (Pi pode levar vários minutos)
    let attempts = 0;

    return new Promise((resolve, reject) => {
        const check = setInterval(() => {
            attempts++;
            if (attempts > maxAttempts) {
                clearInterval(check);
                reject(new Error(
                    `Timeout ao aguardar o modelo carregar (${maxAttempts}s). Raspberry/memória lenta ou GGUF corrompido.`
                ));
                return;
            }

            const req = http.get(`http://127.0.0.1:${LLAMA_PORT}/health`, (res) => {
                if (res.statusCode === 200) {
                    clearInterval(check);
                    res.resume();
                    console.log(`[AI] llama-server pronto.`);
                    resolve();
                    return;
                }
                res.resume();
                if (res.statusCode === 503 && attempts % 15 === 0) {
                    console.log('[AI] Modelo ainda carregando (HTTP 503); aguarde…');
                }
            });
            req.on('error', () => {
                if (attempts >= maxAttempts) {
                    clearInterval(check);
                    reject(new Error('Timeout ao aguardar o llama-server iniciar.'));
                }
            });
        }, 1000);
    });
}

/** Gemma 4 / modelos com thinking: sem isso, max_tokens baixo esgota no reasoning e content fica vazio. */
function mergeChatCompletionBody(bodyObj) {
    return {
        stream: false,
        chat_template_kwargs: { enable_thinking: false },
        ...bodyObj
    };
}

function assistantTextFromChatResponse(json) {
    const msg = json.choices?.[0]?.message;
    if (!msg || typeof msg !== 'object') return null;
    const c = typeof msg.content === 'string' ? msg.content : '';
    const r = typeof msg.reasoning_content === 'string' ? msg.reasoning_content : '';
    const out = (c.trim() || r.trim()) ? (c.trim() || r.trim()) : null;
    return out;
}

function postChatCompletion(bodyObj) {
    const data = JSON.stringify(mergeChatCompletionBody(bodyObj));

    return new Promise((resolve, reject) => {
        const req = http.request({
            hostname: 'localhost',
            port: LLAMA_PORT,
            path: '/v1/chat/completions',
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'Content-Length': Buffer.byteLength(data)
            },
            timeout: 120_000
        }, (res) => {
            let responseData = '';
            res.on('data', (chunk) => responseData += chunk);
            res.on('end', () => {
                try {
                    const json = JSON.parse(responseData);
                    const text = assistantTextFromChatResponse(json);
                    if (text == null) {
                        reject(new Error('Resposta sem content (pense em aumentar max_tokens ou desativar thinking no servidor)'));
                        return;
                    }
                    resolve(text.trim());
                } catch (e) {
                    reject(e);
                }
            });
        });

        req.on('error', reject);
        req.on('timeout', () => {
            req.destroy();
            reject(new Error('Timeout na requisição ao llama-server'));
        });
        req.write(data);
        req.end();
    });
}

/**
 * Uma inferência por vez (fila). Retorna o texto da assistente (trimmed).
 */
async function completeModeration(systemContent, userContent, maxTokens = 32) {
    return enqueueLlama(async () => {
        try {
            await startLlamaServer();
        } catch (err) {
            console.error(`[AI] Erro ao iniciar servidor:`, err.message);
            throw err;
        }

        const bodyObj = {
            messages: [
                { role: 'system', content: systemContent },
                { role: 'user', content: userContent }
            ],
            temperature: 0.1,
            max_tokens: maxTokens
        };

        try {
            return await postChatCompletion(bodyObj);
        } catch (e) {
            console.error('[AI] Erro na inferência:', e.message);
            throw e;
        }
    });
}

/** Compat: moderador binário SIM/NAO (usa a mesma fila). */
async function askLlama(prompt) {
    try {
        const userContent = `Texto para analisar:\n${typeof prompt === 'string' ? prompt : JSON.stringify(prompt)}`;
        const text = await completeModeration(BINARY_RELIGIOUS_MODERATION_SYSTEM, userContent, 32);
        const compact = String(text || '')
            .toLowerCase()
            .trim()
            .replace(/\s+/g, ' ');
        if (/^nao\b/i.test(compact)) return 'NAO';
        if (/^sim\b/i.test(compact)) return 'SIM';
        return 'NAO';
    } catch {
        return 'NAO';
    }
}

function stopLlamaServer() {
    if (llamaProcess) {
        console.log('[AI] Encerrando llama-server...');
        llamaProcess.kill();
        llamaProcess = null;
    }
}

// Garante que o processo morra ao sair
process.on('SIGINT', stopLlamaServer);
process.on('SIGTERM', stopLlamaServer);
process.on('exit', stopLlamaServer);

function aiConfigured() {
    return true;
}

/** Sobe o llama-server se necessário e confere /health — uso ao conectar na live */
async function probeLlamaReady() {
    try {
        await startLlamaServer();
        return true;
    } catch (err) {
        console.warn('[AI] Probe LLM:', err?.message || err);
        return false;
    }
}

module.exports = {
    askLlama,
    completeModeration,
    stopLlamaServer,
    aiConfigured,
    probeLlamaReady,
    mergeChatCompletionBody,
    assistantTextFromChatResponse
};
