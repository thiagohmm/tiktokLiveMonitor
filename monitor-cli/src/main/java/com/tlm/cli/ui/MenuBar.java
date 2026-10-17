package com.tlm.cli.ui;

import java.util.ArrayList;
import java.util.List;
import java.util.Optional;

/**
 * Barra de menu da linha 1: cada rótulo abre a janela correspondente.
 * O clique usa a mesma faixa desenhada.
 */
public final class MenuBar {

    public record Item(String label, WindowKind kind, int x, int width) {
    }

    private static final WindowKind[] ORDER = {
            WindowKind.CHAT,
            WindowKind.GIFTS,
            WindowKind.PINNED,
            WindowKind.MESSAGES,
            WindowKind.LIKES,
            WindowKind.FOLLOWS
    };

    public List<Item> layout(int cols) {
        List<Item> items = new ArrayList<>();
        int x = 0;
        for (WindowKind kind : ORDER) {
            String label = kind.title();
            if (x + label.length() > cols) {
                break;
            }
            items.add(new Item(label, kind, x, label.length()));
            x += label.length();
        }
        return items;
    }

    public Optional<WindowKind> hit(int col, int row, int cols) {
        if (row != 1 || col < 0) {
            return Optional.empty();
        }
        for (Item item : layout(cols)) {
            if (col >= item.x() && col < item.x() + item.width()) {
                return Optional.of(item.kind());
            }
        }
        return Optional.empty();
    }
}
