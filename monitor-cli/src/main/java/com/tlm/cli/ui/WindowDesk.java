package com.tlm.cli.ui;

import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.Deque;
import java.util.List;
import java.util.Optional;

/**
 * Pilha de janelas DOS. O histórico da sessão é repartido para cada janela
 * conforme o filtro dela; o clique traz a janela para a frente.
 */
public final class WindowDesk {

    private static final int HISTORY_LIMIT = 1000;

    private final Deque<FeedLine> history = new ArrayDeque<>();
    private final List<DosWindow> windows = new ArrayList<>();
    private DosWindow drag;
    private int grabDx;
    private int grabDy;
    private int historyGeneration;

    public DosWindow open(WindowKind kind) {
        for (DosWindow window : windows) {
            if (window.kind() == kind) {
                focus(window);
                return window;
            }
        }
        int index = windows.size();
        DosWindow created = new DosWindow(kind, 1 + (index % 4) * 2, 2 + (index % 4), 68, 16);
        created.replay(historyView());
        windows.add(created);
        focus(created);
        return created;
    }

    public Optional<DosWindow> find(WindowKind kind) {
        for (DosWindow window : windows) {
            if (window.kind() == kind) {
                return Optional.of(window);
            }
        }
        return Optional.empty();
    }

    public Optional<DosWindow> focused() {
        for (int i = windows.size() - 1; i >= 0; i--) {
            if (windows.get(i).focused()) {
                return Optional.of(windows.get(i));
            }
        }
        return Optional.empty();
    }

    public List<DosWindow> stack() {
        return List.copyOf(windows);
    }

    public void publish(FeedLine line) {
        history.addLast(line);
        while (history.size() > HISTORY_LIMIT) {
            history.pollFirst();
        }
        for (DosWindow window : windows) {
            window.offer(line);
        }
    }

    public void setGifts(DosWindow window, List<String> names) {
        window.gifts(names);
        window.replay(historyView());
    }

    public void setMinCount(DosWindow window, int minCount) {
        window.minCount(minCount);
        window.replay(historyView());
    }

    public void setUser(DosWindow window, String user) {
        window.user(user);
        window.replay(historyView());
    }

    public void inject(DosWindow window, List<FeedLine> lines) {
        window.inject(lines);
    }

    public void replaceInjected(DosWindow window, List<FeedLine> lines) {
        window.replaceExtras(lines, historyView());
    }

    public int beginHistory() {
        return ++historyGeneration;
    }

    public void showHistory(int generation, String title, String footer, List<FeedLine> lines) {
        if (generation != historyGeneration) {
            return;
        }
        DosWindow window = open(WindowKind.HISTORY);
        window.title(title);
        window.footer(footer);
        window.replaceSnapshot(lines);
    }

    public void closeFocused() {
        focused().ifPresent(this::close);
    }

    public void close(DosWindow window) {
        if (window.kind() == WindowKind.HISTORY) {
            historyGeneration++;
        }
        windows.remove(window);
        if (drag == window) {
            drag = null;
        }
        if (focused().isEmpty() && !windows.isEmpty()) {
            focus(windows.get(windows.size() - 1));
        }
    }

    public void focus(DosWindow window) {
        for (DosWindow other : windows) {
            other.focused(false);
        }
        windows.remove(window);
        windows.add(window);
        window.focused(true);
    }

    public void clearFocus() {
        for (DosWindow window : windows) {
            window.focused(false);
        }
    }

    public void cycleFocus() {
        if (windows.isEmpty()) {
            return;
        }
        Optional<DosWindow> current = focused();
        if (current.isEmpty()) {
            focus(windows.get(windows.size() - 1));
            return;
        }
        int index = windows.indexOf(current.get());
        focus(windows.get((index + 1) % windows.size()));
    }

    public void commitLimitEdits() {
        commitLimitsExcept(null);
    }

    public DosWindow.Zone onClick(int col, int row, int cols, int rows) {
        drag = null;
        for (int i = windows.size() - 1; i >= 0; i--) {
            DosWindow window = windows.get(i);
            DosWindow.Zone zone = window.hit(col, row, cols, rows);
            if (zone == DosWindow.Zone.MISS) {
                continue;
            }
            if (zone == DosWindow.Zone.LIMIT) {
                commitLimitsExcept(window);
                focus(window);
                window.beginLimitEdit();
                return zone;
            }
            commitLimitsExcept(null);
            switch (zone) {
                case CLOSE -> close(window);
                case ORDER -> {
                    focus(window);
                    window.toggleOrder();
                }
                case TITLE -> {
                    focus(window);
                    Frame box = window.visible(cols, rows);
                    drag = window;
                    grabDx = col - box.x();
                    grabDy = row - box.y();
                }
                case BODY -> focus(window);
                case LIMIT, MISS -> {
                }
            }
            return zone;
        }
        commitLimitsExcept(null);
        clearFocus();
        return DosWindow.Zone.MISS;
    }

    private void commitLimitsExcept(DosWindow keep) {
        for (DosWindow window : windows) {
            if (window != keep) {
                window.commitLimitEdit();
            }
        }
    }

    public void onDrag(int col, int row, int cols, int rows) {
        if (drag == null) {
            return;
        }
        drag.moveTo(col - grabDx, row - grabDy, cols, rows);
    }

    public boolean onScroll(int col, int row, int delta, int cols, int rows) {
        for (int i = windows.size() - 1; i >= 0; i--) {
            DosWindow window = windows.get(i);
            DosWindow.Zone zone = window.hit(col, row, cols, rows);
            if (zone == DosWindow.Zone.BODY || zone == DosWindow.Zone.TITLE) {
                window.scrollLines(delta);
                focus(window);
                return true;
            }
        }
        return false;
    }

    private List<FeedLine> historyView() {
        return new ArrayList<>(history);
    }
}
