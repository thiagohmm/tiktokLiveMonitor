package com.tlm.cli.domain.service;

import com.tlm.cli.domain.model.ChatMessage;
import com.tlm.cli.domain.model.DomainEvent;
import com.tlm.cli.domain.model.FollowEvent;
import com.tlm.cli.domain.model.GiftEvent;
import com.tlm.cli.domain.model.LikeEvent;
import com.tlm.cli.domain.model.PinnedCommentEvent;
import com.tlm.cli.domain.model.ServerEvent;
import com.tlm.cli.domain.model.SystemNotice;
import org.junit.jupiter.api.Test;

import java.time.Instant;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

class EventDecoderTest {

    private static final Instant NOW = Instant.parse("2026-10-06T15:00:00Z");

    @Test
    void decodesChatMessage() {
        DomainEvent event = EventDecoder.decode(new ServerEvent("new-chat-message",
                Map.of("uniqueId", "user1", "nickname", "Ana", "comment", "olá"), NOW));
        ChatMessage chat = (ChatMessage) event;
        assertEquals("user1", chat.uniqueId());
        assertEquals("Ana", chat.nickname());
        assertEquals("olá", chat.comment());
        assertEquals(NOW, chat.at());
    }

    @Test
    void decodesGiftWithRepeatCount() {
        DomainEvent event = EventDecoder.decode(new ServerEvent("any-gift-received",
                Map.of("uniqueId", "user2", "giftName", "Rosa", "repeatCount", 5), NOW));
        GiftEvent gift = (GiftEvent) event;
        assertEquals("Rosa", gift.giftName());
        assertEquals(5, gift.count());
    }

    @Test
    void fallingBackToGiftAliasKeys() {
        DomainEvent event = EventDecoder.decode(new ServerEvent("new-gift-user",
                Map.of("uniqueId", "user2", "gift", "Rosa", "count", 3), NOW));
        GiftEvent gift = (GiftEvent) event;
        assertEquals("Rosa", gift.giftName());
        assertEquals(3, gift.count());
    }

    @Test
    void decodesPinnedComment() {
        DomainEvent event = EventDecoder.decode(new ServerEvent("pinned-comment",
                Map.of("uniqueId", "user9", "nickname", "Rita", "comment", "olha o pin",
                        "pinId", "123", "timestamp", 1728200000000L, "isFollower", true), NOW));
        PinnedCommentEvent pinned = (PinnedCommentEvent) event;
        assertEquals("user9", pinned.uniqueId());
        assertEquals("Rita", pinned.nickname());
        assertEquals("olha o pin", pinned.comment());
        assertEquals(NOW, pinned.at());
    }

    @Test
    void pinnedCommentWithoutNicknameFallsBackToUniqueId() {
        DomainEvent event = EventDecoder.decode(new ServerEvent("pinned-comment",
                Map.of("uniqueId", "user9", "comment", "pin sem nick"), NOW));
        PinnedCommentEvent pinned = (PinnedCommentEvent) event;
        assertEquals("user9", pinned.displayName());
    }

    @Test
    void decodesLike() {
        DomainEvent event = EventDecoder.decode(new ServerEvent("new-like-event",
                Map.of("uniqueId", "user3", "likeCount", 12), NOW));
        LikeEvent like = (LikeEvent) event;
        assertEquals(12, like.likeCount());
        assertEquals("user3", like.uniqueId());
    }

    @Test
    void decodesFollower() {
        DomainEvent event = EventDecoder.decode(new ServerEvent("new-follower",
                Map.of("uniqueId", "user4", "nickname", "Bia"), NOW));
        FollowEvent follow = (FollowEvent) event;
        assertEquals("Bia", follow.nickname());
        assertTrue(follow.socialAction() == null || follow.socialAction().isEmpty());
    }

    @Test
    void unknownTypeBecomesSystemNoticeWithRawPayload() {
        DomainEvent event = EventDecoder.decode(new ServerEvent("new-future-event",
                Map.of("qualquer", "coisa", "orgId", "x"), NOW));
        SystemNotice notice = (SystemNotice) event;
        assertEquals("new-future-event", notice.eventType());
        assertTrue(notice.rawPayload().contains("qualquer"));
        assertTrue(notice.rawPayload().contains("coisa"));
    }

    @Test
    void controlEventTypesBecomeSystemNotices() {
        DomainEvent state = EventDecoder.decode(new ServerEvent("server-state",
                Map.of("connected", true, "username", "user5"), NOW));
        DomainEvent connection = EventDecoder.decode(new ServerEvent("connection-status",
                Map.of("status", "connected"), NOW));
        SystemNotice stateNotice = (SystemNotice) state;
        SystemNotice connectionNotice = (SystemNotice) connection;
        assertEquals("server-state", stateNotice.eventType());
        assertEquals("connected", connectionNotice.message());
    }

    @Test
    void nullTypeAndEmptyDataAreTolerated() {
        DomainEvent event = EventDecoder.decode(new ServerEvent(null, null, NOW));
        SystemNotice notice = (SystemNotice) event;
        assertTrue(notice.rawPayload().isBlank());
    }
}
