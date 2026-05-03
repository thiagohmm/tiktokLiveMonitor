const { app, BrowserWindow, ipcMain, dialog } = require('electron');
const path = require('path');
const { spawn } = require('child_process');
const { WebcastPushConnection } = require('tiktok-live-connector');
const { aiConfigured, probeLlamaReady } = require('./ai');
const { analyzeMessage: analyzeMessageModeration } = require('./moderation');

let mainWindow;
let botWindow;
let botActive = false;
let tiktokConnection;
let currentUsername;
let chatBuffer = []; // Ultimas 500 mensagens
let pinnedCommentUsers = new Set();
let processedPinnedMessages = new Set();
let repeatAlertedSequences = new Set();

function repeatSequenceKey(senderKey, commentLower) {
    return JSON.stringify([senderKey, commentLower]);
}

function normalizeId(value) {
    return String(value || '').toLowerCase();
}

function isTargetGift(giftName) {
    const normalizedGiftName = String(giftName || '').toLowerCase();
    const compactGiftName = normalizedGiftName.replace(/[^a-z0-9]/g, '');

    return normalizedGiftName.includes('perfume') ||
        normalizedGiftName.includes('tiny dyny') ||
        normalizedGiftName.includes('tiny diny') ||
        compactGiftName.includes('tinydyny') ||
        compactGiftName.includes('tinydiny');
}

function getGiftTypeFromPayload(data) {
    return data.giftType ?? data.giftDetails?.giftType;
}

function isGiftCountingSettlement(data) {
    const giftType = getGiftTypeFromPayload(data);
    if (Number(giftType) === 1 && data.repeatEnd === false) {
        return false;
    }
    return true;
}

function getGiftRepeatCount(data) {
    const rc = Number(data.repeatCount);
    return Number.isFinite(rc) && rc > 0 ? rc : 1;
}

function aiConfiguredLocal() {
    return true;
}

function createTikTokConnection(username) {
    return new WebcastPushConnection(username, {
        logFetchFallbackErrors: true
    });
}

function formatTikTokConnectionError(error) {
    const message = String(error?.message || error || 'Falha desconhecida ao conectar.');

    if (message.includes("isn't online")) {
        return 'Esse usuário não está ao vivo agora. Abra uma live ativa e tente novamente.';
    }

    if (message.includes('ENOTFOUND') || message.includes('EAI_AGAIN')) {
        return 'Falha de DNS/rede ao acessar TikTok/Euler. Verifique internet, VPN, proxy ou bloqueio de DNS.';
    }

    if (message.includes('SIGN SERVER') || message.includes('sign server') || message.includes('Euler')) {
        return `Falha no serviço de assinatura usado pela conexão do TikTok: ${message}`;
    }

    return message;
}

function setBotStatus(active, text) {
    botActive = active;
    if (mainWindow && !mainWindow.isDestroyed()) {
        mainWindow.webContents.send('bot-status', { active, text });
    }
}

function refreshBotStatusFromUrl() {
    if (!botWindow || botWindow.isDestroyed()) {
        setBotStatus(false, 'Inativo');
        return false;
    }

    const url = botWindow.webContents.getURL();
    const liveActive = url.includes('tiktok.com') && url.includes('/live');
    if (liveActive) {
        setBotStatus(true, botWindow.isVisible() ? 'Pronto na Live' : 'Ativo (Oculto)');
        return true;
    }

    if (url.includes('tiktok.com')) {
        setBotStatus(false, 'Logado / Navegando');
    }
    return false;
}

function pythonChatSenderEnabled() {
    return process.env.TIKTOKLIVE_PYTHON_CHAT === '1';
}

function pythonExecutable() {
    return process.env.PYTHON || process.env.PYTHON_BIN || 'python3';
}

