package inbox

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const Schema = `CREATE TABLE IF NOT EXISTS inbox (
	consumer     text        NOT NULL,
	message_id   uuid        NOT NULL,
	processed_at timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (consumer, message_id)
);`

func Once(ctx context.Context, pool *pgxpool.Pool, consumer, messageID string, sideEffects func(tx pgx.Tx) error) (processed bool, err error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	insertResult, err := tx.Exec(ctx,
		`INSERT INTO inbox(consumer, message_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, consumer, messageID)
	if err != nil {
		return false, err
	}
	if insertResult.RowsAffected() == 0 {
		return false, nil
	}
	if err := sideEffects(tx); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
