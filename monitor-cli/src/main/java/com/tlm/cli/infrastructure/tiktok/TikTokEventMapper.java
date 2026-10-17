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
import io.github.jwdeveloper.tiktok.data.events.common.TikTokHeaderEvent;
import io.github.jwdeveloper.tiktok.data.models.users.User;

import java.time.Instant;

/**
 * Converte eventos da biblioteca TikTokLiveJava nos eventos de domínio da TUI.
 * Funções puras (mapeáveis e testáveis sem conexão real).
 */
public final class TikTokEventMapper {

    private TikTokEventMapper() {
    }

    public static ChatMessage chat(TikTokCommentEvent event) {
        User user = event.getUser();
        return new ChatMessage(instantOf(event), uniqueId(user), nickname(user), text(event.getText()));
    }

    public static GiftEvent gift(TikTokGiftEvent event) {
        User user = event.getUser();
        String name = event.getGift() == null ? "" : nullToEmpty(event.getGift().getName());
        return new GiftEvent(instantOf(event), uniqueId(user), nickname(user), name,
                Math.max(1, event.getCombo()));
    }

    public static LikeEvent like(TikTokLikeEvent event) {
        User user = event.getUser();
        return new LikeEvent(instantOf(event), uniqueId(user), nickname(user), event.getLikes());
    }

    public static FollowEvent follow(TikTokFollowEvent event) {
        User user = event.getUser();
        return new FollowEvent(instantOf(event), uniqueId(user), nickname(user), "seguiu a live");
    }

    public static FollowEvent share(TikTokShareEvent event) {
        User user = event.getUser();
        return new FollowEvent(instantOf(event), uniqueId(user), nickname(user), "compartilhou a live");
    }

    public static PinnedCommentEvent pinned(TikTokCommentEvent message, Instant at) {
        User user = message.getUser();
        String comment = text(message.getText());
        return new PinnedCommentEvent(at, uniqueId(user), nickname(user),
                comment.isEmpty() ? "[sem texto identificado]" : comment);
    }

    /** Timestamp do header protobuf (ms); ausente/zero → agora. */
    public static Instant instantOf(TikTokHeaderEvent event) {
        long millis = event.getTimeStamp();
        return millis > 0 ? Instant.ofEpochMilli(millis) : Instant.now();
    }

    /**
     * Identificador único do TikTok: na biblioteca é {@code getName()}
     * (o proto chama o handle de "username").
     */
    public static String uniqueId(User user) {
        return user == null ? "" : nullToEmpty(user.getName());
    }

    /** Nome de exibição: na biblioteca é {@code getProfileName()}. */
    public static String nickname(User user) {
        return user == null ? "" : nullToEmpty(user.getProfileName());
    }

    private static String text(String value) {
        return value == null ? "" : value;
    }

    private static String nullToEmpty(String value) {
        return value == null ? "" : value;
    }
}
