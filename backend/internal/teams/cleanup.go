package teams

import (
	"database/sql"
	"errors"
	"log"
)

// rollback ignores only the expected result after a transaction has committed.
func rollback(tx *sql.Tx) {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		log.Printf("[teams] rollback failed: %v", err)
	}
}
func closeRows(rows *sql.Rows) {
	if err := rows.Close(); err != nil {
		log.Printf("[teams] close rows: %v", err)
	}
}
