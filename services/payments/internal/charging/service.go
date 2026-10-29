package charging

import "time"

const Consumer = "payments"

type Service struct {
	store Store
	bank  Bank
	now   func() time.Time
}

func NewService(store Store, bank Bank) *Service {
	return &Service{store: store, bank: bank, now: func() time.Time { return time.Now().UTC() }}
}