async function getBotTikTokCookies() {
    const envCookies = {
        sessionId: process.env.TIKTOK_SESSION_ID || '',
        ttTargetIdc: process.env.TIKTOK_TT_TARGET_IDC || ''
    };

    if (!botWindow || botWindow.isDestroyed()) {
        return envCookies;
    }

    try {
        const cookieStore = botWindow.webContents.session.cookies;
        const cookies = [
            ...await cookieStore.get({ url: 'https://www.tiktok.com' }),
            ...await cookieStore.get({ url: 'https://webcast.tiktok.com' })
        ];
        const byName = new Map(cookies.map(cookie => [cookie.name, cookie.value]));

        return {
            sessionId: byName.get('sessionid') || byName.get('sessionid_ss') || byName.get('sid_tt') || byName.get('sid_guard') || envCookies.sessionId,
            ttTargetIdc: byName.get('tt-target-idc') || envCookies.ttTargetIdc
        };
    } catch {
        return envCookies;
    }
}

function runPythonChatSender(username, text, cookies, force = false) {
    return new Promise((resolve) => {
        if (!force && !pythonChatSenderEnabled()) {
            resolve({ ok: false, skipped: true, reason: 'TIKTOKLIVE_PYTHON_CHAT não está ativo.' });
            return;
        }

        if (!username) {
            resolve({ ok: false, reason: 'Live atual não identificada.' });
            return;
        }

        if (!cookies.sessionId) {
            resolve({ ok: false, reason: 'Cookie sessionid não encontrado na janela do bot.' });
            return;
        }

        const scriptBasePath = app.isPackaged ? process.resourcesPath : __dirname;
        const scriptPath = path.join(scriptBasePath, 'scripts', 'send-tiktok-chat.py');
        const args = [
            scriptPath,
            '--username', username,
            '--message', text,
            '--session-id', cookies.sessionId
        ];

        if (tiktokConnection && (tiktokConnection.roomId || typeof tiktokConnection.getRoomId === 'function')) {
            const rid = typeof tiktokConnection.getRoomId === 'function' ? tiktokConnection.getRoomId() : tiktokConnection.roomId;
            if (rid) {
                args.push('--room-id', rid);
            }
        }

        if (cookies.ttTargetIdc) {
            args.push('--tt-target-idc', cookies.ttTargetIdc);
        }

        const env = {
            ...process.env,
            PYTHONUNBUFFERED: '1',
            WHITELIST_AUTHENTICATED_SESSION_ID_HOST: 'tiktok.eulerstream.com'
        };

        const child = spawn(pythonExecutable(), args, {
            cwd: __dirname,
            env,
            stdio: ['ignore', 'pipe', 'pipe']
        });

        let stdout = '';
        let stderr = '';

        child.stdout.on('data', chunk => {
            stdout += chunk.toString();
        });

        child.stderr.on('data', chunk => {
            stderr += chunk.toString();
        });

        child.on('error', error => {
            resolve({ ok: false, reason: error.message });
        });

        child.on('close', code => {
            const output = stdout.trim();
            let payload = null;
            if (output) {
                try {
                    payload = JSON.parse(output.split('\n').pop());
                } catch {
                    payload = null;
                }
            }

            if (code === 0 && payload?.ok) {
                resolve({ ok: true, method: 'python', response: payload.response || null });
                return;
            }

            resolve({
                ok: false,
                reason: payload?.error || stderr.trim() || output || `Python saiu com código ${code}.`
            });
        });
    });
}

ipcMain.handle('get-ui-config', () => ({
    geminiConfigured: aiConfigured()
}));

ipcMain.handle('probe-llm', async () => ({
    llmActive: await probeLlamaReady()
}));

ipcMain.on('open-bot-window', (event) => {
    if (botWindow) {
        if (botWindow.isVisible()) {
            botWindow.hide();
        } else {
            botWindow.show();
        }
        return;
    }

    botWindow = new BrowserWindow({
        width: 1200,
        height: 800,
        title: 'Bot Login - TikTok',
        webPreferences: {
            nodeIntegration: false,
            contextIsolation: true
        }
    });

    if (currentUsername) {
        botWindow.loadURL(`https://www.tiktok.com/@${currentUsername}/live`);
    } else {
        botWindow.loadURL('https://www.tiktok.com/login');
    }

    botWindow.on('close', (e) => {
        // Se a janela principal ainda existe, apenas oculta
        if (mainWindow && !mainWindow.isDestroyed()) {
            e.preventDefault();
            botWindow.hide();
            setBotStatus(botActive, botActive ? 'Ativo (Oculto)' : 'Logado (Oculto)');
        }
    });

    botWindow.on('closed', () => {
        botWindow = null;
        setBotStatus(false, 'Inativo (Janela Fechada)');
    });

    botWindow.webContents.on('did-finish-load', () => {
        refreshBotStatusFromUrl();
    });

    botWindow.webContents.on('did-navigate-in-page', () => {
        refreshBotStatusFromUrl();
    });
});

