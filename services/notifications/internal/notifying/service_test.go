package notifying_test

import (
	"context"
	"errors"
	"testing"

	"github.com/crisnsz/orderflow/internal/platform/events"
	"github.com/crisnsz/orderflow/services/notifications/internal/domain"
	"github.com/crisnsz/orderflow/services/notifications/internal/notifying"
)

type inMemoryStore struct {
	preferences        domain.Preferences
	sent               []domain.Notification
	processedMessageID map[string]bool
}
type inMemoryTx struct{ store *inMemoryStore }

func (tx inMemoryTx) RecordSent(_ context.Context, _ string, notification domain.Notification) error {
	tx.store.sent = append(tx.store.sent, notification)
	return nil
}
func (s *inMemoryStore) Preferences(context.Context, string) (domain.Preferences, error) {
	return s.preferences, nil
}
func (s *inMemoryStore) Once(_ context.Context, consumer, messageID string, work func(notifying.Tx) error) (bool, error) {
	if s.processedMessageID[consumer+messageID] {
		return false, nil
	}
	sentBefore := len(s.sent)
	if err := work(inMemoryTx{s}); err != nil {
		s.sent = s.sent[:sentBefore]
		return false, err
	}
	s.processedMessageID[consumer+messageID] = true
	return true, nil
}

type fakeSender struct {
	channel domain.Channel
	err     error
	calls   int
}

func (f *fakeSender) Channel() domain.Channel { return f.channel }
func (f *fakeSender) Send(context.Context, domain.Notification) error {
	f.calls++
	return f.err
}

func paymentSucceeded(t *testing.T) events.Message {
	message, err := events.New(events.PaymentSucceeded, events.Payload{OrderID: "o1", UserID: "u1", AmountCents: 100})
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func emailAndSMSPreferences() domain.Preferences {
	return domain.Preferences{Channels: map[domain.Channel]string{domain.Email: "a", domain.SMS: "b"}}
}

func TestNotifiesEveryEnabledChannelOnce(t *testing.T) {
	store := &inMemoryStore{processedMessageID: map[string]bool{}, preferences: emailAndSMSPreferences()}
	email, sms := &fakeSender{channel: domain.Email}, &fakeSender{channel: domain.SMS}
	service := notifying.NewService(store, email, sms, &fakeSender{channel: domain.Push})
	message := paymentSucceeded(t)
	for i := 0; i < 2; i++ {
		if err := service.HandleEvent(context.Background(), message); err != nil {
			t.Fatal(err)
		}
	}
	if email.calls != 1 || sms.calls != 1 || len(store.sent) != 2 {
		t.Errorf("email=%d sms=%d recorded=%d, want 1/1/2", email.calls, sms.calls, len(store.sent))
	}
}

func TestChannelFailureFailsTheMessageAndRollsBack(t *testing.T) {
	store := &inMemoryStore{processedMessageID: map[string]bool{}, preferences: emailAndSMSPreferences()}
	service := notifying.NewService(store, &fakeSender{channel: domain.Email}, &fakeSender{channel: domain.SMS, err: errors.New("provider down")})
	if err := service.HandleEvent(context.Background(), paymentSucceeded(t)); err == nil {
		t.Fatal("want error so the message is retried")
	}
	if len(store.sent) != 0 {
		t.Errorf("delivery log kept %d rows from a rolled-back attempt", len(store.sent))
	}
}
