package com.tlm.cli.ui;

import com.googlecode.lanterna.TerminalPosition;
import com.googlecode.lanterna.TerminalSize;
import com.googlecode.lanterna.TextColor;
import com.googlecode.lanterna.graphics.TextGraphics;

import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Locale;
import java.util.Set;

/**
 * Janela sobreposta no estilo DOS: borda, barra de título com [X],
 * corpo rolável e filtros (presente, quantidade, usuário).
 */
public final class DosWindow {

    public enum Zone {
        MISS, CLOSE, TITLE, BODY, ORDER, LIMIT
    }

    /** Faixa clicável do rodapé: ordem e campo do limite. */
    public record Controls(int orderX, int orderWidth, int limitX, int limitWidth) {
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
    private String userLower = "";
    private boolean descending;
    private int limit;
    private String limitDraft = "";
    private boolean editingLimit;
    private int scrollOffset;
    /** Cópia materializada de {@link #presentation()}; nula quando precisa refazer. */
    private List<FeedLine> presentationCache;

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
        invalidatePresentation();
    }

    public void minCount(int minCount) {
        this.minCount = Math.max(0, minCount);
        invalidatePresentation();
    }

    public int minCount() {
        return minCount;
    }

    public void user(String user) {
        if (user == null) {
            this.user = "";
            this.userLower = "";
            invalidatePresentation();
            return;
        }
        String trimmed = user.trim();
        this.user = trimmed.startsWith("@") ? trimmed.substring(1) : trimmed;
        this.userLower = this.user.toLowerCase(Locale.ROOT);
        invalidatePresentation();
    }

    public String user() {
        return user;
    }

    public boolean descending() {
        return descending;
    }

    public void toggleOrder() {
        descending = !descending;
        scrollOffset = 0;
        invalidatePresentation();
    }

    /** {@code 0} mostra o fluxo inteiro; acima disso, só os últimos N. */
    public int limit() {
        return limit;
    }

    public void setLimit(int limit) {
        this.limit = Math.max(0, limit);
        limitDraft = "";
        editingLimit = false;
        scrollOffset = 0;
        invalidatePresentation();
    }

    public boolean editingLimit() {
        return editingLimit;
    }

    public void beginLimitEdit() {
        if (editingLimit) {
            return;
        }
        editingLimit = true;
        limitDraft = limit > 0 ? Integer.toString(limit) : "";
    }

    public void typeLimit(char digit) {
        if (!editingLimit || digit < '0' || digit > '9' || limitDraft.length() >= 4) {
            return;
        }
        limitDraft += digit;
    }

    public void backspaceLimit() {
        if (!editingLimit || limitDraft.isEmpty()) {
            return;
        }
        limitDraft = limitDraft.substring(0, limitDraft.length() - 1);
    }

    public void commitLimitEdit() {
        if (!editingLimit) {
            return;
        }
        editingLimit = false;
        limit = limitDraft.isBlank() ? 0 : Integer.parseInt(limitDraft);
        limitDraft = "";
        scrollOffset = 0;
        invalidatePresentation();
    }

