package postgres

import (
	"context"
	"embed"
	"errors"
	"sort"

	"github.com/crisnsz/orderflow/internal/platform/database"
	"github.com/crisnsz/orderflow/services/inventory/internal/domain"
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

func (s *Store) ListProducts(ctx context.Context) ([]domain.Product, error) {
	rows, err := s.pool.Query(ctx, `SELECT sku, name, price_cents, stock FROM products ORDER BY sku`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var products []domain.Product
	for rows.Next() {
		var product domain.Product
		if err := rows.Scan(&product.SKU, &product.Name, &product.PriceCents, &product.Stock); err != nil {
			return nil, err
		}
		products = append(products, product)
	}
	return products, rows.Err()
}

func (s *Store) InTx(ctx context.Context, work func(domain.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := work(&inventoryTx{tx: tx}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type inventoryTx struct{ tx pgx.Tx }

func (it *inventoryTx) GetReservation(ctx context.Context, orderID string) (*domain.Reservation, error) {
	var reservation domain.Reservation
	err := it.tx.QueryRow(ctx, `SELECT order_id, status FROM reservations WHERE order_id=$1`, orderID).
		Scan(&reservation.OrderID, &reservation.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := it.tx.Query(ctx, `SELECT sku, quantity, unit_price_cents FROM reservation_lines WHERE order_id=$1 ORDER BY sku`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var line domain.PricedLine
		if err := rows.Scan(&line.SKU, &line.Quantity, &line.UnitPriceCents); err != nil {
			return nil, err
		}
		reservation.Lines = append(reservation.Lines, line)
	}
	return &reservation, rows.Err()
}

func (it *inventoryTx) LockProducts(ctx context.Context, skus []string) (map[string]domain.Product, error) {
	sortedSKUs := append([]string(nil), skus...)
	sort.Strings(sortedSKUs)
	rows, err := it.tx.Query(ctx,
		`SELECT sku, name, price_cents, stock FROM products WHERE sku = ANY($1) ORDER BY sku FOR UPDATE`, sortedSKUs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	productsBySKU := map[string]domain.Product{}
	for rows.Next() {
		var product domain.Product
		if err := rows.Scan(&product.SKU, &product.Name, &product.PriceCents, &product.Stock); err != nil {
			return nil, err
		}
		productsBySKU[product.SKU] = product
	}
	return productsBySKU, rows.Err()
}

func (it *inventoryTx) AdjustStock(ctx context.Context, sku string, delta int32) error {
	_, err := it.tx.Exec(ctx, `UPDATE products SET stock = stock + $2 WHERE sku = $1`, sku, delta)
	return err
}

func (it *inventoryTx) SaveReservation(ctx context.Context, reservation domain.Reservation) error {
	if _, err := it.tx.Exec(ctx, `INSERT INTO reservations(order_id, status) VALUES($1,$2)`, reservation.OrderID, reservation.Status); err != nil {
		return err
	}
	for _, line := range reservation.Lines {
		if _, err := it.tx.Exec(ctx,
			`INSERT INTO reservation_lines(order_id, sku, quantity, unit_price_cents) VALUES($1,$2,$3,$4)`,
			reservation.OrderID, line.SKU, line.Quantity, line.UnitPriceCents); err != nil {
			return err
		}
	}
	return nil
}

func (it *inventoryTx) MarkReleased(ctx context.Context, orderID string) error {
	_, err := it.tx.Exec(ctx, `UPDATE reservations SET status='RELEASED', updated_at=now() WHERE order_id=$1`, orderID)
	return err
}
