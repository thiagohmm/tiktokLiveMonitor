const { spawn } = require('child_process');
const path = require('path');
const fs = require('fs');
const http = require('http');
const os = require('os');
const { MODERATION_SYSTEM_PROMPT } = require('./moderation-prompt');
const { GGUF_FILENAME, getSelectedModel } = require('./llm-model');

const BASE_PORT = 8080;

let aiWorker = null; // Single worker instance
let requestQueue = [];
const QUEUE_MAX = 50; // Limite para evitar que a fila cresça infinitamente e cause atrasos extremos
let isRestarting = false;   // Evita spawns simultâneos
let restartRetryTimer = null; // Timer para retry automático após falha no restart

/**
 * Representa um nó de processamento de IA (Local ou Remoto)
 */
class AIWorker {
    constructor(host, port, isLocal = false, process = null) {
        this.host = host;
        this.port = port;
        this.isLocal = isLocal;
        this.process = process;
        this.busy = false;
        this.ready = false;
        this.lastSeen = Date.now();
    }

    async checkHealth() {
        return new Promise((resolve) => {
            const req = http.get(`http://${this.host}:${this.port}/health`, (res) => {
                this.ready = (res.statusCode === 200);
                res.resume();
                resolve(this.ready);
            });
            req.on('error', () => {
                this.ready = false;
                resolve(false);
            });
            req.setTimeout(2000, () => {
                req.destroy();
                this.ready = false;
                resolve(false);
            });
        });
    }

    kill() {
        if (this.process) {
            this.process.kill();
            this.process = null;
        }
    }
}

function getPaths() {
    // Re-importa para garantir que pegamos o nome correto se mudou
    delete require.cache[require.resolve('./llm-model')];
    const { GGUF_FILENAME: currentGguf } = require('./llm-model');

    let baseDir = __dirname;
    if (process.resourcesPath && fs.existsSync(path.join(process.resourcesPath, 'bin'))) {
        baseDir = process.resourcesPath;
    }
    const platform = process.platform === 'win32' ? 'win' : process.platform === 'darwin' ? 'mac' : 'linux';
    const arch = process.arch;
    const binName = platform === 'win' ? 'llama-server.exe' : 'llama-server';
    const archDir = path.join(baseDir, 'bin', platform, arch);
    const binPath = resolveLlamaServerPath(archDir, binName);
    const modelPath = path.join(baseDir, 'models', currentGguf);
    return { binPath, modelPath };
}

function resolveLlamaServerPath(archDir, binName) {
    const ordered = [path.join(archDir, binName), path.join(archDir, 'build', 'bin', binName)];
    for (const p of ordered) if (fs.existsSync(p)) return p;
    try {
        const entries = fs.readdirSync(archDir, { withFileTypes: true });
        for (const e of entries) {
            if (e.isDirectory() && /^llama-b\d+$/i.test(e.name)) {
                const nested = path.join(archDir, e.name, binName);
                if (fs.existsSync(nested)) return nested;
            }
        }
    } catch { /* ignore */ }
    return ordered[0];
}

async function spawnLocalWorker(port) {
    const { binPath, modelPath } = getPaths();
    
    if (!fs.existsSync(binPath) || !fs.existsSync(modelPath)) {
        console.log('[AI-Queue] Binários ou modelo ausentes. Tentando baixar via setup-llm.js...');
        try {
            const { execSync } = require('child_process');
            let baseDir = __dirname;
            if (process.resourcesPath && fs.existsSync(path.join(process.resourcesPath, 'scripts'))) {
                baseDir = process.resourcesPath;
            }
            const setupPath = path.join(baseDir, 'scripts', 'setup-llm.js');
            // Usamos execSync para garantir que o setup termine antes de continuarmos
            execSync(`node "${setupPath}"`, { stdio: 'inherit' });
            
            // Re-checar após o setup
            if (!fs.existsSync(binPath) || !fs.existsSync(modelPath)) {
                console.error('[AI-Queue] Falha ao baixar binários ou modelo após execução do setup.');
                return null;
            }
        } catch (err) {
            console.error('[AI-Queue] Erro ao executar setup-llm.js:', err.message);
            return null;
        }
    }

    let bindHost = process.env.LLAMA_SERVER_BIND || (fs.existsSync('/.dockerenv') ? '0.0.0.0' : '127.0.0.1');
    const binDir = path.dirname(binPath);
    
    // Allocate all available CPU threads to the single worker for maximum speed
    const totalCores = os.cpus().length || 2;
    const threadsPerWorker = String(totalCores);

    const llamaArgs = [
        '-m', modelPath,
        '--host', bindHost,
        '--port', port.toString(),
        '--n-gpu-layers', '0',
        '--threads', threadsPerWorker,
        '--ctx-size', '2048'
    ];
    if (process.env.LLAMA_USE_MMAP !== '1') llamaArgs.push('--no-mmap');

    console.log(`[AI-Queue] Spawning single local worker on port ${port}...`);
    const proc = spawn(binPath, llamaArgs, { 
        cwd: binDir, 
        stdio: 'inherit', // Permite ver o log do llama-server diretamente no console
        env: { ...process.env } 
    });
    
    aiWorker = new AIWorker(bindHost, port, true, proc);

    // Aguarda ficar pronto (health check)
    for (let i = 0; i < 90; i++) {
        await new Promise(r => setTimeout(r, 2000));
        if (await aiWorker.checkHealth()) {
            console.log(`[AI-Queue] Single AI worker na porta ${port} está pronto.`);
            return aiWorker;
        }
    }
    return aiWorker;
}

