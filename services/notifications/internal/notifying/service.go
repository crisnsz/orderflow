package notifying

import "github.com/crisnsz/orderflow/services/notifications/internal/domain"

const Consumer = "notifications"

type Service struct {
	store   Store
	senders map[domain.Channel]Sender
}

func NewService(store Store, senders ...Sender) *Service {
	sendersByChannel := map[domain.Channel]Sender{}
	for _, sender := range senders {
		sendersByChannel[sender.Channel()] = sender
	}
	return &Service{store: store, senders: sendersByChannel}
}
