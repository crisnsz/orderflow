package inventory

import (
	"context"
	"fmt"
	"time"

	inventoryv1 "github.com/crisnsz/orderflow/gen/inventory/v1"
	"github.com/crisnsz/orderflow/services/orders/internal/domain"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type Client struct {
	grpcClient inventoryv1.InventoryServiceClient
}

const callTimeout = 5 * time.Second

var _ domain.Inventory = (*Client)(nil)

func Dial(address string) (*Client, *grpc.ClientConn, error) {
	connection, err := grpc.NewClient(address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()))
	if err != nil {
		return nil, nil, err
	}
	return &Client{grpcClient: inventoryv1.NewInventoryServiceClient(connection)}, connection, nil
}

func (c *Client) Reserve(ctx context.Context, orderID string, items []domain.Item) ([]domain.Item, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	request := &inventoryv1.ReserveRequest{OrderId: orderID}
	for _, item := range items {
		request.Lines = append(request.Lines, &inventoryv1.Line{Sku: item.SKU, Quantity: item.Quantity})
	}
	response, err := c.grpcClient.Reserve(ctx, request)
	if err != nil {
		switch status.Code(err) {
		case codes.FailedPrecondition, codes.NotFound, codes.InvalidArgument:
			return nil, fmt.Errorf("%w: %s", domain.ErrRejected, status.Convert(err).Message())
		}
		return nil, err
	}
	pricedItems := make([]domain.Item, 0, len(response.GetLines()))
	for _, line := range response.GetLines() {
		pricedItems = append(pricedItems, domain.Item{SKU: line.GetSku(), Quantity: line.GetQuantity(), UnitPriceCents: line.GetUnitPriceCents()})
	}
	return pricedItems, nil
}

func (c *Client) Release(ctx context.Context, orderID string) error {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	_, err := c.grpcClient.Release(ctx, &inventoryv1.ReleaseRequest{OrderId: orderID})
	return err
}
