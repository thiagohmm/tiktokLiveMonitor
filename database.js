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
});

function addFalsePositive(comment, category) {
    return new Promise((resolve, reject) => {
        const stmt = db.prepare("INSERT INTO false_positives (comment, category) VALUES (?, ?)");
        stmt.run(comment, category, function(err) {
            if (err) reject(err);
            else resolve(this.lastID);
        });
        stmt.finalize();
    });
}

function getRecentFalsePositives(limit = 10) {
    return new Promise((resolve, reject) => {
        db.all("SELECT comment, category FROM false_positives ORDER BY timestamp DESC LIMIT ?", [limit], (err, rows) => {
            if (err) reject(err);
            else resolve(rows);
        });
    });
}

module.exports = {
    addFalsePositive,
    getRecentFalsePositives
};
