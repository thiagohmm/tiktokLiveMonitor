package com.tlm.cli.domain.port;

import com.tlm.cli.domain.model.ApiException;
import com.tlm.cli.domain.model.UserSession;

/** Porta do stream SSE ({@code GET /events}). */
public interface EventStreamPort {

    /**
     * Abre o stream e retorna imediatamente; a leitura acontece em thread
     * própria (virtual) e os desfechos chegam por {@link EventStreamListener}.
     *
     * @throws ApiException quando a conexão é recusada (401, 503, rede...).
     */
    StreamHandle open(UserSession session, EventStreamListener listener) throws ApiException;
}