async function sendBotMessage(text) {
    console.log(`[Bot] Enviando: ${text}`);

    let pythonSkipped = false;
    try {
        const cookies = await getBotTikTokCookies();
        const pythonResult = await runPythonChatSender(currentUsername, text, cookies);

        if (pythonResult.ok) {
            console.log('[Bot] Mensagem enviada via TikTokLive Python. Response:', pythonResult.response);
            setBotStatus(true, 'Mensagem enviada (Python)');
            return true;
        }

        pythonSkipped = !!pythonResult.skipped;
        if (!pythonSkipped) {
            console.log('[Bot] Python não enviou; tentando pela janela.', pythonResult.reason || pythonResult.error || '');
        }
    } catch (error) {
        console.log('[Bot] Erro no envio Python; tentando pela janela.', error?.message || error);
    }

    if (!botWindow || botWindow.isDestroyed()) {
        setBotStatus(false, 'Inativo');
        console.log('[Bot] Mensagem não enviada: janela do bot não está aberta.');
        return false;
    }

    const ready = botActive || refreshBotStatusFromUrl();
    if (!ready) {
        console.log('[Bot] Mensagem não enviada: Bot não está na página da live.');
        return false;
    }

    const textLiteral = JSON.stringify(text);

    const script = `
        (async function() {
            const text = ${textLiteral};
            const sleep = (ms) => new Promise(resolve => setTimeout(resolve, ms));
            const isVisible = (el) => {
                if (!el) return false;
                try {
                    const style = window.getComputedStyle(el);
                    if (style.display === 'none' || style.visibility === 'hidden' || style.opacity === '0') return false;
                    const rect = el.getBoundingClientRect();
                    if (rect.width < 1 || rect.height < 1) return false;
                    return true;
                } catch (e) {
                    return false;
                }
            };

            const selectors = [
                '[data-e2e="chat-input"]',
                '[data-e2e="comment-input"]',
                '[data-e2e="chat-input-area"] [contenteditable="true"]',
                '[role="textbox"][contenteditable="true"]',
                '[contenteditable="true"][role="textbox"]',
                '[contenteditable="true"][aria-label*="comment" i]',
                '[contenteditable="true"][aria-label*="chat" i]',
                '[contenteditable="true"][aria-label*="mensagem" i]',
                '[contenteditable="true"][aria-label*="coment" i]',
                '[aria-label*="mensagem" i]',
                '[aria-label*="comment" i]',
                '[placeholder*="mensagem" i]',
                '[placeholder*="comment" i]',
                '[placeholder*="message" i]',
                '.public-DraftEditor-content',
                '.DraftEditor-editorContainer [contenteditable="true"]',
                '.editor-content',
                '.webcast-chatroom___input-area',
                '[class*="ChatInput"] [contenteditable="true"]',
                '[class*="CommentInput"] [contenteditable="true"]',
                '[class*="ChatInput"]',
                '[class*="CommentInput"]',
                '.tiktok-1p6ia4n-DivChatInputContainer [contenteditable="true"]',
                'textarea',
                'input[type="text"]',
                'input:not([type="hidden"])'
            ];

            const editableSelector = '[contenteditable="true"], textarea, input[type="text"], input[type="search"], [role="textbox"]';

            const findInDocument = (root) => {
                const results = [];
                const walker = document.createTreeWalker(root, NodeFilter.SHOW_ELEMENT, null, false);
                let node = walker.currentNode;
                while (node) {
                    if (node.nodeType === Node.ELEMENT_NODE) {
                        const matchesSelector = selectors.some(s => {
                            try { return node.matches(s); } catch(e) { return false; }
                        });
                        
                        if (matchesSelector || node.isContentEditable || node.getAttribute('role') === 'textbox') {
                            if (isVisible(node)) {
                                if (node.isContentEditable || node.matches(editableSelector)) {
                                    results.push(node);
                                } else {
                                    const nested = node.querySelector(editableSelector);
                                    if (nested && isVisible(nested)) results.push(nested);
                                }
                            }
                        }

                        if (node.shadowRoot) {
                            results.push(...findInDocument(node.shadowRoot));
                        }
                        
                        if (node.tagName === 'IFRAME') {
                            try {
                                const frameDoc = node.contentDocument || node.contentWindow.document;
                                if (frameDoc) results.push(...findInDocument(frameDoc));
                            } catch (e) {}
                        }
                    }
                    node = walker.nextNode();
                }
                return results;
            };

            let candidates = findInDocument(document);
            candidates = [...new Set(candidates)];
            candidates.sort((a, b) => {
                const rectA = a.getBoundingClientRect();
                const rectB = b.getBoundingClientRect();
                return rectB.top - rectA.top;
            });
            
            const input = candidates[0];

            if (!input) {
                const bodyText = document.body.innerText;
                const loginButton = document.querySelector('[data-e2e="login-button"]') || 
                                   document.querySelector('[class*="login-button"]') ||
                                   document.querySelector('[class*="LoginButton"]') ||
                                   document.querySelector('button[aria-label*="Log in" i]') ||
                                   document.querySelector('button[aria-label*="Entrar" i]');
                
                const isGuest = !!loginButton || 
                              bodyText.includes('interaja com outras pessoas') || 
                              bodyText.includes('Fazer login para comentar') || 
                              bodyText.includes('Faça login para') ||
                              bodyText.includes('Log in to comment');
                
                return {
                    ok: false,
                    reason: isGuest ? 'Usuário não logado no Bot' : 'Input não encontrado',
                    url: location.href,
                    title: document.title,
                    editableCount: document.querySelectorAll(editableSelector).length,
                    htmlSample: bodyText.slice(0, 500),
                    candidatesFound: candidates.length
                };
            }

            input.scrollIntoView({ block: 'center', inline: 'nearest' });
            input.click();
            input.focus();
            await sleep(100);

            const tag = input.tagName;
            if (tag === 'TEXTAREA' || tag === 'INPUT') {
                const valueSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set;
                const textAreaSetter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set;
                const setter = tag === 'TEXTAREA' ? textAreaSetter : valueSetter;
                if (setter) {
                    setter.call(input, text);
                } else {
                    input.value = text;
                }
                input.dispatchEvent(new InputEvent('input', { bubbles: true, inputType: 'insertText', data: text }));
                input.dispatchEvent(new Event('change', { bubbles: true }));
            } else {
                const selection = window.getSelection();
                const range = document.createRange();
                range.selectNodeContents(input);
                selection.removeAllRanges();
                selection.addRange(range);
                document.execCommand('delete', false);
                await sleep(50);
                document.execCommand('insertText', false, text);

                if (!String(input.innerText || input.textContent || '').includes(text)) {
                    input.textContent = text;
                }

                input.dispatchEvent(new InputEvent('beforeinput', { bubbles: true, inputType: 'insertText', data: text }));
                input.dispatchEvent(new InputEvent('input', { bubbles: true, inputType: 'insertText', data: text }));
                input.dispatchEvent(new Event('change', { bubbles: true }));
            }

            const inputText = String(input.value || input.innerText || input.textContent || '').trim();
            if (!inputText.includes(text)) {
                return { ok: false, reason: 'Texto não entrou no input', inputText };
            }

            await sleep(800);

            const inputDoc = input.ownerDocument || document;
            const container = input.closest('form, [data-e2e*="chat" i], [class*="chat" i], [class*="comment" i]') || inputDoc;
            const buttonSelectors = [
                '[data-e2e="chat-send"]',
                '[data-e2e="comment-post"]',
                'button[type="submit"]',
                'button[aria-label*="Send" i]',
                'button[aria-label*="Enviar" i]',
                'button[aria-label*="Post" i]',
                'button[aria-label*="Comentar" i]'
            ];
            const allButtons = Array.from(new Set([
                ...container.querySelectorAll(buttonSelectors.join(',')),
                ...inputDoc.querySelectorAll(buttonSelectors.join(','))
            ])).filter(isVisible);

            const enabledButton = allButtons.find(btn =>
                !btn.disabled &&
                btn.getAttribute('aria-disabled') !== 'true' &&
                !btn.className.toString().toLowerCase().includes('disabled')
            );

            if (enabledButton) {
                enabledButton.click();
                await sleep(300);
                return { ok: true, method: 'button', buttonText: enabledButton.innerText || enabledButton.getAttribute('aria-label') || '' };
            }

            for (const type of ['keydown', 'keypress', 'keyup']) {
                input.dispatchEvent(new KeyboardEvent(type, {
                    key: 'Enter',
                    code: 'Enter',
                    keyCode: 13,
                    which: 13,
                    bubbles: true,
                    cancelable: true
                }));
            }

            await sleep(300);
            return { ok: true, method: 'enter', buttonsFound: allButtons.length };
        })()
    `;

    try {
        const result = await botWindow.webContents.executeJavaScript(script);
        console.log('[Bot] Resultado JS:', result);
        if (result && result.ok) {
            setBotStatus(true, 'Mensagem enviada');
            return true;
        }

        const reason = result?.reason || 'Falha ao enviar';

        if (pythonSkipped) {
            console.log(`[Bot] Janela falhou (${reason}). Tentando Python como fallback (forçado)...`);
            const cookies = await getBotTikTokCookies();
            const fallbackResult = await runPythonChatSender(currentUsername, text, cookies, true);
            if (fallbackResult.ok) {
                console.log('[Bot] Mensagem enviada via Python (fallback forçado). Response:', fallbackResult.response);
                setBotStatus(true, 'Mensagem enviada (Python)');
                return true;
            }
            console.log('[Bot] Fallback Python também falhou:', fallbackResult.reason || fallbackResult.error || 'Erro desconhecido', fallbackResult.response ? JSON.stringify(fallbackResult.response) : '');
        }

        setBotStatus(false, reason);
        return false;
    } catch (err) {
        console.error('[Bot] Erro ao enviar mensagem:', err);
        return false;
    }
}

