package com.tlm.cli.domain.port;

import com.tlm.cli.domain.model.ApiException;
import com.tlm.cli.domain.model.UserSession;

/** Porta de autenticação (login e renovação de CSRF). */
public interface AuthPort {

    /**
     * Faz login e extrai o token do cookie {@code tlm_session} (header
     * {@code Set-Cookie}) junto com o {@code csrfToken} do corpo. 401/403/429
     * e {@code needsActivation} chegam como {@link ApiException}.
     */
    UserSession login(String email, char[] password) throws ApiException;

    /** {@code GET /api/auth/me}: devolve sessão com {@code csrfToken} atualizado. */
    UserSession me(UserSession session) throws ApiException;
}
