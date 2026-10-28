package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/red-copilot/harness/core"
)

func eventDigest(previous string, event core.Event) (string, error) {
	copy := event
	copy.Seq = 0
	data, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte(previous+"\n"), data...))
	return hex.EncodeToString(sum[:]), nil
}

func auditHead(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (string, error) {
	var head string
	err := q.QueryRowContext(ctx, `SELECT hash FROM events ORDER BY seq DESC LIMIT 1`).Scan(&head)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return head, err
}

func appendHashedEventTx(ctx context.Context, tx *sql.Tx, event core.Event) (core.Event, error) {
	previous, err := auditHead(ctx, tx)
	if err != nil {
		return core.Event{}, fmt.Errorf("read audit head: %w", err)
	}
	hash, err := eventDigest(previous, event)
	if err != nil {
		return core.Event{}, fmt.Errorf("hash event: %w", err)
	}
	inserted, err := tx.ExecContext(ctx, `INSERT INTO events(run_id,session_id,source,type,time,data,raw,schema_version,prev_hash,hash) VALUES(?,?,?,?,?,?,?,?,?,?)`, event.RunID, event.SessionID, event.Source, event.Type, formatTime(event.Time), nullableJSON(event.Data), nullableJSON(event.Raw), event.SchemaVersion, previous, hash)
	if err != nil {
		return core.Event{}, err
	}
	event.Seq, err = inserted.LastInsertId()
	return event, err
}

// VerifyEventChain checks the append-only canonical event hash chain.
func (s *Store) VerifyEventChain(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT seq,schema_version,run_id,session_id,source,type,time,data,raw,prev_hash,hash FROM events ORDER BY seq`)
	if err != nil {
		return err
	}
	defer rows.Close()
	previous := ""
	for rows.Next() {
		var event core.Event
		var tm, storedPrev, storedHash string
		var data, raw []byte
		if err := rows.Scan(&event.Seq, &event.SchemaVersion, &event.RunID, &event.SessionID, &event.Source, &event.Type, &tm, &data, &raw, &storedPrev, &storedHash); err != nil {
			return err
		}
		event.Time = parseTime(tm)
		event.Data = data
		event.Raw = raw
		if storedPrev != previous {
			return fmt.Errorf("event %d previous hash mismatch", event.Seq)
		}
		expected, err := eventDigest(storedPrev, event)
		if err != nil {
			return err
		}
		if storedHash != expected {
			return fmt.Errorf("event %d hash mismatch", event.Seq)
		}
		previous = storedHash
	}
	return rows.Err()
}
