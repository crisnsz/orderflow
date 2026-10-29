package grpc

import (
	"context"
	"errors"

	inventoryv1 "github.com/crisnsz/orderflow/gen/inventory/v1"
	"github.com/crisnsz/orderflow/internal/platform/telemetry"
	"github.com/crisnsz/orderflow/services/inventory/internal/domain"
	"github.com/crisnsz/orderflow/services/inventory/internal/listing"
	"github.com/crisnsz/orderflow/services/inventory/internal/releasing"
	"github.com/crisnsz/orderflow/services/inventory/internal/reserving"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	inventoryv1.UnimplementedInventoryServiceServer
	catalog   *listing.Service
	reserving *reserving.Service
	releasing *releasing.Service
}

func NewServer(catalog *listing.Service, reserving *reserving.Service, releasing *releasing.Service) *Server {
	return &Server{catalog: catalog, reserving: reserving, releasing: releasing}
}

func (s *Server) ListProducts(ctx context.Context, _ *inventoryv1.ListProductsRequest) (*inventoryv1.ListProductsResponse, error) {
	products, err := s.catalog.ListProducts(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "list products failed")
	}
	response := &inventoryv1.ListProductsResponse{}
	for _, product := range products {
		response.Products = append(response.Products, &inventoryv1.Product{
			Sku: product.SKU, Name: product.Name, PriceCents: product.PriceCents, Stock: product.Stock})
	}
	return response, nil
}

func (s *Server) Reserve(ctx context.Context, request *inventoryv1.ReserveRequest) (*inventoryv1.ReserveResponse, error) {
	if request.GetOrderId() == "" {
		return nil, status.Error(codes.InvalidArgument, "order_id is required")
	}
	ctx = telemetry.WithOrderID(ctx, request.GetOrderId())
	lines := make([]domain.Line, 0, len(request.GetLines()))
	for _, requestedLine := range request.GetLines() {
		lines = append(lines, domain.Line{SKU: requestedLine.GetSku(), Quantity: requestedLine.GetQuantity()})
	}
	reservation, err := s.reserving.Reserve(ctx, request.GetOrderId(), lines)
	if err != nil {
		return nil, toGRPCError(err)
	}
	response := &inventoryv1.ReserveResponse{TotalCents: reservation.Total()}
	for _, line := range reservation.Lines {
		response.Lines = append(response.Lines, &inventoryv1.PricedLine{
			Sku: line.SKU, Quantity: line.Quantity, UnitPriceCents: line.UnitPriceCents})
	}
	return response, nil
}

func (s *Server) Release(ctx context.Context, request *inventoryv1.ReleaseRequest) (*inventoryv1.ReleaseResponse, error) {
	if request.GetOrderId() == "" {
		return nil, status.Error(codes.InvalidArgument, "order_id is required")
	}
	ctx = telemetry.WithOrderID(ctx, request.GetOrderId())
	released, err := s.releasing.Release(ctx, request.GetOrderId())
	if err != nil {
		return nil, status.Error(codes.Internal, "release failed")
	}
	return &inventoryv1.ReleaseResponse{Released: released}, nil
}

func toGRPCError(err error) error {
	switch {
	case errors.Is(err, domain.ErrInsufficientStock), errors.Is(err, domain.ErrAlreadyReleased):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, domain.ErrUnknownProduct):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, domain.ErrInvalidQuantity), errors.Is(err, domain.ErrEmptyOrder):
		return status.Error(codes.InvalidArgument, err.Error())
	default:
		return status.Error(codes.Internal, "reserve failed")
	}
}
