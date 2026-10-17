package com.tlm.cli.infrastructure.sqlite;

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
import java.util.List;

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
                statement.execute("PRAGMA busy_timeout=5000");
                statement.execute("PRAGMA foreign_keys=ON");
            }
            for (String ddl : SCHEMA) {
                try (Statement statement = connection.createStatement()) {
                    statement.execute(ddl);
                }
            }
        } catch (SQLException e) {
            throw new MonitorException("falha ao abrir o banco local: " + e.getMessage(), e);
        }
    }

    @Override
    public synchronized void recordChat(String liveName, String uniqueId, String nickname, String message,
                                        Instant at) throws MonitorException {
        long live = liveId(liveName);
        try (PreparedStatement ps = connection.prepareStatement(
                "INSERT INTO user_messages (live_id, unique_id, nickname, message, timestamp) VALUES (?, ?, ?, ?, ?)")) {
            ps.setLong(1, live);
            ps.setString(2, safe(uniqueId));
            ps.setString(3, safe(nickname));
            ps.setString(4, message == null ? "" : message);
            ps.setString(5, stamp(at));
            ps.executeUpdate();
        } catch (SQLException e) {
            throw new MonitorException("gravar mensagem: " + e.getMessage(), e);
        }
    }

    @Override
    public synchronized void recordGift(String liveName, String uniqueId, String nickname, String giftName,
                                        int count, Instant at) throws MonitorException {
        long live = liveId(liveName);
        try (PreparedStatement check = connection.prepareStatement(
                "INSERT INTO gifts (live_id, unique_id, nickname, gift_name, repeat_count, timestamp) VALUES (?, ?, ?, ?, ?, ?)")) {
            check.setLong(1, live);
            check.setString(2, safe(uniqueId));
            check.setString(3, safe(nickname));
            check.setString(4, giftName == null || giftName.isBlank() ? "um presente" : giftName);
            check.setInt(5, Math.max(1, count));
            check.setString(6, stamp(at));
            check.executeUpdate();
        } catch (SQLException e) {
            throw new MonitorException("gravar presente: " + e.getMessage(), e);
        }
    }

    @Override
    public synchronized void recordShare(String liveName, String uniqueId, String nickname, Instant at)
            throws MonitorException {
        long live = liveId(liveName);
        try (PreparedStatement ps = connection.prepareStatement(
                "INSERT INTO shares (live_id, unique_id, nickname, timestamp) VALUES (?, ?, ?, ?)")) {
            ps.setLong(1, live);
            ps.setString(2, safe(uniqueId));
            ps.setString(3, safe(nickname));
            ps.setString(4, stamp(at));
            ps.executeUpdate();
        } catch (SQLException e) {
            throw new MonitorException("gravar compartilhamento: " + e.getMessage(), e);
        }
    }

    @Override
    public synchronized void recordLike(String liveName, String uniqueId, String nickname, int likeCount,
                                        Integer totalLikes, Instant at) throws MonitorException {
        long live = liveId(liveName);
        try (PreparedStatement ps = connection.prepareStatement(
                "INSERT INTO likes (live_id, unique_id, nickname, like_count, timestamp) VALUES (?, ?, ?, ?, ?)")) {
            ps.setLong(1, live);
            ps.setString(2, safe(uniqueId));
            ps.setString(3, safe(nickname));
            ps.setInt(4, Math.max(1, likeCount));
            ps.setString(5, stamp(at));
            ps.executeUpdate();
        } catch (SQLException e) {
            throw new MonitorException("gravar curtida: " + e.getMessage(), e);
        }
        if (totalLikes != null && totalLikes >= 0) {
            try (PreparedStatement ps = connection.prepareStatement(
                    "INSERT INTO room_like_totals (live_id, total, updated_at) VALUES (?, ?, ?) "
                            + "ON CONFLICT(live_id) DO UPDATE SET "
                            + "total = MAX(room_like_totals.total, excluded.total), updated_at = excluded.updated_at")) {
                ps.setLong(1, live);
                ps.setLong(2, totalLikes);
                ps.setString(3, stamp(at));
                ps.executeUpdate();
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
        if (!pin.isEmpty()) {
            try (PreparedStatement ps = connection.prepareStatement(
                    "SELECT 1 FROM pinned_comments WHERE live_id = ? AND pin_id = ?")) {
                ps.setLong(1, live);
                ps.setString(2, pin);
                try (ResultSet rs = ps.executeQuery()) {
                    if (rs.next()) {
                        return false;
                    }
                }
            } catch (SQLException e) {
                throw new MonitorException("consultar fixado: " + e.getMessage(), e);
            }
        }
        try (PreparedStatement ps = connection.prepareStatement(
                "INSERT INTO pinned_comments (live_id, live_name, unique_id, nickname, comment, pin_id, is_follower, timestamp)"
                        + " VALUES (?, ?, ?, ?, ?, ?, ?, ?)")) {
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
            ps.executeUpdate();
        } catch (SQLException e) {
            throw new MonitorException("gravar fixado: " + e.getMessage(), e);
        }
        return true;
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
    public synchronized void close() {
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
        try {
            try (PreparedStatement ps = connection.prepareStatement("SELECT id FROM lives WHERE live_name = ?")) {
                ps.setString(1, name);
                try (ResultSet rs = ps.executeQuery()) {
                    if (rs.next()) {
                        return rs.getLong(1);
                    }
                }
            }
            String now = stampNow();
            try (PreparedStatement ps = connection.prepareStatement(
                    "INSERT INTO lives (live_name, started_at, last_seen_at) VALUES (?, ?, ?)",
                    Statement.RETURN_GENERATED_KEYS)) {
                ps.setString(1, name);
                ps.setString(2, now);
                ps.setString(3, now);
                ps.executeUpdate();
                try (ResultSet keys = ps.getGeneratedKeys()) {
                    if (keys.next()) {
                        return keys.getLong(1);
                    }
                }
            }
            try (Statement statement = connection.createStatement();
                 ResultSet rs = statement.executeQuery("SELECT last_insert_rowid()")) {
                return rs.getLong(1);
            }
        } catch (SQLException e) {
            throw new MonitorException("sessão da live: " + e.getMessage(), e);
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
