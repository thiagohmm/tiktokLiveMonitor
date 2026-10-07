package com.tlm.cli.ui;

/** Categoria de exibição do feed (cor definida no TerminalUi). */
public enum Category {
    CHAT, GIFT, LIKE, FOLLOW, PINNED, SYSTEM, INFO;

    /** Converte filtro digitado; {@code null} significa "all". */
    public static Category parseFilter(String text) {
        String kind = text == null ? "" : text.trim().toLowerCase();
        return switch (kind) {
            case "all" -> null;
            case "chat", "comment", "comentarios", "comentários" -> CHAT;
            case "gift", "gifts", "presente", "presentes" -> GIFT;
            case "like", "likes", "curtidas" -> LIKE;
            case "follow", "followers", "seguidores" -> FOLLOW;
            case "pinned", "pin", "fixado", "fixados" -> PINNED;
            case "system", "sistema", "sys" -> SYSTEM;
            default -> throw new IllegalArgumentException("filtro inválido: " + text);
        };
    }
}
