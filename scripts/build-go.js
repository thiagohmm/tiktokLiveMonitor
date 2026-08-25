'use strict';

const { execFileSync } = require('child_process');
const path = require('path');

const root = path.resolve(__dirname, '..');
const out = process.platform === 'win32' ? 'tiktok-live-monitor.exe' : 'tiktok-live-monitor';
const go = process.env.GO || 'go';

execFileSync(go, ['build', '-o', out, '.'], { cwd: root, stdio: 'inherit' });
console.log(`[build-go] Binário gerado: ${path.join(root, out)}`);
