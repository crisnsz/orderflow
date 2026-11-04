package http_test

import (
	"context"
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	httpadapter "github.com/crisnsz/orderflow/services/gateway/internal/adapters/http"
	"github.com/crisnsz/orderflow/services/gateway/internal/domain"
	"github.com/crisnsz/orderflow/services/gateway/internal/storefront"
)

type fakeOrders struct{ placed storefront.Order }

func (f fakeOrders) PlaceOrder(context.Context, string, []storefront.Item) (storefront.Order, error) {
	return f.placed, nil
}
func (f fakeOrders) GetOrder(context.Context, string) (storefront.Order, error) { return f.placed, nil }
func (f fakeOrders) ListOrders(context.Context, string) ([]storefront.Order, error) {
	return []storefront.Order{f.placed}, nil
}

func placeOrderRequest(t *testing.T, placed storefront.Order) *httptest.ResponseRecorder {
	t.Helper()
	signer, err := domain.NewSigner([]byte("0123456789abcdef"), time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := httpadapter.NewHandler(httpadapter.Deps{Signer: signer, Orders: fakeOrders{placed: placed}})
	request := httptest.NewRequest(nethttp.MethodPost, "/api/orders", strings.NewReader(`{"items":[{"sku":"MON-01","quantity":5}]}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&nethttp.Cookie{Name: "orderflow_session", Value: signer.Issue("u-alice")})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestPlaceOrderCancelledAtReservationIsCreatedWithStateReasonAndLocation(t *testing.T) {
	placed := storefront.Order{ID: "o-1", UserID: "u-alice", State: "CANCELLED", CancelReason: "inventory rejected the order: insufficient stock"}
	recorder := placeOrderRequest(t, placed)

	if recorder.Code != nethttp.StatusCreated {
		t.Fatalf("status = %d, want 201", recorder.Code)
	}
	if location := recorder.Header().Get("Location"); location != "/api/orders/o-1" {
		t.Errorf("Location = %q, want /api/orders/o-1", location)
	}
	var body storefront.Order
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.State != "CANCELLED" || body.CancelReason == "" {
		t.Errorf("body state=%q reason=%q, want CANCELLED with a reason", body.State, body.CancelReason)
	}
}
