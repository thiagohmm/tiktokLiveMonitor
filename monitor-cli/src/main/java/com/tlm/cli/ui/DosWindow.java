package com.tlm.cli.ui;

import com.googlecode.lanterna.TerminalPosition;
import com.googlecode.lanterna.TerminalSize;
import com.googlecode.lanterna.TextColor;
import com.googlecode.lanterna.graphics.TextGraphics;

import java.util.ArrayList;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Set;

/**
 * Janela sobreposta no estilo DOS: borda, barra de título com [X],
 * corpo rolável e filtros (presente, quantidade, usuário).
 */
public final class DosWindow {

    public enum Zone {
        MISS, CLOSE, TITLE, BODY
    }

    private static final int MIN_WIDTH = 24;
    private static final int MIN_HEIGHT = 6;

    private final WindowKind kind;
    private final FeedBuffer feed = new FeedBuffer();
    private final List<FeedLine> extras = new ArrayList<>();
    private List<FeedLine> snapshot = List.of();

    private String title;
    private String footer = "";
    private Set<String> giftNames = Set.of();
    private String giftLabel = "";
    private int minCount;
    private String user = "";

    private int x;
    private int y;
    private int width;
    private int height;
    private boolean focused;

    public DosWindow(WindowKind kind, int x, int y, int width, int height) {
        this.kind = kind;
        this.title = kind.title();
        this.x = x;
        this.y = y;
        this.width = Math.max(MIN_WIDTH, width);
        this.height = Math.max(MIN_HEIGHT, height);
    }

    public WindowKind kind() {
        return kind;
    }

    public String title() {
        return title;
    }

    public void title(String title) {
        this.title = title == null || title.isBlank() ? kind.title() : title;
    }

    public void footer(String footer) {
        this.footer = footer == null ? "" : footer;
    }

    public void gifts(List<String> names) {
        LinkedHashSet<String> chosen = new LinkedHashSet<>();
        List<String> labels = new ArrayList<>();
        if (names != null) {
            for (String name : names) {
                if (name == null || name.isBlank()) {
                    continue;
                }
                chosen.add(name.toLowerCase());
                labels.add(name);
            }
        }
        giftNames = Set.copyOf(chosen);
        giftLabel = String.join(", ", labels);
    }

    public void minCount(int minCount) {
        this.minCount = Math.max(0, minCount);
    }

    public int minCount() {
        return minCount;
    }

    public void user(String user) {
        if (user == null) {
            this.user = "";
            return;
        }
        String trimmed = user.trim();
        this.user = trimmed.startsWith("@") ? trimmed.substring(1) : trimmed;
    }

    public String user() {
        return user;
    }

    public boolean focused() {
        return focused;
    }

    public void focused(boolean focused) {
        this.focused = focused;
    }

    public boolean hasCategory(Category category) {
        return feed.hasCategory(category);
    }

    public void inject(List<FeedLine> lines) {
        extras.addAll(lines);
        for (FeedLine line : lines) {
            if (matches(line)) {
                feed.add(line);
            }
        }
    }

    /** Troca o histórico injetado (backfill) e reconstrói com o fluxo ao vivo. */
    public void replaceExtras(List<FeedLine> lines, List<FeedLine> live) {
        extras.clear();
        if (lines != null) {
            extras.addAll(lines);
        }
        replay(live);
    }

    public void replaceSnapshot(List<FeedLine> lines) {
        snapshot = List.copyOf(lines);
        replay(List.of());
    }

    public void offer(FeedLine line) {
        if (kind.tracksLive() && matches(line)) {
            feed.add(line);
        }
    }

    /** Reconstrói o conteúdo a partir do histórico ao vivo (e dos extras). */
    public void replay(List<FeedLine> live) {
        feed.clear();
        List<FeedLine> source = kind.tracksLive() ? live : snapshot;
        for (FeedLine line : source) {
            if (matches(line)) {
                feed.add(line);
            }
        }
        if (kind.tracksLive()) {
            for (FeedLine line : extras) {
                if (matches(line)) {
                    feed.add(line);
                }
            }
        }
    }

    public boolean matches(FeedLine line) {
        if (kind.category() != null && line.category() != kind.category()) {
            return false;
        }
        if (!giftNames.isEmpty() && !giftNames.contains(line.giftName().toLowerCase())) {
            return false;
        }
        if (minCount > 0 && line.giftCount() < minCount) {
            return false;
        }
        if (!user.isBlank() && !line.user().toLowerCase().contains(user.toLowerCase())) {
            return false;
        }
        return true;
    }

    public void scrollLines(int delta) {
        if (delta < 0) {
            feed.pageUp(-delta);
        } else if (delta > 0) {
            feed.pageDown(delta);
        }
    }

    public void scrollPage(int pages, int cols, int rows) {
        scrollLines(pages * innerRows(cols, rows));
    }

    public List<String> texts(int rows) {
        List<String> texts = new ArrayList<>();
        for (FeedLine line : feed.window(rows)) {
            texts.add(line.text());
        }
        return texts;
    }

    public void place(int x, int y, int width, int height) {
        this.x = x;
        this.y = y;
        this.width = Math.max(MIN_WIDTH, width);
        this.height = Math.max(MIN_HEIGHT, height);
    }

    public void moveTo(int x, int y, int cols, int rows) {
        this.x = x;
        this.y = y;
        Frame visible = visible(cols, rows);
        this.x = visible.x();
        this.y = visible.y();
    }