async function sendRepeatWarning(data) {
    const mention = data.uniqueId || data.nickname;
    const prefix = mention ? `@${mention}` : String(data.nickname || 'Atenção');
    const sent = await sendBotMessage(`${prefix} Por favor, evite enviar mensagens repetidas na live!`);
    if (!sent) {
        console.log('[Bot] Aviso de repetição não enviado.', {
            botActive,
            botWindowOpen: Boolean(botWindow && !botWindow.isDestroyed()),
            user: data.uniqueId || data.nickname || null
        });
    }
}

function getTargetGiftLabel(giftName) {
    const normalizedGiftName = String(giftName || '').toLowerCase();
    const compactGiftName = normalizedGiftName.replace(/[^a-z0-9]/g, '');

    if (
        normalizedGiftName.includes('tiny dyny') ||
        normalizedGiftName.includes('tiny diny') ||
        compactGiftName.includes('tinydyny') ||
        compactGiftName.includes('tinydiny')
    ) {
        return 'Tiny Dyny';
    }

    if (normalizedGiftName.includes('perfume')) {
        return 'Perfume';
    }

    return 'Presente Alvo';
}

function getUserFromObject(data) {
    if (!data) return { uniqueId: null, nickname: null };

    const user = data.user || data.member || data.sender || data.author || data.owner || {};
    const uniqueId = data.uniqueId || user.uniqueId || user.secUid || user.id || null;
    const nickname = data.nickname || user.nickname || user.displayName || uniqueId || null;

    return { uniqueId, nickname };
}

