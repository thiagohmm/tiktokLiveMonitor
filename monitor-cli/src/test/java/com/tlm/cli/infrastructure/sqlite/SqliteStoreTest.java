package com.tlm.cli.infrastructure.sqlite;

import com.tlm.cli.domain.model.GiftEntry;
import com.tlm.cli.domain.model.PinnedCommentEntry;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.nio.file.Path;
import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.ResultSet;
import java.sql.Statement;
import java.time.Instant;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

class SqliteStoreTest {

    @TempDir
    Path dir;

    private SqliteStore store;
    private Path file;

    @BeforeEach
    void open() throws Exception {
        file = dir.resolve("test.db");
        store = new SqliteStore(file);
    }

    @AfterEach
    void close() {
        store.close();
    }

    @Test
    void createsDatabaseFileAndParentDirectories() throws Exception {
        Path nested = dir.resolve("sub/deeper/local.db");
        try (SqliteStore other = new SqliteStore(nested)) {
            assertTrue(java.nio.file.Files.exists(nested));
        }
    }

    @Test
    void recordsEventsWithoutError() throws Exception {
        Instant at = Instant.parse("2026-10-17T01:00:00Z");
        store.recordChat("alice", "u1", "Nick", "oi", at);
        store.recordGift("alice", "u1", "Nick", "Rosa", 3, at);
        store.recordShare("alice", "u2", "Bob", at);
        store.recordLike("alice", "u3", "Cris", 5, 100, at);
    }

    @Test
    void pinsDedupeByPinIdWithinTheSameLive() throws Exception {
        Instant at = Instant.parse("2026-10-17T01:00:00Z");
        assertTrue(store.recordPinned("alice", "u1", "Nick", "primeiro", "p1", true, at));
        assertFalse(store.recordPinned("alice", "u1", "Nick", "repetido", "p1", true, at.plusSeconds(5)));
        // pin_id vazio não deduplica
        assertTrue(store.recordPinned("alice", "u1", "Nick", "sem chave", "", null, at));
        assertTrue(store.recordPinned("alice", "u1", "Nick", "sem chave de novo", "", null, at));
    }

    @Test
    void recentPinnedFiltersByLiveOrdersDescAndLimits() throws Exception {
        Instant t1 = Instant.parse("2026-10-17T01:00:00Z");
        Instant t2 = Instant.parse("2026-10-17T02:00:00Z");
        Instant t3 = Instant.parse("2026-10-17T03:00:00Z");
        store.recordPinned("alice", "u1", "Nick", "a-t1", "a1", null, t1);
        store.recordPinned("bob", "u2", "Bob", "b-t2", "b1", null, t2);
        store.recordPinned("alice", "u1", "Nick", "a-t3", "a3", null, t3);

        List<PinnedCommentEntry> alice = store.recentPinned("alice", 10);
        assertEquals(List.of("a-t3", "a-t1"), alice.stream().map(PinnedCommentEntry::comment).toList());

        List<PinnedCommentEntry> all = store.recentPinned("", 10);
        assertEquals(List.of("a-t3", "b-t2", "a-t1"), all.stream().map(PinnedCommentEntry::comment).toList());

        List<PinnedCommentEntry> limited = store.recentPinned("", 2);
        assertEquals(2, limited.size());
        assertEquals("a-t3", limited.get(0).comment());
    }

    @Test
    void recentGiftsFiltersByLiveOrdersDescAndLimits() throws Exception {
        Instant t1 = Instant.parse("2026-10-17T01:00:00Z");
        Instant t2 = Instant.parse("2026-10-17T02:00:00Z");
        Instant t3 = Instant.parse("2026-10-17T03:00:00Z");
        store.recordGift("alice", "u1", "Nick", "Rosa", 1, t1);
        store.recordGift("bob", "u2", "Bob", "Dino", 2, t2);
        store.recordGift("alice", "u1", "Ana", "Fenix", 3, t3);

        List<GiftEntry> alice = store.recentGifts("alice", 10);
        assertEquals(List.of("Fenix", "Rosa"), alice.stream().map(GiftEntry::giftName).toList());
        assertEquals(3, alice.get(0).count());
        assertEquals("Ana", alice.get(0).nickname());

        assertTrue(store.recentGifts("", 10).isEmpty());
        assertTrue(store.recentGifts("ninguem", 10).isEmpty());

        List<GiftEntry> limited = store.recentGifts("alice", 1);
        assertEquals(1, limited.size());
        assertEquals("Fenix", limited.get(0).giftName());
    }

    @Test
    void likeTotalNeverGoesBackwards() throws Exception {
        Instant at = Instant.parse("2026-10-17T01:00:00Z");
        store.recordLike("alice", "u1", "Nick", 1, 100, at);
        store.recordLike("alice", "u1", "Nick", 2, 90, at.plusSeconds(1));
        store.recordLike("alice", "u1", "Nick", 3, 130, at.plusSeconds(2));
        assertEquals(130, roomTotal());
    }

    @Test
    void blankLiveNameFallsBackToPlaceholderSession() throws Exception {
        store.recordChat("", "u1", "Nick", "sem live", Instant.now());
        List<PinnedCommentEntry> empty = store.recentPinned("", 10);
        assertTrue(empty.isEmpty());
    }

    private long roomTotal() throws Exception {
        try (Connection connection = DriverManager.getConnection("jdbc:sqlite:" + file.toAbsolutePath());
             Statement statement = connection.createStatement();
             ResultSet rs = statement.executeQuery("SELECT total FROM room_like_totals")) {
            assertTrue(rs.next());
            return rs.getLong(1);
        }
    }
}
