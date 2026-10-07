package com.tlm.cli.application;

import com.tlm.cli.domain.model.ApiException;
import com.tlm.cli.domain.model.DomainEvent;
import com.tlm.cli.domain.model.LiveMonitorState;
import com.tlm.cli.domain.model.LiveEntry;
import com.tlm.cli.domain.model.PinnedCommentEntry;
import com.tlm.cli.domain.model.ServerEvent;
import com.tlm.cli.domain.model.UserSession;
import com.tlm.cli.domain.port.LiveApiPort;
import com.tlm.cli.domain.service.EventDecoder;

import java.util.List;
import java.util.Map;
import java.util.concurrent.CopyOnWriteArrayList;

/**
 * Barramento de eventos para os listeners (a UI): decodifica frames do SSE,
 * aplica o caso especial de {@code server-state} e busca o estado via REST.
 */
public final class MonitorHub {

    private final LiveApiPort api;
    private final SessionManager sessions;
    private final CopyOnWriteArrayList<MonitorListener> listeners = new CopyOnWriteArrayList<>();

    public MonitorHub(LiveApiPort api, SessionManager sessions) {
        this.api = api;
        this.sessions = sessions;
    }

    public void addListener(MonitorListener listener) {
        listeners.add(listener);
    }

    /** Frame bruto do SSE: roteia {@code server-state} ou decodifica o evento. */
    public void observe(ServerEvent event) {
        if ("server-state".equals(event.type())) {
            boolean connected = Boolean.TRUE.equals(event.data().get("connected"));
            String username = event.string("username").orElse("");
            stateChanged(new LiveMonitorState(connected, username, ""));
            return;
        }
        DomainEvent decoded = EventDecoder.decode(event);
        for (MonitorListener listener : listeners) {
            listener.onDomainEvent(decoded);
        }
    }

    public void stateChanged(LiveMonitorState state) {
        for (MonitorListener listener : listeners) {
            listener.onStateChanged(state);
        }
    }

    public void streamUp() {
        for (MonitorListener listener : listeners) {
            listener.onStreamUp();
        }
    }

    public void streamDown(String reason) {
        for (MonitorListener listener : listeners) {
            listener.onStreamDown(reason);
        }
    }

    public void notice(String message) {
        for (MonitorListener listener : listeners) {
            listener.onNotice(message);
        }
    }

    public void alert(String message) {
        for (MonitorListener listener : listeners) {
            listener.onAlert(message);
        }
    }

    /** Re-sincroniza o header via {@code GET /api/state} ( após conectar/reconectar). */
    public void refreshState() {
        UserSession session = sessions.current().orElse(null);
        if (session == null) {
            return;
        }
        try {
            stateChanged(api.state(session));
        } catch (ApiException ignored) {
            // Sem estado novo; a UI continua com o último estado conhecido.
        }
    }

    /** Lista de lives monitoradas pela organização (uso futuro da UI). */
    public List<LiveEntry> lives() {
        UserSession session = sessions.current().orElse(null);
        if (session == null) {
            return List.of();
        }
        try {
            return api.lives(session);
        } catch (ApiException e) {
            return List.of();
        }
    }

    /**
     * Catálogo de presentes da live ({@code GET /api/available-gifts});
     * propagando erros para a UI reportar via {@code runSafely}.
     */
    public List<String> availableGifts(String live) throws ApiException {
        return api.availableGifts(session(), live);
    }

    /**
     * Histórico de comentários fixados ({@code GET /api/pinned-comments});
     * propagando erros para a UI reportar via {@code runSafely}.
     */
    public List<PinnedCommentEntry> pinnedComments(String live, int limit) throws ApiException {
        return api.pinnedComments(session(), live, limit);
    }

    private UserSession session() throws ApiException {
        UserSession session = sessions.current().orElse(null);
        if (session == null) {
            throw new ApiException(401, "sessão não iniciada", Map.of());
        }
        return session;
    }
}
