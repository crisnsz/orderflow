package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/crisnsz/orderflow/services/gateway/internal/storefront"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const requestTimeout = 5 * time.Second

type Client struct {
	baseURL    string
	httpClient *http.Client
}

var _ storefront.Payments = (*Client)(nil)

func New(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout:   requestTimeout,
			Transport: otelhttp.NewTransport(http.DefaultTransport)},
	}
}

func (c *Client) do(ctx context.Context, method, path string, requestBody, responseBody any) error {
	bodyReader := bytes.NewReader(nil)
	if requestBody != nil {
		encoded, _ := json.Marshal(requestBody)
		bodyReader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusBadRequest {
		return storefront.ErrInvalid
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("payments admin %s %s: status %d", method, path, response.StatusCode)
	}
	return json.NewDecoder(response.Body).Decode(responseBody)
}

func (c *Client) Cards(ctx context.Context, userID string) ([]storefront.Card, error) {
	var cards []storefront.Card
	err := c.do(ctx, http.MethodGet, "/cards?user_id="+url.QueryEscape(userID), nil, &cards)
	if cards == nil {
		cards = []storefront.Card{}
	}
	return cards, err
}

type failureStageBody struct {
	Stage string `json:"stage"`
}

func (c *Client) FailureStage(ctx context.Context) (string, error) {
	var body failureStageBody
	err := c.do(ctx, http.MethodGet, "/admin/failure", nil, &body)
	return body.Stage, err
}

func (c *Client) SetFailureStage(ctx context.Context, stage string) error {
	var body failureStageBody
	return c.do(ctx, http.MethodPut, "/admin/failure", failureStageBody{Stage: stage}, &body)
}