function registerWorker(host, port) {
    if (aiWorker && aiWorker.host === host && aiWorker.port === port) {
        aiWorker.lastSeen = Date.now();
        aiWorker.ready = true;
    } else {
        console.log(`[AI-Queue] Novo worker registrado: ${host}:${port}`);
        // If it's a new remote worker, we replace the current one
        if (aiWorker && aiWorker.isLocal) {
            aiWorker.kill();
        }
        aiWorker = new AIWorker(host, port, false);
    }
}

async function processQueue() {
    if (requestQueue.length === 0) return;

    if (!aiWorker || !aiWorker.ready || aiWorker.busy) {
        // Só tenta (re)iniciar se não houver outro restart em andamento
        if (!isRestarting && (!aiWorker || !aiWorker.ready)) {
            isRestarting = true;
            if (restartRetryTimer) {
                clearTimeout(restartRetryTimer);
                restartRetryTimer = null;
            }

            const currentQueueSize = requestQueue.length;
            console.log(`[AI-Queue] Worker indisponível. Tentando (re)iniciar... (Fila: ${currentQueueSize})`);

            if (aiWorker && aiWorker.isLocal) {
                aiWorker.kill();
                aiWorker = null;
            }

            await spawnLocalWorker(BASE_PORT);
            isRestarting = false;

            if (!aiWorker || !aiWorker.ready) {
                console.warn('[AI-Queue] Falha ao reativar worker local. Tentando novamente em 15s.');
                // Agenda retry automático — garante recuperação mesmo sem novas mensagens chegando
                restartRetryTimer = setTimeout(() => {
                    restartRetryTimer = null;
                    processQueue();
                }, 15_000);
                return;
            }

            // Worker recuperado — processa a fila
            processQueue();
        }
        return;
    }

    const task = requestQueue.shift();
    aiWorker.busy = true;
    
    console.log(`[AI] Processando tarefa... (Restantes na fila: ${requestQueue.length})`);
    task.run(aiWorker)
        .then(res => {
            console.log(`[AI] Resposta recebida: ${res}`);
            task.resolve(res);
        })
        .catch(err => {
            console.error(`[AI] Erro ao processar tarefa:`, err.message);
            // Marca worker como não pronto para disparar restart na próxima iteração.
            // Recoloca a tarefa na fila SEM rejeitar a promise — ela será resolvida
            // quando o worker se recuperar.
            if (aiWorker) aiWorker.ready = false;
            requestQueue.unshift(task);
        })
        .finally(() => {
            if (aiWorker) aiWorker.busy = false;
            processQueue();
        });
}

async function completeModeration(systemContent, userContent, maxTokens = 32) {
    if (requestQueue.length >= QUEUE_MAX) {
        console.warn(`[AI-Queue] Fila cheia (${requestQueue.length}). Descartando mensagem para manter performance.`);
        return 'NAO'; // Retorna NAO por padrão para não travar o fluxo
    }

    return new Promise((resolve, reject) => {
        const task = {
            run: async (worker) => {
                const bodyObj = {
                    messages: [{ role: 'system', content: systemContent }, { role: 'user', content: userContent }],
                    temperature: 0.1,
                    max_tokens: maxTokens,
                    stream: false,
                    chat_template_kwargs: { enable_thinking: false }
                };
                const data = JSON.stringify(bodyObj);
                
                // Log do input (resumido se for muito grande)
                const userContentPreview = userContent.length > 100 ? userContent.substring(0, 100) + '...' : userContent;
                console.log(`[AI] Chamando LLM com: "${userContentPreview.replace(/\n/g, ' ')}"`);

                return new Promise((res, rej) => {
                    const req = http.request({
                        hostname: worker.host,
                        port: worker.port,
                        path: '/v1/chat/completions',
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(data) },
                        timeout: 120_000
                    }, (httpRes) => {
                        let respData = '';
                        httpRes.on('data', c => respData += c);
                        httpRes.on('end', () => {
                            try {
                                const json = JSON.parse(respData);
                                const text = assistantTextFromChatResponse(json);
                                if (text == null) rej(new Error('Resposta vazia'));
                                else res(text.trim());
                            } catch (e) { rej(e); }
                        });
                    });
                    req.on('error', rej);
                    req.write(data);
                    req.end();
                });
            },
            resolve,
            reject
        };

        requestQueue.push(task);
        processQueue();
    });
}

