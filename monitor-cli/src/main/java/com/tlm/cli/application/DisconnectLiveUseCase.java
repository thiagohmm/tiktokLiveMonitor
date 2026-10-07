package com.tlm.cli.application;

import com.tlm.cli.domain.model.ApiException;
import com.tlm.cli.domain.port.LiveApiPort;

import java.time.Duration;

/** Caso de uso: desconectar a {@code POST /api/disconnect?username=<user>}. */
public final class DisconnectLiveUseCase {

    /** Timeout curto para a desconexão no encerramento do CLI. */
    private static final Duration EXIT_TIMEOUT = Duration.ofSeconds(3);

    private final LiveApiPort api;
    private final SessionManager sessions;
    private final MonitorHub hub;

    public DisconnectLiveUseCase(LiveApiPort api, SessionManager sessions, MonitorHub hub) {
        this.api = api;
        this.sessions = sessions;
        this.hub = hub;
    }

    /**
     * @param username username do TikTok; vazio/nulo desconecta o monitor
     *                 corrente (semântica do backend).
     */
    public void execute(String username) throws ApiException {
        String target = username == null ? "" : username.trim();
        try {
            sessions.send(session -> {
                api.disconnect(session, target);
                return null;
            });
        } catch (ApiException error) {
            if (error.status() == 401 && sessions.recoverAfter401()) {
                sessions.send(session -> {
                    api.disconnect(session, target);
                    return null;
                });
            } else {
                throw error;
            }
        }
        hub.notice(target.isEmpty() ? "Monitor corrente desconectado." : "Monitor de @" + target + " desconectado.");
        hub.refreshState();
    }

    /**
     * Desconexão best-effort ao encerrar o CLI (monitor corrente, username
     * vazio): timeout curto, roda na thread chamadora, sem prompt de senha
     * (evita {@code recoverAfter401()} durante o shutdown) e sem atualizar a
     * UI (que já está fechando). Qualquer erro é silenciado.
     */
    public void executeBestEffort() {
        try {
            sessions.send(session -> {
                api.disconnect(session, "", EXIT_TIMEOUT);
                return null;
            });
        } catch (ApiException | RuntimeException ignored) {
            // Sem sessão, rede fora ou sessão revogada: nada a fazer no fim de
            // vida; o monitor segue ativo no servidor (limitação aceita).
        }
    }
}