function textFromDisplayText(displayText) {
    if (!displayText) return null;
    if (typeof displayText === 'string') return displayText;
    if (displayText.defaultPattern) return displayText.defaultPattern;
    if (displayText.format) return displayText.format;
    if (displayText.displayText) return textFromDisplayText(displayText.displayText);
    const pieces = displayText.pieces || displayText.piecesList;
    if (Array.isArray(pieces)) {
        const text = pieces
            .map(piece => piece.stringValue || piece.text || piece.userValue?.nickname || piece.userValue?.uniqueId || '')
            .join('')
            .trim();
        return text || null;
    }
    return null;
}

function getPinnedContent(data) {
    const pinnedSource = data.chatMessage ||
        data.pinMessage ||
        data.pinnedMessage ||
        data.socialMessage ||
        data.giftMessage ||
        data.memberMessage ||
        data.likeMessage ||
        data;

    const candidates = [
        data.content,
        data.comment,
        data.text,
        data.message,
        data.description,
        data.pinnedText,
        data.pinnedComment,
        typeof data.pinnedMessage === 'string' ? data.pinnedMessage : null,
        pinnedSource.comment,
        pinnedSource.content,
        pinnedSource.text,
        pinnedSource.message,
        pinnedSource.actionDescription,
        textFromDisplayText(pinnedSource.common?.displayText),
        textFromDisplayText(pinnedSource.publicAreaMessageCommon?.displayText),
        textFromDisplayText(pinnedSource.publicAreaCommon?.userLabel),
        textFromDisplayText(pinnedSource.trayDisplayText),
        textFromDisplayText(pinnedSource.displayTextForAnchor),
        textFromDisplayText(pinnedSource.displayTextForAudience)
    ];

    const content = candidates.find(value => typeof value === 'string' && value.trim());
    return content ? content.trim() : null;
}

