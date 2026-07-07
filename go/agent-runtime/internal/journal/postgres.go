package journal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
)

// Postgres stores events in the journal table from migrations/0001_journal.sql. The primary
// key (job_id, seq) makes a racing second writer fail instead of interleaving.
type Postgres struct {
	db *sql.DB
}

func OpenPostgres(ctx context.Context, dsn string) (*Postgres, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return &Postgres{db: db}, nil
}

func (p *Postgres) Close() error { return p.db.Close() }

func (p *Postgres) Append(ctx context.Context, ev Event) (Event, error) {
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}
	payload := []byte(ev.Payload)
	if payload == nil {
		payload = []byte("null")
	}
	err := p.db.QueryRowContext(ctx, `
		INSERT INTO journal (job_id, seq, kind, step_id, attempt, idem_key, payload, at)
		VALUES ($1, COALESCE((SELECT max(seq) FROM journal WHERE job_id = $1), 0) + 1, $2, $3, $4, $5, $6, $7)
		RETURNING seq`,
		ev.JobID, string(ev.Kind), ev.StepID, ev.Attempt, ev.Key, payload, ev.At).Scan(&ev.Seq)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ev, ErrConflict
	}
	return ev, err
}

func (p *Postgres) Load(ctx context.Context, jobID string) ([]Event, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT seq, kind, step_id, attempt, idem_key, payload, at
		FROM journal WHERE job_id = $1 ORDER BY seq`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		ev := Event{JobID: jobID}
		var kind string
		var payload []byte
		if err := rows.Scan(&ev.Seq, &kind, &ev.StepID, &ev.Attempt, &ev.Key, &payload, &ev.At); err != nil {
			return nil, err
		}
		ev.Kind = Kind(kind)
		if string(payload) != "null" {
			ev.Payload = payload
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

func (p *Postgres) Jobs(ctx context.Context) ([]string, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT DISTINCT job_id FROM journal ORDER BY job_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing jobs: %w", err)
	}
	return out, nil
}
