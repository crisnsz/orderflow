package channels

import (
	"context"
	"log/slog"

	"github.com/crisnsz/orderflow/services/notifications/internal/domain"
	"github.com/crisnsz/orderflow/services/notifications/internal/notifying"
)

type Log struct{ channel domain.Channel }

var _ notifying.Sender = Log{}

func New(channel domain.Channel) Log { return Log{channel: channel} }

func All() []notifying.Sender {
	return []notifying.Sender{New(domain.Email), New(domain.SMS), New(domain.Push)}
}

func (l Log) Channel() domain.Channel { return l.channel }

func (l Log) Send(ctx context.Context, notification domain.Notification) error {
	slog.InfoContext(ctx, "notification sent (simulated)",
		"channel", string(l.channel), "to", notification.Destination, "subject", notification.Subject, "body", notification.Body)
	return nil
}
