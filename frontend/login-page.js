const loginForm = document.getElementById('loginForm');
const signupForm = document.getElementById('signupForm');
const errorBox = document.getElementById('loginError');
const successBox = document.getElementById('loginSuccess');
const attemptsInfo = document.getElementById('attemptsInfo');
const loginBtn = document.getElementById('loginBtn');
const signupBtn = document.getElementById('signupBtn');
const recoverBtn = document.getElementById('recoverBtn');
const emailInput = document.getElementById('email');
const passwordInput = document.getElementById('password');
const recoverForm = document.getElementById('recoverForm');
const panelHint = document.getElementById('panelHint');

let lockoutTimer = null;
let currentTab = 'login';

function showError(message) {
    errorBox.textContent = message;
    errorBox.classList.add('visible');
    successBox.classList.remove('visible');
}

function hideError() {
    errorBox.classList.remove('visible');
}

function showSuccess(message) {
    successBox.textContent = message;
    successBox.classList.add('visible');
    errorBox.classList.remove('visible');
}

function hideSuccess() {
    successBox.classList.remove('visible');
}

function showAttempts(message) {
    attemptsInfo.textContent = message;
    attemptsInfo.classList.remove('hidden');
}

function hideAttempts() {
    attemptsInfo.classList.add('hidden');
}

function setFormLocked(locked) {
    loginBtn.disabled = locked;
    passwordInput.disabled = locked;
    recoverBtn.disabled = locked;
}

function clearLockoutTimer() {
    if (lockoutTimer) {
        clearInterval(lockoutTimer);
        lockoutTimer = null;
    }
}

function startLockoutCountdown(seconds) {
    clearLockoutTimer();
    let remaining = Math.max(1, Number(seconds) || 0);
    setFormLocked(true);
    signupBtn.disabled = true;

    function tick() {
        const mins = Math.floor(remaining / 60);
        const secs = remaining % 60;
        const clock = mins > 0
            ? mins + ' min ' + String(secs).padStart(2, '0') + ' s'
            : secs + ' s';
        showError('Muitas tentativas. Tente novamente em ' + clock + '.');
        if (remaining <= 0) {
            clearLockoutTimer();
            hideError();
            setFormLocked(false);
            signupBtn.disabled = false;
            return;
        }

        remaining -= 1;
    }

    tick();
    lockoutTimer = setInterval(tick, 1000);
}

function switchTab(tab) {
    currentTab = tab;
    document.querySelectorAll('.tab').forEach(button => {
        button.classList.toggle('active', button.dataset.tab === tab);
    });
    loginForm.classList.toggle('hidden', tab !== 'login');
    signupForm.classList.toggle('hidden', tab !== 'signup');
    recoverForm.classList.add('hidden');
    hideError();
    hideSuccess();
    hideAttempts();
    if (tab === 'login') {
        panelHint.textContent = 'Entre com o e-mail e a senha da sua assinatura.';
    } else {
        panelHint.textContent = 'Crie sua conta e informe seu canal para solicitar uma avaliação. Aguarde a orientação da equipe antes de pagar. O acesso é liberado após a confirmação do pagamento.';
    }
}

function showRecoverPanel() {
    currentTab = 'recover';
    document.querySelectorAll('.tab').forEach(button => button.classList.remove('active'));
    loginForm.classList.add('hidden');
    signupForm.classList.add('hidden');
    recoverForm.classList.remove('hidden');
    hideError();
    hideSuccess();
    hideAttempts();
    panelHint.textContent = 'Informe o e-mail da sua conta e enviaremos um link para redefinir a senha.';
}

document.querySelectorAll('.tab').forEach(button => {
    button.addEventListener('click', () => switchTab(button.dataset.tab));
});

document.getElementById('forgotLink').addEventListener('click', (event) => {
    event.preventDefault();
    showRecoverPanel();
});

document.getElementById('backToLoginLink').addEventListener('click', (event) => {
    event.preventDefault();
    switchTab('login');
});

(async function init() {
    if (new URLSearchParams(window.location.search).get('tab') === 'signup') {
        switchTab('signup');
    }
    await window.TLMAuth.loadAuthConfig();
    const response = await fetch('/api/auth/config');
    const config = await response.json();
    if (!config.enabled) {
        window.location.href = '/index.html';
    }
})();

loginForm.addEventListener('submit', async (event) => {
    event.preventDefault();
    hideError();
    hideSuccess();
    loginBtn.disabled = true;
    try {
        const signedIn = await window.TLMAuth.signIn(emailInput.value, passwordInput.value);
        if (!signedIn) {
            return; // redirecionamento para ativação já iniciado
        }
        const next = new URLSearchParams(window.location.search).get('next');
        window.location.href = (() => {
            if (!next) return '/index.html';
            // O parser WHATWG trata "\\" como "/" em posicao de autoridade,
            // entao next=/\evil.com resolveria para https://evil.com/.
            // Exigir mesma origem cobre isso e qualquer variacao futura.
            if (next.includes('\\')) return '/index.html';
            try {
                const url = new URL(next, window.location.origin);
                if (url.origin !== window.location.origin) return '/index.html';
                return url.pathname + url.search + url.hash;
            } catch (error) {
                return '/index.html';
            }
        })();
    } catch (error) {
        if (error.locked && error.retryAfterSec) {
            startLockoutCountdown(error.retryAfterSec);
        } else {
            showError(error.message || 'Não foi possível entrar.');
            if (typeof error.remainingAttempts === 'number') {
                showAttempts('Tentativas restantes: ' + error.remainingAttempts);
            }
        }
    } finally {
        if (!lockoutTimer) {
            loginBtn.disabled = false;
        }
    }
});

signupForm.addEventListener('submit', async (event) => {
    event.preventDefault();
    hideError();
    hideSuccess();
    signupBtn.disabled = true;
    try {
        const result = await window.TLMAuth.signUp({
            email: document.getElementById('signupEmail').value,
            password: document.getElementById('signupPassword').value,
            displayName: document.getElementById('signupName').value,
            notes: document.getElementById('signupNotes').value,
        });
        signupForm.reset();
        switchTab('login');
        showSuccess(result.message || 'Cadastro recebido. Após o pagamento, o administrador libera o acesso.');
    } catch (error) {
        if (error.locked && error.retryAfterSec) {
            startLockoutCountdown(error.retryAfterSec);
        } else {
            showError(error.message || 'Não foi possível concluir o cadastro.');
        }
    } finally {
        if (!lockoutTimer) {
            signupBtn.disabled = false;
        }
    }
});
recoverForm.addEventListener('submit', async (event) => {
    event.preventDefault();
    hideError();
    hideSuccess();
    recoverBtn.disabled = true;
    try {
        const result = await window.TLMAuth.requestPasswordReset(document.getElementById('recoverEmail').value);
        recoverForm.reset();
        showSuccess(result.message || 'Se este e-mail estiver cadastrado, enviaremos um link de redefinição.');
    } catch (error) {
        if (error.locked && error.retryAfterSec) {
            startLockoutCountdown(error.retryAfterSec);
        } else {
            showError(error.message || 'Não foi possível solicitar a redefinição de senha.');
        }
    } finally {
        if (!lockoutTimer) {
            recoverBtn.disabled = false;
        }
    }
});
