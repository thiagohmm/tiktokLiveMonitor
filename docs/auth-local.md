# Autenticação local e equipes no VPS

Usuários, senhas, sessões, organizações e cobrança de vagas ficam no PostgreSQL
do VPS. SMTP/Resend apenas entrega mensagens. A conta administrativa existente
é importada pelo ID e papel originais; e-mail não concede administração.

## Primeira publicação

Não publique o frontend novo com o backend antigo nem remova credenciais da
origem antes da exportação. A troca de autenticação exige novo login e nova
senha para todos. Atualizações seguintes seguem o deploy normal.

1. Confirme imagem, compose e diretório realmente utilizados pelos containers;
   o diretório de deploy pode estar desatualizado em relação à imagem ativa.
2. Faça backup do PostgreSQL e copie o `.env` antigo para uma pasta privada de
   backup (permissões 700/600). Não imprima seus valores.
3. Exporte os usuários e perfis da origem, sem senhas ou sessões:

   ```sh
   python3 deploy/export-legacy-users.py --source-env backups/auth-before.env --output backups/users.json
   ```

   Esse utilitário é exclusivamente de migração; a aplicação não o utiliza.
   Ele interrompe a exportação se perfis e metadados divergirem em papel/ativação.
4. Gere a nova imagem, que também contém `/app/import-identities`.
5. Coloque a aplicação em manutenção e repita a exportação final para um novo
   arquivo. Não permita alterações administrativas na origem durante a troca.
6. Execute o importador em um container temporário da imagem nova, com o mesmo
   ambiente e rede do backend. Valide primeiro com rollback:

   ```sh
   docker compose -f docker-compose.production.yml run --rm --no-deps \
     -v "$PWD/backups:/migration" --entrypoint /app/import-identities backend \
     --file /migration/users.json
   ```

7. Aplique após resolver inconsistências. Preserve os links num arquivo privado:

   ```sh
   docker compose -f docker-compose.production.yml run --rm --no-deps \
     -v "$PWD/backups:/migration" --entrypoint /app/import-identities backend \
     --file /migration/users.json --apply \
     --activation-links-file /migration/activation-links.tsv
   ```

   O arquivo é criado com modo 600 e não deve ser enviado ao repositório. Abrir
   o link correspondente ao administrador define sua nova senha sem trocar ID
   ou papel. O importador nunca sobrescreve senhas locais já definidas.
8. Suba o backend novo e publique o frontend completo, incluindo `invite.html`,
   `invite.js` e `teams-ui.js`. Valide o login administrativo antes de reabrir.
9. Envie os links aos usuários com uma execução adicional usando `--apply
   --send-activation`, com SMTP/Resend configurado. Cada reenvio invalida o link
   anterior daquele usuário; contas com senha local já definida são ignoradas.
10. Remova `SUPABASE_*` do ambiente da aplicação, preserve o backup e valide
    operação sem acesso à origem. Não há fallback remoto de autenticação.

Mantenha o backup e o projeto antigo por pelo menos sete dias e até concluir a
validação. Não execute `docker compose down -v`. Falhas após a troca exigem
correção local ou restauração coordenada; sessões antigas não são reaproveitadas.

## Equipes e lives

- Dono principal + duas vagas adicionais incluídas. Outros donos consomem vagas.
- Ajudantes visualizam dados; donos operam e gerenciam. A API aplica a regra,
  inclusive em chamadas feitas fora do navegador.
- Convites valem sete dias e reservam vagas. Reenvio invalida o link anterior.
- Revogação remove o vínculo, preserva conta/histórico e desconecta SSE.
- Apenas o administrador geral registra usernames TikTok autorizados.
- Regras antigas permanecem sem expansão até regularização. No painel de cada
  organização, registre as vagas extras necessárias e selecione o dono principal
  e a posição de todos os membros antes de salvar a alocação.
- Salvar a primeira alocação ativa as regras da organização. Cadastre as contas
  TikTok antes disso. Novas organizações com dono já são ativadas nesse fluxo.

## Mensalidades

No painel administrativo, abra **Equipe e mensalidade** na organização.

1. Configure o preço global por vaga em reais.
2. Registre referência única, quantidade extra, valor recebido e período.
3. Para exceções, selecione liberação temporária e informe justificativa.
4. Para redução, realoque/revogue ocupantes das vagas removidas primeiro.

Renovação antecipada começa no vencimento atual. Vencimento sugerido é um mês
calendário, ajustado ao último dia do mês e exibido no horário de São Paulo.
Períodos sobrepostos e referências repetidas são rejeitados. Vencimento suspende
apenas vagas extras; renovar não restaura membros revogados.

## Configuração e manutenção

`DATABASE_URL`, `AUTH_ENABLED=1` e `SITE_URL` substituem a configuração de
identidade anterior. `SITE_URL` deve ser a origem pública exata (HTTPS em
produção). Alterações exigem Origin e CSRF; proxies devem preservar cookies e
cabeçalhos. `AUTH_ENABLED=0` é recusado com origem HTTPS.

Backup inclui usuários, hashes, sessões, tokens e todas as tabelas operacionais.
Não exponha o PostgreSQL ou os backups publicamente. O SMTP segue usando
`SMTP_*` ou `RESEND_API_KEY` e `MAIL_FROM`.

## Validação

Use PostgreSQL descartável em `TEST_DATABASE_URL`; nunca aponte testes para
produção. Execute `go test -race ./...`, `go vet ./...`, lint e `npm test`.

Critérios: login/recuperação local, conta administrativa preservada, nenhuma
chamada remota de identidade, isolamento entre duas organizações, leitura dos
ajudantes, concorrência de convites, revogação e vencimento com SSE aberto.
