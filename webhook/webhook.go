// Package webhook checks the signature of Reedapt webhook deliveries.
//
// Every delivery carries a Reedapt-Signature header, "t=<unix seconds>,v1=<hex>",
// where v1 is the HMAC-SHA256 of "<t>.<raw body>" under the endpoint's whsec_
// secret. Check it against the raw request body, exactly as it arrived.
//
//	body, _ := io.ReadAll(r.Body)
//	event, err := webhook.ConstructEvent(body, r.Header.Get("Reedapt-Signature"), secret)
package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	reedapt "github.com/Reedapt/reedapt-go"
)

// SignatureHeader is the header that carries a delivery's signature.
const SignatureHeader = "Reedapt-Signature"

// DefaultTolerance is the oldest signature accepted.
const DefaultTolerance = 300 * time.Second

// Event types. New ones may be added; handle the ones you know and ignore the rest.
const (
	DubCompleted   = "dub.completed"
	DubFailed      = "dub.failed"
	DubChecked     = "dub.checked"
	BatchCompleted = "batch.completed"
	Test           = "webhook.test"
)

// VerificationError means a delivery's signature didn't match, or its timestamp was too old.
type VerificationError struct{ Message string }

func (e *VerificationError) Error() string { return e.Message }

// Option changes how a delivery is checked.
type Option func(*options)

type options struct{ tolerance time.Duration }

// WithTolerance sets the oldest signature accepted. It defaults to 5 minutes.
func WithTolerance(d time.Duration) Option { return func(o *options) { o.tolerance = d } }

var now = time.Now

// Verify checks a delivery's Reedapt-Signature header against the raw request
// body. It returns a *VerificationError if the header is missing or malformed,
// the signature is older than the tolerance, or it doesn't match.
func Verify(payload []byte, header, secret string, opts ...Option) error {
	o := options{tolerance: DefaultTolerance}
	for _, opt := range opts {
		opt(&o)
	}
	if header == "" {
		return &VerificationError{"The Reedapt-Signature header is missing."}
	}
	var timestamp string
	var signatures []string
	for _, item := range strings.Split(header, ",") {
		name, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(name) {
		case "t":
			timestamp = strings.TrimSpace(value)
		case "v1":
			signatures = append(signatures, strings.TrimSpace(value))
		}
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || seconds < 0 || strings.HasPrefix(timestamp, "+") || len(signatures) == 0 {
		return &VerificationError{"The Reedapt-Signature header is malformed."}
	}
	age := now().Sub(time.Unix(seconds, 0))
	if age < 0 {
		age = -age
	}
	if age > o.tolerance {
		return &VerificationError{"The signature is too old. Check your server's clock."}
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "."))
	mac.Write(payload)
	expected := mac.Sum(nil)
	for _, given := range signatures {
		if b, err := hex.DecodeString(given); err == nil && hmac.Equal(b, expected) {
			return nil
		}
	}
	return &VerificationError{"The signature doesn't match. Use the endpoint's secret and the raw body, not re-encoded JSON."}
}

// Event is a delivery's parsed body.
type Event struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	CreatedAt string          `json:"created_at"`
	Data      json.RawMessage `json:"data"`
}

// ConstructEvent verifies a delivery and returns its parsed event.
func ConstructEvent(payload []byte, header, secret string, opts ...Option) (*Event, error) {
	if err := Verify(payload, header, secret, opts...); err != nil {
		return nil, err
	}
	var e Event
	if err := json.Unmarshal(payload, &e); err != nil {
		return nil, err
	}
	return &e, nil
}

var errNoField = errors.New("webhook: the event has no such data")

// Dub returns the dub of a dub.completed, dub.failed or dub.checked event.
func (e *Event) Dub() (*reedapt.Dub, error) {
	var d struct {
		Dub *reedapt.Dub `json:"dub"`
	}
	if err := json.Unmarshal(e.Data, &d); err != nil {
		return nil, err
	}
	if d.Dub == nil {
		return nil, errNoField
	}
	return d.Dub, nil
}

// Quality returns the quality report of a dub.checked event.
func (e *Event) Quality() (*reedapt.Quality, error) {
	var d struct {
		Quality *reedapt.Quality `json:"quality"`
	}
	if err := json.Unmarshal(e.Data, &d); err != nil {
		return nil, err
	}
	if d.Quality == nil {
		return nil, errNoField
	}
	return d.Quality, nil
}

// Batch returns the summary of a batch.completed event.
func (e *Event) Batch() (*reedapt.BatchSummary, error) {
	var d struct {
		Batch *reedapt.BatchSummary `json:"batch"`
	}
	if err := json.Unmarshal(e.Data, &d); err != nil {
		return nil, err
	}
	if d.Batch == nil {
		return nil, errNoField
	}
	return d.Batch, nil
}
