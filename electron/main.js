'use strict';

const { app, BrowserWindow, dialog } = require('electron');
const { spawn, spawnSync } = require('child_process');
const http = require('http');
const path = require('path');
const fs = require('fs');

const APP_ROOT = path.resolve(__dirname, '..');
const HOST = '127.0.0.1';
const PORT = Number(process.env.PORT || process.env.MONITOR_PORT || 3001);
const BASE_URL = `http://${HOST}:${PORT}`;

let serverProcess = null;
let mainWindow = null;
let reusedServer = false;
let quitting = false;

function binaryName() {
  return process.platform === 'win32' ? 'tiktok-live-monitor.exe' : 'tiktok-live-monitor';
}

function findBinary() {
  const candidate = path.join(APP_ROOT, binaryName());
  return fs.existsSync(candidate) ? candidate : null;
}

function buildBinary() {
  const result = spawnSync(process.env.GO || 'go', ['build', '-o', binaryName(), '.'], {
    cwd: APP_ROOT,
    stdio: 'inherit',
  });
  if (result.error || result.status !== 0) return null;
  return path.join(APP_ROOT, binaryName());
}

function isReady(url, timeoutMs = 1500) {
  return new Promise((resolve) => {
    const req = http.get(url, { timeout: timeoutMs }, (res) => {
      res.resume();
      resolve(res.statusCode === 200);
    });
    req.on('timeout', () => {
      req.destroy();
      resolve(false);
    });
    req.on('error', () => resolve(false));
  });
}

async function waitForReady(timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (await isReady(`${BASE_URL}/api/readiness`)) return true;
    await new Promise((resolve) => setTimeout(resolve, 400));
  }
  return false;
}

async function startServer() {
  if (await isReady(`${BASE_URL}/api/readiness`, 800)) {
    reusedServer = true;
    return;
  }

  const bin = findBinary() || buildBinary();
  if (!bin) {
    throw new Error('Servidor Go não encontrado e falha ao compilar com `go build`.');
  }

  await new Promise((resolve, reject) => {
    const child = spawn(bin, [], {
      cwd: APP_ROOT,
      env: { ...process.env, HOST, PORT: String(PORT) },
      stdio: ['ignore', 'pipe', 'pipe'],
      detached: process.platform !== 'win32',
    });

    let stderrTail = '';
    child.stdout.on('data', (chunk) => process.stdout.write(chunk));
    child.stderr.on('data', (chunk) => {
      stderrTail = (stderrTail + chunk.toString()).slice(-4000);
      process.stderr.write(chunk);
    });

    child.once('error', (err) => {
      serverProcess = null;
      reject(err);
    });

    child.once('exit', (code) => {
      if (serverProcess !== child) return;
      serverProcess = null;
      if (!quitting && !reusedServer) {
        dialog.showErrorBox(
          'Monitor de Live TikTok',
          `O servidor encerrou inesperadamente (código ${code}).\n\n${stderrTail}`
        );
      }
    });

    serverProcess = child;
    resolve();
  });

  const ready = await waitForReady(60000);
  if (!ready) {
    stopServer();
    throw new Error('O servidor não ficou pronto a tempo (verifique os logs).');
  }
}

function createWindow() {
  mainWindow = new BrowserWindow({
    width: 1280,
    height: 860,
    minWidth: 920,
    minHeight: 620,
    backgroundColor: '#0b0d12',
    autoHideMenuBar: true,
    title: 'Monitor de Live TikTok',
    webPreferences: {
      contextIsolation: true,
      nodeIntegration: false,
    },
  });
  mainWindow.loadURL(BASE_URL);
  mainWindow.on('closed', () => {
    mainWindow = null;
  });
}

function stopServer() {
  if (!serverProcess || reusedServer) return;
  quitting = true;
  const child = serverProcess;
  serverProcess = null;
  try {
    if (process.platform === 'win32') {
      child.kill();
    } else {
      process.kill(-child.pid, 'SIGTERM');
    }
  } catch {
    // grupo de processos já encerrado
  }
}

app.whenReady().then(async () => {
  try {
    await startServer();
  } catch (err) {
    const message = err && err.message ? err.message : String(err);
    dialog.showErrorBox('Monitor de Live TikTok', message);
    app.quit();
    return;
  }

  createWindow();

  app.on('activate', () => {
    if (BrowserWindow.getAllWindows().length === 0) createWindow();
  });
});

app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') app.quit();
});

app.on('before-quit', stopServer);
app.on('will-quit', stopServer);
