package timeline

import (
	"encoding/json"
	"log/slog"
	"sync"
	"time"
)

const subscriberBufferSize = 64

type Event struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	OrderID string          `json:"order_id"`
	UserID  string          `json:"user_id"`
	At      time.Time       `json:"at"`
	Data    json.RawMessage `json:"data"`
}

type Hub struct {
	mu                 sync.Mutex
	eventsByOrder      map[string][]Event
	seenMessageIDs     map[string]map[string]bool
	orderInsertionList []string
	subscribers        map[string]map[chan Event]struct{}
	maxTrackedOrders   int
}

func NewHub(maxTrackedOrders int) *Hub {
	return &Hub{
		eventsByOrder:    map[string][]Event{},
		seenMessageIDs:   map[string]map[string]bool{},
		subscribers:      map[string]map[chan Event]struct{}{},
		maxTrackedOrders: maxTrackedOrders,
	}
}

func (h *Hub) Publish(event Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.trackOrder(event.OrderID)
	if h.seenMessageIDs[event.OrderID][event.ID] {
		return
	}
	h.seenMessageIDs[event.OrderID][event.ID] = true
	h.eventsByOrder[event.OrderID] = append(h.eventsByOrder[event.OrderID], event)
	for subscriber := range h.subscribers[event.OrderID] {
		select {
		case subscriber <- event:
		default:
			slog.Warn("sse subscriber too slow; event dropped", "order_id", event.OrderID, "type", event.Type)
		}
	}
}

func (h *Hub) trackOrder(orderID string) {
	if h.seenMessageIDs[orderID] != nil {
		return
	}
	h.seenMessageIDs[orderID] = map[string]bool{}
	h.orderInsertionList = append(h.orderInsertionList, orderID)
	if len(h.orderInsertionList) > h.maxTrackedOrders {
		evictedOrderID := h.orderInsertionList[0]
		h.orderInsertionList = h.orderInsertionList[1:]
		delete(h.eventsByOrder, evictedOrderID)
		delete(h.seenMessageIDs, evictedOrderID)
	}
}

func (h *Hub) Subscribe(orderID string) (replay []Event, live <-chan Event, cancel func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	replay = append([]Event(nil), h.eventsByOrder[orderID]...)
	subscriber := make(chan Event, subscriberBufferSize)
	if h.subscribers[orderID] == nil {
		h.subscribers[orderID] = map[chan Event]struct{}{}
	}
	h.subscribers[orderID][subscriber] = struct{}{}
	return replay, subscriber, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.subscribers[orderID], subscriber)
		if len(h.subscribers[orderID]) == 0 {
			delete(h.subscribers, orderID)
		}
	}
}
