package com.tlm.cli.ui;

import com.googlecode.lanterna.TerminalTextUtils;
import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

class TerminalTextTest {

    @Test
    void dropsBreaksMarksAndEmoji() {
        String fitted = TerminalText.fit("oi\nmundo 😀\u200D", 40);
        assertFalse(fitted.contains("\n"));
        assertFalse(fitted.contains("😀"));
        assertTrue(fitted.contains("oi"));
        assertTrue(fitted.contains("mundo"));
        assertTrue(fitted.contains("?"));
    }

    @Test
    void clipsToColumnBudget() {
        String fitted = TerminalText.fit("abcdefghij", 4);
        assertTrue(TerminalTextUtils.getColumnWidth(fitted) <= 4);
        assertTrue(fitted.startsWith("abc"));
    }

    @Test
    void keepsShortText() {
        assertEquals("oi", TerminalText.fit("oi", 10));
    }
}
