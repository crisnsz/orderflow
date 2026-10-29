package postgres

import (
	"context"
	"embed"
	"errors"

	"github.com/crisnsz/orderflow/internal/platform/database"
	"github.com/crisnsz/orderflow/internal/platform/events"
	"github.com/crisnsz/orderflow/internal/platform/inbox"
	"github.com/crisnsz/orderflow/internal/platform/outbox"
	"github.com/crisnsz/orderflow/services/payments/internal/charging"
	"github.com/crisnsz/orderflow/services/payments/internal/domain"
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

var _ charging.Store = (*Store)(nil)

func (s *Store) Once(ctx context.Context, consumer, messageID string, work func(charging.Tx) error) (bool, error) {
	return inbox.Once(ctx, s.pool, consumer, messageID, func(tx pgx.Tx) error { return work(&paymentTx{tx: tx}) })
}

func (s *Store) CardFor(ctx context.Context, userID string) (*domain.Card, error) {
	var card domain.Card
	err := s.pool.QueryRow(ctx,
		`SELECT user_id, brand, last4, token, autopay_consent, exp_month, exp_year FROM cards WHERE user_id=$1`, userID).
		Scan(&card.UserID, &card.Brand, &card.Last4, &card.Token, &card.AutopayConsent, &card.ExpiryMonth, &card.ExpiryYear)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &card, err
}

type paymentTx struct{ tx pgx.Tx }

func (pt *paymentTx) SavePayment(ctx context.Context, payment domain.Payment) error {
	_, err := pt.tx.Exec(ctx,
		`INSERT INTO payments(order_id, user_id, invoice_id, amount_cents, status, auth_ref, sale_ref, reason)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8)
		 ON CONFLICT (order_id) DO UPDATE SET status=EXCLUDED.status, auth_ref=EXCLUDED.auth_ref,
		   sale_ref=EXCLUDED.sale_ref, reason=EXCLUDED.reason`,
		payment.OrderID, payment.UserID, payment.InvoiceID, payment.AmountCents,
		payment.Status, payment.AuthorizationRef, payment.SaleRef, payment.Reason)
	return err
}

func (pt *paymentTx) Outbox(ctx context.Context, message events.Message) error {
	return outbox.Insert(ctx, pt.tx, message)
}
