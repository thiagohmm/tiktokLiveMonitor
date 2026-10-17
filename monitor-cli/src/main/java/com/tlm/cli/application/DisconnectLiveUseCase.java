package com.tlm.cli.application;

import com.tlm.cli.domain.port.MonitorEnginePort;

/** Caso de uso: desconectar a live atual ({@code :disconnect}). */
public final class DisconnectLiveUseCase {

    private final MonitorEnginePort engine;
    private final MonitorHub hub;

    public DisconnectLiveUseCase(MonitorEnginePort engine, MonitorHub hub) {
        this.engine = engine;
        this.hub = hub;
    }

    /**
     * @param username ignorado no modo local (há uma única live por engine);
     *                 mantido por compatibilidade com o comando.
     */
    public void execute(String username) {
        engine.disconnect();
        hub.refreshState();
        hub.notice("monitor desconectado.");
    }

    /** Desconexão silenciosa usada no encerramento do CLI. */
    public void executeBestEffort() {
        try {
            engine.disconnect();
        } catch (RuntimeException ignored) {
            // Encerramento: nada a fazer se a engine já caiu.
        }
    }
}
