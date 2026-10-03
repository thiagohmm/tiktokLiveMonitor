(function () {
    const money = cents => (cents / 100).toLocaleString('pt-BR', {style:'currency',currency:'BRL'});
    const date = value => value ? new Date(value).toLocaleString('pt-BR',{timeZone:'America/Sao_Paulo'}) : 'sem liberação';
    const node = (tag,text) => {const element=document.createElement(tag);if(text!==undefined)element.textContent=text;return element;};
    async function api(path,body,method) {
        const options=body===undefined?{}:{method:method||'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)};
        if(method)options.method=method;
        const response=await TLMAuth.authFetch(path,options);const data=await response.json();if(!response.ok)throw new Error(data.error||'Operação não concluída');return data;
    }
    function field(form,label,type,value='') {
        const wrapper=node('label',label);const input=node('input');input.type=type;input.value=value;wrapper.appendChild(input);form.appendChild(wrapper);return input;
    }
    function button(parent,label,action) {
        const element=node('button',label);element.type='button';element.className='secondary';element.addEventListener('click',action);parent.appendChild(element);return element;
    }
    function summaryText(summary) {
        const occupied=summary.seats.filter(s=>s.userId);
        return `${occupied.filter(s=>s.included).length} de 2 vagas incluídas · ${occupied.filter(s=>!s.included&&!s.suspended).length} de ${summary.extra} vagas extras · ${summary.pending} convites pendentes · ${money(summary.priceCents)} por vaga/mês · vencimento ${date(summary.expiresAt)}`;
    }
    function allocation(parent,members,summary,submit,admin) {
        const form=node('form');form.className='form-grid';form.appendChild(node('p','Alocação da equipe: vagas 1 e 2 estão incluídas. Vagas seguintes exigem liberação mensal.'));
        const primary=node('select');members.filter(m=>m.role==='owner').forEach(m=>primary.appendChild(new Option(m.email||m.userId,m.userId)));
        primary.value=summary.primaryOwner||members.find(m=>m.role==='owner')?.userId||'';primary.disabled=!admin;
        const primaryLabel=node('label','Dono principal');primaryLabel.appendChild(primary);form.appendChild(primaryLabel);
        const rows=node('div');form.appendChild(rows);let controls=[];
        function render() {
            rows.replaceChildren();controls=[];
            members.filter(m=>m.userId!==primary.value).forEach((member,index)=>{
                const label=node('label',member.email||member.userId);const input=node('input');input.type='number';input.min='1';input.max='1002';input.required=true;
                input.value=summary.seats.find(s=>s.userId===member.userId)?.number||index+1;label.appendChild(input);rows.appendChild(label);controls.push({member,input});
            });
        }
        primary.addEventListener('change',render);render();
        const status=node('p');form.appendChild(status);const save=node('button',admin?'Regularizar / salvar alocação':'Salvar alocação');form.appendChild(save);
        form.addEventListener('submit',async event=>{event.preventDefault();save.disabled=true;try{await submit({primaryOwner:primary.value,seats:controls.map(c=>({userId:c.member.userId,number:Number(c.input.value)}))});status.textContent='Alocação salva.';}catch(error){status.textContent=error.message;}finally{save.disabled=false;}});parent.appendChild(form);
    }
    async function loadOwner(members) {
        const container=document.getElementById('teamSeats');const list=document.getElementById('teamInvitations');if(!container||!list)return;
        const [summary,invitations]=await Promise.all([api('/api/org/seats'),api('/api/org/invitations')]);container.replaceChildren();list.replaceChildren();
        container.appendChild(node('p',summaryText(summary)));
        container.appendChild(node('p','Para contratar ou renovar vagas extras, confirme o pagamento com o administrador: thiagohmm@gmail.com.'));
        const inviteButton=document.getElementById('createMemberBtn');if(inviteButton)inviteButton.disabled=!summary.enabled;
        if(!summary.enabled)container.appendChild(node('p','A equipe precisa ser regularizada pelo administrador geral antes de novos convites.'));
        else allocation(container,members,summary,async body=>{await api('/api/org/seats',body);await loadOwner(members);},false);
        list.appendChild(node('h3','Convites'));
        for(const invitation of invitations.invitations||[]) {
            const row=node('p',`${invitation.email} · ${invitation.status} · envio ${invitation.delivery} · validade ${date(invitation.expiresAt)}`);
            if(invitation.status==='pending') {
                button(row,'Reenviar',async()=>{try{await api(`/api/org/invitations/${invitation.id}/resend`,{});await loadOwner(members);}catch(error){alert(error.message);}});
                button(row,'Cancelar',async()=>{try{await api(`/api/org/invitations/${invitation.id}`,undefined,'DELETE');await loadOwner(members);}catch(error){alert(error.message);}});
            }
            list.appendChild(row);
        }
    }
    async function openAdmin(org) {
        document.getElementById('teamAdministration')?.remove();
        const panel=node('section');panel.id='teamAdministration';panel.className='panel';panel.appendChild(node('h2',`Equipe e mensalidade — ${org.name}`));
        button(panel,'Fechar',()=>panel.remove());document.getElementById('adminContent').prepend(panel);
        const prefix=`/api/admin/orgs/${encodeURIComponent(org.id)}`;
        try {
            const data=await api(prefix+'/seat-subscription');panel.appendChild(node('p',summaryText(data.summary)));
            const priceForm=node('form');priceForm.className='form-grid';const price=field(priceForm,'Preço global por vaga (R$)','number',data.summary.priceCents/100);price.step='.01';price.min='.01';price.required=true;
            priceForm.appendChild(node('button','Salvar preço'));const priceStatus=node('p');priceForm.appendChild(priceStatus);
            priceForm.addEventListener('submit',async event=>{event.preventDefault();try{await api('/api/admin/billing/price',{priceCents:Math.round(Number(price.value)*100)});await openAdmin(org);}catch(error){priceStatus.textContent=error.message;}});panel.appendChild(priceForm);
            panel.appendChild(node('h3','Confirmar pagamento / liberar período'));
            const form=node('form');form.className='form-grid';const reference=field(form,'Referência única do pagamento','text');reference.required=true;
            const quantity=field(form,'Quantidade de vagas extras','number',1);quantity.min='0';quantity.max='1000';quantity.required=true;
            const amount=field(form,'Valor recebido (R$)','number',data.summary.priceCents/100);amount.step='.01';amount.min='0';amount.required=true;
            const start=field(form,'Início (vazio: próximo período disponível)','datetime-local');const end=field(form,'Vencimento (vazio: um mês)','datetime-local');
            const kind=node('select');kind.append(new Option('Pagamento mensal','payment'),new Option('Liberação temporária','temporary'));form.appendChild(kind);const notes=field(form,'Observação / justificativa','text');
            quantity.addEventListener('input',()=>{if(kind.value==='payment')amount.value=(Number(quantity.value)*data.summary.priceCents/100).toFixed(2)});
            const submit=node('button','Registrar liberação');const status=node('p');form.append(submit,status);
            form.addEventListener('submit',async event=>{event.preventDefault();submit.disabled=true;try{const body={reference:reference.value,quantity:Number(quantity.value),amountCents:Math.round(Number(amount.value)*100),kind:kind.value,notes:notes.value};if(start.value)body.startsAt=new Date(start.value).toISOString();if(end.value)body.expiresAt=new Date(end.value).toISOString();await api(prefix+'/seat-subscription',body);await openAdmin(org);}catch(error){status.textContent=error.message;submit.disabled=false;}});panel.appendChild(form);
            panel.appendChild(node('h3','Regularização e posição das vagas'));
            allocation(panel,org.memberList||[],data.summary,async body=>{await api(prefix+'/allocation',body);await openAdmin(org);},true);
            panel.appendChild(node('h3','Convidar ajudante'));
            const invitationForm=node('form');invitationForm.className='form-grid';const email=field(invitationForm,'E-mail do convidado','email');email.required=true;invitationForm.appendChild(node('button','Enviar convite'));const invitationStatus=node('p');invitationForm.appendChild(invitationStatus);
            invitationForm.addEventListener('submit',async event=>{event.preventDefault();try{await api(prefix+'/invitations',{email:email.value});await openAdmin(org);}catch(error){invitationStatus.textContent=error.message;}});panel.appendChild(invitationForm);
            const invitations=await api(prefix+'/invitations');for(const invitation of invitations.invitations){const row=node('p',`${invitation.email} · ${invitation.status} · envio ${invitation.delivery}`);if(invitation.status==='pending'){button(row,'Reenviar',async()=>{try{await api(prefix+'/invitations/'+invitation.id+'/resend',{});await openAdmin(org);}catch(error){alert(error.message);}});button(row,'Cancelar',async()=>{try{await api(prefix+'/invitations/'+invitation.id,undefined,'DELETE');await openAdmin(org);}catch(error){alert(error.message);}});}panel.appendChild(row);}
            panel.appendChild(node('h3','Contas TikTok autorizadas'));
            const liveData=await api(prefix+'/allowed-lives');for(const live of liveData.lives){const row=node('p',`${live.username} · ${live.active?'ativa':'desativada'}`);button(row,live.active?'Desativar':'Ativar',async()=>{try{await api(prefix+'/allowed-lives',{username:live.username,active:!live.active});await openAdmin(org);}catch(error){alert(error.message);}});panel.appendChild(row);}
            const liveForm=node('form');liveForm.className='form-grid';const username=field(liveForm,'Username TikTok','text');username.required=true;liveForm.appendChild(node('button','Autorizar conta'));const liveStatus=node('p');liveForm.appendChild(liveStatus);
            liveForm.addEventListener('submit',async event=>{event.preventDefault();try{await api(prefix+'/allowed-lives',{username:username.value,active:true});await openAdmin(org);}catch(error){liveStatus.textContent=error.message;}});panel.appendChild(liveForm);
            panel.appendChild(node('h3','Períodos registrados'));
            data.payments.forEach(payment=>panel.appendChild(node('p',`${payment.reference} · ${payment.quantity} vagas · ${money(payment.amountCents)} · ${date(payment.startsAt)} até ${date(payment.expiresAt)} · ${payment.kind} · ${payment.notes}`)));
            button(panel,'Consultar auditoria',async()=>{try{const audit=await api(prefix+'/audit');const output=node('pre');output.style.whiteSpace='pre-wrap';output.textContent=JSON.stringify(audit.audit,null,2);panel.appendChild(output);}catch(error){alert(error.message);}});
        } catch(error) {panel.appendChild(node('p',error.message));}
        panel.scrollIntoView({behavior:'smooth'});
    }
    window.TeamUI={loadOwner,openAdmin};
})();
