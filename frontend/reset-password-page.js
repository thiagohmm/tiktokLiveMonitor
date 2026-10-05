const resetForm = document.getElementById('resetForm');
const resetBtn = document.getElementById('resetBtn');
const newPasswordInput = document.getElementById('newPassword');
const confirmInput = document.getElementById('confirmPassword');
const errorBox = document.getElementById('resetError');
const successBox = document.getElementById('resetSuccess');

const hashParams = new URLSearchParams(window.location.hash.replace(/^#/, ''));
const accessToken = hashParams.get('token') || '';
const linkType = hashParams.get('type') || '';
if (accessToken) history.replaceState(null, '', window.location.pathname + window.location.search);

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

function disableForm() {
    newPasswordInput.disabled = true;
    confirmInput.disabled = true;
    resetBtn.disabled = true;
}

(async function init() {
    await window.TLMAuth.loadAuthConfig();
    if (!accessToken || !['recovery','activation'].includes(linkType)) {
        disableForm();
        showError('Link inválido ou expirado. Solicite uma nova redefinição de senha.');
        return;
    }

})();

resetForm.addEventListener('submit', async (event) => {
    event.preventDefault();
    if (!accessToken || !["recovery","activation"].includes(linkType)) return;
    hideError();
    hideSuccess();
    const password = newPasswordInput.value;
    if (password.length < 12) {
        showError('A nova senha deve ter pelo menos 12 caracteres.');
        return;
    }
    if (password !== confirmInput.value) {
        showError('As senhas não conferem.');
        return;
    }
    resetBtn.disabled = true;
    try {
        await window.TLMAuth.resetPassword(accessToken, password);
        showSuccess('Senha redefinida com sucesso. Redirecionando para o login...');
        setTimeout(() => { window.location.href = '/login.html'; }, 1500);
    } catch (error) {
        showError(error.message || 'Não foi possível redefinir a senha.');
        resetBtn.disabled = false;
    }
});
