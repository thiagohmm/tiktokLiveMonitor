package com.tlm.cli.application;

import com.tlm.cli.domain.model.MonitorException;
import com.tlm.cli.domain.port.MonitorEnginePort;

/** Caso de uso: conectar a uma live do TikTok ({@code :connect <user>}). */
public final class ConnectLiveUseCase {

    private final MonitorEnginePort engine;
    private final MonitorHub hub;

    public ConnectLiveUseCase(MonitorEnginePort engine, MonitorHub hub) {
        this.engine = engine;
        this.hub = hub;
    }

    public void execute(String username) throws MonitorException {
        String target = username == null ? "" : username.trim();
        if (target.isEmpty()) {
            throw new MonitorException("informe o username da live");
        }
        if (engine.isConnected() && engine.currentLive().equalsIgnoreCase(target)) {
            hub.notice("já monitorando @" + target);
            return;
        }
        engine.connect(target);
        hub.refreshState();
        hub.notice("conectando a @" + target + "...");
    }
}
