package reedapt

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	mrand "math/rand"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Client talks to the Reedapt API. It is safe for concurrent use.
type Client struct {
	// Uploads sends files to Reedapt.
	Uploads *UploadsService
	// Dubs starts, reads and manages dubs.
	Dubs *DubsService
	// Runs answers requests for changes to dubs. Early access.
	Runs *RunsService
	// WebhookEndpoints manages where Reedapt sends events.
	WebhookEndpoints *WebhookEndpointsService
	// Languages lists the languages dubs can be made in.
	Languages *LanguagesService
	// Usage reads minutes used and left.
	Usage *UsageService

	apiKey     string
	baseURL    string
	httpClient *http.Client
	timeout    time.Duration
	maxRetries int
	headers    map[string]string
	backoff    func(attempt int) time.Duration
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL sets the API address. It defaults to the REEDAPT_BASE_URL
// environment variable, then DefaultBaseURL.
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") } }

// WithHTTPClient sets the http.Client requests are sent with.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.httpClient = h } }

// WithTimeout sets how long one request may take, response body included.
// It defaults to 60 seconds. Zero disables it.
func WithTimeout(d time.Duration) Option { return func(c *Client) { c.timeout = d } }

// WithMaxRetries sets how many times a request that failed in a way worth
// retrying is sent again. It defaults to 2. Zero turns retries off.
func WithMaxRetries(n int) Option { return func(c *Client) { c.maxRetries = n } }

// WithDefaultHeaders adds headers to every request. The Authorization header cannot be replaced.
func WithDefaultHeaders(h map[string]string) Option {
	return func(c *Client) {
		for k, v := range h {
			c.headers[k] = v
		}
	}
}

// NewClient makes a client. An empty apiKey falls back to the REEDAPT_API_KEY
// environment variable; with neither, requests return ErrNoAPIKey.
func NewClient(apiKey string, opts ...Option) *Client {
	if apiKey == "" {
		apiKey = os.Getenv("REEDAPT_API_KEY")
	}
	c := &Client{
		apiKey:     apiKey,
		baseURL:    strings.TrimRight(DefaultBaseURL, "/"),
		httpClient: &http.Client{},
		timeout:    60 * time.Second,
		maxRetries: 2,
		headers:    map[string]string{},
		backoff:    defaultBackoff,
	}
	if env := os.Getenv("REEDAPT_BASE_URL"); env != "" {
		c.baseURL = strings.TrimRight(env, "/")
	}
	for _, opt := range opts {
		opt(c)
	}
	c.Uploads = &UploadsService{c}
	c.Dubs = newDubsService(c)
	c.Runs = &RunsService{c}
	c.WebhookEndpoints = &WebhookEndpointsService{c}
	c.Languages = &LanguagesService{c}
	c.Usage = &UsageService{c}
	return c
}

type requestConfig struct {
	idempotencyKey string
	timeout        *time.Duration
	maxRetries     *int
	headers        map[string]string
}

// RequestOption changes one request.
type RequestOption func(*requestConfig)

// WithIdempotencyKey sets the Idempotency-Key header. Creating uploads and
// dubs sends one automatically; pass your own to make a repeat safe across restarts.
func WithIdempotencyKey(key string) RequestOption {
	return func(r *requestConfig) { r.idempotencyKey = key }
}

// WithRequestTimeout overrides the client's timeout for one request.
func WithRequestTimeout(d time.Duration) RequestOption {
	return func(r *requestConfig) { r.timeout = &d }
}

// WithRequestMaxRetries overrides the client's retry count for one request.
func WithRequestMaxRetries(n int) RequestOption { return func(r *requestConfig) { r.maxRetries = &n } }

// WithHeader adds a header to one request.
func WithHeader(key, value string) RequestOption {
	return func(r *requestConfig) {
		if r.headers == nil {
			r.headers = map[string]string{}
		}
		r.headers[key] = value
	}
}

func newConfig(opts []RequestOption) *requestConfig {
	cfg := &requestConfig{}
	for _, o := range opts {
		o(cfg)
	}
	return cfg
}

// without returns the options with the idempotency key dropped, for the
// follow-up calls of a multi-request helper.
func (r *requestConfig) without() []RequestOption {
	copied := *r
	copied.idempotencyKey = ""
	return []RequestOption{func(c *requestConfig) { *c = copied }}
}

// retryKind says whether a request can be sent again after any failure
// without doing the work twice.
type retryKind int

const (
	retrySafe retryKind = iota
	retryUnsafe
)

type request struct {
	method     string
	path       string
	query      url.Values
	body       any
	retry      retryKind
	idempotent bool // send a generated Idempotency-Key
	text       bool // the answer is a file's text, not JSON
}

func defaultBackoff(attempt int) time.Duration {
	ms := math.Min(500*math.Pow(2, float64(attempt)), 8000) * (0.75 + mrand.Float64()*0.25)
	return time.Duration(ms * float64(time.Millisecond))
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// do sends the request, retrying where that is safe, and decodes the answer
// into out (a *string when the answer is text). out may be nil.
func (c *Client) do(ctx context.Context, r *request, opts []RequestOption, out any) error {
	if c.apiKey == "" {
		return ErrNoAPIKey
	}
	cfg := newConfig(opts)
	target := c.baseURL + r.path
	if len(r.query) > 0 {
		target += "?" + r.query.Encode()
	}
	var body []byte
	if r.body != nil {
		var err error
		if body, err = json.Marshal(r.body); err != nil {
			return fmt.Errorf("reedapt: encoding the request: %w", err)
		}
	}
	key := cfg.idempotencyKey
	if key == "" && r.idempotent {
		key = newUUID()
	}
	// A request carrying an idempotency key is safe to send again.
	retry := r.retry
	if key != "" {
		retry = retrySafe
	}
	maxRetries := c.maxRetries
	if cfg.maxRetries != nil {
		maxRetries = *cfg.maxRetries
	}
	timeout := c.timeout
	if cfg.timeout != nil {
		timeout = *cfg.timeout
	}

	for attempt := 0; ; attempt++ {
		status, header, data, err := c.send(ctx, r.method, target, body, key, cfg, timeout)
		if err == nil && status >= 200 && status < 300 {
			return decode(data, r.text, out)
		}
		if err == nil {
			err = newError(status, readError(data), header)
		} else if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempt >= maxRetries || !shouldRetry(err, retry) {
			return err
		}
		delay := c.backoff(attempt)
		var apiErr *Error
		if errors.As(err, &apiErr) {
			if d, ok := apiErr.RetryAfter(); ok && d <= time.Minute {
				delay = d
			}
		}
		if err := sleep(ctx, delay); err != nil {
			return err
		}
	}
}

func (c *Client) send(ctx context.Context, method, target string, body []byte, key string, cfg *requestConfig, timeout time.Duration) (int, http.Header, []byte, error) {
	reqCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(reqCtx, method, target, reader)
	if err != nil {
		return 0, nil, nil, &ConnectionError{Message: fmt.Sprintf("%s %s: %v", method, target, err), Err: err}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "reedapt-go/"+Version)
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	for k, v := range cfg.headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	res, err := c.httpClient.Do(req)
	if err == nil {
		defer res.Body.Close()
		var data []byte
		// Read the body inside the timeout, so a stalled answer also times out.
		if data, err = io.ReadAll(res.Body); err == nil {
			return res.StatusCode, res.Header, data, nil
		}
	}
	path := req.URL.Path
	if ctx.Err() == nil && errors.Is(reqCtx.Err(), context.DeadlineExceeded) {
		return 0, nil, nil, &ConnectionError{
			Timeout: true,
			Message: fmt.Sprintf("%s %s took longer than %s.", method, path, timeout),
			Err:     err,
		}
	}
	return 0, nil, nil, &ConnectionError{Message: fmt.Sprintf("%s %s failed: %v", method, path, err), Err: err}
}

func decode(data []byte, text bool, out any) error {
	if out == nil {
		return nil
	}
	if text {
		if s, ok := out.(*string); ok {
			*s = string(data)
		}
		return nil
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("reedapt: reading the answer: %w", err)
	}
	return nil
}

func readError(data []byte) *errorBody {
	var wrapper struct {
		Error *errorBody `json:"error"`
	}
	if json.Unmarshal(data, &wrapper) != nil {
		return nil
	}
	return wrapper.Error
}

func shouldRetry(err error, retry retryKind) bool {
	var conn *ConnectionError
	if errors.As(err, &conn) {
		return retry == retrySafe
	}
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	switch {
	case e.Status == 429:
		// Other 429s are limits that last minutes or hours, not a busy minute.
		return e.Code == "RATE_LIMITED"
	case e.Status == 503:
		return true
	case retry == retryUnsafe:
		return false
	case e.Status == 409:
		return e.Code == "IDEMPOTENCY_IN_PROGRESS"
	}
	return e.Status == 408 || e.Status == 500 || e.Status == 502 || e.Status == 504
}

func escape(id string) string { return url.PathEscape(id) }
