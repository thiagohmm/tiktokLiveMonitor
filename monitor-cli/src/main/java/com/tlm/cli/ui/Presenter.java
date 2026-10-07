package com.tlm.cli.ui;

import com.tlm.cli.domain.model.ChatMessage;
import com.tlm.cli.domain.model.DomainEvent;
import com.tlm.cli.domain.model.FollowEvent;
import com.tlm.cli.domain.model.GiftEvent;
import com.tlm.cli.domain.model.LikeEvent;
import com.tlm.cli.domain.model.PinnedCommentEvent;
import com.tlm.cli.domain.model.SystemNotice;

import java.time.Instant;
import java.time.ZoneId;
import java.time.format.DateTimeFormatter;

/**
 * Traduz eventos de domínio em linhas de feed com cor por tipo
 * (chat=ciano, gift=amarelo, like=magenta, follow=verde, fixado=azul,
 * sistema/erro=vermelho).
 */
public final class Presenter {

    private static final DateTimeFormatter CLOCK = DateTimeFormatter.ofPattern("HH:mm:ss");

    public FeedLine line(DomainEvent event) {
        String stamp = stamp(event.at());
        return switch (event) {
            case ChatMessage m -> new FeedLine(stamp + "@" + m.displayName() + ": " + m.comment(), Category.CHAT);
            case GiftEvent g ->
                    new FeedLine(stamp + "@" + g.displayName() + " enviou " + giftText(g), Category.GIFT, g.giftName());
            case LikeEvent l -> new FeedLine(stamp + "@" + l.displayName() + " " + likeText(l), Category.LIKE);
            case FollowEvent f -> new FeedLine(stamp + "@" + f.displayName() + " " + followText(f), Category.FOLLOW);
            case PinnedCommentEvent p ->
                    new FeedLine(stamp + "[fixado] @" + p.displayName() + ": " + p.comment(), Category.PINNED);
            case SystemNotice n -> new FeedLine(stamp + noticeText(n), Category.SYSTEM);
        };
    }

    private static String giftText(GiftEvent gift) {
        String name = gift.giftName() == null || gift.giftName().isBlank() ? "um presente" : gift.giftName();
        return gift.count() > 1 ? name + " x" + gift.count() : name;
    }

    private static String likeText(LikeEvent like) {
        return like.likeCount() != null ? like.likeCount() + " curtidas" : "curtiu";
    }

    private static String followText(FollowEvent follow) {
        String action = follow.socialAction() == null || follow.socialAction().isBlank()
                ? "seguiu a live"
                : follow.socialAction();
        return action;
    }

    private static String noticeText(SystemNotice notice) {
        if (notice.message() != null && !notice.message().isBlank()) {
            return notice.message();
        }
        return notice.rawPayload().isBlank() ? "evento de sistema" : notice.rawPayload();
    }

    private static String stamp(Instant at) {
        return "[" + CLOCK.format(at.atZone(ZoneId.systemDefault())) + "] ";
    }
}
