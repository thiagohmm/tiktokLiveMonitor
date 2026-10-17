package com.tlm.cli.domain.port;

import com.tlm.cli.domain.model.MonitorException;
import com.tlm.cli.domain.model.PinnedCommentEntry;

import java.time.Instant;
import java.util.List;

/**
 * Persistência local (SQLite) dos eventos capturados e do histórico
 * consultável pelos comandos da TUI.
 */
public interface LocalStorePort extends AutoCloseable {

    void recordChat(String liveName, String uniqueId, String nickname, String message, Instant at)
            throws MonitorException;

    void recordGift(String liveName, String uniqueId, String nickname, String giftName, int count, Instant at)
            throws MonitorException;

    void recordShare(String liveName, String uniqueId, String nickname, Instant at) throws MonitorException;

    /**
     * Registra uma curtida; {@code totalLikes} (quando presente) atualiza o
     * total da sala mantendo sempre o maior valor visto.
     */
    void recordLike(String liveName, String uniqueId, String nickname, int likeCount, Integer totalLikes,
                    Instant at) throws MonitorException;

    /**
     * Registra um comentário fixado. {@code pinId} não vazio deduplica dentro
     * da mesma live.
     *
     * @return {@code false} quando o pin já existia (duplicidade ignorada).
     */
    boolean recordPinned(String liveName, String uniqueId, String nickname, String comment, String pinId,
                         Boolean isFollower, Instant at) throws MonitorException;

    /**
     * Fixados mais recentes; {@code liveName} vazio traz todas as lives.
     */
    List<PinnedCommentEntry> recentPinned(String liveName, int limit) throws MonitorException;

    @Override
    void close();
}
