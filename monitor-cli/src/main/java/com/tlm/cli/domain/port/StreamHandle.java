package com.tlm.cli.domain.port;

/** Alça do stream aberto: fechar encerra a leitura (sessão SSE local). */
public interface StreamHandle extends AutoCloseable {

    @Override
    void close();
}
