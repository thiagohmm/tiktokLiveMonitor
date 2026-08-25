'use strict';

/**
 * Instalação completa do runtime desktop:
 * 1) CPython standalone + deps do agente (sem Python do sistema)
 * 2) GGUF + llama-server (scripts/setup-llm.js)
 */

const { spawnSync } = require('child_process');
const path = require('path');

const root = path.join(__dirname, '..');

function run(script, args = []) {
    const r = spawnSync(process.execPath, [path.join(__dirname, script), ...args], {
        cwd: root,
        stdio: 'inherit',
        env: process.env,
    });
    if (r.error) throw r.error;
    if (r.status !== 0) {
        process.exit(r.status || 1);
    }
}

const osArg = process.argv[2];
const archArg = process.argv[3];
const extra = [];
if (osArg) extra.push(osArg);
if (archArg) extra.push(archArg);

console.log('[Setup] 1/2 runtime Python embutido...');
run('setup-python.js', extra);
console.log('[Setup] 2/2 modelos LLM + llama-server...');
run('setup-llm.js', extra);
console.log('[Setup] Concluído.');
