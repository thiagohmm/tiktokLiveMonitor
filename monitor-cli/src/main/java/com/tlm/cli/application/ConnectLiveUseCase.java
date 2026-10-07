package com.tlm.cli.application;

import com.tlm.cli.domain.model.ApiException;
import com.tlm.cli.domain.model.UserSession;
import com.tlm.cli.domain.port.LiveApiPort;

/** Caso de uso: conectar a uma live do TikTok ({@code POST /api/connect}). */
public final class ConnectLiveUseCase {

    private final LiveApiPort api;
    private final SessionManager sessions;
    private final MonitorHub hub;

    public ConnectLiveUseCase(LiveApiPort api, SessionManager sessions, MonitorHub hub) {
        this.api = api;
        this.sessions = sessions;
        this.hub = hub;
    }

    public void execute(String username) throws ApiException {
        try {
            sessions.send(session -> {
                api.connect(session, username);
                return null;
            });
        } catch (ApiException error) {
            if (error.status() == 401 && sessions.recoverAfter401()) {
                sessions.send(session -> {
                    api.connect(session, username);
                    return null;
                });
            } else {
                throw error;
            }
        }
        hub.notice("Monitorando @" + username);
        hub.refreshState();
    }
}
