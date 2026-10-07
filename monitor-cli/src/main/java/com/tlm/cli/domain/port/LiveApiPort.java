package com.tlm.cli.domain.port;

import com.tlm.cli.domain.model.ApiException;
import com.tlm.cli.domain.model.LiveEntry;
import com.tlm.cli.domain.model.LiveMonitorState;
import com.tlm.cli.domain.model.PinnedCommentEntry;
import com.tlm.cli.domain.model.UserSession;

import java.time.Duration;
import java.util.List;

/** Porta REST da API de lives (state/lives/connect/disconnect/gifts/pinned). */
public interface LiveApiPort {

    /** Timeout padrão das chamadas REST. */
    Duration DEFAULT_TIMEOUT = Duration.ofSeconds(30);

    LiveMonitorState state(UserSession session) throws ApiException;

    List<LiveEntry> lives(UserSession session) throws ApiException;

    /**
     * {@code POST /api/connect} com corpo {@code {"username": ...}}. A
     * implementação deve enviar {@code Origin} + {@code X-CSRF-Token}.
     */
    void connect(UserSession session, String username) throws ApiException;

    /**
     * {@code POST /api/disconnect?username=<user>}; username vazio desconecta
     * o monitor corrente. Delega para {@link #disconnect(UserSession, String, Duration)}
     * com o timeout padrão.
     */
    default void disconnect(UserSession session, String username) throws ApiException {
        disconnect(session, username, DEFAULT_TIMEOUT);
    }

    /**
     * Variante com timeout por chamada (ex.: 3s no encerramento best-effort).
     */
    void disconnect(UserSession session, String username, Duration timeout) throws ApiException;

    /**
     * Nomes dos presentes disponíveis na live
     * ({@code GET /api/available-gifts?live=<user>}); vazio quando a live não
     * tem catálogo em cache.
     */
    List<String> availableGifts(UserSession session, String live) throws ApiException;

    /**
     * Comentários fixados ({@code GET /api/pinned-comments?live=<user>});
     * {@code live} vazio resolve a live corrente da org (pode vir org-wide).
     */
    List<PinnedCommentEntry> pinnedComments(UserSession session, String live, int limit) throws ApiException;
}
