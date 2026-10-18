package com.tlm.cli.infrastructure.sqlite;

import com.tlm.cli.domain.model.GiftEntry;
import com.tlm.cli.domain.model.MonitorException;
import com.tlm.cli.domain.model.PinnedCommentEntry;
import com.tlm.cli.domain.port.LocalStorePort;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.sql.Statement;
import java.time.Instant;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

/**
 * Persistência local em SQLite (arquivo único, conexão única com lock).
 * Timestamps são gravados como ISO-8601 UTC; {@code pin_id} deduplica os
 * fixados dentro da mesma live (índice único parcial).
 */
public final class SqliteStore implements LocalStorePort {

    private static final String[] SCHEMA = {
            """
            CREATE TABLE IF NOT EXISTS lives (
                id INTEGER PRIMARY KEY,
                live_name TEXT NOT NULL UNIQUE,
                started_at TEXT NOT NULL,
                last_seen_at TEXT NOT NULL
            )""",
            """
            CREATE TABLE IF NOT EXISTS user_messages (
                id INTEGER PRIMARY KEY,
                live_id INTEGER NOT NULL REFERENCES lives(id),
                unique_id TEXT NOT NULL,
                nickname TEXT NOT NULL,
                message TEXT NOT NULL,
                timestamp TEXT NOT NULL
            )""",
            "CREATE INDEX IF NOT EXISTS idx_user_messages_live ON user_messages(live_id, timestamp)",
            """
            CREATE TABLE IF NOT EXISTS gifts (
                id INTEGER PRIMARY KEY,
                live_id INTEGER NOT NULL REFERENCES lives(id),
                unique_id TEXT NOT NULL,
                nickname TEXT NOT NULL,
                gift_name TEXT NOT NULL,
                repeat_count INTEGER NOT NULL DEFAULT 1,
                timestamp TEXT NOT NULL
            )""",
            "CREATE INDEX IF NOT EXISTS idx_gifts_live ON gifts(live_id, timestamp)",
            """
            CREATE TABLE IF NOT EXISTS shares (
                id INTEGER PRIMARY KEY,
                live_id INTEGER NOT NULL REFERENCES lives(id),
                unique_id TEXT NOT NULL,
                nickname TEXT NOT NULL,
                timestamp TEXT NOT NULL
            )""",
            """
            CREATE TABLE IF NOT EXISTS likes (
                id INTEGER PRIMARY KEY,
                live_id INTEGER NOT NULL REFERENCES lives(id),
                unique_id TEXT NOT NULL,
                nickname TEXT NOT NULL,
                like_count INTEGER NOT NULL DEFAULT 1,
                timestamp TEXT NOT NULL
            )""",
            """
            CREATE TABLE IF NOT EXISTS room_like_totals (
                live_id INTEGER PRIMARY KEY REFERENCES lives(id),
                total INTEGER NOT NULL DEFAULT 0,
                updated_at TEXT NOT NULL
            )""",
            """
            CREATE TABLE IF NOT EXISTS pinned_comments (
                id INTEGER PRIMARY KEY,
                live_id INTEGER NOT NULL REFERENCES lives(id),
                live_name TEXT NOT NULL,
                unique_id TEXT NOT NULL,
                nickname TEXT NOT NULL,
                comment TEXT NOT NULL,
                pin_id TEXT,
                is_follower INTEGER,
                timestamp TEXT NOT NULL
            )""",
            "CREATE UNIQUE INDEX IF NOT EXISTS idx_pinned_comments_pin "
                    + "ON pinned_comments(live_id, pin_id) WHERE pin_id IS NOT NULL AND pin_id <> ''",
            "CREATE INDEX IF NOT EXISTS idx_pinned_comments_live_ts ON pinned_comments(live_id, timestamp)",
    };

    private final Connection connection;
    /** Cache nome da live → id; evita um SELECT por evento. */
    private final Map<String, Long> liveIds = new HashMap<>();
    private final PreparedStatement selectLiveId;
    private final PreparedStatement insertLive;
    private final PreparedStatement insertChat;
    private final PreparedStatement insertGift;
    private final PreparedStatement insertShare;
    private final PreparedStatement insertLike;
    private final PreparedStatement upsertLikeTotal;
    private final PreparedStatement insertPinned;
    private final PreparedStatement insertPinnedIgnore;

