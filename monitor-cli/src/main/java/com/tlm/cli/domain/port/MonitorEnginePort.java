package com.tlm.cli.domain.port;

import com.tlm.cli.domain.model.MonitorException;

import java.util.List;

/**
 * Engine de monitoramento: conecta diretamente a uma live do TikTok e
 * publica os eventos no {@link EventSink} informado na construção.
 */
public interface MonitorEnginePort {

    /**
     * Conecta a {@code username} (sem '@'). Assíncrono: o resultado real
     * (sucesso/erro/reconexão) chega pelo sink. Se já houver outra live
     * ativa, ela é desconectada antes.
     */
    void connect(String username) throws MonitorException;

    /** Desconecta a live atual (idempotente). */
    void disconnect();

    /** Live atualmente monitorada (vazio quando desconectado). */
    String currentLive();

    boolean isConnected();

    /** Nomes dos presentes do catálogo da live (vazio antes de carregar). */
    List<String> availableGifts();

    /** Libera recursos da engine (desconexão best-effort inclusa). */
    void close();
}
