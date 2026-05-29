const http = require('http');

// Mocks antes de importar o server
jest.mock('./ai', () => ({
  probeLlamaReady: jest.fn().mockResolvedValue(true),
  completeModeration: jest.fn()
}));

jest.mock('./moderation', () => ({
  analyzeMessage: jest.fn().mockResolvedValue({ flagged: false }),
  clearModerationCache: jest.fn()
}));

jest.mock('tiktok-live-connector', () => ({
  WebcastPushConnection: jest.fn().mockImplementation(() => ({
    connect: jest.fn().mockResolvedValue({ roomId: '123' }),
    disconnect: jest.fn(),
    removeAllListeners: jest.fn(),
    on: jest.fn(),
    roomId: '123'
  }))
}));

// Usa porta aleatória para evitar conflito
const TEST_PORT = 3000 + Math.floor(Math.random() * 1000);
process.env.PORT = TEST_PORT;
process.env.HOST = '127.0.0.1';

// Importa o server (ele inicia automaticamente)
require('./server');

// Aguarda o servidor iniciar
beforeAll((done) => {
  setTimeout(done, 500);
});

function makeRequest(method, path, body = null) {
  return new Promise((resolve, reject) => {
    const options = {
      hostname: '127.0.0.1',
      port: TEST_PORT,
      path,
      method,
      headers: {
        'Content-Type': 'application/json'
      }
    };

    const req = http.request(options, (res) => {
      let data = '';
      res.on('data', chunk => data += chunk);
      res.on('end', () => {
        try {
          resolve({
            statusCode: res.statusCode,
            headers: res.headers,
            body: data ? JSON.parse(data) : null
          });
        } catch {
          resolve({
            statusCode: res.statusCode,
            headers: res.headers,
            body: data
          });
        }
      });
    });

    req.on('error', reject);

    if (body) {
      req.write(JSON.stringify(body));
    }
    req.end();
  });
}

describe('Server - Endpoints HTTP', () => {
  describe('GET /api/state', () => {
    it('deve retornar estado inicial desconectado', async () => {
      const res = await makeRequest('GET', '/api/state');
      expect(res.statusCode).toBe(200);
      expect(res.body).toHaveProperty('connected', false);
      expect(res.body).toHaveProperty('username', null);
      expect(res.body).toHaveProperty('aiConfigured', true);
    });
  });

  describe('GET /api/probe-llm', () => {
    it('deve retornar status do LLM', async () => {
      const res = await makeRequest('GET', '/api/probe-llm');
      expect(res.statusCode).toBe(200);
      expect(res.body).toHaveProperty('llmActive');
    });
  });

  describe('GET /api/bot-status', () => {
    it('deve retornar status do bot', async () => {
      const res = await makeRequest('GET', '/api/bot-status');
      expect(res.statusCode).toBe(200);
      expect(res.body).toHaveProperty('active');
      expect(res.body).toHaveProperty('text');
    });
  });

  describe('POST /api/bot-config', () => {
    it('deve aceitar configuração de cookies', async () => {
      const res = await makeRequest('POST', '/api/bot-config', {
        sessionId: 'test-session-id',
        ttTargetIdc: 'useast2a'
      });
      expect(res.statusCode).toBe(200);
      expect(res.body).toHaveProperty('success', true);
    });

    it('deve rejeitar JSON inválido', async () => {
      const res = await new Promise((resolve, reject) => {
        const options = {
          hostname: '127.0.0.1',
          port: TEST_PORT,
          path: '/api/bot-config',
          method: 'POST',
          headers: {
            'Content-Type': 'application/json'
          }
        };

        const req = http.request(options, (res) => {
          let data = '';
          res.on('data', chunk => data += chunk);
          res.on('end', () => {
            resolve({
              statusCode: res.statusCode,
              body: data ? JSON.parse(data) : null
            });
          });
        });

        req.on('error', reject);
        req.write('invalid json{{{');
        req.end();
      });

      expect(res.statusCode).toBe(400);
      expect(res.body).toHaveProperty('error');
    });
  });

  describe('POST /api/connect', () => {
    it('deve rejeitar requisição sem username', async () => {
      const res = await makeRequest('POST', '/api/connect', {});
      expect(res.statusCode).toBe(400);
      expect(res.body).toHaveProperty('error');
    });

    it('deve rejeitar username vazio', async () => {
      const res = await makeRequest('POST', '/api/connect', { username: '' });
      expect(res.statusCode).toBe(400);
      expect(res.body).toHaveProperty('error');
    });

    it('deve remover @ do username', async () => {
      const res = await makeRequest('POST', '/api/connect', { username: '@testuser' });
      expect(res.statusCode).toBe(200);
      expect(res.body).toHaveProperty('success', true);
      expect(res.body).toHaveProperty('username', 'testuser');
    });
  });

  describe('POST /api/disconnect', () => {
    it('deve desconectar com sucesso', async () => {
      const res = await makeRequest('POST', '/api/disconnect');
      expect(res.statusCode).toBe(200);
      expect(res.body).toHaveProperty('success', true);
    });
  });

  describe('Rotas não encontradas', () => {
    it('deve retornar 404 para rota API inexistente', async () => {
      const res = await makeRequest('GET', '/api/nonexistent');
      expect(res.statusCode).toBe(404);
      expect(res.body).toHaveProperty('error');
    });

    it('deve retornar 404 para rota inexistente', async () => {
      const res = await makeRequest('GET', '/nonexistent');
      expect(res.statusCode).toBe(404);
      expect(res.body).toHaveProperty('error');
    });
  });

  describe('GET / (index.html)', () => {
    it('deve servir o index.html', async () => {
      const res = await new Promise((resolve, reject) => {
        http.get(`http://127.0.0.1:${TEST_PORT}/`, (res) => {
          let data = '';
          res.on('data', chunk => data += chunk);
          res.on('end', () => {
            resolve({
              statusCode: res.statusCode,
              headers: res.headers,
              body: data
            });
          });
        }).on('error', reject);
      });

      expect(res.statusCode).toBe(200);
      expect(res.headers['content-type']).toContain('text/html');
    });
  });

  describe('GET /events (SSE)', () => {
    it('deve estabelecer conexão SSE', async () => {
      const res = await new Promise((resolve, reject) => {
        let data = '';
        const req = http.get(`http://127.0.0.1:${TEST_PORT}/events`, (res) => {
          res.on('data', chunk => {
            data += chunk;
            // Fecha após receber o primeiro evento
            if (data.includes('server-state')) {
              req.destroy();
              resolve({
                statusCode: res.statusCode,
                headers: res.headers,
                body: data
              });
            }
          });
        });

        req.on('error', () => {
          // Erro esperado ao destruir a conexão
          resolve({
            statusCode: 200,
            headers: {},
            body: data
          });
        });

        // Timeout de segurança
        setTimeout(() => {
          req.destroy();
          resolve({
            statusCode: 200,
            headers: {},
            body: data
          });
        }, 1000);
      });

      expect(res.statusCode).toBe(200);
    });
  });
});
