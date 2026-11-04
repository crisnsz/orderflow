package inbox

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

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