function getPinnedUser(data, content) {
    const sources = [
        data.chatMessage,
        data.pinMessage,
        data.pinnedMessage,
        data.socialMessage,
        data.giftMessage,
        data.memberMessage,
        data.likeMessage,
        data.user,
        data
    ].filter(source => source && typeof source === 'object');

    for (const source of sources) {
        const user = getUserFromObject(source);
        if (user.uniqueId) {
            return user;
        }
    }

    const mentionMatch = content && content.match(/@([a-zA-Z0-9._]+)/);
    if (mentionMatch) {
        return { uniqueId: mentionMatch[1].toLowerCase(), nickname: mentionMatch[1] };
    }

    if (content) {
        const contentLower = content.toLowerCase();
        const sender = chatBuffer.find(m => {
            const commentLower = String(m.comment || '').toLowerCase();
            return contentLower.includes(commentLower) || commentLower.includes(contentLower);
        });
        if (sender) {
            return { uniqueId: sender.uniqueId, nickname: sender.nickname };
        }
    }

    return { uniqueId: null, nickname: null };
}

function logPinnedComment(content, user, data) {
    console.log('\n📌 COMENTÁRIO FIXADO:');
    if (user.uniqueId) {
        console.log(`👤 Autor: ${user.nickname || user.uniqueId} (@${user.uniqueId})`);
    } else {
        console.log('👤 Autor: Não identificado');
    }
    console.log(`💬 Mensagem: ${content || '[sem texto identificado]'}`);
    console.log(`📎 Método: ${data.method || 'desconhecido'} | Ação: ${data.action ?? 'desconhecida'} | Pin ID: ${data.pinId || 'n/a'}`);
    console.log('----------------------------\n');
}

function getPinnedMessageKey(data) {
    return data.pinId ||
        data.msgId ||
        data.chatMessage?.common?.msgId ||
        data.chatMessage?.msgId ||
        `${data.pinTime || ''}:${data.chatMessage?.comment || ''}`;
}

function handlePinnedMessage(data) {
    if (!data || data.method === 'unpin' || data.action === 2) {
        return;
    }

    const messageKey = getPinnedMessageKey(data);
    if (messageKey && processedPinnedMessages.has(messageKey)) {
        return;
    }
    if (messageKey) {
        processedPinnedMessages.add(messageKey);
        if (processedPinnedMessages.size > 200) {
            processedPinnedMessages = new Set(Array.from(processedPinnedMessages).slice(-100));
        }
    }

    const content = getPinnedContent(data);
    const pinnedUser = getPinnedUser(data, content);

    // Sempre mostra o evento de fixado no console, mesmo se a lib mudar o formato.
    logPinnedComment(content, pinnedUser, data);

    mainWindow.webContents.send('pinned-comment', {
        uniqueId: pinnedUser.uniqueId,
        nickname: pinnedUser.nickname || pinnedUser.uniqueId || 'Nao identificado',
        comment: content || '[sem texto identificado]',
        pinId: data.pinId || data.msgId || null,
        timestamp: Date.now()
    });

    if (pinnedUser.uniqueId) {
        const pinnedUniqueId = rememberPinnedUser(pinnedUser.uniqueId);
        mainWindow.webContents.send('mark-user-red', pinnedUniqueId);
    }

    // Comentario fixado nao e presente. Ele apenas marca o usuario como prioritario;
    // a entrada na fila de presentes deve acontecer somente no evento real de gift.
}

