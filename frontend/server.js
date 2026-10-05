#!/usr/bin/env node
// Dev server estático do frontend.
//
// Uso:
//   TLM_API_BASE=http://localhost:3001 npm run dev
//
// Serve os arquivos de frontend/web/ e injeta TLM_API_BASE no placeholder
// __API_BASE__ de config.js, permitindo apontar para um backend em outra
// origem (o backend precisa de CORS_ALLOWED_ORIGINS=http://localhost:3000).
'use strict';

const http = require('http');
const fs = require('fs');
const path = require('path');

const ROOT = __dirname;
const PORT = Number(process.env.PORT || 3000);
const API_BASE = (process.env.TLM_API_BASE || '').replace(/\/+$/, '');

const MIME = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'application/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
  '.ico': 'image/x-icon',
};

const server = http.createServer((req, res) => {
  let requestPath;
  try {
    requestPath = decodeURIComponent(new URL(req.url, 'http://localhost').pathname);
  } catch (error) {
    // Percent-encoding invalido (ex.: GET /%) lancava URIError e derrubava o
    // processo do dev server.
    res.writeHead(400, { 'Content-Type': 'text/plain; charset=utf-8' });
    res.end('bad request');
    return;
  }
  const urlPath = /^\/promo\/?$/.test(requestPath) ? '/landing.html'
    : /^\/login\/?$/.test(requestPath) ? '/login.html' : requestPath;
  // Dotfiles (.env.local, .git/config, .vercel/...) nunca sao servidos: o
  // arquivo frontend/.env.local guarda credenciais e ficava acessivel via
  // GET /.env.local com o dev server rodando.
  if (urlPath.split('/').some(segment => segment.startsWith('.'))) {
    res.writeHead(404, { 'Content-Type': 'text/plain; charset=utf-8' });
    res.end('not found');
    return;
  }
  let file = path.normalize(path.join(ROOT, urlPath));
  if (!file.startsWith(ROOT + path.sep) && file !== ROOT) {
    res.writeHead(403);
    res.end('forbidden');
    return;
  }
  if (path.basename(file) === '' || !fs.existsSync(file) || fs.statSync(file).isDirectory()) {
    file = path.join(ROOT, 'index.html');
  }

  let body = fs.readFileSync(file);
  res.setHeader('Cache-Control', 'no-store');
  res.setHeader('Content-Type', MIME[path.extname(file)] || 'application/octet-stream');
  if (path.basename(file) === 'config.js' && API_BASE) {
    body = body.toString('utf8').replace(/"__API_BASE__"/, JSON.stringify(API_BASE));
  }
  res.writeHead(200);
  res.end(body);
});

server.listen(PORT, () => {
  console.log(`Frontend em http://localhost:${PORT} (API: ${API_BASE || 'mesma origem'})`);
});
