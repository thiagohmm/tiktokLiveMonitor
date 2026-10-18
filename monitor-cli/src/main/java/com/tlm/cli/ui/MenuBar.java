package com.tlm.cli.ui;

import java.util.ArrayList;
import java.util.List;
import java.util.Optional;

/**
 * Barra de menu da linha 1: cada rótulo abre a janela correspondente e, à
 * direita, o campo de conectar (ou o botão de desconectar).
 */
public final class MenuBar {

    public record Item(String label, WindowKind kind, int x, int width) {
    }

    /** O que a barra desenha, para o clique cair na mesma faixa. */
    public record Chrome(
            List<Item> windows,
            String action,
            int actionX,
            int actionWidth,
            String field,
            int fieldX,
            int fieldWidth,
            boolean connected) {
    }

    public sealed interface Target {
        record Open(WindowKind kind) implements Target {
        }

        record ConnectField() implements Target {
        }

        record ConnectSubmit() implements Target {
        }

        record Disconnect() implements Target {
        }
    }

    private static final int FIELD_INNER = 12;

    private static final WindowKind[] ORDER = {
            WindowKind.CHAT,
            WindowKind.GIFTS,
            WindowKind.PINNED,
            WindowKind.MESSAGES,
            WindowKind.LIKES,
            WindowKind.FOLLOWS
    };

    public List<Item> layout(int cols) {
        return chrome(cols, false, "").windows();
    }

    public Chrome chrome(int cols, boolean connected, String draft) {
        String action = connected ? "Desconectar" : "Conectar";
        String field = connected ? "" : fieldText(draft);
        int fieldWidth = field.length();
        int actionX = 0;
        int fieldX = connected ? 0 : action.length() + 1;
        int windowsAt = action.length() + (connected ? 0 : 1 + fieldWidth) + 1;

        List<Item> items = new ArrayList<>();
        int x = windowsAt;
        for (WindowKind kind : ORDER) {
            String label = kind.title();
            if (x + label.length() > cols) {
                break;
            }
            items.add(new Item(label, kind, x, label.length()));
            x += label.length();
        }
        return new Chrome(items, action, actionX, action.length(), field, fieldX, fieldWidth, connected);
    }

    public Optional<WindowKind> hit(int col, int row, int cols) {
        return hit(col, row, cols, false, "").flatMap(target -> switch (target) {
            case Target.Open open -> Optional.of(open.kind());
            case Target.ConnectField ignored -> Optional.empty();
            case Target.ConnectSubmit ignored -> Optional.empty();
            case Target.Disconnect ignored -> Optional.empty();
        });
    }

    public Optional<Target> hit(int col, int row, int cols, boolean connected, String draft) {
        if (row != 1 || col < 0) {
            return Optional.empty();
        }
        Chrome bar = chrome(cols, connected, draft);
        if (col >= bar.actionX() && col < bar.actionX() + bar.actionWidth()) {
            return Optional.of(connected ? new Target.Disconnect() : new Target.ConnectSubmit());
        }
        if (!connected && col >= bar.fieldX() && col < bar.fieldX() + bar.fieldWidth()) {
            return Optional.of(new Target.ConnectField());
        }
        for (Item item : bar.windows()) {
            if (col >= item.x() && col < item.x() + item.width()) {
                return Optional.of(new Target.Open(item.kind()));
            }
        }
        return Optional.empty();
    }

    private static String fieldText(String draft) {
        String shown = draft == null ? "" : draft;
        if (shown.length() > FIELD_INNER) {
            shown = shown.substring(shown.length() - FIELD_INNER);
        }
        return "[" + shown + " ".repeat(FIELD_INNER - shown.length()) + "]";
    }
}