    public SqliteStore(Path file) throws MonitorException {
        try {
            Path parent = file.toAbsolutePath().getParent();
            if (parent != null) {
                Files.createDirectories(parent);
            }
        } catch (IOException e) {
            throw new MonitorException("não foi possível criar o diretório do banco: " + e.getMessage(), e);
        }
        try {
            this.connection = DriverManager.getConnection("jdbc:sqlite:" + file.toAbsolutePath());
            this.connection.setAutoCommit(true);
            try (Statement statement = connection.createStatement()) {
                statement.execute("PRAGMA journal_mode=WAL");
                // WAL + NORMAL: sem fsync por INSERT; no pior caso (queda de
                // energia) perdem-se os últimos eventos, sem corromper o banco.
                statement.execute("PRAGMA synchronous=NORMAL");
                statement.execute("PRAGMA busy_timeout=5000");
                statement.execute("PRAGMA foreign_keys=ON");
            }
            for (String ddl : SCHEMA) {
                try (Statement statement = connection.createStatement()) {
                    statement.execute(ddl);
                }
            }
            // Statements do caminho quente reutilizados; os métodos são
            // synchronized, então não há uso concorrente.
            selectLiveId = connection.prepareStatement("SELECT id FROM lives WHERE live_name = ?");
            insertLive = connection.prepareStatement(
                    "INSERT INTO lives (live_name, started_at, last_seen_at) VALUES (?, ?, ?)",
                    Statement.RETURN_GENERATED_KEYS);
            insertChat = connection.prepareStatement(
                    "INSERT INTO user_messages (live_id, unique_id, nickname, message, timestamp) VALUES (?, ?, ?, ?, ?)");
            insertGift = connection.prepareStatement(
                    "INSERT INTO gifts (live_id, unique_id, nickname, gift_name, repeat_count, timestamp) VALUES (?, ?, ?, ?, ?, ?)");
            insertShare = connection.prepareStatement(
                    "INSERT INTO shares (live_id, unique_id, nickname, timestamp) VALUES (?, ?, ?, ?)");
            insertLike = connection.prepareStatement(
                    "INSERT INTO likes (live_id, unique_id, nickname, like_count, timestamp) VALUES (?, ?, ?, ?, ?)");
            upsertLikeTotal = connection.prepareStatement(
                    "INSERT INTO room_like_totals (live_id, total, updated_at) VALUES (?, ?, ?) "
                            + "ON CONFLICT(live_id) DO UPDATE SET "
                            + "total = MAX(room_like_totals.total, excluded.total), updated_at = excluded.updated_at");
            insertPinned = connection.prepareStatement(
                    "INSERT INTO pinned_comments (live_id, live_name, unique_id, nickname, comment, pin_id, is_follower, timestamp)"
                            + " VALUES (?, ?, ?, ?, ?, ?, ?, ?)");
            insertPinnedIgnore = connection.prepareStatement(
                    "INSERT OR IGNORE INTO pinned_comments "
                            + "(live_id, live_name, unique_id, nickname, comment, pin_id, is_follower, timestamp)"
                            + " VALUES (?, ?, ?, ?, ?, ?, ?, ?)");
        } catch (SQLException e) {
            throw new MonitorException("falha ao abrir o banco local: " + e.getMessage(), e);
        }
    }

    @Override
    public synchronized void recordChat(String liveName, String uniqueId, String nickname, String message,
                                        Instant at) throws MonitorException {
        long live = liveId(liveName);
        try {
            insertChat.setLong(1, live);
            insertChat.setString(2, safe(uniqueId));
            insertChat.setString(3, safe(nickname));
            insertChat.setString(4, message == null ? "" : message);
            insertChat.setString(5, stamp(at));
            insertChat.executeUpdate();
        } catch (SQLException e) {
            throw new MonitorException("gravar mensagem: " + e.getMessage(), e);
        }
    }

