package com.tlm.cli.ui;

/**
 * Linha pronta para o feed (texto já formatado com timestamp). O nome do
 * presente é preenchido nas linhas de categoria GIFT para o filtro por
 * presente; em qualquer outra linha vale "" (n/a).
 */
public record FeedLine(String text, Category category, String giftName) {

    public FeedLine {
        text = text == null ? "" : text;
        giftName = giftName == null ? "" : giftName;
    }

    /** Construtor de compatibilidade para linhas sem presente associado. */
    public FeedLine(String text, Category category) {
        this(text, category, "");
    }
}
