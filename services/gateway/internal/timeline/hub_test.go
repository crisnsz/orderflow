package timeline_test

import (
	"testing"
	"time"

	"github.com/crisnsz/orderflow/services/gateway/internal/timeline"
)

func eventOf(orderID, messageID string) timeline.Event {
	return timeline.Event{ID: messageID, Type: "t", OrderID: orderID}
}

func TestLateSubscriberGetsReplayThenLive(t *testing.T) {
	hub := timeline.NewHub(10)
	hub.Publish(eventOf("o1", "m1"))
	hub.Publish(eventOf("o1", "m2"))

	replay, live, cancel := hub.Subscribe("o1")
	defer cancel()
	if len(replay) != 2 {
		t.Fatalf("replay = %d events, want 2", len(replay))
	}
	hub.Publish(eventOf("o1", "m3"))
	select {
	case event := <-live:
		if event.ID != "m3" {
			t.Errorf("live event = %s, want m3", event.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("no live event")
	}
}

func TestSubscribersOnlySeeTheirOrder(t *testing.T) {
	hub := timeline.NewHub(10)
	_, live, cancel := hub.Subscribe("o1")
	defer cancel()
	hub.Publish(eventOf("o2", "m1"))
	select {
	case event := <-live:
		t.Fatalf("received event of another order: %+v", event)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestRedeliveredMessagesAreDropped(t *testing.T) {
	hub := timeline.NewHub(10)
	hub.Publish(eventOf("o1", "m1"))
	hub.Publish(eventOf("o1", "m1"))
	if replay, _, cancel := hub.Subscribe("o1"); len(replay) != 1 {
		t.Fatalf("replay = %d, want 1", len(replay))
	} else {
		cancel()
	}
}

func TestOldestTimelinesAreEvicted(t *testing.T) {
	hub := timeline.NewHub(2)
	hub.Publish(eventOf("o1", "m"))
	hub.Publish(eventOf("o2", "m"))
	hub.Publish(eventOf("o3", "m"))
	if replay, _, cancel := hub.Subscribe("o1"); len(replay) != 0 {
		t.Fatalf("o1 should have been evicted, replay = %d", len(replay))
	} else {
		cancel()
	}
}
