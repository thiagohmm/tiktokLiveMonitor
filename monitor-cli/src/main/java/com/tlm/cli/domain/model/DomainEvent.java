package com.tlm.cli.domain.model;

/** Eventos de domínio decodificados a partir do payload aberto do SSE. */
public sealed interface DomainEvent
        permits ChatMessage, GiftEvent, LikeEvent, FollowEvent, PinnedCommentEvent, SystemNotice {

    /** Momento de recebimento (fallback: instante da decodificação). */
    java.time.Instant at();
}
