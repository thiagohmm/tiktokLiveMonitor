package com.tlm.cli.ui;

import org.junit.jupiter.api.Test;

import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

class GiftSelectionTest {

    private static final List<String> CATALOG = List.of("Rosa", "Dino", "Fenix");

    @Test
    void numericTokensMapToLatestListOneBased() {
        GiftSelection.Result result = GiftSelection.resolve("1,3", CATALOG);
        assertTrue(result.ok());
        assertEquals(List.of("Rosa", "Fenix"), result.names());
    }

    @Test
    void nameTokensAreLiteralAndCaseKept() {
        GiftSelection.Result result = GiftSelection.resolve("Rosa, Dino", CATALOG);
        assertTrue(result.ok());
        assertEquals(List.of("Rosa", "Dino"), result.names());
    }

    @Test
    void unknownNamesAreAcceptedAsTyped() {
        GiftSelection.Result result = GiftSelection.resolve("rosa guerreiro", CATALOG);
        assertTrue(result.ok());
        assertEquals(List.of("rosa guerreiro"), result.names());
    }

    @Test
    void mixedNumbersAndNamesResolve() {
        GiftSelection.Result result = GiftSelection.resolve("2,Rosa", CATALOG);
        assertTrue(result.ok());
        assertEquals(List.of("Dino", "Rosa"), result.names());
    }

    @Test
    void duplicatesAreRemoved() {
        GiftSelection.Result result = GiftSelection.resolve("1,1,Rosa", CATALOG);
        assertTrue(result.ok());
        assertEquals(List.of("Rosa"), result.names());
    }

    @Test
    void outOfRangeIndexRejectsWholeSelectionWithReason() {
        GiftSelection.Result result = GiftSelection.resolve("1,4", CATALOG);
        assertFalse(result.ok());
        assertTrue(result.error().contains("4"), result.error());
        assertTrue(result.names().isEmpty());
    }

    @Test
    void zeroIndexIsOutOfRange() {
        GiftSelection.Result result = GiftSelection.resolve("0", CATALOG);
        assertFalse(result.ok());
        assertTrue(result.error().contains("1-3"), result.error());
    }

    @Test
    void emptyCatalogRejectsNumericToken_withRetryGuidance() {
        GiftSelection.Result result = GiftSelection.resolve("1", List.of());
        assertFalse(result.ok());
        assertTrue(result.error().contains(":gift"), result.error());
        assertTrue(result.names().isEmpty());
    }

    @Test
    void emptyCatalogStillAcceptsNames() {
        GiftSelection.Result result = GiftSelection.resolve("Dino", List.of());
        assertTrue(result.ok());
        assertEquals(List.of("Dino"), result.names());
    }

    @Test
    void blankSelectionIsRejected() {
        GiftSelection.Result result = GiftSelection.resolve(" , ,", CATALOG);
        assertFalse(result.ok());
        assertTrue(result.error().contains("nenhum presente"), result.error());
        assertTrue(result.names().isEmpty());
    }

    @Test
    void whitespaceIsTrimmed() {
        GiftSelection.Result result = GiftSelection.resolve("  2 ,  1  ", CATALOG);
        assertTrue(result.ok());
        assertEquals(List.of("Dino", "Rosa"), result.names());
    }
}
