// Build para Vercel: copia os arquivos estáticos para dist/ e fixa a base da
// API em vazio (same-origin + rewrites do vercel.json).
// Rode localmente para inspecionar: npm run build && ls dist
import { cpSync, mkdirSync, readFileSync, writeFileSync, rmSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const root = path.dirname(fileURLToPath(import.meta.url));
const dist = path.join(root, 'dist');

rmSync(dist, { recursive: true, force: true });
mkdirSync(dist, { recursive: true });

for (const entry of ['index.html', 'admin.html', 'login.html', 'auth.js', 'renderer.js', 'admin.js', 'vendor']) {
  cpSync(path.join(root, entry), path.join(dist, entry), { recursive: true });
}

let config = readFileSync(path.join(root, 'config.js'), 'utf8');
config = config.replace('"__API_BASE__"', '""');
writeFileSync(path.join(dist, 'config.js'), config);

console.log('dist/ pronto (base da API: same-origin, via rewrites do vercel.json)');