function rememberPinnedUser(uniqueId) {
    const normalizedId = normalizeId(uniqueId);
    if (normalizedId) {
        pinnedCommentUsers.add(normalizedId);
    }
    return normalizedId;
}

function reportFatalError(context, error) {
    const message = `${context}\n\n${error?.stack || error?.message || String(error)}`;
    console.error(message);

    if (app.isReady()) {
        dialog.showErrorBox('TikTok Live Monitor', message);
    }
}

process.on('uncaughtException', (error) => {
    reportFatalError('Erro não tratado no processo principal.', error);
});

process.on('unhandledRejection', (error) => {
    reportFatalError('Promise rejeitada sem tratamento.', error);
});

function createWindow() {
    mainWindow = new BrowserWindow({
        width: 1000,
        height: 800,
        webPreferences: {
            nodeIntegration: true,
            contextIsolation: false
        }
    });

    mainWindow.webContents.on('render-process-gone', (event, details) => {
        reportFatalError(`Renderer encerrado inesperadamente (${details.reason}).`, new Error(details.exitCode ? `Exit code: ${details.exitCode}` : 'Sem exit code'));
    });

    mainWindow.webContents.on('did-fail-load', (event, errorCode, errorDescription) => {
        reportFatalError(`Falha ao carregar a interface (${errorCode}).`, new Error(errorDescription));
    });

    mainWindow.loadFile(path.join(__dirname, 'index.html')).catch((error) => {
        reportFatalError('Falha ao abrir index.html.', error);
    });

    mainWindow.on('closed', () => {
        mainWindow = null;
        if (botWindow) {
            botWindow.destroy(); // Usa destroy para ignorar o e.preventDefault() do 'close'
            botWindow = null;
        }
    });
}

app.whenReady().then(createWindow);

app.on('window-all-closed', () => {
    if (process.platform !== 'darwin') app.quit();
});

