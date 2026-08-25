const fs = require('fs');
const path = require('path');
const os = require('os');
const https = require('https');
const { execSync, execFileSync } = require('child_process');
const unzipper = require('unzipper');
const tar = require('tar');

// Registro espelhando internal/config/config.go (var Models).
const MODELS = {
    'gemma-4b': {
        filename: 'gemma-4-E4B-it-Q4_K_M.gguf',
        url: 'https://huggingface.co/unsloth/gemma-4-E4B-it-GGUF/resolve/main/gemma-4-E4B-it-Q4_K_M.gguf',
    },
    'llama-3.2-3b': {
        filename: 'Llama-3.2-3B-Instruct-Q4_K_M.gguf',
        url: 'https://huggingface.co/bartowski/Llama-3.2-3B-Instruct-GGUF/resolve/main/Llama-3.2-3B-Instruct-Q4_K_M.gguf',
    },
};
const DEFAULT_MODEL_KEY = 'gemma-4b';

/** Mesma versão em todas as plataformas (ubuntu-arm64 só existe a partir de builds recentes). */
const LLAMA_CPP_RELEASE_TAG = 'b8999';
const LLAMA_CPP_REPO = 'ggml-org/llama.cpp';
const LLAMA_CPP_SOURCE_URL = `https://github.com/${LLAMA_CPP_REPO}/archive/refs/tags/${LLAMA_CPP_RELEASE_TAG}.tar.gz`;

const MODELS_DIR = path.join(__dirname, '..', 'models');
const BIN_DIR = path.join(__dirname, '..', 'bin');

function hasGgufMagic(filePath) {
    const fd = fs.openSync(filePath, 'r');
    try {
        const buf = Buffer.alloc(4);
        const n = fs.readSync(fd, buf, 0, 4, 0);
        return n === 4 && buf.toString('ascii') === 'GGUF';
    } finally {
        fs.closeSync(fd);
    }
}

/** Re-download se ficou vazio (bug redirect antigo) ou GGUF quebrado */
function removeBadExistingFile(dest) {
    if (!fs.existsSync(dest)) return false;
    const stat = fs.statSync(dest);
    const base = path.basename(dest);
    if (stat.size === 0) {
        fs.unlinkSync(dest);
        console.log(`[Setup] Removendo arquivo vazio (re-download): ${base}`);
        return true;
    }
    if (/\.gguf$/i.test(dest)) {
        try {
            if (!hasGgufMagic(dest)) {
                fs.unlinkSync(dest);
                console.log(`[Setup] Removendo GGUF inválido (re-download): ${base}`);
                return true;
            }
        } catch {
            fs.unlinkSync(dest);
            console.log(`[Setup] Removendo GGUF ilegível (re-download): ${base}`);
            return true;
        }
    }
    return false;
}

/** Segue redirects via HEAD e retorna o Content-Length esperado do arquivo (ou null se indeterminado). */
function getContentLength(url, maxHops = 5) {
    return new Promise((resolve) => {
        const hop = (currentUrl, hops) => {
            if (hops <= 0) return resolve(null);
            https
                .request(currentUrl, { method: 'HEAD' }, (response) => {
                    if (
                        response.statusCode === 301 || response.statusCode === 302 ||
                        response.statusCode === 303 || response.statusCode === 307 ||
                        response.statusCode === 308
                    ) {
                        const loc = response.headers.location;
                        if (!loc) return resolve(null);
                        const next = loc.startsWith('http') ? loc : new URL(loc, currentUrl).href;
                        return hop(next, hops - 1);
                    }
                    const len = parseInt(response.headers['content-length'], 10);
                    resolve(Number.isFinite(len) && len > 0 ? len : null);
                })
                .on('error', () => resolve(null))
                .end();
        };
        hop(url, maxHops);
    });
}

