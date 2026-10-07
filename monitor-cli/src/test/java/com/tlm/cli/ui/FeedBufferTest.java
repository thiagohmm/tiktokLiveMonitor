package com.tlm.cli.ui;

import org.junit.jupiter.api.Test;

import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

class FeedBufferTest {

    private static FeedLine line(String text, Category category) {
        return new FeedLine(text, category);
    }

    @Test
    void showsLastLinesByDefault() {
        FeedBuffer feed = new FeedBuffer();
        for (int i = 1; i <= 10; i++) {
            feed.add(line("linha " + i, Category.CHAT));
        }
        List<FeedLine> window = feed.window(3);
        assertEquals(3, window.size());
        assertEquals("linha 10", window.get(2).text());
        assertTrue(feed.atBottom());
    }

    @Test
    void scrolledUpViewKeepsAnchorWhenNewLinesArrive() {
        FeedBuffer feed = new FeedBuffer();
        for (int i = 1; i <= 10; i++) {
            feed.add(line("linha " + i, Category.CHAT));
        }
        feed.pageUp(10); // vai para o topo
        List<FeedLine> before = feed.window(3);
        assertEquals("linha 1", before.get(0).text(), "pageUp total deve mostrar a primeira linha");

        // novas linhas chegando NÃO devem deslizar a visão
        feed.add(line("nova 1", Category.CHAT));
        feed.add(line("nova 2", Category.CHAT));
        List<FeedLine> after = feed.window(3);
        assertEquals(before.get(0).text(), after.get(0).text(), "âmora-visual deve se manter");
        assertTrue(!feed.atBottom());
    }

    @Test
    void filterChangeRestoresOlderLines() {
        FeedBuffer feed = new FeedBuffer();
        feed.add(line("c1", Category.CHAT));
        feed.add(line("g1", Category.GIFT));
        feed.add(line("c2", Category.CHAT));
        feed.setFilter(l -> l.category() == Category.GIFT);
        assertEquals(1, feed.window(10).size());
        feed.setFilter(l -> true);
        assertEquals(3, feed.window(10).size());
    }

    @Test
    void giftFilterMatchesByGiftNameOfTheLine() {
        FeedBuffer feed = new FeedBuffer();
        feed.add(new FeedLine("g1", Category.GIFT, "Rosa"));
        feed.add(new FeedLine("g2", Category.GIFT, "Dino"));
        feed.add(new FeedLine("c1", Category.CHAT));
        feed.setFilter(l -> l.giftName().equalsIgnoreCase("rosa"));
        assertEquals(List.of("g1"), names(feed.window(10)));

        feed.setFilter(l -> true);
        assertEquals(3, feed.window(10).size());
    }

    @Test
    void infoAndSystemLinesStayVisibleUnderAnyFilter() {
        FeedBuffer feed = new FeedBuffer();
        feed.add(new FeedLine("regulamento", Category.INFO));
        feed.add(new FeedLine("erro de API", Category.SYSTEM));
        feed.add(new FeedLine("c1", Category.CHAT));
        feed.add(new FeedLine("g1", Category.GIFT));

        feed.setFilter(l -> l.category() == Category.GIFT);
        assertEquals(List.of("regulamento", "erro de API", "g1"), names(feed.window(10)));

        feed.setFilter(l -> l.category() == Category.LIKE);
        assertEquals(List.of("regulamento", "erro de API"), names(feed.window(10)));

        feed.setFilter(l -> l.category() == Category.PINNED);
        assertEquals(List.of("regulamento", "erro de API"), names(feed.window(10)));
    }

    @Test
    void clearResetsView() {
        FeedBuffer feed = new FeedBuffer();
        feed.add(line("x", Category.SYSTEM));
        feed.clear();
        assertTrue(feed.isEmpty());
        assertTrue(feed.atBottom());
    }

    @Test
    void headTrimKeepsScrollAnchorBounded() {
        FeedBuffer feed = new FeedBuffer(5);
        for (int i = 1; i <= 20; i++) {
            feed.add(line("l" + i, Category.CHAT));
        }
        // capacidade 5: view mantém l16..l20; pageUp(10) → offset 4 (máx)
        feed.pageUp(10);
        assertEquals(List.of("l16"), names(feed.window(50)));

        // nova linha: head l16 é descartada; a linha imediatamente acima
        // (l17) mantém-se visível (âncora acompanha a poda da cabeça)
        feed.add(line("nova", Category.CHAT));
        assertEquals(List.of("l17"), names(feed.window(50)));

        feed.jumpToBottom();
        assertEquals(List.of("l17", "l18", "l19", "l20", "nova"), names(feed.window(5)));
    }

    private static List<String> names(List<FeedLine> lines) {
        return lines.stream().map(FeedLine::text).toList();
    }
}
