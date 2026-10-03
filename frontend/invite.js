(async function () {
    const token = new URLSearchParams(location.hash.slice(1)).get('token') || sessionStorage.getItem('tlm.invitation') || '';
    if (token) sessionStorage.setItem('tlm.invitation', token);
    history.replaceState(null, '', location.pathname);
    await TLMAuth.loadAuthConfig();
    const user = await TLMAuth.refreshMe();
    document.getElementById('identity').textContent = user ? `Aceitar com ${user.email}` : 'Defina sua senha se ainda não possui conta.';
    document.getElementById('passwordLabel').hidden = !!user;
    document.getElementById('invitePassword').required = !user;
    const result = document.getElementById('result');
    if (!token) { result.textContent = 'Abra o link do convite recebido por e-mail.'; document.getElementById('inviteForm').hidden = true; }
    document.getElementById('inviteForm').addEventListener('submit', async event => {
        event.preventDefault();
        const button=event.currentTarget.querySelector('button');button.disabled=true;
        try {
            const response = await TLMAuth.authFetch('/api/auth/invitations/accept', {method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({token,password:document.getElementById('invitePassword').value,displayName:document.getElementById('inviteName').value})});
            const data=await response.json();if(!response.ok)throw new Error(data.error);
            sessionStorage.removeItem('tlm.invitation');result.textContent='Convite aceito. Entre para acompanhar a organização.';
            location.href=user?'/':'/login.html';
        } catch(error) {result.textContent=error.message;button.disabled=false;}
    });
})();
