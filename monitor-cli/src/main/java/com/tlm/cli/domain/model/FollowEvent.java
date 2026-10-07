package com.tlm.cli.domain.model;

import java.time.Instant;

/** Seguidor novo ou evento social ({@code new-follower} / {@code new-social-event}). */
public record FollowEvent(Instant at, String uniqueId, String nickname, String socialAction) implements DomainEvent {

    public String displayName() {
        return ChatMessage.display(nickname, uniqueId);
    }
}
