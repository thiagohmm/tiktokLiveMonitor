const { app, BrowserWindow, ipcMain } = require('electron');
const path = require('path');
const fs = require('fs');
const { spawn } = require('child_process');
const monitor = require('./monitor');
const { addFeedback } = require('./database');
const { probeLlamaReady, aiConfigured, stopLlamaServer } = require('./ai');
const llmModel = require('./llm-model');

// Inicia o servidor Express em paralelo
require('./server');

let mainWindow;

function createWindow() {
    mainWindow = new BrowserWindow({
        width: 1200,
        height: 900,
        title: "TikTok Live Monitor",
        webPreferences: {
            nodeIntegration: true,
            contextIsolation: false
        }
    });

    // Carrega a URL do servidor Express com retry caso o servidor ainda não esteja pronto
    const loadUrl = () => {
        mainWindow.loadURL('http://localhost:3000').catch(err => {
            console.log('Servidor ainda não pronto, tentando novamente em 200ms...');
            setTimeout(loadUrl, 200);
        });
    };
    
    loadUrl();

    mainWindow.on('closed', () => {
        mainWindow = null;
    });
}

app.whenReady().then(createWindow);

app.on('window-all-closed', () => {
    if (process.platform !== 'darwin') {
        app.quit();
    }
});

app.on('activate', () => {
    if (mainWindow === null) {
        createWindow();
    }
});

// Encaminha eventos do monitor para o renderer via IPC
monitor.onEvent((type, data) => {
    if (mainWindow && mainWindow.webContents) {
        mainWindow.webContents.send(type, data);
    }
});

// Handlers IPC solicitados pelo renderer.js
ipcMain.on('connect-tiktok', async (event, username) => {
    try {
        await monitor.startMonitoring(username);
    } catch (err) {
        console.error('Erro ao conectar TikTok via IPC:', err);
    }
});

ipcMain.on('disconnect-tiktok', () => {
    monitor.stopMonitoring();
});

ipcMain.on('send-feedback', async (event, data) => {
    try {
        await addFeedback(data.comment, data.category, data.expected);
    } catch (err) {
        console.error('Erro ao salvar feedback via IPC:', err);
    }
});

ipcMain.handle('probe-llm', async () => {
    try {
        const ready = await probeLlamaReady();
        return { llmActive: ready };
    } catch (err) {
        return { llmActive: false, error: err.message };
    }
});

ipcMain.handle('get-ui-config', () => {
    return {
        isElectron: true,
        geminiConfigured: aiConfigured(),
        selectedModel: llmModel.getSelectedModel(),
        models: llmModel.MODELS
    };
});

ipcMain.handle('change-model', async (event, modelKey) => {
    const success = llmModel.setSelectedModel(modelKey);
    if (success) {
        stopLlamaServer(); // Força reinício com novo modelo
    }
    return success;
});

ipcMain.handle('run-setup', async (event) => {
    return new Promise((resolve) => {
        let baseDir = __dirname;
        if (process.resourcesPath && fs.existsSync(path.join(process.resourcesPath, 'scripts'))) {
            baseDir = process.resourcesPath;
        }
        const setupPath = path.join(baseDir, 'scripts', 'setup-llm.js');
        
        const child = spawn('node', [setupPath], { stdio: ['inherit', 'pipe', 'inherit'] });
        
        child.stdout.on('data', (data) => {
            const lines = data.toString().split('\n');
            for (const line of lines) {
                if (!line.trim()) continue;
                try {
                    const json = JSON.parse(line);
                    if (json.type === 'progress') {
                        if (mainWindow) mainWindow.webContents.send('setup-progress', json);
                    }
                } catch (e) {
                    console.log(`[Setup] ${line}`);
                }
            }
        });

        child.on('close', (code) => {
            resolve(code === 0);
        });
    });
});
