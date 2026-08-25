'use strict';

/**
 * Bootstrap de CPython standalone + deps do agente.
 * Não exige Python instalado no sistema: baixa python-build-standalone
 * e instala requirements.txt com o pip embutido.
 */

const fs = require('fs');
const path = require('path');
const https = require('https');
const { execFileSync } = require('child_process');
const tar = require('tar');

const ROOT = path.join(__dirname, '..');
const RUNTIME_DIR = path.join(ROOT, 'runtime');
const PYTHON_DIR = path.join(RUNTIME_DIR, 'python');
const REQUIREMENTS = path.join(ROOT, 'requirements.txt');

/** Release pinada do astral/python-build-standalone (CPython 3.12). */
const PBS_TAG = '20250317';
const PBS_VERSION = '3.12.9';

const MARKER = path.join(PYTHON_DIR, '.tiktok-agent-deps-ok');

function normalizeArch(arch) {
    const a = String(arch || '').toLowerCase();
    if (a === 'amd64' || a === 'x86_64') return 'x64';
    if (a === 'aarch64') return 'arm64';
    return a;
}

function platformTriple(osRaw, archRaw) {
    const os = String(osRaw || process.platform).toLowerCase();
    const arch = normalizeArch(archRaw || process.arch);

    if (os === 'darwin' || os === 'mac') {
        return arch === 'arm64'
            ? 'aarch64-apple-darwin'
            : 'x86_64-apple-darwin';
    }
    if (os === 'linux') {
        return arch === 'arm64'
            ? 'aarch64-unknown-linux-gnu'
            : 'x86_64-unknown-linux-gnu';
    }
    if (os === 'win32' || os === 'win' || os === 'windows') {
        if (arch === 'arm64') {
            return 'aarch64-pc-windows-msvc';
        }
        return 'x86_64-pc-windows-msvc';
    }
    throw new Error(`SO não suportado para runtime Python: ${osRaw}`);
}

function artifactURL(osRaw, archRaw) {
    const triple = platformTriple(osRaw, archRaw);
    const name = `cpython-${PBS_VERSION}+${PBS_TAG}-${triple}-install_only_stripped.tar.gz`;
    return {
        name,
        url: `https://github.com/astral-sh/python-build-standalone/releases/download/${PBS_TAG}/${name}`,
    };
}

function resolvePythonBin(pythonRoot = PYTHON_DIR) {
    const candidates = [
        path.join(pythonRoot, 'python.exe'),
        path.join(pythonRoot, 'bin', 'python3'),
        path.join(pythonRoot, 'bin', 'python'),
    ];
    for (const c of candidates) {
        if (fs.existsSync(c)) return c;
    }
    return null;
}

function downloadFile(url, dest) {
    return new Promise((resolve, reject) => {
        https
            .get(url, (response) => {
                if (
                    response.statusCode === 301 ||
                    response.statusCode === 302 ||
                    response.statusCode === 303 ||
                    response.statusCode === 307 ||
                    response.statusCode === 308
                ) {
                    const loc = response.headers.location;
                    if (!loc) {
                        reject(new Error('Redirect sem Location'));
                        return;
                    }
                    const next = loc.startsWith('http') ? loc : new URL(loc, url).href;
                    downloadFile(next, dest).then(resolve).catch(reject);
                    return;
                }
                if (response.statusCode !== 200) {
                    reject(new Error(`Falha ao baixar (${response.statusCode}): ${url}`));
                    return;
                }
                const total = parseInt(response.headers['content-length'], 10);
                let done = 0;
                let last = -1;
                const file = fs.createWriteStream(dest);
                response.on('data', (chunk) => {
                    done += chunk.length;
                    if (total > 0) {
                        const pct = Math.floor((done / total) * 100);
                        if (pct !== last) {
                            last = pct;
                            console.log(JSON.stringify({ type: 'progress', filename: path.basename(dest), progress: pct }));
                        }
                    }
                });
                response.pipe(file);
                file.on('finish', () => {
                    file.close((err) => (err ? reject(err) : resolve()));
                });
                file.on('error', (err) => {
                    fs.unlink(dest, () => {});
                    reject(err);
                });
            })
            .on('error', (err) => {
                fs.unlink(dest, () => {});
                reject(err);
            });
    });
}

function depsReady(pythonBin) {
    if (!pythonBin || !fs.existsSync(MARKER)) return false;
    try {
        execFileSync(
            pythonBin,
            ['-c', 'import fastapi, uvicorn, httpx, fastembed'],
            { stdio: 'ignore', timeout: 60000 }
        );
        return true;
    } catch {
        return false;
    }
}

async function ensurePython(osRaw, archRaw) {
    fs.mkdirSync(RUNTIME_DIR, { recursive: true });

    let pythonBin = resolvePythonBin();
    if (pythonBin && depsReady(pythonBin)) {
        console.log(`[Setup-Python] Runtime pronto: ${pythonBin}`);
        return pythonBin;
    }

    if (!pythonBin) {
        const { name, url } = artifactURL(osRaw, archRaw);
        const archive = path.join(RUNTIME_DIR, name);
        console.log(`[Setup-Python] Baixando CPython standalone (${name})...`);
        await downloadFile(url, archive);

        // Remove instalação anterior incompleta.
        fs.rmSync(PYTHON_DIR, { recursive: true, force: true });
        fs.rmSync(path.join(RUNTIME_DIR, 'python'), { recursive: true, force: true });

        console.log('[Setup-Python] Extraindo runtime...');
        await tar.extract({ file: archive, cwd: RUNTIME_DIR });

        // install_only extrai pasta top-level "python/"
        if (!fs.existsSync(PYTHON_DIR)) {
            throw new Error(`Extração não criou ${PYTHON_DIR}`);
        }
        pythonBin = resolvePythonBin();
        if (!pythonBin) {
            throw new Error('python não encontrado após extração do standalone');
        }
        try {
            fs.unlinkSync(archive);
        } catch {
            /* ignore */
        }
    }

    console.log(`[Setup-Python] Instalando dependências do agente (${REQUIREMENTS})...`);
    execFileSync(pythonBin, ['-m', 'pip', 'install', '--upgrade', 'pip'], {
        stdio: 'inherit',
        cwd: ROOT,
    });
    execFileSync(pythonBin, ['-m', 'pip', 'install', '-r', REQUIREMENTS], {
        stdio: 'inherit',
        cwd: ROOT,
    });

    // Valida imports críticos do agente.
    execFileSync(pythonBin, ['-c', 'import fastapi, uvicorn, httpx, fastembed'], {
        stdio: 'inherit',
        cwd: ROOT,
        timeout: 120000,
    });
    fs.writeFileSync(MARKER, `${new Date().toISOString()}\n`, 'utf8');
    console.log(`[Setup-Python] OK: ${pythonBin}`);
    return pythonBin;
}

async function main() {
    const osRaw = process.argv[2] || process.platform;
    const archRaw = process.argv[3] || process.arch;
    try {
        await ensurePython(osRaw, archRaw);
    } catch (err) {
        console.error('[Setup-Python] Falha:', err && err.message ? err.message : err);
        process.exit(1);
    }
}

if (require.main === module) {
    main();
}

module.exports = {
    ensurePython,
    resolvePythonBin,
    depsReady,
    PYTHON_DIR,
    RUNTIME_DIR,
};