async function downloadFile(url, dest) {
    removeBadExistingFile(dest);
    // Detecta download truncado/corrompido: se o arquivo local já existe mas tem
    // tamanho diferente do esperado no servidor, remove e re-baixa.
    const expected = await getContentLength(url);
    if (fs.existsSync(dest) && expected !== null) {
        const actual = fs.statSync(dest).size;
        if (actual !== expected) {
            fs.unlinkSync(dest);
            console.log(`[Setup] Tamanho incorreto (${actual} != ${expected}); removendo para re-download: ${path.basename(dest)}`);
        }
    }
    if (fs.existsSync(dest)) {
        console.log(`[Setup] Arquivo já existe: ${path.basename(dest)}`);
        return;
    }

    console.log(`[Setup] Baixando: ${url} -> ${dest}`);
    return new Promise((resolve, reject) => {
        https
            .get(url, (response) => {
                if (response.statusCode === 302 || response.statusCode === 301) {
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
                    reject(new Error(`Falha ao baixar: ${response.statusCode}`));
                    return;
                }

                const totalSize = parseInt(response.headers['content-length'], 10);
                let downloadedSize = 0;
                let lastReportedProgress = -1;

                const file = fs.createWriteStream(dest);
                response.on('data', (chunk) => {
                    downloadedSize += chunk.length;
                    if (totalSize > 0) {
                        const progress = Math.floor((downloadedSize / totalSize) * 100);
                        if (progress !== lastReportedProgress) {
                            lastReportedProgress = progress;
                            console.log(JSON.stringify({ type: 'progress', filename: path.basename(dest), progress }));
                        }
                    }
                });

                response.pipe(file);
                file.on('finish', () => {
                    file.close();
                    if (totalSize > 0 && downloadedSize !== totalSize) {
                        fs.unlink(dest, () => {});
                        reject(new Error(`Download incompleto: ${downloadedSize}/${totalSize} bytes`));
                        return;
                    }
                    console.log(`[Setup] Download concluído: ${path.basename(dest)}`);
                    resolve();
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

function normalizeArch(arch) {
    const a = String(arch || '').toLowerCase();
    if (a === 'amd64' || a === 'x86_64') return 'x64';
    if (a === 'aarch64') return 'arm64';
    return a;
}

function normalizeOs(os) {
    const o = String(os || '').toLowerCase();
    if (o === 'windows' || o === 'win32') return 'win';
    return o;
}

function getLlamaArtifact(osRaw, archRaw) {
    const os = normalizeOs(osRaw);
    const arch = normalizeArch(archRaw);
    const tag = LLAMA_CPP_RELEASE_TAG;
    const base = `https://github.com/${LLAMA_CPP_REPO}/releases/download/${tag}`;

    if (os === 'win') {
        if (arch === 'arm64') {
            return { url: `${base}/llama-${tag}-bin-win-cpu-arm64.zip`, ext: 'zip' };
        }
        if (arch === 'x64') {
            return { url: `${base}/llama-${tag}-bin-win-vulkan-x64.zip`, ext: 'zip' };
        }
        throw new Error(`Windows: use arch x64 ou arm64 (recebido: ${archRaw}).`);
    }

    if (os === 'mac') {
        const m = arch === 'arm64' ? 'arm64' : 'x64';
        return { url: `${base}/llama-${tag}-bin-macos-${m}.tar.gz`, ext: 'tar.gz' };
    }

    if (os === 'linux') {
        if (arch === 'x64' || arch === 'arm64') {
            return { url: `${base}/llama-${tag}-bin-ubuntu-${arch}.tar.gz`, ext: 'tar.gz' };
        }
        throw new Error(
            `Linux: arch não suportada (${archRaw}). Raspberry Pi: use Pi OS 64-bit (aarch64/arm64), não 32-bit.`
        );
    }

    throw new Error(`SO não suportado: ${osRaw}`);
}

async function extractArtifact(archivePath, ext, targetBinDir) {
    await fs.promises.mkdir(targetBinDir, { recursive: true });
    if (ext === 'zip') {
        await fs.createReadStream(archivePath).pipe(unzipper.Extract({ path: targetBinDir })).promise();
        return;
    }
    if (ext === 'tar.gz') {
        await tar.extract({ cwd: targetBinDir, file: archivePath });
        return;
    }
    throw new Error(`Formato de arquivo não suportado: ${ext}`);
}

function chmodLlamaBinaries(targetBinDir, targetOs) {
    if (targetOs === 'win') return;
    const touch = (abs) => {
        try {
            if (fs.existsSync(abs)) execSync(`chmod +x "${abs}"`, { stdio: 'ignore' });
        } catch {
            /* ignore */
        }
    };
    touch(path.join(targetBinDir, 'llama-server'));
    touch(path.join(targetBinDir, 'build', 'bin', 'llama-server'));
    try {
        const dirs = fs.readdirSync(targetBinDir, { withFileTypes: true });
        for (const d of dirs) {
            if (!d.isDirectory() || !/^llama-b\d+$/i.test(d.name)) continue;
            touch(path.join(targetBinDir, d.name, 'llama-server'));
        }
    } catch {
        /* ignore */
    }
}

function resolveExistingLlamaBinary(targetBinDir) {
    const candidates = [
        path.join(targetBinDir, 'llama-server'),
        path.join(targetBinDir, 'build', 'bin', 'llama-server'),
    ];
    for (const candidate of candidates) {
        if (fs.existsSync(candidate)) return candidate;
    }
    try {
        const dirs = fs.readdirSync(targetBinDir, { withFileTypes: true });
        for (const d of dirs) {
            if (!d.isDirectory() || !/^llama-b\d+$/i.test(d.name)) continue;
            const nested = path.join(targetBinDir, d.name, 'llama-server');
            if (fs.existsSync(nested)) return nested;
        }
    } catch {
        /* ignore */
    }
    return null;
}

function isUsableLlamaBinary(binPath) {
    if (!binPath) return false;
    try {
        execFileSync(binPath, ['--help'], { stdio: 'ignore', timeout: 5000 });
        return true;
    } catch {
        return false;
    }
}

async function buildLlamaFromSource(targetBinDir) {
    const sourceArchive = path.join(BIN_DIR, `llama-src-${LLAMA_CPP_RELEASE_TAG}.tar.gz`);
    const sourceDir = path.join(BIN_DIR, `llama-src-${LLAMA_CPP_RELEASE_TAG}`);
    const buildDir = path.join(sourceDir, 'build');

    fs.rmSync(sourceDir, { recursive: true, force: true });
    fs.mkdirSync(sourceDir, { recursive: true });

    await downloadFile(LLAMA_CPP_SOURCE_URL, sourceArchive);

    console.log('[Setup] Extraindo fontes do llama.cpp...');
    execFileSync('tar', ['-xzf', sourceArchive, '-C', sourceDir, '--strip-components=1'], { stdio: 'inherit' });

    console.log('[Setup] Configurando build local do llama-server...');
    execFileSync(
        'cmake',
        [
            '-S', sourceDir,
            '-B', buildDir,
            '-DCMAKE_BUILD_TYPE=Release',
            '-DGGML_NATIVE=ON',
            '-DLLAMA_BUILD_SERVER=ON',
            '-DLLAMA_BUILD_EXAMPLES=OFF',
            '-DLLAMA_BUILD_TESTS=OFF',
            '-DLLAMA_BUILD_TOOLS=ON',
        ],
        { stdio: 'inherit' }
    );

    console.log('[Setup] Compilando llama-server localmente...');
    execFileSync(
        'cmake',
        ['--build', buildDir, '--config', 'Release', '-j', String(Math.max(1, os.cpus().length))],
        { stdio: 'inherit' }
    );

    fs.rmSync(targetBinDir, { recursive: true, force: true });
    fs.mkdirSync(targetBinDir, { recursive: true });
    fs.cpSync(path.join(buildDir, 'bin'), targetBinDir, { recursive: true, force: true });
    chmodLlamaBinaries(targetBinDir, 'linux');
}

async function setup() {
    const rawOs = process.argv[2] || (process.platform === 'win32' ? 'win' : process.platform === 'darwin' ? 'mac' : 'linux');
    const rawArch = process.argv[3] || process.arch;
    const targetOs = normalizeOs(rawOs);
    const targetArch = normalizeArch(rawArch);

    console.log(`[Setup] Alvo: ${targetOs} (${targetArch})`);

    if (!fs.existsSync(MODELS_DIR)) fs.mkdirSync(MODELS_DIR, { recursive: true });
    if (!fs.existsSync(BIN_DIR)) fs.mkdirSync(BIN_DIR, { recursive: true });

    // Modelo selecionado em model-config.json (mesmo arquivo lido pelo Go).
    let modelKey = DEFAULT_MODEL_KEY;
    try {
        const cfg = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'model-config.json'), 'utf8'));
        if (cfg.selectedModel && MODELS[cfg.selectedModel]) modelKey = cfg.selectedModel;
    } catch { /* usa o padrão */ }
    const info = MODELS[modelKey];
    const GGUF_FILENAME = info.filename;
    const DOWNLOAD_URL = info.url;

    const modelDest = path.join(MODELS_DIR, GGUF_FILENAME);
    await downloadFile(DOWNLOAD_URL, modelDest);

    const existingBinary = resolveExistingLlamaBinary(path.join(BIN_DIR, targetOs, targetArch));
    if (targetOs === 'linux' && targetArch === 'arm64') {
        if (isUsableLlamaBinary(existingBinary)) {
            console.log(`[Setup] llama-server já funcional: ${existingBinary}`);
            return;
        }

        console.log('[Setup] Binário pré-compilado incompatível ou ausente; compilando localmente para ARM64.');
        try {
            await buildLlamaFromSource(path.join(BIN_DIR, targetOs, targetArch));
        } catch (err) {
            console.error(`[Setup] Falha ao compilar llama-server localmente: ${err.message}`);
            process.exit(1);
        }
        return;
    }

    let artifact;
    try {
        artifact = getLlamaArtifact(targetOs, targetArch);
    } catch (err) {
        console.warn(`[Setup] ${err.message}`);
        console.warn('[Setup] Download do llama-server ignorado nesta máquina.');
        return;
    }

    const archiveDest = path.join(BIN_DIR, `llama-bin-${targetOs}-${targetArch}.${artifact.ext}`);
    const targetBinDir = path.join(BIN_DIR, targetOs, targetArch);

    try {
        // Binários geralmente já existem se rodando via setup inicial, 
        // mas o modelo pode ser trocado.
        if (!fs.existsSync(targetBinDir)) {
            await downloadFile(artifact.url, archiveDest);
            console.log(`[Setup] Extraindo binários (${artifact.ext})...`);
            fs.rmSync(targetBinDir, { recursive: true, force: true });
            fs.mkdirSync(targetBinDir, { recursive: true });
            await extractArtifact(archiveDest, artifact.ext, targetBinDir);
            console.log(`[Setup] Binários extraídos em: ${targetBinDir}`);
            chmodLlamaBinaries(targetBinDir, targetOs);
        }
    } catch (err) {
        console.error(`[Setup] Erro ao baixar/extrair binário do llama.cpp: ${err.message}`);
        console.log(`[Setup] DICA: Confira se o asset existe em https://github.com/${LLAMA_CPP_REPO}/releases/tag/${LLAMA_CPP_RELEASE_TAG}`);
    }
}

setup().catch((err) => {
    console.error(`[Setup] Falha crítica:`, err);
    process.exit(1);
});
