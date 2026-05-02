#!/usr/bin/env node
/**
 * Envia mensagens ao llama-server (OpenAI-compatible) e imprime a resposta.
 *
 * Uso:
 *   npm run chat-llm -- "Só Jesus salva"          (default: moderação religiosa SIM/NAO)
 *   npm run chat-llm -- -s "Seu system" -- "…"   (modo assistente genérico)
 *   LLAMA_PORT=8080 npm run chat-llm -- --max-tokens 50 -- "teste"   (-- não vai no user)
 *   echo "Olá" | npm run chat-llm --
 *
 * Env: LLAMA_HOST (default 127.0.0.1), LLAMA_PORT (default 8080)
 */

const http = require('http');
const { BINARY_RELIGIOUS_MODERATION_SYSTEM } = require('../moderation-prompt');
const { mergeChatCompletionBody, assistantTextFromChatResponse } = require('../ai');

const host = process.env.LLAMA_HOST || '127.0.0.1';
const port = Number(process.env.LLAMA_PORT) || 8080;

function parseArgs(argv) {
    let system = BINARY_RELIGIOUS_MODERATION_SYSTEM;
    let systemExplicit = false;
    let maxTokens = null;
    let temperature = null;
    let rawJson = false;
    let waitHealth = true;
    const rest = [];

    for (let i = 0; i < argv.length; i++) {
        const a = argv[i];
        if (a === '--system' || a === '-s') {
            system = argv[++i] ?? '';
            systemExplicit = true;
            continue;
        }
        if (a === '--max-tokens') {
            maxTokens = Number(argv[++i]);
            continue;
        }
        if (a === '--temperature' || a === '-t') {
            temperature = Number(argv[++i]);
            continue;
        }
        if (a === '--json') {
            rawJson = true;
            continue;
        }
        if (a === '--no-wait') {
            waitHealth = false;
            continue;
        }
        if (a === '--help' || a === '-h') {
            return { help: true };
        }
        /* fim de opções estilo GNU; senão "--" virava texto do user (ex.: resposta "-- Vcs estão...") */
        if (a === '--') {
            continue;
        }
        rest.push(a);
    }

    const user = rest.join(' ').trim();

    if (maxTokens == null || Number.isNaN(maxTokens)) {
        maxTokens = systemExplicit ? 256 : 32;
    }
    if (temperature == null || Number.isNaN(temperature)) {
        temperature = systemExplicit ? 0.7 : 0.1;
    }

    return { system, systemExplicit, maxTokens, temperature, rawJson, waitHealth, user };
}

function readStdin() {
    return new Promise((resolve, reject) => {
        let data = '';
        process.stdin.setEncoding('utf8');
        process.stdin.on('data', (chunk) => {
            data += chunk;
        });
        process.stdin.on('end', () => resolve(data.trim()));
        process.stdin.on('error', reject);
    });
}

function httpRequest(options, bodyStr) {
    return new Promise((resolve, reject) => {
        const req = http.request(options, (res) => {
            let buf = '';
            res.on('data', (c) => {
                buf += c;
            });
            res.on('end', () => {
                resolve({ statusCode: res.statusCode, body: buf });
            });
        });
        req.on('error', reject);
        req.on('timeout', () => {
            req.destroy();
            reject(new Error('timeout'));
        });
        req.setTimeout(300_000);
        if (bodyStr) req.write(bodyStr);
        req.end();
    });
}

async function waitForHealthReady(timeoutMs = 600_000) {
    const start = Date.now();
    while (Date.now() - start < timeoutMs) {
        try {
            const { statusCode, body } = await httpRequest(
                {
                    hostname: host,
                    port,
                    path: '/health',
                    method: 'GET'
                },
                null
            );
            if (statusCode === 200) return;
            const elapsed = Math.floor((Date.now() - start) / 1000);
            if (statusCode === 503 && elapsed > 0 && elapsed % 15 === 0) {
                process.stderr.write('[chat-llm] Modelo ainda carregando (503)…\n');
            }
        } catch {
            /* retry */
        }
        await new Promise((r) => setTimeout(r, 1000));
    }
    throw new Error(`llama-server em http://${host}:${port} não respondeu 200 em /health dentro do tempo.`);
}

async function main() {
    const opts = parseArgs(process.argv.slice(2));
    if (opts.help) {
        console.log(`
Uso:
  npm run chat-llm -- "texto do chat a classificar"
  npm run chat-llm -- -s "Você é um assistente útil." -- "pergunta livre"

Opções:
  -s, --system TEXT    Prompt de sistema (default: moderação religiosa SIM/NAO, igual ao app)
      --max-tokens N    Limite de tokens (default: 32 com moderação, 256 com -s)
  -t, --temperature N Temperatura (default: 0.1 com moderação, 0.7 com -s)
      --json            Imprime JSON completo da API
      --no-wait         Não espera /health == 200 antes do POST
  -h, --help            Esta ajuda

  Um "--" sozinho entre flags e a mensagem é ignorado (não entra no texto a classificar).

  Gemma 4 / modelos com thinking: o script envia enable_thinking:false; ainda assim use
  --max-tokens 32 (ou mais) se a saída vier vazia com limite muito baixo.

Variáveis de ambiente:
  LLAMA_HOST   (default: 127.0.0.1)
  LLAMA_PORT   (default: 8080)
`);
        process.exit(0);
    }

    let userText = opts.user;
    if (!userText && !process.stdin.isTTY) {
        userText = await readStdin();
    }
    if (!userText) {
        console.error('Erro: passe a mensagem como argumento ou via stdin.');
        console.error('Ex.: npm run chat-llm -- "Olá"');
        process.exit(1);
    }

    if (opts.waitHealth) {
        process.stderr.write(`[chat-llm] Aguardando ${host}:${port}/health …\n`);
        await waitForHealthReady();
    }

    const userContent = opts.systemExplicit
        ? userText
        : `Texto para analisar:\n${JSON.stringify(userText)}`;

    const payload = mergeChatCompletionBody({
        messages: [
            { role: 'system', content: opts.system },
            { role: 'user', content: userContent }
        ],
        temperature: opts.temperature,
        max_tokens: opts.maxTokens
    });

    const data = JSON.stringify(payload);
    process.stderr.write(`[chat-llm] POST /v1/chat/completions (inferência pode demorar no CPU)…\n`);

    const { statusCode, body } = await httpRequest(
        {
            hostname: host,
            port,
            path: '/v1/chat/completions',
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
                'Content-Length': Buffer.byteLength(data)
            }
        },
        data
    );

    if (opts.rawJson) {
        console.log(body);
        process.exit(statusCode >= 200 && statusCode < 300 ? 0 : 1);
    }

    if (statusCode < 200 || statusCode >= 300) {
        console.error(`Erro HTTP ${statusCode}:`, body.slice(0, 2000));
        process.exit(1);
    }

    let json;
    try {
        json = JSON.parse(body);
    } catch (e) {
        console.error('Resposta não é JSON:', body.slice(0, 500));
        process.exit(1);
    }

    const text = assistantTextFromChatResponse(json);
    if (text == null) {
        console.error(
            'Resposta vazia (content + reasoning). Com Gemma 4 / thinking, use --max-tokens 32 ou mais. Corpo:',
            JSON.stringify(json, null, 2).slice(0, 3000)
        );
        process.exit(1);
    }

    console.log(text.trim());
}

main().catch((err) => {
    console.error('[chat-llm]', err.message || err);
    process.exit(1);
});
