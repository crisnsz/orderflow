package notifying

import (
	"context"
	"errors"

	"github.com/crisnsz/orderflow/services/notifications/internal/domain"
)

var ErrPermanent = errors.New("permanent failure")

type Sender interface {
	Channel() domain.Channel
	Send(ctx context.Context, notification domain.Notification) error
}

type Tx interface {
	RecordSent(ctx context.Context, messageID string, notification domain.Notification) error
}

type Store interface {
	Once(ctx context.Context, consumer, messageID string, work func(Tx) error) (bool, error)
	Preferences(ctx context.Context, userID string) (domain.Preferences, error)
}
