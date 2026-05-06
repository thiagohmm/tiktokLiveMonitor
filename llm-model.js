const fs = require('fs');
const path = require('path');

const CONFIG_PATH = path.join(__dirname, 'model-config.json');

const MODELS = {
    'gemma-4b': {
        name: 'Gemma 4B (Padrão)',
        filename: 'google_gemma-4-E4B-it-Q4_K_M.gguf',
        url: 'https://huggingface.co/bartowski/google_gemma-4-E4B-it-GGUF/resolve/main/google_gemma-4-E4B-it-Q4_K_M.gguf',
        dockerFilename: 'google_gemma-4-E2B-it-Q4_K_M.gguf',
        dockerUrl: 'https://huggingface.co/bartowski/google_gemma-4-E2B-it-GGUF/resolve/main/google_gemma-4-E2B-it-Q4_K_M.gguf'
    },
    'llama-3.2-3b': {
        name: 'Llama 3.2 (3B Instruct)',
        filename: 'Llama-3.2-3B-Instruct-Q4_K_M.gguf',
        url: 'https://huggingface.co/bartowski/Llama-3.2-3B-Instruct-GGUF/resolve/main/Llama-3.2-3B-Instruct-Q4_K_M.gguf'
    }
};

function getConfig() {
    try {
        if (fs.existsSync(CONFIG_PATH)) {
            return JSON.parse(fs.readFileSync(CONFIG_PATH, 'utf8'));
        }
    } catch (e) {
        console.error('Erro ao ler model-config.json:', e);
    }
    return { selectedModel: 'gemma-4b' };
}

function saveConfig(config) {
    try {
        fs.writeFileSync(CONFIG_PATH, JSON.stringify(config, null, 2));
    } catch (e) {
        console.error('Erro ao salvar model-config.json:', e);
    }
}

const config = getConfig();
const isDocker = fs.existsSync('/.dockerenv');
const selectedKey = config.selectedModel || 'gemma-4b';
const modelInfo = MODELS[selectedKey] || MODELS['gemma-4b'];

const finalFilename = (isDocker && modelInfo.dockerFilename) ? modelInfo.dockerFilename : modelInfo.filename;
const finalUrl = (isDocker && modelInfo.dockerUrl) ? modelInfo.dockerUrl : modelInfo.url;

module.exports = {
    GGUF_FILENAME: finalFilename,
    DOWNLOAD_URL: finalUrl,
    MODELS,
    getSelectedModel: () => selectedKey,
    setSelectedModel: (key) => {
        if (MODELS[key]) {
            saveConfig({ selectedModel: key });
            return true;
        }
        return false;
    }
};
