package grpc

import (
	"context"
	"time"

	inventoryv1 "github.com/crisnsz/orderflow/gen/inventory/v1"
	ordersv1 "github.com/crisnsz/orderflow/gen/orders/v1"
	"github.com/crisnsz/orderflow/services/gateway/internal/storefront"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

const (
	placeOrderTimeout = 15 * time.Second
	queryTimeout      = 5 * time.Second
)

func dial(address string) (*grpc.ClientConn, error) {
	return grpc.NewClient(address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()))
}

func toStorefrontError(err error) error {
	switch status.Code(err) {
	case codes.NotFound:
		return storefront.ErrNotFound
	case codes.InvalidArgument:
		return storefront.ErrInvalid
	}
	return err
}

type Orders struct{ client ordersv1.OrdersServiceClient }

var _ storefront.Orders = (*Orders)(nil)

func DialOrders(address string) (*Orders, *grpc.ClientConn, error) {
	connection, err := dial(address)
	if err != nil {
		return nil, nil, err
	}
	return &Orders{client: ordersv1.NewOrdersServiceClient(connection)}, connection, nil
}

func fromProtoOrder(protoOrder *ordersv1.Order) storefront.Order {
	order := storefront.Order{
		ID: protoOrder.GetId(), UserID: protoOrder.GetUserId(), State: protoOrder.GetState(),
		TotalCents: protoOrder.GetTotalCents(), CancelReason: protoOrder.GetCancelReason(),
		CreatedAt: protoOrder.GetCreatedAt(), Items: []storefront.Item{}, History: []storefront.Transition{}}
	for _, item := range protoOrder.GetItems() {
		order.Items = append(order.Items, storefront.Item{
			SKU: item.GetSku(), Quantity: item.GetQuantity(), UnitPriceCents: item.GetUnitPriceCents()})
	}
	for _, transition := range protoOrder.GetHistory() {
		order.History = append(order.History, storefront.Transition{
			From: transition.GetFrom(), To: transition.GetTo(), Trigger: transition.GetTrigger(), At: transition.GetAt()})
	}
	return order
}

func (o *Orders) PlaceOrder(ctx context.Context, userID string, items []storefront.Item) (storefront.Order, error) {
	ctx, cancel := context.WithTimeout(ctx, placeOrderTimeout)
	defer cancel()
	request := &ordersv1.PlaceOrderRequest{UserId: userID}
	for _, item := range items {
		request.Items = append(request.Items, &ordersv1.Item{Sku: item.SKU, Quantity: item.Quantity})
	}
	response, err := o.client.PlaceOrder(ctx, request)
	if err != nil {
		return storefront.Order{}, toStorefrontError(err)
	}
	return fromProtoOrder(response), nil
}

func (o *Orders) GetOrder(ctx context.Context, orderID string) (storefront.Order, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	response, err := o.client.GetOrder(ctx, &ordersv1.GetOrderRequest{Id: orderID})
	if err != nil {
		return storefront.Order{}, toStorefrontError(err)
	}
	return fromProtoOrder(response), nil
}

func (o *Orders) ListOrders(ctx context.Context, userID string) ([]storefront.Order, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	response, err := o.client.ListOrders(ctx, &ordersv1.ListOrdersRequest{UserId: userID})
	if err != nil {
		return nil, toStorefrontError(err)
	}
	orders := []storefront.Order{}
	for _, protoOrder := range response.GetOrders() {
		orders = append(orders, fromProtoOrder(protoOrder))
	}
	return orders, nil
}

type Catalog struct {
	client inventoryv1.InventoryServiceClient
}

var _ storefront.Catalog = (*Catalog)(nil)

func DialCatalog(address string) (*Catalog, *grpc.ClientConn, error) {
	connection, err := dial(address)
	if err != nil {
		return nil, nil, err
	}
	return &Catalog{client: inventoryv1.NewInventoryServiceClient(connection)}, connection, nil
}

func (c *Catalog) ListProducts(ctx context.Context) ([]storefront.Product, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	response, err := c.client.ListProducts(ctx, &inventoryv1.ListProductsRequest{})
	if err != nil {
		return nil, err
	}
	products := []storefront.Product{}
	for _, protoProduct := range response.GetProducts() {
		products = append(products, storefront.Product{
			SKU: protoProduct.GetSku(), Name: protoProduct.GetName(),
			PriceCents: protoProduct.GetPriceCents(), Stock: protoProduct.GetStock()})
	}
	return products, nil
}
