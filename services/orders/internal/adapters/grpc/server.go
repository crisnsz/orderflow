package grpc

import (
	"context"
	"errors"
	"log/slog"
	"time"

	ordersv1 "github.com/crisnsz/orderflow/gen/orders/v1"
	"github.com/crisnsz/orderflow/internal/platform/telemetry"
	"github.com/crisnsz/orderflow/services/orders/internal/domain"
	"github.com/crisnsz/orderflow/services/orders/internal/fetching"
	"github.com/crisnsz/orderflow/services/orders/internal/placing"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	ordersv1.UnimplementedOrdersServiceServer
	placingOrders  *placing.Service
	fetchingOrders *fetching.Service
}

func NewServer(placingOrders *placing.Service, fetchingOrders *fetching.Service) *Server {
	return &Server{placingOrders: placingOrders, fetchingOrders: fetchingOrders}
}

func (s *Server) PlaceOrder(ctx context.Context, request *ordersv1.PlaceOrderRequest) (*ordersv1.Order, error) {
	items := make([]domain.Item, 0, len(request.GetItems()))
	for _, requestedItem := range request.GetItems() {
		items = append(items, domain.Item{SKU: requestedItem.GetSku(), Quantity: requestedItem.GetQuantity()})
	}
	order, err := s.placingOrders.PlaceOrder(ctx, request.GetUserId(), items)
	if err != nil {
		return nil, toGRPCError(ctx, err)
	}
	return toProtoOrder(order), nil
}

func (s *Server) GetOrder(ctx context.Context, request *ordersv1.GetOrderRequest) (*ordersv1.Order, error) {
	ctx = telemetry.WithOrderID(ctx, request.GetId())
	order, err := s.fetchingOrders.GetOrder(ctx, request.GetId())
	if err != nil {
		return nil, toGRPCError(ctx, err)
	}
	return toProtoOrder(order), nil
}

func (s *Server) ListOrders(ctx context.Context, request *ordersv1.ListOrdersRequest) (*ordersv1.ListOrdersResponse, error) {
	orders, err := s.fetchingOrders.ListOrders(ctx, request.GetUserId())
	if err != nil {
		return nil, toGRPCError(ctx, err)
	}
	response := &ordersv1.ListOrdersResponse{}
	for _, order := range orders {
		response.Orders = append(response.Orders, toProtoOrder(order))
	}
	return response, nil
}

func toGRPCError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, placing.ErrInvalid):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, domain.ErrNotFound):
		return status.Error(codes.NotFound, "order not found")
	default:
		slog.ErrorContext(ctx, "request failed", "error", err)
		return status.Error(codes.Internal, "internal error")
	}
}

func toProtoOrder(order *domain.Order) *ordersv1.Order {
	protoOrder := &ordersv1.Order{
		Id: order.ID, UserId: order.UserID, State: string(order.State), TotalCents: order.TotalCents,
		CancelReason: order.CancelReason, CreatedAt: order.CreatedAt.Format(time.RFC3339),
	}
	for _, item := range order.Items {
		protoOrder.Items = append(protoOrder.Items, &ordersv1.Item{Sku: item.SKU, Quantity: item.Quantity, UnitPriceCents: item.UnitPriceCents})
	}
	for _, transition := range order.History {
		protoOrder.History = append(protoOrder.History, &ordersv1.Transition{
			From: string(transition.From), To: string(transition.To), Trigger: string(transition.Trigger),
			At: transition.At.Format(time.RFC3339Nano)})
	}
	return protoOrder
}
