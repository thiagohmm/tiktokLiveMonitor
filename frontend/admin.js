(function () {
    const byId = id => document.getElementById(id);
    const content = byId('adminContent');
    const message = byId('pageMessage');
    const usersBody = byId('usersBody');
    const livesBody = byId('livesBody');

    function showMessage(text, kind = 'error') {
        message.textContent = text;
        message.className = `message visible ${kind}`;
    }

    function clearMessage() {
        message.textContent = '';
        message.className = 'message';
    }

    function cell(row, value) {
        const td = document.createElement('td');
        td.textContent = value == null || value === '' ? '—' : String(value);
        row.appendChild(td);
        return td;
    }

    async function api(path, options) {
        const response = await window.TLMAuth.authFetch(path, options);
        const payload = await response.json().catch(() => ({}));
        if (!response.ok) throw new Error(payload.error || `Erro HTTP ${response.status}`);
        return payload;
    }

    function formatDate(value) {
        if (!value) return '—';
        const date = new Date(value);
        return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString('pt-BR');
    }

    function formatDuration(start, end) {
        const ms = new Date(end).getTime() - new Date(start).getTime();
        if (!Number.isFinite(ms) || ms < 0) return '—';
        const minutes = Math.round(ms / 60000);
        return `${Math.floor(minutes / 60)}h ${minutes % 60}min`;
    }

    function actionButton(label, className, handler) {
        const button = document.createElement('button');
        button.type = 'button';
        button.textContent = label;
        if (className) button.className = className;
        button.addEventListener('click', handler);
        return button;
    }

    function renderUsers(users) {
        usersBody.replaceChildren();
        const subscribers = (users || []).filter(user => user.role !== 'admin');
        if (!subscribers.length) {
            const row = document.createElement('tr');
            const td = cell(row, 'Nenhum assinante cadastrado.');
            td.colSpan = 6;
            usersBody.appendChild(row);
            return;
        }
        subscribers.forEach(user => {
            const row = document.createElement('tr');
            cell(row, user.email);
            cell(row, user.displayName);
            const statusCell = document.createElement('td');
            const badge = document.createElement('span');
            badge.className = `badge ${user.active ? 'approved' : 'pending'}`;
            badge.textContent = user.active ? 'Aprovado' : 'Aguardando aprovação';
            statusCell.appendChild(badge);
            row.appendChild(statusCell);
            cell(row, formatDate(user.subscriptionExpiresAt));
            cell(row, user.notes);
            const actions = document.createElement('td');
            actions.appendChild(actionButton(user.active ? 'Suspender' : 'Aprovar', 'secondary', () => toggleUser(user)));
            actions.appendChild(actionButton('Remover', 'danger', () => deleteUser(user)));
            row.appendChild(actions);
            usersBody.appendChild(row);
        });
    }

    async function loadUsers() {
        try {
            renderUsers((await api('/api/admin/users')).users || []);
        } catch (error) {
            showMessage(error.message);
        }
    }

    async function createUser() {
        clearMessage();
        const expires = byId('subscriberExpires').value;
        const body = {
            email: byId('subscriberEmail').value,
            password: byId('subscriberPassword').value,
            displayName: byId('subscriberName').value,
            notes: byId('subscriberNotes').value,
        };
        if (expires) body.subscriptionExpiresAt = new Date(expires).toISOString();
        try {
            await api('/api/admin/users', {
                method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
            });
            ['subscriberEmail', 'subscriberPassword', 'subscriberName', 'subscriberExpires', 'subscriberNotes']
                .forEach(id => { byId(id).value = ''; });
            showMessage('Assinante cadastrado e aguardando aprovação.', 'success');
            await loadUsers();
        } catch (error) {
            showMessage(error.message);
        }
    }

    async function toggleUser(user) {
        try {
            await api('/api/admin/users/update', {
                method: 'PATCH', headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ id: user.id, active: !user.active }),
            });
            showMessage(user.active ? 'Assinante suspenso.' : 'Pagamento aprovado e acesso liberado.', 'success');
            await loadUsers();
        } catch (error) {
            showMessage(error.message);
        }
    }

    async function deleteUser(user) {
        if (!confirm(`Remover o assinante ${user.email}?`)) return;
        try {
            await api('/api/admin/users/delete?id=' + encodeURIComponent(user.id), { method: 'POST' });
            showMessage('Assinante removido.', 'success');
            await loadUsers();
        } catch (error) {
            showMessage(error.message);
        }
    }

    function renderLives(lives) {
        livesBody.replaceChildren();
        if (!lives || !lives.length) {
            const row = document.createElement('tr');
            const td = cell(row, 'Nenhuma live registrada.');
            td.colSpan = 7;
            livesBody.appendChild(row);
            return;
        }
        lives.forEach(live => {
            const row = document.createElement('tr');
            cell(row, live.name);
            cell(row, live.day);
            cell(row, formatDate(live.startedAt));
            cell(row, formatDate(live.endedAt));
            cell(row, formatDuration(live.startedAt, live.endedAt));
            cell(row, live.events || 0);
            const actions = document.createElement('td');
            actions.appendChild(actionButton('Deletar', 'danger', () => deleteLive(live.name)));
            row.appendChild(actions);
            livesBody.appendChild(row);
        });
    }

    async function loadLives() {
        try {
            renderLives((await api('/api/admin/lives?limit=200')).lives || []);
        } catch (error) {
            showMessage(error.message);
        }
    }

    async function deleteLive(name) {
        if (!confirm(`Deletar todos os dados da live "${name}"?`)) return;
        try {
            await api('/api/admin/lives/delete?live=' + encodeURIComponent(name), { method: 'POST' });
            showMessage('Live removida.', 'success');
            await loadLives();
        } catch (error) {
            showMessage(error.message);
        }
    }

    byId('backBtn').addEventListener('click', () => { window.location.href = '/'; });
    byId('logoutBtn').addEventListener('click', () => window.TLMAuth.signOut());
    byId('refreshUsersBtn').addEventListener('click', loadUsers);
    byId('refreshLivesBtn').addEventListener('click', loadLives);
    byId('createSubscriberBtn').addEventListener('click', createUser);

    (async function bootstrap() {
        const user = await window.TLMAuth.requireAdmin();
        if (!user) {
            if (window.location.pathname === '/admin.html') {
                showMessage('Administração indisponível. Configure e ative a autenticação Supabase.');
            }
            return;
        }
        byId('adminIdentity').textContent = `Administrador: ${user.email || user.displayName || user.id}`;
        content.style.display = 'block';
        await Promise.all([loadUsers(), loadLives()]);
    })();
})();
