const sqlite3 = require('sqlite3').verbose();
const path = require('path');

const dbPath = path.join(__dirname, 'feedback.db');
const db = new sqlite3.Database(dbPath);

// Inicializa a tabela
db.serialize(() => {
    db.run(`
        CREATE TABLE IF NOT EXISTS false_positives (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            comment TEXT NOT NULL,
            category TEXT NOT NULL,
            expected TEXT DEFAULT 'NAO',
            timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
        )
    `);

    db.run(`
        CREATE TABLE IF NOT EXISTS anomaly_logs (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            live_name TEXT NOT NULL,
            day DATE NOT NULL,
            timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
            uniqueId TEXT,
            comment TEXT NOT NULL,
            is_anomaly BOOLEAN,
            category TEXT
        )
    `);
});

function addFalsePositive(comment, category) {
    return addFeedback(comment, category, 'NAO');
}

function addFeedback(comment, category, expected) {
    return new Promise((resolve, reject) => {
        const stmt = db.prepare("INSERT INTO false_positives (comment, category, expected) VALUES (?, ?, ?)");
        stmt.run(comment, category, expected || 'NAO', function(err) {
            if (err) reject(err);
            else resolve(this.lastID);
        });
        stmt.finalize();
    });
}

function logAnomaly(liveName, comment, isAnomaly, category, uniqueId) {
//...
    return new Promise((resolve, reject) => {
        const now = new Date();
        const day = now.toISOString().split('T')[0];
        const stmt = db.prepare(`
            INSERT INTO anomaly_logs (live_name, day, uniqueId, comment, is_anomaly, category)
            VALUES (?, ?, ?, ?, ?, ?)
        `);
        stmt.run(liveName, day, uniqueId, comment, isAnomaly ? 1 : 0, category, function(err) {
            if (err) reject(err);
            else resolve(this.lastID);
        });
        stmt.finalize();
    });
}

function cleanupOldAnomalies() {
    return new Promise((resolve, reject) => {
        // Deleta registros onde o 'day' é menor que o dia atual
        db.run("DELETE FROM anomaly_logs WHERE day < date('now', 'localtime')", function(err) {
            if (err) {
                console.error('[Database] Erro ao limpar anomalias antigas:', err);
                reject(err);
            } else {
                if (this.changes > 0) {
                    console.log(`[Database] Limpeza concluída: ${this.changes} registros antigos de anomalias removidos.`);
                }
                resolve(this.changes);
            }
        });
    });
}

function getRecentFeedbacks(limit = 10) {
    return new Promise((resolve, reject) => {
        db.all("SELECT comment, category, expected FROM false_positives ORDER BY timestamp DESC LIMIT ?", [limit], (err, rows) => {
            if (err) reject(err);
            else resolve(rows);
        });
    });
}

module.exports = {
    addFalsePositive,
    addFeedback,
    getRecentFeedbacks,
    logAnomaly,
    cleanupOldAnomalies
};
