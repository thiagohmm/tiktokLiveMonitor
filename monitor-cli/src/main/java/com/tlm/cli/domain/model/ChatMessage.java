package com.tlm.cli.domain.model;

import java.time.Instant;

/** Comentário de chat ({@code new-chat-message}). */
public record ChatMessage(Instant at, String uniqueId, String nickname, String comment) implements DomainEvent {

    public String displayName() {
        return display(nickname, uniqueId);
    }

    static String display(String nickname, String fallback) {
        if (nickname != null && !nickname.isBlank()) {
            return nickname;
        }
        return fallback == null ? "" : fallback;
    }
}
