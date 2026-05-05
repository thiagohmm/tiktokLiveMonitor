const express = require('express');
const http = require('http');
const path = require('path');
const fs = require('fs');
const { 
    startMonitoring, 
    stopMonitoring, 
    getState, 
    onEvent, 
    setSettings, 
    getSettings,
    forceCheck
} = require('./main');
const { getRecentModerations, clearHistory, deleteModeration, addFeedback } = require('./database');
const { probeLlamaReady, aiConfigured } = require('./ai');

const app = express();
const server = http.createServer(app);
const PORT = process.env.PORT || 3000;
const HOST = process.env.HOST || '0.0.0.0'; // Garante escuta em todas interfaces no Docker

app.use(express.json());
app.use(express.static(__dirname));

// Rota para o Chart.js (UMD build para o navegador)
app.get('/vendor/chart.js', (req, res) => {
    res.sendFile(path.join(__dirname, 'node_modules', 'chart.js', 'dist', 'chart.umd.js'));
});

// SSE: Server-Sent Events para atualizar a UI em tempo real
const sseClients = new Set();

app.get('/events', (req, res) => {
    res.setHeader('Content-Type', 'text/event-stream');
    res.setHeader('Cache-Control', 'no-cache');
    res.setHeader('Connection', 'keep-alive');
    res.flushHeaders();

    // Enviar estado inicial para o cliente que acabou de conectar
    const state = getState();
    const initialState = {
        ...state,
        aiConfigured: aiConfigured()
    };
    res.write(`event: server-state\ndata: ${JSON.stringify(initialState)}\n\n`);

    sseClients.add(res);
    req.on('close', () => sseClients.delete(res));
});

function broadcast(type, data) {
    const payload = JSON.stringify(data);
    for (const client of sseClients) {
        // SSE format: event: type\ndata: payload\n\n
        client.write(`event: ${type}\n`);
        client.write(`data: ${payload}\n\n`);
    }
}

// Escutar eventos do monitor
onEvent((type, data) => broadcast(type, data));

// Endpoints
app.get('/api/state', (req, res) => res.json(getState()));
app.get('/api/settings', (req, res) => res.json(getSettings()));
app.get('/api/history', async (req, res) => {
    try {
        const history = await getRecentModerations(100);
        res.json(history);
    } catch (err) {
        res.status(500).json({ error: err.message });
    }
});

app.post('/api/connect', async (req, res) => {
    const { username } = req.body;
    if (!username) return res.status(400).json({ error: 'Username is required' });
    
    try {
        await startMonitoring(username);
        res.json({ success: true });
    } catch (err) {
        res.status(500).json({ error: err.message });
    }
});

app.post('/api/disconnect', (req, res) => {
    stopMonitoring();
    res.json({ success: true });
});

app.post('/api/settings', (req, res) => {
    setSettings(req.body);
    res.json({ success: true });
});

app.post('/api/clear-history', async (req, res) => {
    try {
        await clearHistory();
        res.json({ success: true });
    } catch (err) {
        res.status(500).json({ error: err.message });
    }
});

app.delete('/api/history/:id', async (req, res) => {
    try {
        await deleteModeration(req.params.id);
        res.json({ success: true });
    } catch (err) {
        res.status(500).json({ error: err.message });
    }
});

app.post('/api/force-check', async (req, res) => {
    try {
        await forceCheck();
        res.json({ success: true });
    } catch (err) {
        res.status(500).json({ error: err.message });
    }
});

app.post('/api/feedback', async (req, res) => {
    const { comment, category, expected } = req.body;
    try {
        await addFeedback(comment, category, expected);
        res.json({ success: true });
    } catch (err) {
        res.status(500).json({ error: err.message });
    }
});

app.get('/api/probe-llm', async (req, res) => {
    try {
        const ready = await probeLlamaReady();
        res.json({ llmActive: ready });
    } catch (err) {
        res.json({ llmActive: false, error: err.message });
    }
});

server.listen(PORT, HOST, () => {
    console.log(`Server running at http://${HOST}:${PORT}`);
});

function shutdownServer() {
    console.log('Shutting down server...');
    stopMonitoring();
    
    for (const res of [...sseClients]) {
        try {
            res.end();
        } catch {
            /* ignore */
        }
    }
    sseClients.clear();

    server.close(() => process.exit(0));

    if (typeof server.closeAllConnections === 'function') {
        server.closeAllConnections();
    }

    setTimeout(() => process.exit(0), 2000);
}

process.on('SIGTERM', shutdownServer);
process.on('SIGINT', shutdownServer);
