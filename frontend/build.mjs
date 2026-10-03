// Build estático do VPS; API na mesma origem.
// Rode localmente para inspecionar: npm run build && ls dist
import { cpSync, mkdirSync, readFileSync, writeFileSync, rmSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const root = path.dirname(fileURLToPath(import.meta.url));
const dist = path.join(root, 'dist');

rmSync(dist, { recursive: true, force: true });
mkdirSync(dist, { recursive: true });

for (const entry of ['index.html', 'landing.html', 'landing.css', 'landing-offer.js', 'admin.html', 'login.html', 'reset-password.html', 'auth.js', 'renderer.js', 'admin.js', 'teams-ui.js', 'invite.html', 'invite.js', 'pix.js', 'vendor']) {
  cpSync(path.join(root, entry), path.join(dist, entry), { recursive: true });
}

let config = readFileSync(path.join(root, 'config.js'), 'utf8');
config = config.replace('"__API_BASE__"', '""');
writeFileSync(path.join(dist, 'config.js'), config);

console.log('dist/ pronto (API na mesma origem do VPS)');
