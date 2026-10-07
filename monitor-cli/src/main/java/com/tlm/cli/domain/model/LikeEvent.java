package com.tlm.cli.domain.model;

import java.time.Instant;

/** Curtida ({@code new-like-event}). */
public record LikeEvent(Instant at, String uniqueId, String nickname, Integer likeCount) implements DomainEvent {

    public String displayName() {
        return ChatMessage.display(nickname, uniqueId);
    }
}
