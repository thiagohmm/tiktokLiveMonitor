package com.tlm.cli.ui;

import com.tlm.cli.domain.model.ChatMessage;
import com.tlm.cli.domain.model.GiftEvent;
import com.tlm.cli.domain.model.LikeEvent;
import com.tlm.cli.domain.model.PinnedCommentEvent;
import com.tlm.cli.domain.model.SystemNotice;
import org.junit.jupiter.api.Test;

import java.time.Instant;
import java.time.ZoneId;
import java.time.format.DateTimeFormatter;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

class PresenterTest {

    private static final Instant NOW = Instant.parse("2026-10-06T15:01:02Z");

    private final Presenter presenter = new Presenter();

    private static String stamp() {
        return DateTimeFormatter.ofPattern("HH:mm:ss").withZone(ZoneId.systemDefault()).format(NOW);
    }

    @Test
    void chatLineIsCyanAndStamped() {
        FeedLine line = presenter.line(new ChatMessage(NOW, "user1", "Ana", "bom dia"));
        assertEquals(Category.CHAT, line.category());
        assertTrue(line.text().startsWith("[" + stamp() + "] @Ana: bom dia"), line.text());
    }

    @Test
    void giftLineIsYellowAndShowsCount() {
        FeedLine line = presenter.line(new GiftEvent(NOW, "user2", "Ana", "Rosa", 3));
        assertEquals(Category.GIFT, line.category());
        assertTrue(line.text().contains("@Ana enviou Rosa x3"), line.text());
    }

    @Test
    void giftWithoutNameHasFallbackText() {
        FeedLine line = presenter.line(new GiftEvent(NOW, "user2", "Ana", "", 1));
        assertTrue(line.text().contains("enviou um presente"), line.text());
    }

    @Test
    void giftLineCarriesOriginalGiftNameForFiltering() {
        FeedLine line = presenter.line(new GiftEvent(NOW, "user2", "Ana", "Fenix", 3));
        assertEquals("Fenix", line.giftName());
    }

    @Test
    void giftLineWithoutNameCarriesFallbackNameForFiltering() {
        FeedLine line = presenter.line(new GiftEvent(NOW, "user2", "Ana", "", 1));
        assertEquals("", line.giftName());
    }

    @Test
    void pinnedCommentLineIsBlue() {
        FeedLine line = presenter.line(new PinnedCommentEvent(NOW, "user9", "Rita", "olha o pin"));
        assertEquals(Category.PINNED, line.category());
        assertTrue(line.text().startsWith("[" + stamp() + "] [fixado] @Rita: olha o pin"), line.text());
    }

    @Test
    void pinnedCommentLineFallsBackToUniqueId() {
        FeedLine line = presenter.line(new PinnedCommentEvent(NOW, "user9", "", "pin sem nick"));
        assertTrue(line.text().contains("[fixado] @user9: pin sem nick"), line.text());
    }

    @Test
    void likeLineShowsCount() {
        FeedLine line = presenter.line(new LikeEvent(NOW, "user3", "", 42));
        assertEquals(Category.LIKE, line.category());
        assertTrue(line.text().contains("@user3 42 curtidas"), line.text());
    }

    @Test
    void followDefaultsToFollowAction() {
        FeedLine line = presenter.line(new com.tlm.cli.domain.model.FollowEvent(NOW, "user4", "Caio", ""));
        assertEquals(Category.FOLLOW, line.category());
        assertTrue(line.text().contains("@Caio seguiu"), line.text());
    }

    @Test
    void systemNoticePrefersMessage() {
        FeedLine line = presenter.line(new SystemNotice(NOW, "servidor caiu", "err", "{\"erro\":true}"));
        assertEquals(Category.SYSTEM, line.category());
        assertTrue(line.text().contains("servidor caiu"), line.text());
        assertFalse(line.text().contains("{\"erro\":true}"));
    }

    @Test
    void systemNoticeWithoutMessageUsesRaw() {
        FeedLine line = presenter.line(new SystemNotice(NOW, "", "", "{\"x\":1}"));
        assertTrue(line.text().contains("{\"x\":1}"), line.text());
    }

    @Test
    void systemNoticeRawWithTypeNameIsRendered() {
        FeedLine line = presenter.line(new SystemNotice(NOW, "", "mystery", "evento 'mystery' {\"x\":1}"));
        assertTrue(line.text().contains("mystery"), line.text());
    }
}