function assistantTextFromChatResponse(json) {
    const msg = json.choices?.[0]?.message;
    if (!msg) return null;
    return (msg.content || msg.reasoning_content || '').trim() || null;
}

async function askLlama(prompt) {
    try {
        const text = await completeModeration(MODERATION_SYSTEM_PROMPT, `Texto: ${prompt}`, 32);
        const compact = String(text || '').toLowerCase();
        return /^sim\b/i.test(compact) ? 'SIM' : 'NAO';
    } catch { return 'NAO'; }
}

/**
 * Escolhe, via LLM, qual pergunta recente mais provavelmente se relaciona ao presente enviado.
 * Retorna null quando não há correlação confiável.
 */
async function correlateGiftQuestionWithLlm({ giftName, giftUser, candidates }) {
    const safeCandidates = Array.isArray(candidates) ? candidates.slice(0, 8) : [];
    if (!safeCandidates.length) return null;

    const systemPrompt = [
        'Você correlaciona evento de presente no chat com uma pergunta recente.',
        'Escolha APENAS uma opção da lista de perguntas candidatas.',
        'Responda com UMA ÚNICA token:',
        '- NAO (sem correlação clara)',
        '- IDX_<N> (ex: IDX_2 para escolher a opção 2)',
        'Use IDX apenas quando a ligação for clara por autor, menção, contexto ou sequência temporal próxima.',
        'Sem explicações.'
    ].join('\n');

    const serializedCandidates = safeCandidates
        .map((c, idx) => {
            const user = String(c.nickname || c.uniqueId || 'desconhecido');
            const uid = String(c.uniqueId || '');
            const age = Number(c.ageMs || 0);
            const ageSec = Math.max(0, Math.round(age / 1000));
            return `${idx + 1}. user=${JSON.stringify(user)} uid=${JSON.stringify(uid)} ageSec=${ageSec} text=${JSON.stringify(String(c.comment || ''))}`;
        })
        .join('\n');

    const userPrompt = [
        `Presente: ${JSON.stringify(String(giftName || ''))}`,
        `Quem enviou presente: ${JSON.stringify(String(giftUser?.nickname || giftUser?.uniqueId || 'desconhecido'))}`,
        `UID de quem enviou presente: ${JSON.stringify(String(giftUser?.uniqueId || ''))}`,
        '',
        'Candidatas:',
        serializedCandidates
    ].join('\n');

    try {
        const raw = await completeModeration(systemPrompt, userPrompt, 12);
        const normalized = String(raw || '').trim().toUpperCase();
        if (!normalized || normalized.startsWith('NAO')) return null;

        const match = normalized.match(/IDX[_\s-]?(\d+)/i);
        if (!match) return null;

        const oneBased = Number(match[1]);
        if (!Number.isFinite(oneBased) || oneBased < 1 || oneBased > safeCandidates.length) {
            return null;
        }

        return safeCandidates[oneBased - 1] || null;
    } catch {
        return null;
    }
}

function stopLlamaServer() {
    console.log('[AI-Queue] Encerrando worker local...');
    if (aiWorker) {
        aiWorker.kill();
        aiWorker = null;
    }
}

process.on('SIGINT', stopLlamaServer);
process.on('SIGTERM', stopLlamaServer);
process.on('exit', stopLlamaServer);

async function probeLlamaReady() {
    if (!aiWorker) {
        await spawnLocalWorker(BASE_PORT);
    }
    if (!aiWorker) return false;
    const healthy = await aiWorker.checkHealth();
    console.log(`[AI-Status] Verificação de saúde: ${healthy ? 'OK' : 'FALHA'}`);
    return healthy;
}

module.exports = {
    askLlama,
    completeModeration,
    stopLlamaServer,
    aiConfigured: () => true,
    probeLlamaReady,
    registerWorker,
    correlateGiftQuestionWithLlm
};