ipcMain.on('connect-tiktok', (event, username) => {
    if (tiktokConnection) {
        tiktokConnection.removeAllListeners();
        tiktokConnection.disconnect();
    }

    currentUsername = username;
    chatBuffer = [];
    pinnedCommentUsers.clear();
    processedPinnedMessages.clear();
    repeatAlertedSequences.clear();
    tiktokConnection = createTikTokConnection(username);

    tiktokConnection.connect().then(state => {
        const roomId = state.roomId || tiktokConnection.roomId;
        console.log(`Conectado à live de ${username}. Room ID: ${roomId}`);
        event.reply('connection-status', { success: true, username, roomId: roomId });

        // Se o bot estiver aberto, navega para a live
        if (botWindow) {
            botWindow.loadURL(`https://www.tiktok.com/@${username}/live`);
        }
    }).catch(err => {
        console.error('Falha ao conectar:', err);
        event.reply('connection-status', { success: false, error: formatTikTokConnectionError(err) });
    });

    // Monitorar chat para o buffer de 500 mensagens
    tiktokConnection.on('chat', (data) => {
        const comment = String(data.comment || '').trim();
        if (!comment) {
            return;
        }

        const msg = {
            uniqueId: data.uniqueId,
            nickname: data.nickname,
            comment,
            timestamp: Date.now()
        };

        const commentLower = comment.toLowerCase();
        const senderKey = normalizeId(data.uniqueId);
        const repeatWindowMs = 60000;
        const repeatsRequired = 3;
        const now = Date.now();

        const priorSameUserSameText = chatBuffer.filter(m =>
            normalizeId(m.uniqueId) === senderKey &&
            m.comment.trim().toLowerCase() === commentLower &&
            (now - m.timestamp) < repeatWindowMs
        );

        const isRepeat = Boolean(senderKey) && priorSameUserSameText.length >= repeatsRequired - 1;
        const seqKey = repeatSequenceKey(senderKey, commentLower);

        if (isRepeat) {
            if (!repeatAlertedSequences.has(seqKey)) {
                repeatAlertedSequences.add(seqKey);
                mainWindow.webContents.send('flagged-message', {
                    uniqueId: data.uniqueId,
                    nickname: data.nickname,
                    comment,
                    reason: 'Mensagem repetida',
                    category: 'REPETICAO'
                });

                void sendRepeatWarning(data);
            }
        } else {
            repeatAlertedSequences.delete(seqKey);
        }

        // Realiza análise de moderação (Regex + IA Local)
        analyzeMessageModeration(comment, data.uniqueId, data.nickname, chatBuffer)
            .then(result => {
                if (result.flagged) {
                    mainWindow.webContents.send('flagged-message', {
                        uniqueId: data.uniqueId,
                        nickname: data.nickname,
                        comment,
                        reason: result.reason,
                        category: result.category || null
                    });
                }
            })
            .catch(err => {
                console.error('[AI] Erro na moderação:', err.message);
            });

        chatBuffer.push(msg);
        if (chatBuffer.length > 500) {
            chatBuffer.shift();
        }
        // Notificar renderer para o gráfico
        mainWindow.webContents.send('new-chat-message', msg);
    });

    tiktokConnection.on('member', (data) => {
        mainWindow.webContents.send('live-user-connected', {
            uniqueId: data.uniqueId,
            nickname: data.nickname,
            timestamp: Date.now()
        });
    });

    // Monitorar presentes (Gifts)
    tiktokConnection.on('gift', (data) => {
        const uniqueId = normalizeId(data.uniqueId);
        const targetGift = isTargetGift(data.giftName);
        const isPinnedUser = pinnedCommentUsers.has(uniqueId);

        if (!isGiftCountingSettlement(data)) {
            return;
        }

        console.log('\n🎁 PRESENTE RECEBIDO:');
        console.log(`👤 Usuário: ${data.uniqueId}`);
        console.log(`🎁 Presente: ${data.giftName} (ID: ${data.giftId})`);
        console.log(`🎯 Alvo: ${targetGift ? 'Sim' : 'Não'}`);
        console.log(`⭐ Prioritário: ${isPinnedUser ? 'Sim' : 'Não'}`);
        console.log('----------------------------\n');

        const giftType = getGiftTypeFromPayload(data);
        const repeatQty = getGiftRepeatCount(data);

        mainWindow.webContents.send('any-gift-received', {
            uniqueId: data.uniqueId,
            nickname: data.nickname,
            giftName: data.giftName,
            giftId: data.giftId,
            giftType,
            repeatCount: repeatQty,
            repeatEnd: data.repeatEnd,
            isTargetGift: targetGift,
            isRed: targetGift && isPinnedUser
        });

        if (targetGift) {
            mainWindow.webContents.send('new-gift-user', {
                uniqueId: data.uniqueId,
                nickname: data.nickname,
                giftName: data.giftName,
                isRed: isPinnedUser
            });
        }
    });

    // Monitorar mensagens fixadas
    tiktokConnection.on('roomPin', (data) => {
        handlePinnedMessage(data);
    });

    // Nesta versão da lib, algumas lives recebem o fixado em decodedData,
    // mas não emitem o alias roomPin.
    tiktokConnection.on('decodedData', (type, data) => {
        if (type === 'WebcastRoomPinMessage' || data?.method === 'WebcastRoomPinMessage') {
            handlePinnedMessage(data);
        }
    });

    tiktokConnection.on('roomUser', (data) => {
        // Algumas versões da lib mandam info de fixado aqui
        if (data && data.displayType === 'pm_mt_guidance_share') {
             // Pode ser um aviso de fixação
        }
    });
});

ipcMain.on('disconnect-tiktok', (event) => {
    if (tiktokConnection) {
        tiktokConnection.removeAllListeners();
        tiktokConnection.disconnect();
        tiktokConnection = null;
    }
    
    currentUsername = null;

    // Fecha a janela do bot ao desconectar, se o usuário desejar
    if (botWindow) {
        botWindow.destroy();
        botWindow = null;
        botActive = false;
    }

    chatBuffer = [];
    pinnedCommentUsers.clear();
    processedPinnedMessages.clear();
    repeatAlertedSequences.clear();
    event.reply('connection-status', { success: false, error: 'Desconectado pelo usuário' });
});
