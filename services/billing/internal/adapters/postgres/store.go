package postgres

import (
	"context"
	"embed"
	"errors"

	"github.com/crisnsz/orderflow/internal/platform/database"
	"github.com/crisnsz/orderflow/internal/platform/events"
	"github.com/crisnsz/orderflow/internal/platform/inbox"
	"github.com/crisnsz/orderflow/internal/platform/outbox"
	"github.com/crisnsz/orderflow/services/billing/internal/domain"
	"github.com/crisnsz/orderflow/services/billing/internal/invoicing"
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

var _ invoicing.Store = (*Store)(nil)

func (s *Store) Once(ctx context.Context, consumer, messageID string, work func(invoicing.Tx) error) (bool, error) {
	return inbox.Once(ctx, s.pool, consumer, messageID, func(tx pgx.Tx) error { return work(&billingTx{tx: tx}) })
}

type billingTx struct{ tx pgx.Tx }

func (bt *billingTx) SaveInvoice(ctx context.Context, invoice domain.Invoice) (string, bool, error) {
	var number string
	err := bt.tx.QueryRow(ctx, `
		INSERT INTO invoices(number, order_id, user_id, currency, net_cents, tax_cents, total_cents, issued_at)
		VALUES('INV-' || nextval('invoice_number_seq'), $1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (order_id) DO NOTHING RETURNING number`,
		invoice.OrderID, invoice.UserID, invoice.Currency, invoice.NetCents, invoice.TaxCents, invoice.TotalCents, invoice.IssuedAt).Scan(&number)
	if errors.Is(err, pgx.ErrNoRows) {
		err = bt.tx.QueryRow(ctx, `SELECT number FROM invoices WHERE order_id=$1`, invoice.OrderID).Scan(&number)
		return number, false, err
	}
	if err != nil {
		return "", false, err
	}
	for _, line := range invoice.Lines {
		if _, err := bt.tx.Exec(ctx, `INSERT INTO invoice_lines(invoice_number, sku, quantity, unit_price_cents) VALUES($1,$2,$3,$4)`,
			number, line.SKU, line.Quantity, line.UnitPriceCents); err != nil {
			return "", false, err
		}
	}
	return number, true, nil
}

func (bt *billingTx) Outbox(ctx context.Context, message events.Message) error {
	return outbox.Insert(ctx, bt.tx, message)
}
