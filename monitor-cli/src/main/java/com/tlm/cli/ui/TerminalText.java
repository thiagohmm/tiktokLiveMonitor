package com.tlm.cli.ui;

import com.googlecode.lanterna.TerminalTextUtils;

/**
 * Texto seguro para a célula do terminal: sem controle, sem sequências que o
 * terminal junta num glifo só (emoji, combinantes) e cortado pela largura
 * em colunas, que é o que a Lanterna realmente avança.
 */
public final class TerminalText {

    private TerminalText() {
    }

    public static String fit(String text, int columns) {
        if (columns <= 0) {
            return "";
        }
        String plain = plain(text);
        if (TerminalTextUtils.getColumnWidth(plain) <= columns) {
            return plain;
        }
        if (columns == 1) {
            return TerminalTextUtils.fitString(plain, 1);
        }
        String withMark = TerminalTextUtils.fitString(plain, columns - 1) + "…";
        if (TerminalTextUtils.getColumnWidth(withMark) <= columns) {
            return withMark;
        }
        return TerminalTextUtils.fitString(plain, columns);
    }

    private static String plain(String text) {
        if (text == null || text.isEmpty()) {
            return "";
        }
        StringBuilder out = new StringBuilder(text.length());
        text.codePoints().forEach(codePoint -> append(out, codePoint));
        return out.toString();
    }

    private static void append(StringBuilder out, int codePoint) {
        if (codePoint == '\n' || codePoint == '\r' || codePoint == '\t') {
            out.append(' ');
            return;
        }
        if (codePoint < 32 || codePoint == 127) {
            return;
        }
        int type = Character.getType(codePoint);
        if (type == Character.NON_SPACING_MARK
                || type == Character.ENCLOSING_MARK
                || type == Character.COMBINING_SPACING_MARK
                || type == Character.FORMAT) {
            return;
        }
        if (codePoint > 0xFFFF) {
            out.append('?');
            return;
        }
        out.append((char) codePoint);
    }
}
