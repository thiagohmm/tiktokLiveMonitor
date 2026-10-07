package com.tlm.cli.ui;

import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.List;
import java.util.function.Predicate;

/**
 * Histórico rolável do feed: guarda todas as linhas (limite de memória) e
 * mantém a visão filtrada atual + offset de rolagem de baixo para cima.
 */
public final class FeedBuffer {

    private static final int DEFAULT_CAPACITY = 1000;

    private final int capacity;
    private final ArrayDeque<FeedLine> all = new ArrayDeque<>();
    private final List<FeedLine> view = new ArrayList<>();
    private volatile Predicate<FeedLine> filter = always();
    private volatile int scrollOffset;

    public FeedBuffer() {
        this(DEFAULT_CAPACITY);
    }

    public FeedBuffer(int capacity) {
        this.capacity = capacity;
    }

    /** Adiciona linha; contadores ficam por fora (a UI decide o que conta). */
    public synchronized void add(FeedLine line) {
        all.addLast(line);
        while (all.size() > capacity) {
            all.pollFirst();
        }
        if (filter.test(line)) {
            view.add(line);
            if (scrollOffset > 0) {
                // Rolado para cima: mantém a âncora visual (a visão cresceu
                // uma linha no fim; compensa o offset para não deslizar).
                scrollOffset = Math.min(scrollOffset + 1, Math.max(0, view.size() - 1));
            }
            int overflow = view.size() - capacity;
            if (overflow > 0) {
                // Cabeça descartada empurra a âncora junto.
                view.subList(0, overflow).clear();
                scrollOffset = Math.max(0, scrollOffset - overflow);
            }
        }
    }

    /**
     * Troca o filtro; todos os eventos continuam disponíveis ao voltar p/ all.
     * O predicado recebido é envolvido admitindo sempre INFO e SYSTEM, para que
     * confirmações ("filtro: X") e erros de API permaneçam visíveis sob
     * qualquer filtro ativo.
     */
    public synchronized void setFilter(Predicate<FeedLine> newFilter) {
        this.filter = wrap(newFilter);
        rebuildView();
    }

    private static Predicate<FeedLine> wrap(Predicate<FeedLine> newFilter) {
        Predicate<FeedLine> base = newFilter == null ? always() : newFilter;
        return line -> base.test(line) || line.category() == Category.INFO || line.category() == Category.SYSTEM;
    }

    private static Predicate<FeedLine> always() {
        return line -> true;
    }

    public synchronized void clear() {
        all.clear();
        view.clear();
        scrollOffset = 0;
    }

    public synchronized void scrollUp() {
        scrollOffset = Math.min(scrollOffset + 1, Math.max(0, view.size() - 1));
    }

    public synchronized void pageUp(int rows) {
        scrollOffset = Math.min(scrollOffset + Math.max(1, rows), Math.max(0, view.size() - 1));
    }

    public synchronized void pageDown(int rows) {
        scrollOffset = Math.max(0, scrollOffset - Math.max(1, rows));
    }

    public synchronized void jumpToBottom() {
        scrollOffset = 0;
    }

    public synchronized boolean atBottom() {
        return scrollOffset == 0;
    }

    /** Janela visível (a partir de baixo, respeitando offset); menor que rows. */
    public synchronized List<FeedLine> window(int rows) {
        if (view.isEmpty()) {
            return List.of();
        }
        int visible = Math.max(1, rows);
        int last = view.size() - 1 - scrollOffset;
        int first = Math.max(0, last - visible + 1);
        return new ArrayList<>(view.subList(first, last + 1));
    }

    public synchronized boolean isEmpty() {
        return view.isEmpty();
    }

    private void rebuildView() {
        view.clear();
        for (FeedLine line : all) {
            if (filter.test(line)) {
                view.add(line);
            }
        }
        scrollOffset = 0;
        trim();
    }

    private void trim() {
        int overflow = view.size() - capacity;
        if (overflow > 0) {
            view.subList(0, overflow).clear();
        }
    }
}
