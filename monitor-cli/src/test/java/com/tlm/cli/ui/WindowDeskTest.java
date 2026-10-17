package com.tlm.cli.ui;

import org.junit.jupiter.api.Test;

import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

class WindowDeskTest {

    private static final int COLS = 100;
    private static final int ROWS = 30;

    @Test
    void eachKindIsOneLiveWindowThatReceivesOnlyItsLines() {
        WindowDesk desk = new WindowDesk();
        DosWindow chat = desk.open(WindowKind.CHAT);
        DosWindow gifts = desk.open(WindowKind.GIFTS);
        desk.publish(new FeedLine("oi", Category.CHAT, "", "Ana", 0));
        desk.publish(new FeedLine("rosa", Category.GIFT, "Rosa", "Bia", 2));
        desk.publish(new FeedLine("dino", Category.GIFT, "Dino", "Ana", 9));

        assertEquals(List.of("oi"), chat.texts(10));
        assertEquals(List.of("rosa", "dino"), gifts.texts(10));
        assertEquals(gifts, desk.focused().orElseThrow());
    }

    @Test
    void giftFiltersKeepNameQuantityAndUser() {
        WindowDesk desk = new WindowDesk();
        desk.publish(new FeedLine("rosa", Category.GIFT, "Rosa", "Ana", 2));
        desk.publish(new FeedLine("dino", Category.GIFT, "Dino", "Bia", 9));
        DosWindow gifts = desk.open(WindowKind.GIFTS);

        desk.setGifts(gifts, List.of("Rosa"));
        assertEquals(List.of("rosa"), gifts.texts(10));

        desk.setGifts(gifts, List.of());
        desk.setMinCount(gifts, 5);
        assertEquals(List.of("dino"), gifts.texts(10));

        desk.setMinCount(gifts, 0);
        desk.setUser(gifts, "ana");
        assertEquals(List.of("rosa"), gifts.texts(10));
    }

    @Test
    void clickCloseRemovesWindowAndClickBringsItForward() {
        WindowDesk desk = new WindowDesk();
        DosWindow chat = desk.open(WindowKind.CHAT);
        DosWindow gifts = desk.open(WindowKind.GIFTS);
        chat.place(0, 2, 30, 10);
        gifts.place(40, 2, 30, 10);

        desk.onClick(10, 5, COLS, ROWS);
        assertEquals(chat, desk.stack().get(desk.stack().size() - 1));

        int closeColumn = 40 + 30 - 4;
        desk.onClick(closeColumn, 2, COLS, ROWS);
        assertTrue(desk.find(WindowKind.GIFTS).isEmpty());
        assertEquals(chat, desk.focused().orElseThrow());
    }

    @Test
    void dragMovesTheFocusedWindow() {
        WindowDesk desk = new WindowDesk();
        DosWindow chat = desk.open(WindowKind.CHAT);
        chat.place(2, 2, 40, 10);
        desk.onClick(6, 2, COLS, ROWS);
        desk.onDrag(20, 6, COLS, ROWS);
        Frame box = chat.visible(COLS, ROWS);
        assertEquals(16, box.x());
        assertEquals(6, box.y());
    }

    @Test
    void closedHistoryDoesNotReopenWhenTheLoadFinishes() {
        WindowDesk desk = new WindowDesk();
        int generation = desk.beginHistory();
        desk.showHistory(generation, " PRESENTES ", " fecha ",
                List.of(new FeedLine("carregando", Category.INFO)));
        desk.closeFocused();
        desk.showHistory(generation, " PRESENTES ", " fecha ",
                List.of(new FeedLine("tarde", Category.GIFT, "Rosa", "Ana", 1)));
        assertTrue(desk.find(WindowKind.HISTORY).isEmpty());
    }

    @Test
    void menuHitMatchesTheDrawnLabel() {
        MenuBar menu = new MenuBar();
        List<MenuBar.Item> items = menu.layout(80);
        MenuBar.Item gifts = items.stream().filter(item -> item.kind() == WindowKind.GIFTS).findFirst().orElseThrow();
        assertEquals(WindowKind.GIFTS, menu.hit(gifts.x(), 1, 80).orElseThrow());
        assertFalse(menu.hit(gifts.x(), 0, 80).isPresent());
    }

    @Test
    void wheelScrollsTheWindowUnderThePointer() {
        WindowDesk desk = new WindowDesk();
        DosWindow chat = desk.open(WindowKind.CHAT);
        for (int i = 0; i < 30; i++) {
            desk.publish(new FeedLine("c" + i, Category.CHAT, "", "Ana", 0));
        }
        assertEquals("c29", chat.texts(1).get(0));
        assertTrue(desk.onScroll(4, 6, -1, COLS, ROWS));
        assertEquals("c28", chat.texts(1).get(0));
    }
}
