package com.tlm.cli.ui;

import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.Collections;
import java.util.Iterator;
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
    private final ArrayDeque<FeedLine> view = new ArrayDeque<>();
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
            view.addLast(line);
            if (scrollOffset > 0) {
                // Rolado para cima: mantém a âncora visual (a visão cresceu
                // uma linha no fim; compensa o offset para não deslizar).
                scrollOffset = Math.min(scrollOffset + 1, Math.max(0, view.size() - 1));
            }
            while (view.size() > capacity) {
                // Cabeça descartada empurra a âncora junto.
                view.pollFirst();
                scrollOffset = Math.max(0, scrollOffset - 1);
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
        Iterator<FeedLine> it = view.descendingIterator();
        int skip = scrollOffset;
        while (skip > 0 && it.hasNext()) {
            it.next();
            skip--;
        }
        List<FeedLine> reversed = new ArrayList<>(Math.min(visible, view.size()));
        while (it.hasNext() && reversed.size() < visible) {
            reversed.add(it.next());
        }
        Collections.reverse(reversed);
        return reversed;
    }

    public synchronized boolean isEmpty() {
        return view.isEmpty();
    }

    public synchronized int size() {
        return view.size();
    }

    /** Cópia cronológica da visão filtrada (a mais antiga primeiro). */
    public synchronized List<FeedLine> lines() {
        return List.copyOf(view);
    }

    /** Existe alguma linha daquela categoria no histórico completo? */
    public synchronized boolean hasCategory(Category category) {
        for (FeedLine line : all) {
            if (line.category() == category) {
                return true;
            }
        }
        return false;
    }

    private void rebuildView() {
        view.clear();
        for (FeedLine line : all) {
            if (filter.test(line)) {
                view.addLast(line);
            }
        }
        scrollOffset = 0;
        trim();
    }

    private void trim() {
        while (view.size() > capacity) {
            view.pollFirst();
        }
    }
}
