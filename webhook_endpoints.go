package reedapt

import (
	"context"
	"net/url"
	"strconv"
)

// WebhookEndpointsService manages where Reedapt sends events.
type WebhookEndpointsService struct{ c *Client }

// Create adds an endpoint. The answer's Secret is shown only this once.
func (s *WebhookEndpointsService) Create(ctx context.Context, params *CreateWebhookEndpointParams, opts ...RequestOption) (*WebhookEndpoint, error) {
	var out WebhookEndpoint
	// Not retried after a network failure: it could add the endpoint twice.
	err := s.c.do(ctx, &request{method: "POST", path: "/webhook_endpoints", body: params, retry: retryUnsafe}, opts, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// List gets the account's endpoints.
func (s *WebhookEndpointsService) List(ctx context.Context, opts ...RequestOption) (*WebhookEndpointList, error) {
	var out WebhookEndpointList
	err := s.c.do(ctx, &request{method: "GET", path: "/webhook_endpoints", retry: retrySafe}, opts, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Delete removes an endpoint.
func (s *WebhookEndpointsService) Delete(ctx context.Context, endpointID string, opts ...RequestOption) error {
	return s.c.do(ctx, &request{method: "DELETE", path: "/webhook_endpoints/" + escape(endpointID), retry: retrySafe}, opts, nil)
}

// SendTest sends a webhook.test event to the endpoint.
func (s *WebhookEndpointsService) SendTest(ctx context.Context, endpointID string, opts ...RequestOption) error {
	return s.c.do(ctx, &request{method: "POST", path: "/webhook_endpoints/" + escape(endpointID) + "/test", retry: retryUnsafe}, opts, nil)
}

// Deliveries gets recent deliveries to the endpoint, with what it answered. limit 0 uses the API's default.
func (s *WebhookEndpointsService) Deliveries(ctx context.Context, endpointID string, limit int, opts ...RequestOption) (*WebhookDeliveryList, error) {
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var out WebhookDeliveryList
	err := s.c.do(ctx, &request{method: "GET", path: "/webhook_endpoints/" + escape(endpointID) + "/deliveries", query: q, retry: retrySafe}, opts, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ResendDelivery sends a past delivery's event to the endpoint again, now. It
// keeps its event id, so a receiver that skips repeats by event id skips it too.
func (s *WebhookEndpointsService) ResendDelivery(ctx context.Context, endpointID, deliveryID string, opts ...RequestOption) (*WebhookDelivery, error) {
	var out WebhookDelivery
	err := s.c.do(ctx, &request{
		method: "POST",
		path:   "/webhook_endpoints/" + escape(endpointID) + "/deliveries/" + escape(deliveryID) + "/resend",
		retry:  retryUnsafe,
	}, opts, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}
