package com.tlm.cli.ui;

/**
 * Tipo de janela da TUI. Cada tipo abre uma caixa própria; a de histórico
 * não acompanha o fluxo ao vivo.
 */
public enum WindowKind {
    MESSAGES(" Mensagens ", null),
    CHAT(" Comentários ", Category.CHAT),
    GIFTS(" Presentes ", Category.GIFT),
    PINNED(" Fixados ", Category.PINNED),
    LIKES(" Curtidas ", Category.LIKE),
    FOLLOWS(" Seguidores ", Category.FOLLOW),
    HISTORY(" Histórico ", null);

    private final String title;
    private final Category category;

    WindowKind(String title, Category category) {
        this.title = title;
        this.category = category;
    }

    public String title() {
        return title;
    }

    /** {@code null} aceita qualquer categoria (mensagens da live ou histórico). */
    public Category category() {
        return category;
    }

    public boolean tracksLive() {
        return this != HISTORY;
    }
}