    public Frame visible(int cols, int rows) {
        int top = 2;
        int bottom = Math.max(top, rows - 1);
        int maxWidth = Math.max(1, cols);
        int maxHeight = Math.max(1, bottom - top);
        int w = Math.min(Math.max(1, width), maxWidth);
        int h = Math.min(Math.max(1, height), maxHeight);
        int left = x;
        int topEdge = Math.max(top, y);
        if (left < 0) {
            left = 0;
        }
        if (left + w > cols) {
            left = Math.max(0, cols - w);
        }
        if (topEdge + h > bottom) {
            topEdge = Math.max(top, bottom - h);
        }
        return new Frame(left, topEdge, w, h);
    }

    public int innerRows(int cols, int rows) {
        return Math.max(1, visible(cols, rows).height() - 2);
    }

    public Zone hit(int col, int row, int cols, int rows) {
        Frame box = visible(cols, rows);
        if (col < box.x() || row < box.y() || col >= box.x() + box.width() || row >= box.y() + box.height()) {
            return Zone.MISS;
        }
        if (row == box.y()) {
            if (col >= box.x() + box.width() - 4 && col <= box.x() + box.width() - 2) {
                return Zone.CLOSE;
            }
            return Zone.TITLE;
        }
        if (row == box.y() + box.height() - 1) {
            return Zone.TITLE;
        }
        return Zone.BODY;
    }

    public void draw(TextGraphics g, int cols, int rows) {
        Frame box = visible(cols, rows);
        int x0 = box.x();
        int y0 = box.y();
        int w = box.width();
        int h = box.height();
        TextColor bar = focused ? TextColor.ANSI.CYAN : TextColor.ANSI.BLUE;
        g.setBackgroundColor(TextColor.ANSI.BLUE);
        g.setForegroundColor(TextColor.ANSI.WHITE_BRIGHT);
        g.fillRectangle(new TerminalPosition(x0, y0), new TerminalSize(w, h), ' ');
        horizontal(g, x0, y0, w, '╔', '╗');
        for (int row = 1; row < h - 1; row++) {
            g.putString(x0, y0 + row, "║");
            g.putString(x0 + w - 1, y0 + row, "║");
        }
        if (h > 1) {
            horizontal(g, x0, y0 + h - 1, w, '╚', '╝');
        }
        g.setBackgroundColor(bar);
        g.setForegroundColor(TextColor.ANSI.WHITE_BRIGHT);
        g.putString(x0 + 1, y0, " ".repeat(Math.max(0, w - 2)));
        String label = TerminalText.fit(title, Math.max(0, w - 6));
        g.putString(x0 + 2, y0, label);
        g.setBackgroundColor(TextColor.ANSI.RED);
        g.setForegroundColor(TextColor.ANSI.WHITE_BRIGHT);
        g.putString(x0 + w - 4, y0, "[X]");

        int innerWidth = Math.max(1, w - 4);
        int inner = Math.max(0, h - 2);
        List<FeedLine> lines = feed.window(inner);
        int start = y0 + 1 + Math.max(0, inner - lines.size());
        if (lines.isEmpty()) {
            g.setBackgroundColor(TextColor.ANSI.BLUE);
            g.setForegroundColor(TextColor.ANSI.WHITE);
            g.putString(x0 + 2, y0 + 1, TerminalText.fit("aguardando a live…", innerWidth));
        }
        for (int i = 0; i < lines.size(); i++) {
            FeedLine line = lines.get(i);
            g.setBackgroundColor(TextColor.ANSI.BLUE);
            g.setForegroundColor(colorOf(line.category()));
            g.putString(x0 + 2, start + i, TerminalText.fit(line.text(), innerWidth));
        }
        if (h > 1) {
            g.setBackgroundColor(bar);
            g.setForegroundColor(TextColor.ANSI.WHITE_BRIGHT);
            String status = TerminalText.fit(footer.isBlank() ? summary() : footer, Math.max(0, w - 4));
            int at = x0 + Math.max(1, (w - status.codePointCount(0, status.length())) / 2);
            g.putString(at, y0 + h - 1, status);
        }
        g.setBackgroundColor(TextColor.ANSI.DEFAULT);
    }

    private String summary() {
        List<String> parts = new ArrayList<>();
        if (!giftLabel.isBlank()) {
            parts.add(giftLabel);
        }
        if (minCount > 0) {
            parts.add("qtd>=" + minCount);
        }
        if (!user.isBlank()) {
            parts.add("@" + user);
        }
        String filters = parts.isEmpty() ? "tudo" : String.join(" ", parts);
        return " " + filters + "   [X] fecha ";
    }

    private static void horizontal(TextGraphics g, int x, int y, int width, char left, char right) {
        g.setBackgroundColor(TextColor.ANSI.BLUE);
        g.setForegroundColor(TextColor.ANSI.WHITE_BRIGHT);
        g.putString(x, y, String.valueOf(left) + "═".repeat(Math.max(0, width - 2)) + right);
    }

    private static TextColor colorOf(Category category) {
        return switch (category) {
            case CHAT -> TextColor.ANSI.CYAN_BRIGHT;
            case GIFT -> TextColor.ANSI.YELLOW_BRIGHT;
            case LIKE -> TextColor.ANSI.MAGENTA_BRIGHT;
            case FOLLOW -> TextColor.ANSI.GREEN_BRIGHT;
            case PINNED -> TextColor.ANSI.WHITE_BRIGHT;
            case SYSTEM -> TextColor.ANSI.RED_BRIGHT;
            case INFO -> TextColor.ANSI.WHITE;
        };
    }

}
