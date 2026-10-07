package com.tlm.cli.domain.service;

import com.tlm.cli.domain.model.ChatMessage;
import com.tlm.cli.domain.model.DomainEvent;
import com.tlm.cli.domain.model.FollowEvent;
import com.tlm.cli.domain.model.GiftEvent;
import com.tlm.cli.domain.model.LikeEvent;
import com.tlm.cli.domain.model.PinnedCommentEvent;
import com.tlm.cli.domain.model.ServerEvent;
import com.tlm.cli.domain.model.SystemNotice;

import java.time.Instant;
import java.util.List;
import java.util.Optional;

/**
 * Decodifica o payload aberto do SSE em eventos de domínio tipados. Tipos
 * desconhecidos viram {@link SystemNotice} com o payload bruto — nunca falham.
 */
public final class EventDecoder {

    private EventDecoder() {
    }

    public static DomainEvent decode(ServerEvent event) {
        String type = event.type() == null ? "" : event.type();
        Instant at = instantOf(event);
        return switch (type) {
            case "new-chat-message" -> chat(at, event);
            case "pinned-comment" -> pinned(at, event);
            case "any-gift-received", "new-gift-user" -> gift(at, event);
            case "new-like-event" -> like(at, event);
            case "new-follower", "new-social-event" -> follow(at, event);
            default -> system(at, event);
        };
    }

    private static DomainEvent chat(Instant at, ServerEvent event) {
        return new ChatMessage(
                at,
                textOf(event, "uniqueId"),
                textOf(event, "nickname"),
                textOf(event, "comment"));
    }

    private static DomainEvent pinned(Instant at, ServerEvent event) {
        return new PinnedCommentEvent(
                at,
                textOf(event, "uniqueId"),
                textOf(event, "nickname"),
                textOf(event, "comment"));
    }

    private static DomainEvent gift(Instant at, ServerEvent event) {
        String giftName = firstTextOf(event, List.of("giftName", "gift", "label", "giftId"));
        return new GiftEvent(
                at,
                textOf(event, "uniqueId"),
                textOf(event, "nickname"),
                giftName,
                intOf(event, "repeatCount", "count", "amount"));
    }

    private static DomainEvent like(Instant at, ServerEvent event) {
        return new LikeEvent(
                at,
                textOf(event, "uniqueId"),
                textOf(event, "nickname"),
                likeCount(event));
    }

    private static DomainEvent follow(Instant at, ServerEvent event) {
        return new FollowEvent(
                at,
                textOf(event, "uniqueId"),
                textOf(event, "nickname"),
                textOf(event, "socialAction"));
    }

    private static DomainEvent system(Instant at, ServerEvent event) {
        String message = firstTextOf(event, List.of("message", "status", "error", "reason"));
        String raw = event.type() == null ? "" : "evento '" + event.type() + "' " + event.data();
        return new SystemNotice(at, message, event.type(), raw);
    }

    private static Integer likeCount(ServerEvent event) {
        for (String key : List.of("likeCount", "count", "totalLikes", "likes")) {
            Optional<Object> value = event.value(key);
            if (value.isPresent() && value.get() instanceof Number n) {
                return n.intValue();
            }
        }
        return null;
    }

    private static int intOf(ServerEvent event, String... keys) {
        for (String key : keys) {
            Optional<Object> value = event.value(key);
            if (value.isPresent() && value.get() instanceof Number n) {
                return Math.max(1, n.intValue());
            }
        }
        return 1;
    }

    private static String textOf(ServerEvent event, String key) {
        Object value = event.data().get(key);
        return value == null ? "" : String.valueOf(value);
    }

    private static String firstTextOf(ServerEvent event, List<String> keys) {
        for (String key : keys) {
            Object value = event.data().get(key);
            if (value != null && !String.valueOf(value).isBlank()) {
                return String.valueOf(value);
            }
        }
        return "";
    }

    private static Instant instantOf(ServerEvent event) {
        return event.receivedAt();
    }
}
