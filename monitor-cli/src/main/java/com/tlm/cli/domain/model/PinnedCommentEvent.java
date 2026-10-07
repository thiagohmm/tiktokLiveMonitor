package com.tlm.cli.domain.model;

import java.time.Instant;

/** Comentário fixado ao vivo ({@code pinned-comment} via SSE). */
public record PinnedCommentEvent(Instant at, String uniqueId, String nickname, String comment)
        implements DomainEvent {

    public String displayName() {
        return ChatMessage.display(nickname, uniqueId);
    }
}
