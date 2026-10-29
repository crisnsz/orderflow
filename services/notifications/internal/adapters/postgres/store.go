package postgres

import (
	"context"
	"embed"
	"errors"

	"github.com/crisnsz/orderflow/internal/platform/database"
	"github.com/crisnsz/orderflow/internal/platform/inbox"
	"github.com/crisnsz/orderflow/services/notifications/internal/domain"
	"github.com/crisnsz/orderflow/services/notifications/internal/notifying"
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

var _ notifying.Store = (*Store)(nil)

func (s *Store) Once(ctx context.Context, consumer, messageID string, work func(notifying.Tx) error) (bool, error) {
	return inbox.Once(ctx, s.pool, consumer, messageID, func(tx pgx.Tx) error { return work(&notificationTx{tx: tx}) })
}

func (s *Store) Preferences(ctx context.Context, userID string) (domain.Preferences, error) {
	var emailAddress, phoneNumber, pushToken *string
	var emailEnabled, smsEnabled, pushEnabled bool
	err := s.pool.QueryRow(ctx,
		`SELECT email_addr, phone, push_token, email_on, sms_on, push_on FROM preferences WHERE user_id=$1`, userID).
		Scan(&emailAddress, &phoneNumber, &pushToken, &emailEnabled, &smsEnabled, &pushEnabled)
	preferences := domain.Preferences{UserID: userID, Channels: map[domain.Channel]string{}}
	if errors.Is(err, pgx.ErrNoRows) {
		return preferences, nil
	}
	if err != nil {
		return preferences, err
	}
	if emailEnabled && emailAddress != nil {
		preferences.Channels[domain.Email] = *emailAddress
	}
	if smsEnabled && phoneNumber != nil {
		preferences.Channels[domain.SMS] = *phoneNumber
	}
	if pushEnabled && pushToken != nil {
		preferences.Channels[domain.Push] = *pushToken
	}
	return preferences, nil
}

type notificationTx struct{ tx pgx.Tx }

func (nt *notificationTx) RecordSent(ctx context.Context, messageID string, notification domain.Notification) error {
	_, err := nt.tx.Exec(ctx,
		`INSERT INTO notification_log(message_id, order_id, channel, subject) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`,
		messageID, notification.OrderID, string(notification.Channel), notification.Subject)
	return err
}
