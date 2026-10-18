package com.tlm.cli.infrastructure.tiktok;

import com.tlm.cli.domain.model.PinnedCommentEvent;
import io.github.jwdeveloper.tiktok.data.models.users.User;
import io.github.jwdeveloper.tiktok.messages.data.CommonMessageData;
import io.github.jwdeveloper.tiktok.messages.webcast.WebcastRoomPinMessage;

import java.time.Instant;
import java.util.Optional;

/**
 * Extrai o conteúdo fixado de um {@code WebcastRoomPinMessage} cru.
 * Puro e testável: não toca em store, sink nem UI.
 */
public final class PinExtractor {

    private PinExtractor() {
    }

    /** Resultado da extração: evento de domínio + metadados de dedupe. */
    public record Pin(String uniqueId, String nickname, String comment, Boolean follower, Instant at,
                      String key) {
    }

    /**
     * Extrai o pin ou retorna vazio quando é unpin
     * ({@code action == 2} ou {@code method == "unpin"}).
     */
    public static Optional<Pin> extract(WebcastRoomPinMessage msg) {
        if (msg == null) {
            return Optional.empty();
        }
        if (msg.getAction() == 2 || "unpin".equalsIgnoreCase(msg.getMethod())) {
            return Optional.empty();
        }
        io.github.jwdeveloper.tiktok.messages.data.User protoUser = null;
        String text = "";
        if (msg.hasChatMessage()) {
            protoUser = msg.getChatMessage().hasUser() ? msg.getChatMessage().getUser() : null;
            text = msg.getChatMessage().getContent();
        } else if (msg.hasSocialMessage()) {
            protoUser = msg.getSocialMessage().hasUser() ? msg.getSocialMessage().getUser() : null;
            text = "compartilhou/seguiu a live";
        } else if (msg.hasGiftMessage()) {
            protoUser = msg.getGiftMessage().hasUser() ? msg.getGiftMessage().getUser() : null;
            text = msg.getGiftMessage().hasGift() ? msg.getGiftMessage().getGift().getName() : "";
        } else if (msg.hasMemberMessage()) {
            protoUser = msg.getMemberMessage().hasUser() ? msg.getMemberMessage().getUser() : null;
            text = msg.getMemberMessage().getActionDescription();
        } else if (msg.hasLikeMessage()) {
            protoUser = msg.getLikeMessage().hasUser() ? msg.getLikeMessage().getUser() : null;
            text = "curtiu a live";
        }
        User user = protoUser == null ? null : User.map(protoUser);
        String comment = text == null || text.isBlank() ? "[sem texto identificado]" : text;
        Instant at = msg.getPinTime() > 0 ? Instant.ofEpochMilli(msg.getPinTime()) : Instant.now();
        return Optional.of(new Pin(TikTokEventMapper.uniqueId(user), TikTokEventMapper.nickname(user),
                comment, user == null ? null : user.isFollower(), at, keyOf(msg, comment)));
    }

    /** Monta o evento de domínio a partir do resultado da extração. */
    public static PinnedCommentEvent toEvent(Pin pin) {
        return new PinnedCommentEvent(pin.at(), pin.uniqueId(), pin.nickname(), pin.comment());
    }

    /** Chave de dedupe: {@code pin_id} → {@code common.msgId} → {@code pinTime:texto}. */
    static String keyOf(WebcastRoomPinMessage msg, String text) {
        if (msg.getPinId() > 0) {
            return "p:" + msg.getPinId();
        }
        CommonMessageData common = msg.hasCommon() ? msg.getCommon() : null;
        if (common != null && common.getMsgId() > 0) {
            return "m:" + common.getMsgId();
        }
        long pinTime = msg.getPinTime();
        if (pinTime > 0) {
            return "t:" + pinTime + ":" + (text == null ? "" : text.hashCode());
        }
        return null;
    }
}
