package com.tlm.cli.infrastructure.tiktok;

import com.tlm.cli.domain.model.ChatMessage;
import com.tlm.cli.domain.model.FollowEvent;
import com.tlm.cli.domain.model.GiftEvent;
import com.tlm.cli.domain.model.LikeEvent;
import com.tlm.cli.domain.model.PinnedCommentEvent;
import io.github.jwdeveloper.tiktok.data.events.TikTokCommentEvent;
import io.github.jwdeveloper.tiktok.data.events.gift.TikTokGiftEvent;
import io.github.jwdeveloper.tiktok.data.events.social.TikTokFollowEvent;
import io.github.jwdeveloper.tiktok.data.events.social.TikTokLikeEvent;
import io.github.jwdeveloper.tiktok.data.events.social.TikTokShareEvent;
import org.junit.jupiter.api.Test;

import java.time.Instant;
import java.time.temporal.ChronoUnit;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

class TikTokEventMapperTest {

    @Test
    void mapsChat() {
        ChatMessage message = TikTokEventMapper.chat(TikTokCommentEvent.of("Rita", "olha o pin"));
        assertEquals("Rita", message.nickname());
        assertEquals("olha o pin", message.comment());
        assertNotNull(message.at());
    }

    @Test
    void chatWithoutHeaderTimestampFallsBackToNow() {
        Instant before = Instant.parse("2026-10-17T01:00:00Z").minus(1, ChronoUnit.MINUTES);
        ChatMessage message = TikTokEventMapper.chat(TikTokCommentEvent.of("Rita", "oi"));
        assertTrue(message.at().isAfter(before) && !message.at().isAfter(Instant.now().plusSeconds(1)));
    }

    @Test
    void uniqueIdIsTheHandleAndNicknameIsTheDisplayName() {
        io.github.jwdeveloper.tiktok.messages.webcast.WebcastChatMessage proto =
                io.github.jwdeveloper.tiktok.messages.webcast.WebcastChatMessage.newBuilder()
                        .setUser(io.github.jwdeveloper.tiktok.messages.data.User.newBuilder()
                                .setNickname("Mostrinha")
                                .setUsername("handle123"))
                        .setContent("oi")
                        .build();
        ChatMessage message = TikTokEventMapper.chat(new TikTokCommentEvent(proto));
        assertEquals("handle123", message.uniqueId());
        assertEquals("Mostrinha", message.nickname());
    }

    @Test
    void mapsGiftWithMinimumCountOne() {
        GiftEvent gift = TikTokEventMapper.gift(TikTokGiftEvent.of("Rosa", 1, 1));
        assertEquals("Rosa", gift.giftName());
        assertEquals(1, gift.count());
    }

    @Test
    void mapsLike() {
        LikeEvent like = TikTokEventMapper.like(TikTokLikeEvent.of("Cris", 7));
        assertEquals(7, like.likeCount());
    }

    @Test
    void mapsFollowAndShareWithActions() {
        FollowEvent follow = TikTokEventMapper.follow(TikTokFollowEvent.of("Bob"));
        assertEquals("seguiu a live", follow.socialAction());
        FollowEvent share = TikTokEventMapper.share(TikTokShareEvent.of("Bob", 3));
        assertEquals("compartilhou a live", share.socialAction());
    }

    @Test
    void mapsPinnedWithText() {
        Instant at = Instant.parse("2026-10-17T01:00:00Z");
        PinnedCommentEvent pinned = TikTokEventMapper.pinned(TikTokCommentEvent.of("Rita", "fixei!"), at);
        assertEquals("Rita", pinned.nickname());
        assertEquals("fixei!", pinned.comment());
        assertEquals(at, pinned.at());
    }

    @Test
    void pinnedWithoutTextGetsPlaceholder() {
        PinnedCommentEvent pinned = TikTokEventMapper.pinned(TikTokCommentEvent.of("Rita", ""), Instant.now());
        assertEquals("[sem texto identificado]", pinned.comment());
    }
}
