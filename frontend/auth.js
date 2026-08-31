(function () {
    const SESSION_KEY = 'tlm.auth.session';
    const nativeFetch = window.fetch.bind(window);

    let authConfig = {
        enabled: false,
        supabaseUrl: '',
        supabaseAnonKey: '',
        maxLoginAttempts: 5,
        lockoutMinutes: 15,
        theme: { pink: '#fe2c55', cyan: '#25f4ee', bg: '#0b0d12' },
    };
    let supabaseClient = null;
    let currentUser = null;

    function readSession() {
        try {
            const raw = sessionStorage.getItem(SESSION_KEY);
            return raw ? JSON.parse(raw) : null;
        } catch (_) {
            return null;
        }
    }

    function writeSession(session) {
        if (!session) {
            sessionStorage.removeItem(SESSION_KEY);
            return;
        }
        sessionStorage.setItem(SESSION_KEY, JSON.stringify(session));
    }

    function applyTheme(theme) {
        if (!theme) return;
        const root = document.documentElement;
        if (theme.pink) root.style.setProperty('--pink', theme.pink);
        if (theme.cyan) root.style.setProperty('--cyan', theme.cyan);
        if (theme.bg) root.style.setProperty('--bg', theme.bg);
    }

    async function loadAuthConfig() {
        const response = await nativeFetch('/api/auth/config');
        if (!response.ok) {
            throw new Error('Não foi possível carregar a configuração de login.');
        }
        authConfig = await response.json();
        applyTheme(authConfig.theme);
        if (authConfig.enabled && authConfig.supabaseUrl && authConfig.supabaseAnonKey && window.supabase) {
            supabaseClient = window.supabase.createClient(authConfig.supabaseUrl, authConfig.supabaseAnonKey, {
                auth: {
                    persistSession: false,
                    autoRefreshToken: false,
                    detectSessionInUrl: false,
                },
            });
        }
    }

    function getAccessToken() {
        const session = readSession();
        return session && session.access_token ? session.access_token : '';
    }

    async function refreshMe() {
        const token = getAccessToken();
        if (!authConfig.enabled) {
            currentUser = { role: 'admin', active: true, authenticated: true };
            return currentUser;
        }
        const headers = {};
        if (token) headers.Authorization = 'Bearer ' + token;
        const response = await nativeFetch('/api/auth/me', { headers });
        if (!response.ok) {
            writeSession(null);
            currentUser = null;
            return null;
        }
        currentUser = await response.json();
        return currentUser;
    }

    async function authFetch(input, init) {
        const options = init ? { ...init } : {};
        const headers = new Headers(options.headers || {});
        const token = getAccessToken();
        if (token) {
            headers.set('Authorization', 'Bearer ' + token);
        }
        options.headers = headers;
        const response = await nativeFetch(input, options);
        if (response.status === 401 && authConfig.enabled) {
            writeSession(null);
            currentUser = null;
            if (!String(input).includes('/api/auth/')) {
                window.location.href = '/login.html';
            }
        }
        return response;
    }

    // SSE via fetch + ReadableStream: EventSource não envia headers, e o
    // token não pode mais viajar na query string (vazava em logs/Referer).
    function createEventStream() {
        const listeners = {};
        let errorHandler = null;
        let stopped = false;
        let reconnectDelay = 1000;

        function dispatchFrame(frame) {
            let eventName = 'message';
            let data = '';
            for (const line of frame.split('\n')) {
                if (line.startsWith('event:')) {
                    eventName = line.slice(6).trim();
                } else if (line.startsWith('data:')) {
                    data = data ? data + '\n' + line.slice(5).trim() : line.slice(5).trim();
                }
            }
            (listeners[eventName] || []).forEach(handler => {
                try {
                    handler({ type: eventName, data });
                } catch (err) {
                    console.error('SSE handler error:', err);
                }
            });
        }

        async function connectOnce() {
            const headers = {};
            const token = getAccessToken();
            if (token) {
                headers['Authorization'] = 'Bearer ' + token;
            }
            const response = await nativeFetch('/events', { headers, cache: 'no-store' });
            if (response.status === 401 && authConfig.enabled) {
                writeSession(null);
                currentUser = null;
                window.location.href = '/login.html';
                return;
            }
            if (!response.ok || !response.body) {
                throw new Error('SSE HTTP ' + response.status);
            }
            reconnectDelay = 1000;
            const reader = response.body.getReader();
            const decoder = new TextDecoder();
            let buffer = '';
            for (;;) {
                const { value, done } = await reader.read();
                if (stopped || done) return;
                buffer += decoder.decode(value, { stream: true });
                let idx;
                while ((idx = buffer.indexOf('\n\n')) !== -1) {
                    const frame = buffer.slice(0, idx);
                    buffer = buffer.slice(idx + 2);
                    if (frame.trim()) dispatchFrame(frame);
                }
            }
        }

        (async function connectLoop() {
            while (!stopped) {
                try {
                    await connectOnce();
                } catch (_) {
                    // stream caiu: reconecta com backoff
                }
                if (stopped) return;
                if (typeof errorHandler === 'function') {
                    try { errorHandler({ type: 'error' }); } catch (_) { /* noop */ }
                }
                await new Promise(resolve => setTimeout(resolve, reconnectDelay));
                reconnectDelay = Math.min(reconnectDelay * 2, 15000);
            }
        })();

        return {
            addEventListener(name, handler) {
                (listeners[name] = listeners[name] || []).push(handler);
            },
            close() {
                stopped = true;
            },
            get onerror() {
                return errorHandler;
            },
            set onerror(fn) {
                errorHandler = typeof fn === 'function' ? fn : null;
            },
        };
    }

    async function signIn(email, password) {
        const response = await nativeFetch('/api/auth/login', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                email: String(email || '').trim(),
                password: String(password || ''),
            }),
        });
        const payload = await response.json().catch(() => ({}));
        if (!response.ok) {
            const err = new Error(payload.error || 'Não foi possível entrar.');
            err.locked = !!payload.locked;
            err.retryAfterSec = payload.retryAfterSec || 0;
            err.remainingAttempts = payload.remainingAttempts;
            throw err;
        }
        writeSession(payload.session);
        return refreshMe();
    }

    async function signOut() {
        const token = getAccessToken();
        try {
            const headers = {};
            if (token) headers.Authorization = 'Bearer ' + token;
            await nativeFetch('/api/auth/logout', { method: 'POST', headers });
        } catch (_) {
            // ignore network errors; local session is still cleared
        }
        writeSession(null);
        currentUser = null;
        if (supabaseClient) {
            try {
                await supabaseClient.auth.signOut({ scope: 'global' });
            } catch (_) {
                // ignore
            }
        }
        window.location.href = '/login.html';
    }

    async function requireSession() {
        await loadAuthConfig();
        if (!authConfig.enabled) {
            currentUser = { role: 'admin', active: true, authenticated: true };
            return currentUser;
        }

        const me = await refreshMe();
        if (!me || !me.authenticated) {
            window.location.href = '/login.html';
            return null;
        }
        if (!me.active) {
            alert('Sua conta está desativada. Fale com o administrador.');
            await signOut();
            return null;
        }
        return me;
    }

    function isAdmin() {
        return !!currentUser && currentUser.role === 'admin';
    }

    function isAuthEnabled() {
        return !!authConfig.enabled;
    }

    async function requireAdmin() {
        await loadAuthConfig();
        if (!authConfig.enabled) {
            currentUser = null;
            return null;
        }
        const me = await refreshMe();
        if (!me || !me.authenticated) {
            window.location.href = '/login.html?next=' + encodeURIComponent('/admin.html');
            return null;
        }
        if (!me.active || me.role !== 'admin') {
            alert('Acesso exclusivo para administradores.');
            window.location.href = '/';
            return null;
        }
        return me;
    }

    window.TLMAuth = {
        applyTheme,
        authFetch,
        createEventStream,
        getAccessToken,
        getUser: () => currentUser,
        isAdmin,
        isAuthEnabled,
        loadAuthConfig,
        refreshMe,
        requireAdmin,
        requireSession,
        signIn,
        signOut,
    };
})();
