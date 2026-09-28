(function () {
    const byId = id => document.getElementById(id);
    const content = byId('adminContent');
    const message = byId('pageMessage');
    const usersBody = byId('usersBody');
    const livesBody = byId('livesBody');
    const pendingBanner = byId('pendingBanner');
    const orgsBody = byId('orgsBody');
    const teamBody = byId('teamBody');
    const legacyBody = byId('legacyBody');
    const NEW_OWN_ORG = '__new_own_org__';
    // A organização de legado não aceita membros (só o admin da plataforma a vê).
    const LEGACY_ORG_ID = '00000000-0000-0000-0000-000000000001';
    const assignableOrgs = () => organizations.filter(org => org.id !== LEGACY_ORG_ID);
    const ROLE_LABELS = { owner: 'Dono', operator: 'Operador' };
    let organizations = [];
    let me = null;
    // O admin da plataforma sem organização opera o legado: a lista de lives
    // dele é a do legado e cada sessão ganha o botão de mover.
    let viewingLegacy = false;

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

    // Datas de sessão (YYYY-MM-DD) em pt-BR; sem Date() para não deslocar o dia
    // por fuso horário.
    function formatDay(day) {
        const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(String(day || ''));
        return match ? `${match[3]}/${match[2]}/${match[1]}` : (day || '—');
    }

    // Parâmetros do delete: o id seleciona a sessão; live e day são a trava de
    // segurança do backend (409 se não baterem com a sessão).
    function sessionDeleteParams(live) {
        return new URLSearchParams({ id: live.id, live: live.name || '', day: live.day || '' }).toString();
    }

    // ---- Lives do legado: helpers puros (cobertos por admin-legacy.test.mjs) ----

    // Agrupa as sessões do legado por nome da live, a mais recente primeiro.
    function groupLegacyLives(lives) {
        const groups = new Map();
        (lives || []).forEach(live => {
            const name = String(live.name || '');
            let group = groups.get(name);
            if (!group) {
                group = { name, sessionIds: [], events: 0, firstDay: '', lastDay: '' };
                groups.set(name, group);
            }
            group.sessionIds.push(live.id);
            group.events += Number(live.events) || 0;
            const day = String(live.day || '');
            if (day && (!group.firstDay || day < group.firstDay)) group.firstDay = day;
            if (day && day > group.lastDay) group.lastDay = day;
        });
        return [...groups.values()].sort((a, b) =>
            b.lastDay.localeCompare(a.lastDay) || a.name.localeCompare(b.name));
    }

    // Texto do confirm() antes de mover lives do legado.
    function legacyMoveConfirmText(label, sessions, orgName, copySettings) {
        const count = sessions === 1 ? '1 sessão' : `${sessions} sessões`;
        let text = `Mover ${label} (${count}) do legado para "${orgName}"? `
            + 'Todos os eventos dessas sessões passam a ser vistos só por essa organização.';
        if (copySettings) text += ' As configurações do legado vão substituir as da organização.';
        return text;
    }

    // ---- fim dos helpers puros ----

    function actionButton(label, className, handler) {
        const button = document.createElement('button');
        button.type = 'button';
        button.textContent = label;
        if (className) button.className = className;
        button.addEventListener('click', handler);
        return button;
    }

    function renderUsers(users, pendingCount) {
        usersBody.replaceChildren();
        const subscribers = (users || []).filter(user => user.role !== 'admin');
        const pending = typeof pendingCount === 'number'
            ? pendingCount
            : subscribers.filter(user => !user.active).length;
        if (pendingBanner) {
            if (pending > 0) {
                pendingBanner.textContent = pending === 1
                    ? '1 cadastro aguardando confirmação de pagamento.'
                    : pending + ' cadastros aguardando confirmação de pagamento.';
                pendingBanner.classList.add('visible');
            } else {
                pendingBanner.classList.remove('visible');
            }
        }
        if (!subscribers.length) {
            const row = document.createElement('tr');
            const td = cell(row, 'Nenhum assinante cadastrado.');
            td.colSpan = 7;
            usersBody.appendChild(row);
            return;
        }
        subscribers.forEach(user => {
            const row = document.createElement('tr');
            if (!user.active) row.className = 'pending-row';
            cell(row, user.email);
            cell(row, user.displayName);
            const statusCell = document.createElement('td');
            const badge = document.createElement('span');
            badge.className = `badge ${user.active ? 'approved' : 'pending'}`;
            badge.textContent = user.active ? 'Aprovado' : 'Aguardando pagamento';
            statusCell.appendChild(badge);
            row.appendChild(statusCell);
            cell(row, formatDate(user.subscriptionExpiresAt));
            cell(row, user.notes);
            row.appendChild(orgAssignCell(user));
            const actions = document.createElement('td');
            actions.appendChild(actionButton(
                user.active ? 'Suspender' : 'Aprovar pagamento',
                user.active ? 'secondary' : 'approve',
                () => toggleUser(user)
            ));
            actions.appendChild(actionButton('Remover', 'danger', () => deleteUser(user)));
            row.appendChild(actions);
            usersBody.appendChild(row);
        });
    }

    // Select de organização por assinante: trocar move a conta de organização
    // (como operador; o papel de dono é dado na tela de equipe ou na criação).
    function orgAssignCell(user) {
        const td = document.createElement('td');
        const select = document.createElement('select');
        select.appendChild(new Option('Sem organização', ''));
        assignableOrgs().forEach(org => select.appendChild(new Option(org.name, org.id)));
        select.appendChild(new Option('+ Nova organização própria', NEW_OWN_ORG));
        select.value = user.orgId || '';
        select.title = user.orgRole ? ROLE_LABELS[user.orgRole] || user.orgRole : '';
        select.addEventListener('change', () => assignUserOrg(user, select));
        td.appendChild(select);
        if (user.orgRole) {
            const role = document.createElement('div');
            role.className = 'muted';
            role.style.fontSize = '.75rem';
            role.textContent = ROLE_LABELS[user.orgRole] || user.orgRole;
            td.appendChild(role);
        }
        return td;
    }

    async function assignUserOrg(user, select) {
        clearMessage();
        const orgId = select.value;
        try {
            if (orgId === NEW_OWN_ORG) {
                const result = await api('/api/admin/orgs', {
                    method: 'POST', headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ name: user.displayName || user.email, ownerUserId: user.id }),
                });
                if (result.ownerError) throw new Error(result.ownerError);
            } else if (!orgId) {
                if (!confirm(`Remover ${user.email} da organização? A conta perde o acesso aos dados.`)) {
                    select.value = user.orgId || '';
                    return;
                }
                await api('/api/admin/orgs/members?userId=' + encodeURIComponent(user.id), { method: 'DELETE' });
            } else {
                await api('/api/admin/orgs/members', {
                    method: 'POST', headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ orgId, userId: user.id, role: user.orgId === orgId ? user.orgRole : 'operator' }),
                });
            }
            showMessage('Organização do assinante atualizada.', 'success');
            await Promise.all([loadUsers(), loadOrgs()]);
        } catch (error) {
            select.value = user.orgId || '';
            showMessage(error.message);
        }
    }

    async function loadUsers() {
        try {
            const payload = await api('/api/admin/users');
            renderUsers(payload.users || [], payload.pendingCount);
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
        const orgId = byId('subscriberOrg').value;
        if (orgId) {
            body.orgId = orgId;
            body.orgRole = byId('subscriberOrgRole').value;
        }
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
            const body = { id: user.id, active: !user.active };
            if (!user.active && !user.subscriptionExpiresAt) {
                const expires = new Date();
                expires.setDate(expires.getDate() + 30);
                body.subscriptionExpiresAt = expires.toISOString();
            }
            await api('/api/admin/users/update', {
                method: 'PATCH', headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(body),
            });
            showMessage(user.active
                ? 'Assinante suspenso.'
                : (user.subscriptionExpiresAt
                    ? 'Pagamento aprovado e acesso liberado.'
                    : 'Pagamento aprovado. Acesso liberado por 30 dias.'),
                'success');
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

    // ---- Organizações (admin da plataforma) ----

    function fillOrgSelect() {
        const select = byId('subscriberOrg');
        if (!select) return;
        const current = select.value;
        select.replaceChildren(new Option('Sem organização', ''));
        assignableOrgs().forEach(org => select.appendChild(new Option(org.name, org.id)));
        select.value = organizations.some(org => org.id === current) ? current : '';
    }

    function renderOrgs() {
        orgsBody.replaceChildren();
        if (!organizations.length) {
            const row = document.createElement('tr');
            cell(row, 'Nenhuma organização.').colSpan = 6;
            orgsBody.appendChild(row);
            return;
        }
        organizations.forEach(org => {
            const row = document.createElement('tr');
            if (!org.active) row.className = 'inactive-row';
            cell(row, org.name);
            cell(row, org.maxLives);
            const owners = (org.memberList || []).filter(m => m.role === 'owner').map(m => m.email || m.userId);
            cell(row, `${org.members || 0}${owners.length ? ' · donos: ' + owners.join(', ') : ''}`);
            const statusCell = document.createElement('td');
            const badge = document.createElement('span');
            badge.className = `badge ${org.active ? 'approved' : 'pending'}`;
            badge.textContent = org.active ? 'Ativa' : 'Desativada';
            statusCell.appendChild(badge);
            row.appendChild(statusCell);
            cell(row, formatDate(org.createdAt));
            const actions = document.createElement('td');
            actions.appendChild(actionButton('Renomear', 'secondary', () => editOrg(org, 'name')));
            actions.appendChild(actionButton('Limite de lives', 'secondary', () => editOrg(org, 'maxLives')));
            if (org.id !== LEGACY_ORG_ID) {
            if (org.id !== LEGACY_ORG_ID) {
                actions.appendChild(actionButton(org.active ? 'Desativar' : 'Ativar', org.active ? 'danger' : 'approve',
                    () => editOrg(org, 'active')));
            }
            }
            row.appendChild(actions);
            orgsBody.appendChild(row);
        });
    }

    async function loadOrgs() {
        try {
            organizations = (await api('/api/admin/orgs')).organizations || [];
            renderOrgs();
            fillOrgSelect();
            // Os seletores de destino dependem das organizações (e de estarem ativas).
            const reloads = [];
            if (!byId('legacyPanel').hidden) reloads.push(loadLegacy());
            if (viewingLegacy) reloads.push(loadLives());
            await Promise.all(reloads);
        } catch (error) {
            showMessage(error.message);
        }
    }

    async function createOrg() {
        clearMessage();
        const body = {
            name: byId('orgName').value,
            maxLives: Number(byId('orgMaxLives').value) || 0,
            ownerEmail: byId('orgOwnerEmail').value,
            ownerPassword: byId('orgOwnerPassword').value,
            ownerName: byId('orgOwnerName').value,
        };
        try {
            const result = await api('/api/admin/orgs', {
                method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
            });
            ['orgName', 'orgMaxLives', 'orgOwnerEmail', 'orgOwnerPassword', 'orgOwnerName']
                .forEach(id => { byId(id).value = ''; });
            // Recarrega antes da mensagem: um erro de recarga não pode esconder
            // que o dono não foi cadastrado.
            await Promise.all([loadOrgs(), loadUsers()]);
            if (result.ownerError) {
                showMessage(`Organização criada, mas o dono não foi cadastrado: ${result.ownerError}`);
            } else {
                showMessage('Organização criada.', 'success');
            }
        } catch (error) {
            showMessage(error.message);
        }
    }

    async function editOrg(org, field) {
        clearMessage();
        const body = { id: org.id };
        if (field === 'name') {
            const name = prompt('Novo nome da organização:', org.name);
            if (name == null) return;
            body.name = name;
        } else if (field === 'maxLives') {
            const value = prompt('Lives simultâneas permitidas (1 a 50):', String(org.maxLives));
            if (value == null) return;
            body.maxLives = Number(value);
        } else {
            if (org.active && !confirm(`Desativar "${org.name}"? As lives dela são encerradas e os membros perdem o acesso.`)) return;
            body.active = !org.active;
        }
        try {
            await api('/api/admin/orgs/update', {
                method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
            });
            showMessage('Organização atualizada.', 'success');
            await loadOrgs();
        } catch (error) {
            showMessage(error.message);
        }
    }

    // ---- Equipe (dono da organização) ----

    function renderTeam(members) {
        teamBody.replaceChildren();
        if (!members.length) {
            const row = document.createElement('tr');
            cell(row, 'Nenhum membro.').colSpan = 4;
            teamBody.appendChild(row);
            return;
        }
        members.forEach(member => {
            const row = document.createElement('tr');
            cell(row, member.email || member.userId);
            cell(row, ROLE_LABELS[member.role] || member.role);
            cell(row, formatDate(member.createdAt));
            const actions = document.createElement('td');
            if (me && member.userId === me.id) {
                actions.textContent = 'Você';
                actions.className = 'muted';
            } else {
                const nextRole = member.role === 'owner' ? 'operator' : 'owner';
                actions.appendChild(actionButton(
                    nextRole === 'owner' ? 'Tornar dono' : 'Tornar operador', 'secondary',
                    () => setMemberRole(member, nextRole)));
                actions.appendChild(actionButton('Remover', 'danger', () => removeMember(member)));
            }
            row.appendChild(actions);
            teamBody.appendChild(row);
        });
    }

    async function loadTeam() {
        try {
            renderTeam((await api('/api/org/members')).members || []);
        } catch (error) {
            showMessage(error.message);
        }
    }

    async function createMember() {
        clearMessage();
        const body = {
            email: byId('memberEmail').value,
            password: byId('memberPassword').value,
            displayName: byId('memberName').value,
            role: byId('memberRole').value,
        };
        try {
            await api('/api/org/members', {
                method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
            });
            ['memberEmail', 'memberPassword', 'memberName'].forEach(id => { byId(id).value = ''; });
            showMessage('Membro adicionado. Ele já pode entrar com o e-mail e a senha informados.', 'success');
            await loadTeam();
        } catch (error) {
            showMessage(error.message);
        }
    }

    async function setMemberRole(member, role) {
        clearMessage();
        try {
            await api('/api/org/members/update', {
                method: 'PATCH', headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ userId: member.userId, role }),
            });
            showMessage('Papel atualizado.', 'success');
            await loadTeam();
        } catch (error) {
            showMessage(error.message);
        }
    }

    async function removeMember(member) {
        if (!confirm(`Remover ${member.email || member.userId} da equipe? A conta será excluída.`)) return;
        clearMessage();
        try {
            await api('/api/org/members/delete?userId=' + encodeURIComponent(member.userId), { method: 'POST' });
            showMessage('Membro removido.', 'success');
            await loadTeam();
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
            if (viewingLegacy) {
                const select = targetOrgSelect();
                actions.appendChild(select);
                actions.appendChild(actionButton('Mover', 'secondary', () =>
                    moveLegacy({ sessionIds: [live.id] }, `a live "${live.name}" de ${formatDay(live.day)}`, 1, select)));
            }
            actions.appendChild(actionButton('Deletar', 'danger', () => deleteLive(live)));
            row.appendChild(actions);
            livesBody.appendChild(row);
        });
    }

    // ---- Lives do legado (admin da plataforma) ----

    // Destinos possíveis: organizações de cliente ativas.
    function targetOrgSelect() {
        const select = document.createElement('select');
        select.appendChild(new Option('Escolha a organização', ''));
        assignableOrgs().filter(org => org.active)
            .forEach(org => select.appendChild(new Option(org.name, org.id)));
        return select;
    }

    function renderLegacy(lives) {
        legacyBody.replaceChildren();
        const groups = groupLegacyLives(lives);
        if (!groups.length) {
            const row = document.createElement('tr');
            cell(row, 'Nenhuma live no legado.').colSpan = 6;
            legacyBody.appendChild(row);
            return;
        }
        groups.forEach(group => {
            const row = document.createElement('tr');
            cell(row, group.name);
            cell(row, group.sessionIds.length);
            cell(row, group.firstDay === group.lastDay
                ? formatDay(group.firstDay)
                : `${formatDay(group.firstDay)} a ${formatDay(group.lastDay)}`);
            cell(row, group.events);
            const selectCell = document.createElement('td');
            const select = targetOrgSelect();
            selectCell.appendChild(select);
            row.appendChild(selectCell);
            const actions = document.createElement('td');
            actions.appendChild(actionButton('Mover para organização', 'approve', () =>
                moveLegacy({ liveNames: [group.name] }, `a live "${group.name}"`, group.sessionIds.length, select)));
            row.appendChild(actions);
            legacyBody.appendChild(row);
        });
    }

    async function loadLegacy() {
        try {
            renderLegacy((await api('/api/admin/lives/assign?limit=500')).lives || []);
        } catch (error) {
            showMessage(error.message);
        }
    }

    async function moveLegacy(selection, label, sessions, select) {
        clearMessage();
        const org = organizations.find(o => o.id === select.value);
        if (!org) {
            showMessage('Escolha a organização de destino.');
            return;
        }
        const copySettings = byId('legacyCopySettings').checked;
        if (!confirm(legacyMoveConfirmText(label, sessions, org.name, copySettings))) return;
        try {
            const result = await api('/api/admin/lives/assign', {
                method: 'POST', headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ ...selection, orgId: org.id, copySettings }),
            });
            const moved = result.moved === 1 ? '1 sessão movida' : `${result.moved} sessões movidas`;
            showMessage(`${moved} para "${org.name}"${result.settingsCopied ? ' (configurações copiadas)' : ''}.`, 'success');
            const reloads = [loadLegacy()];
            if (viewingLegacy) reloads.push(loadLives());
            await Promise.all(reloads);
        } catch (error) {
            showMessage(error.message);
        }
    }

    async function loadLives() {
        try {
            renderLives((await api('/api/admin/lives?limit=200')).lives || []);
        } catch (error) {
            showMessage(error.message);
        }
    }

    async function deleteLive(live) {
        if (!live || !live.id) return;
        if (!confirm(`Deletar os dados da live "${live.name}" do dia ${formatDay(live.day)}? Essa ação não pode ser desfeita.`)) return;
        try {
            await api('/api/admin/lives/session/delete?' + sessionDeleteParams(live), { method: 'POST' });
            showMessage('Live removida.', 'success');
            await loadLives();
        } catch (error) {
            showMessage(error.message);
        }
    }

    byId('backBtn').addEventListener('click', () => { window.location.href = '/index.html'; });
    byId('logoutBtn').addEventListener('click', () => window.TLMAuth.signOut());
    byId('refreshUsersBtn').addEventListener('click', loadUsers);
    byId('refreshLivesBtn').addEventListener('click', loadLives);
    byId('createSubscriberBtn').addEventListener('click', createUser);
    byId('refreshOrgsBtn').addEventListener('click', loadOrgs);
    byId('createOrgBtn').addEventListener('click', createOrg);
    byId('refreshTeamBtn').addEventListener('click', loadTeam);
    byId('refreshLegacyBtn').addEventListener('click', loadLegacy);
    byId('createMemberBtn').addEventListener('click', createMember);

    (async function bootstrap() {
        const user = await window.TLMAuth.requireOrgManager();
        if (!user) return;
        me = user;
        const platformAdmin = user.role === 'admin';
        const hasOrg = !!user.orgId && !user.orgError;
        const who = user.email || user.displayName || user.id || 'modo local';
        byId('adminIdentity').textContent = platformAdmin
            ? `Administrador da plataforma: ${who}${user.orgName ? ' · ' + user.orgName : ''}`
            : `Dono de ${user.orgName || 'organização'}: ${who}`;
        if (user.orgName) byId('teamTitle').textContent = `Equipe — ${user.orgName}`;
        content.style.display = 'block';
        viewingLegacy = platformAdmin && user.orgId === LEGACY_ORG_ID;
        byId('orgsPanel').hidden = !platformAdmin;
        byId('legacyPanel').hidden = !platformAdmin;
        byId('usersPanel').hidden = !platformAdmin;
        byId('teamPanel').hidden = !(hasOrg && user.canManageOrg) || viewingLegacy;
        byId('livesPanel').hidden = !(hasOrg && user.canManageOrg);
        if (viewingLegacy) byId('livesTitle').textContent = 'Lives e horários — legado';
        const tasks = [];
        // loadOrgs também carrega as lives do legado (e a lista de lives quando
        // ela é a do legado), que precisam das organizações de destino.
        if (platformAdmin) tasks.push(loadOrgs().then(loadUsers));
        if (hasOrg && user.canManageOrg && !viewingLegacy) tasks.push(loadTeam(), loadLives());
        await Promise.all(tasks);
    })();
})();
