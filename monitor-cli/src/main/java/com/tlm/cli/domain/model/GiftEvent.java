package com.tlm.cli.domain.model;

import java.time.Instant;

/** Presente recebido ({@code any-gift-received} / {@code new-gift-user}). */
public record GiftEvent(Instant at, String uniqueId, String nickname, String giftName, int count)
        implements DomainEvent {

    public String displayName() {
        return ChatMessage.display(nickname, uniqueId);
    }
}
