package postgres

import (
	"context"
	"embed"
	"errors"

	"github.com/crisnsz/orderflow/internal/platform/database"
	"github.com/crisnsz/orderflow/internal/platform/events"
	"github.com/crisnsz/orderflow/internal/platform/inbox"
	"github.com/crisnsz/orderflow/internal/platform/outbox"
	"github.com/crisnsz/orderflow/services/orders/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	return database.Migrate(ctx, pool, migrations, "migrations")
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ domain.Repository = (*Store)(nil)

func (s *Store) InTx(ctx context.Context, work func(domain.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := work(&orderTx{tx: tx}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) Once(ctx context.Context, consumer, messageID string, work func(domain.Tx) error) (bool, error) {
	return inbox.Once(ctx, s.pool, consumer, messageID, func(tx pgx.Tx) error { return work(&orderTx{tx: tx}) })
}

func (s *Store) Get(ctx context.Context, orderID string) (*domain.Order, error) {
	return loadOrder(ctx, s.pool, orderID, false)
}

func (s *Store) ListByUser(ctx context.Context, userID string, limit int) ([]*domain.Order, error) {
	orderIDs, err := s.latestOrderIDs(ctx, userID, limit)
	if err != nil {
		return nil, err
	}
	var orders []*domain.Order
	for _, orderID := range orderIDs {
		order, err := loadOrder(ctx, s.pool, orderID, false)
		if err != nil {
			return nil, err
		}
		orders = append(orders, order)
	}
	return orders, nil
}

func (s *Store) latestOrderIDs(ctx context.Context, userID string, limit int) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT id FROM orders WHERE user_id=$1 ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	var orderIDs []string
	for rows.Next() {
		var orderID string
		if err := rows.Scan(&orderID); err != nil {
			rows.Close()
			return nil, err
		}
		orderIDs = append(orderIDs, orderID)
	}
	rows.Close()
	return orderIDs, nil
}

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func loadOrder(ctx context.Context, db querier, orderID string, lockRow bool) (*domain.Order, error) {
	order, err := loadOrderRow(ctx, db, orderID, lockRow)
	if err != nil {
		return nil, err
	}
	if err := loadItems(ctx, db, order); err != nil {
		return nil, err
	}
	if err := loadHistory(ctx, db, order); err != nil {
		return nil, err
	}
	order.MarkPersisted()
	return order, nil
}

func loadOrderRow(ctx context.Context, db querier, orderID string, lockRow bool) (*domain.Order, error) {
	query := `SELECT id, user_id, state, total_cents, cancel_reason, created_at FROM orders WHERE id=$1`
	if lockRow {
		query += ` FOR UPDATE`
	}
	var order domain.Order
	err := db.QueryRow(ctx, query, orderID).Scan(
		&order.ID, &order.UserID, &order.State, &order.TotalCents, &order.CancelReason, &order.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &order, nil
}

func loadItems(ctx context.Context, db querier, order *domain.Order) error {
	rows, err := db.Query(ctx, `SELECT sku, quantity, unit_price_cents FROM order_items WHERE order_id=$1 ORDER BY sku`, order.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var item domain.Item
		if err := rows.Scan(&item.SKU, &item.Quantity, &item.UnitPriceCents); err != nil {
			rows.Close()
			return err
		}
		order.Items = append(order.Items, item)
	}
	rows.Close()
	return nil
}

func loadHistory(ctx context.Context, db querier, order *domain.Order) error {
	rows, err := db.Query(ctx, `SELECT from_state, to_state, trigger, at FROM saga_log WHERE order_id=$1 ORDER BY id`, order.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var transition domain.Transition
		if err := rows.Scan(&transition.From, &transition.To, &transition.Trigger, &transition.At); err != nil {
			return err
		}
		order.History = append(order.History, transition)
	}
	return rows.Err()
}

type orderTx struct{ tx pgx.Tx }

func (ot *orderTx) Insert(ctx context.Context, order *domain.Order) error {
	if _, err := ot.tx.Exec(ctx, `INSERT INTO orders(id, user_id, state, total_cents, created_at) VALUES($1,$2,$3,$4,$5)`,
		order.ID, order.UserID, order.State, order.TotalCents, order.CreatedAt); err != nil {
		return err
	}
	return ot.saveItems(ctx, order)
}

func (ot *orderTx) Load(ctx context.Context, orderID string) (*domain.Order, error) {
	return loadOrder(ctx, ot.tx, orderID, true)
}

func (ot *orderTx) Save(ctx context.Context, order *domain.Order) error {
	if _, err := ot.tx.Exec(ctx,
		`UPDATE orders SET state=$2, total_cents=$3, cancel_reason=$4, updated_at=now() WHERE id=$1`,
		order.ID, order.State, order.TotalCents, order.CancelReason); err != nil {
		return err
	}
	if err := ot.saveItems(ctx, order); err != nil {
		return err
	}
	for _, transition := range order.NewTransitions() {
		if _, err := ot.tx.Exec(ctx, `INSERT INTO saga_log(order_id, from_state, to_state, trigger, at) VALUES($1,$2,$3,$4,$5)`,
			order.ID, transition.From, transition.To, transition.Trigger, transition.At); err != nil {
			return err
		}
	}
	order.MarkPersisted()
	return nil
}

func (ot *orderTx) saveItems(ctx context.Context, order *domain.Order) error {
	for _, item := range order.Items {
		if _, err := ot.tx.Exec(ctx,
			`INSERT INTO order_items(order_id, sku, quantity, unit_price_cents) VALUES($1,$2,$3,$4)
			 ON CONFLICT (order_id, sku) DO UPDATE SET quantity=EXCLUDED.quantity, unit_price_cents=EXCLUDED.unit_price_cents`,
			order.ID, item.SKU, item.Quantity, item.UnitPriceCents); err != nil {
			return err
		}
	}
	return nil
}

func (ot *orderTx) Outbox(ctx context.Context, message events.Message) error {
	return outbox.Insert(ctx, ot.tx, message)
}
