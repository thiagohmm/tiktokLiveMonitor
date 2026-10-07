package com.tlm.cli.domain.port;

import com.tlm.cli.domain.model.ApiException;
import com.tlm.cli.domain.model.ServerEvent;

/** Desfechos do stream SSE, entregues pela thread de leitura. */
public interface EventStreamListener {

    /** Conexão aceita pelo servidor (HTTP 200, streaming começou). */
    void onConnected();

    /** Frame {@code event: <tipo>} decodificado. */
    void onEvent(ServerEvent event);

    /** Servidor encerrou o stream (EOF) ou o cliente parou. */
    void onClosed();

    /** Recusa de conexão ou falha de rede em pleno stream. */
    void onFailed(ApiException error);
}