    @Override
    public synchronized void recordGift(String liveName, String uniqueId, String nickname, String giftName,
                                        int count, Instant at) throws MonitorException {
        long live = liveId(liveName);
        try {
            insertGift.setLong(1, live);
            insertGift.setString(2, safe(uniqueId));
            insertGift.setString(3, safe(nickname));
            insertGift.setString(4, giftName == null || giftName.isBlank() ? "um presente" : giftName);
            insertGift.setInt(5, Math.max(1, count));
            insertGift.setString(6, stamp(at));
            insertGift.executeUpdate();
        } catch (SQLException e) {
            throw new MonitorException("gravar presente: " + e.getMessage(), e);
        }
    }

    @Override
    public synchronized void recordShare(String liveName, String uniqueId, String nickname, Instant at)
            throws MonitorException {
        long live = liveId(liveName);
        try {
            insertShare.setLong(1, live);
            insertShare.setString(2, safe(uniqueId));
            insertShare.setString(3, safe(nickname));
            insertShare.setString(4, stamp(at));
            insertShare.executeUpdate();
        } catch (SQLException e) {
            throw new MonitorException("gravar compartilhamento: " + e.getMessage(), e);
        }
    }

    @Override
    public synchronized void recordLike(String liveName, String uniqueId, String nickname, int likeCount,
                                        Integer totalLikes, Instant at) throws MonitorException {
        long live = liveId(liveName);
        try {
            insertLike.setLong(1, live);
            insertLike.setString(2, safe(uniqueId));
            insertLike.setString(3, safe(nickname));
            insertLike.setInt(4, Math.max(1, likeCount));
            insertLike.setString(5, stamp(at));
            insertLike.executeUpdate();
        } catch (SQLException e) {
            throw new MonitorException("gravar curtida: " + e.getMessage(), e);
        }
        if (totalLikes != null && totalLikes >= 0) {
            try {
                upsertLikeTotal.setLong(1, live);
                upsertLikeTotal.setLong(2, totalLikes);
                upsertLikeTotal.setString(3, stamp(at));
                upsertLikeTotal.executeUpdate();
            } catch (SQLException e) {
                throw new MonitorException("gravar total de curtidas: " + e.getMessage(), e);
            }
        }
    }

    @Override
    public synchronized boolean recordPinned(String liveName, String uniqueId, String nickname, String comment,
                                             String pinId, Boolean isFollower, Instant at) throws MonitorException {
        long live = liveId(liveName);
        String pin = pinId == null ? "" : pinId.trim();
        PreparedStatement ps = pin.isEmpty() ? insertPinned : insertPinnedIgnore;
        try {
            ps.setLong(1, live);
            ps.setString(2, safe(liveName));
            ps.setString(3, uniqueId == null || uniqueId.isBlank() ? "unknown" : uniqueId);
            ps.setString(4, nickname == null || nickname.isBlank() ? safe(uniqueId) : nickname);
            ps.setString(5, comment == null || comment.isBlank() ? "[sem texto identificado]" : comment);
            if (pin.isEmpty()) {
                ps.setNull(6, java.sql.Types.VARCHAR);
            } else {
                ps.setString(6, pin);
            }
            if (isFollower == null) {
                ps.setNull(7, java.sql.Types.INTEGER);
            } else {
                ps.setInt(7, isFollower ? 1 : 0);
            }
            ps.setString(8, stamp(at));
            // O índice único parcial (live_id, pin_id) ignora replays; o retorno
            // preserva o contrato: true = fixado novo, false = duplicado.
            return ps.executeUpdate() > 0;
        } catch (SQLException e) {
            throw new MonitorException("gravar fixado: " + e.getMessage(), e);
        }
    }

