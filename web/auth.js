(function () {
    const SESSION_KEY = 'tlm.auth.session';

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
        const response = await fetch('/api/auth/config');
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
        if (!token) {
            currentUser = null;
            return null;
        }
        const response = await fetch('/api/auth/me', {
            headers: { Authorization: 'Bearer ' + token },
        });
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
        const response = await fetch(input, options);
        if (response.status === 401 && authConfig.enabled) {
            writeSession(null);
            currentUser = null;
            if (!String(input).includes('/api/auth/')) {
                window.location.href = '/login.html';
            }
        }
        return response;
    }

    function eventsURL() {
        const token = getAccessToken();
        if (!token) return '/events';
        return '/events?access_token=' + encodeURIComponent(token);
    }

    async function getLoginStatus(email) {
        const query = new URLSearchParams({ email: String(email || '').trim() });
        const response = await fetch('/api/auth/login?' + query.toString());
        if (!response.ok) {
            return null;
        }
        return response.json();
    }

    async function signIn(email, password) {
        const response = await fetch('/api/auth/login', {
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
        if (token) {
            try {
                await fetch('/api/auth/logout', {
                    method: 'POST',
                    headers: { Authorization: 'Bearer ' + token },
                });
            } catch (_) {
                // ignore network errors; local session is still cleared
            }
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

        const session = readSession();
        if (!session || !session.access_token) {
            window.location.href = '/login.html';
            return null;
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

    window.TLMAuth = {
        applyTheme,
        authFetch,
        eventsURL,
        getAccessToken,
        getLoginStatus,
        getUser: () => currentUser,
        isAdmin,
        loadAuthConfig,
        refreshMe,
        requireSession,
        signIn,
        signOut,
    };
})();
