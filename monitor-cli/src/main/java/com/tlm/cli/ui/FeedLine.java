package com.tlm.cli.ui;

/**
 * Linha pronta para o feed (texto já formatado com timestamp).
 * {@code giftName} e {@code giftCount} alimentam o filtro da janela de
 * presentes; {@code user} é o apelido exibido (vazio quando não há autor).
 */
public record FeedLine(String text, Category category, String giftName, String user, int giftCount) {

    public FeedLine {
        text = text == null ? "" : text;
        giftName = giftName == null ? "" : giftName;
        user = user == null ? "" : user;
        giftCount = Math.max(0, giftCount);
    }

    /** Linha sem presente e sem autor (avisos da própria TUI). */
    public FeedLine(String text, Category category) {
        this(text, category, "", "", 0);
    }

    /** Linha de presente identificada só pelo nome. */
    public FeedLine(String text, Category category, String giftName) {
        this(text, category, giftName, "", 0);
    }
}