    public void cancelLimitEdit() {
        editingLimit = false;
        limitDraft = "";
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
        int added = 0;
        for (FeedLine line : lines) {
            if (matches(line)) {
                feed.add(line);
                added++;
            }
        }
        invalidatePresentation();
        holdScroll(added);
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
            invalidatePresentation();
            holdScroll(1);
        }
    }

    /** Reconstrói o conteúdo a partir do histórico ao vivo (e dos extras). */
    public void replay(List<FeedLine> live) {
        scrollOffset = 0;
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
        invalidatePresentation();
    }

    public boolean matches(FeedLine line) {
        if (kind.category() != null && line.category() != kind.category()) {
            return false;
        }
        if (!giftNames.isEmpty() && !giftMatch(line.giftName())) {
            return false;
        }
        if (minCount > 0 && line.giftCount() < minCount) {
            return false;
        }
        if (!userLower.isEmpty() && !containsIgnoreCase(line.user(), userLower)) {
            return false;
        }
        return true;
    }

    private boolean giftMatch(String giftName) {
        for (String name : giftNames) {
            if (name.equalsIgnoreCase(giftName)) {
                return true;
            }
        }
        return false;
    }

    private static boolean containsIgnoreCase(String haystack, String needleLower) {
        int needle = needleLower.length();
        if (needle == 0) {
            return true;
        }
        int last = haystack.length() - needle;
        for (int i = 0; i <= last; i++) {
            if (haystack.regionMatches(true, i, needleLower, 0, needle)) {
                return true;
            }
        }
        return false;
    }

    public void scrollLines(int delta) {
        int max = Math.max(0, presentation().size() - 1);
        if (delta < 0) {
            scrollOffset = Math.min(scrollOffset + (-delta), max);
        } else if (delta > 0) {
            scrollOffset = Math.max(0, scrollOffset - delta);
        }
    }

    public void scrollPage(int pages, int cols, int rows) {
        scrollLines(pages * innerRows(cols, rows));
    }

    public List<String> texts(int rows) {
        List<String> texts = new ArrayList<>();
        for (FeedLine line : visibleLines(rows)) {
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
            Controls controls = controls(box);
            if (col >= controls.orderX() && col < controls.orderX() + controls.orderWidth()) {
                return Zone.ORDER;
            }
            if (col >= controls.limitX() && col < controls.limitX() + controls.limitWidth()) {
                return Zone.LIMIT;
            }
            return Zone.TITLE;
        }
        return Zone.BODY;
    }

    public Controls controls(int cols, int rows) {
        return controls(visible(cols, rows));
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
        List<FeedLine> lines = visibleLines(inner);
        int start = descending
                ? y0 + 1
                : y0 + 1 + Math.max(0, inner - lines.size());
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
            int footerRow = y0 + h - 1;
            g.setBackgroundColor(bar);
            g.setForegroundColor(TextColor.ANSI.WHITE_BRIGHT);
            g.putString(x0 + 1, footerRow, " ".repeat(Math.max(0, w - 2)));
            Controls controls = controls(box);
            g.setBackgroundColor(TextColor.ANSI.WHITE);
            g.setForegroundColor(TextColor.ANSI.BLACK);
            g.putString(controls.orderX(), footerRow, orderLabel());
            if (editingLimit) {
                g.setBackgroundColor(TextColor.ANSI.CYAN);
            }
            g.putString(controls.limitX(), footerRow, limitText());
            g.setBackgroundColor(bar);
            g.setForegroundColor(TextColor.ANSI.WHITE_BRIGHT);
            String status = footer.isBlank() ? summary() : footer;
            int statusX = controls.limitX() + controls.limitWidth() + 1;
            int room = Math.max(0, x0 + w - 1 - statusX);
            if (room > 0) {
                g.putString(statusX, footerRow, TerminalText.fit(status, room));
            }
        }
        g.setBackgroundColor(TextColor.ANSI.DEFAULT);
    }

    private Controls controls(Frame box) {
        int orderX = box.x() + 1;
        String order = orderLabel();
        int limitX = orderX + order.length() + 1;
        String limitField = limitText();
        return new Controls(orderX, order.length(), limitX, limitField.length());
    }

    private String orderLabel() {
        return descending ? "decrescente" : "crescente";
    }

    private String limitText() {
        String value;
        if (editingLimit) {
            value = limitDraft.isEmpty() ? " " : limitDraft;
        } else {
            value = limit > 0 ? Integer.toString(limit) : " ";
        }
        return "[" + value + "]";
    }

    private List<FeedLine> presentation() {
        if (presentationCache != null) {
            return presentationCache;
        }
        List<FeedLine> lines = new ArrayList<>(feed.lines());
        if (limit > 0 && lines.size() > limit) {
            lines = new ArrayList<>(lines.subList(lines.size() - limit, lines.size()));
        }
        if (descending) {
            Collections.reverse(lines);
        }
        presentationCache = lines;
        return lines;
    }

    private void invalidatePresentation() {
        presentationCache = null;
    }

    private List<FeedLine> visibleLines(int rows) {
        List<FeedLine> lines = presentation();
        if (lines.isEmpty() || rows <= 0) {
            return List.of();
        }
        if (!descending) {
            int last = lines.size() - 1 - scrollOffset;
            if (last < 0) {
                return List.of();
            }
            int first = Math.max(0, last - rows + 1);
            return new ArrayList<>(lines.subList(first, last + 1));
        }
        int first = Math.min(scrollOffset, lines.size());
        int last = Math.min(lines.size(), first + rows);
        if (first >= last) {
            return List.of();
        }
        return new ArrayList<>(lines.subList(first, last));
    }

    /** Mantém a linha que estava na tela quando chega evento e a pessoa rolou. */
    private void holdScroll(int added) {
        if (scrollOffset > 0 && added > 0) {
            int size = feed.size();
            if (limit > 0) {
                size = Math.min(size, limit);
            }
            scrollOffset = Math.min(scrollOffset + added, Math.max(0, size - 1));
        }
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
