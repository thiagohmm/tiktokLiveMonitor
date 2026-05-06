const fs = require('fs');
const path = require('path');
const https = require('https');
const { execSync } = require('child_process');
const unzipper = require('unzipper');
const tar = require('tar');

const { GGUF_FILENAME, DOWNLOAD_URL } = require('../llm-model');

/** Mesma versão em todas as plataformas (ubuntu-arm64 só existe a partir de builds recentes). */
const LLAMA_CPP_RELEASE_TAG = 'b8999';
const LLAMA_CPP_REPO = 'ggml-org/llama.cpp';

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

async function downloadFile(url, dest) {
    removeBadExistingFile(dest);
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

async function setup() {
    const rawOs = process.argv[2] || (process.platform === 'win32' ? 'win' : process.platform === 'darwin' ? 'mac' : 'linux');
    const rawArch = process.argv[3] || process.arch;
    const targetOs = normalizeOs(rawOs);
    const targetArch = normalizeArch(rawArch);

    console.log(`[Setup] Alvo: ${targetOs} (${targetArch})`);

    if (!fs.existsSync(MODELS_DIR)) fs.mkdirSync(MODELS_DIR, { recursive: true });
    if (!fs.existsSync(BIN_DIR)) fs.mkdirSync(BIN_DIR, { recursive: true });

    const modelDest = path.join(MODELS_DIR, GGUF_FILENAME);
    await downloadFile(DOWNLOAD_URL, modelDest);

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
