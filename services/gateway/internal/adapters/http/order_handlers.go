package http

import (
	"log/slog"
	nethttp "net/http"

	"github.com/crisnsz/orderflow/internal/platform/telemetry"
	"github.com/crisnsz/orderflow/services/gateway/internal/domain"
	"github.com/crisnsz/orderflow/services/gateway/internal/storefront"
)

const maxItemsPerOrder = 20

func (s *Server) products(w nethttp.ResponseWriter, r *nethttp.Request, _ domain.User) {
	products, err := s.Catalog.ListProducts(r.Context())
	if err != nil {
		writeUpstreamError(w, r, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, products)
}

func (s *Server) placeOrder(w nethttp.ResponseWriter, r *nethttp.Request, user domain.User) {
	var request struct {
		Items []storefront.Item `json:"items"`
	}
	if !decodeJSONBody(w, r, &request) {
		return
	}
	if len(request.Items) == 0 || len(request.Items) > maxItemsPerOrder {
		writeError(w, nethttp.StatusBadRequest, "between 1 and 20 items are required")
		return
	}
	order, err := s.Orders.PlaceOrder(r.Context(), user.ID, request.Items)
	if err != nil {
		writeUpstreamError(w, r, err)
		return
	}
	slog.InfoContext(telemetry.WithOrderID(r.Context(), order.ID), "order placed through gateway", "user_id", user.ID, "state", order.State)
	w.Header().Set("Location", "/api/orders/"+order.ID)
	writeJSON(w, nethttp.StatusCreated, order)
}

func (s *Server) listOrders(w nethttp.ResponseWriter, r *nethttp.Request, user domain.User) {
	orders, err := s.Orders.ListOrders(r.Context(), user.ID)
	if err != nil {
		writeUpstreamError(w, r, err)
		return
	}
	writeJSON(w, nethttp.StatusOK, orders)
}

func (s *Server) ownedOrder(w nethttp.ResponseWriter, r *nethttp.Request, user domain.User) (storefront.Order, bool) {
	order, err := s.Orders.GetOrder(r.Context(), r.PathValue("id"))
	if err == nil && order.UserID != user.ID {
		err = storefront.ErrNotFound
	}
	if err != nil {
		writeUpstreamError(w, r, err)
		return storefront.Order{}, false
	}
	return order, true
}

func (s *Server) getOrder(w nethttp.ResponseWriter, r *nethttp.Request, user domain.User) {
	if order, ok := s.ownedOrder(w, r, user); ok {
		writeJSON(w, nethttp.StatusOK, order)
	}
}