    @Override
    public synchronized List<PinnedCommentEntry> recentPinned(String liveName, int limit) throws MonitorException {
        int bounded = Math.min(200, Math.max(1, limit));
        boolean byLive = liveName != null && !liveName.isBlank();
        String sql = "SELECT live_name, unique_id, nickname, comment, timestamp FROM pinned_comments "
                + (byLive ? "WHERE live_name = ? " : "")
                + "ORDER BY timestamp DESC, id DESC LIMIT ?";
        List<PinnedCommentEntry> out = new ArrayList<>();
        try (PreparedStatement ps = connection.prepareStatement(sql)) {
            int index = 1;
            if (byLive) {
                ps.setString(index++, liveName.trim());
            }
            ps.setInt(index, bounded);
            try (ResultSet rs = ps.executeQuery()) {
                while (rs.next()) {
                    out.add(new PinnedCommentEntry(rs.getString(1), rs.getString(2), rs.getString(3),
                            rs.getString(4), rs.getString(5)));
                }
            }
        } catch (SQLException e) {
            throw new MonitorException("consultar fixados: " + e.getMessage(), e);
        }
        return out;
    }

    @Override
    public synchronized List<GiftEntry> recentGifts(String liveName, int limit) throws MonitorException {
        if (liveName == null || liveName.isBlank()) {
            return List.of();
        }
        int bounded = Math.min(200, Math.max(1, limit));
        String sql = """
                SELECT g.nickname, g.gift_name, g.repeat_count, g.timestamp
                FROM gifts g
                JOIN lives l ON l.id = g.live_id
                WHERE l.live_name = ?
                ORDER BY g.timestamp DESC, g.id DESC
                LIMIT ?
                """;
        List<GiftEntry> out = new ArrayList<>();
        try (PreparedStatement ps = connection.prepareStatement(sql)) {
            ps.setString(1, liveName.trim());
            ps.setInt(2, bounded);
            try (ResultSet rs = ps.executeQuery()) {
                while (rs.next()) {
                    out.add(new GiftEntry(rs.getString(1), rs.getString(2), rs.getInt(3), rs.getString(4)));
                }
            }
        } catch (SQLException e) {
            throw new MonitorException("consultar presentes: " + e.getMessage(), e);
        }
        return out;
    }

    @Override
    public synchronized void close() {
        for (PreparedStatement statement : List.of(selectLiveId, insertLive, insertChat, insertGift,
                insertShare, insertLike, upsertLikeTotal, insertPinned, insertPinnedIgnore)) {
            try {
                statement.close();
            } catch (SQLException ignored) {
                // Encerramento: fechar o statement não é acionável aqui.
            }
        }
        try {
            connection.close();
        } catch (SQLException ignored) {
            // Encerramento: fechar o arquivo não é acionável aqui.
        }
    }

    private long liveId(String liveName) throws MonitorException {
        String name = safe(liveName);
        if (name.isEmpty()) {
            name = "desconhecida";
        }
        Long cached = liveIds.get(name);
        if (cached != null) {
            return cached;
        }
        try {
            selectLiveId.setString(1, name);
            try (ResultSet rs = selectLiveId.executeQuery()) {
                if (rs.next()) {
                    long id = rs.getLong(1);
                    liveIds.put(name, id);
                    return id;
                }
            }
            String now = stampNow();
            insertLive.setString(1, name);
            insertLive.setString(2, now);
            insertLive.setString(3, now);
            insertLive.executeUpdate();
            long id;
            try (ResultSet keys = insertLive.getGeneratedKeys()) {
                if (keys.next()) {
                    id = keys.getLong(1);
                } else {
                    id = lastInsertRowId();
                }
            }
            liveIds.put(name, id);
            return id;
        } catch (SQLException e) {
            throw new MonitorException("sessão da live: " + e.getMessage(), e);
        }
    }

    private long lastInsertRowId() throws SQLException {
        try (Statement statement = connection.createStatement();
             ResultSet rs = statement.executeQuery("SELECT last_insert_rowid()")) {
            return rs.next() ? rs.getLong(1) : 0L;
        }
    }

    /** Exposto ao teste: 0=OFF, 1=NORMAL, 2=FULL. */
    synchronized int synchronousMode() {
        try (Statement statement = connection.createStatement();
             ResultSet rs = statement.executeQuery("PRAGMA synchronous")) {
            return rs.next() ? rs.getInt(1) : -1;
        } catch (SQLException e) {
            return -1;
        }
    }

    private static String stamp(Instant at) {
        return (at == null ? Instant.now() : at).toString();
    }

    private static String stampNow() {
        return Instant.now().toString();
    }

    private static String safe(String value) {
        return value == null ? "" : value.trim();
    }
}
