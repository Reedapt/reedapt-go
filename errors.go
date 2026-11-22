package reedapt

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// FieldProblem names a request field the API found invalid.
type FieldProblem struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

type errorBody struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	RequestID string         `json:"request_id"`
	Fields    []FieldProblem `json:"fields"`
}

// Error is what the API answered when it refused a request. Branch on Code,
// not on Message.
//
//	var apiErr *reedapt.Error
//	if errors.As(err, &apiErr) && apiErr.Code == "TOO_MANY_DUBS_RUNNING" { ... }
type Error struct {
	// Status is the HTTP status code.
	Status int
	// Code is a stable identifier such as NOT_FOUND. It is HTTP_<status> when
	// the answer carried none.
	Code string
	// Message is a plain-English description.
	Message string
	// RequestID is the id to quote to support.
	RequestID string
	// Fields lists the invalid fields of a 422.
	Fields []FieldProblem
	// Header holds the response headers.
	Header http.Header
}

func newError(status int, body *errorBody, header http.Header) *Error {
	e := &Error{Status: status, Header: header}
	if body != nil {
		e.Code, e.Message, e.RequestID, e.Fields = body.Code, body.Message, body.RequestID, body.Fields
	}
	if e.Code == "" {
		e.Code = "HTTP_" + strconv.Itoa(status)
	}
	if e.Message == "" {
		e.Message = fmt.Sprintf("The API answered %d.", status)
	}
	if e.RequestID == "" && header != nil {
		e.RequestID = header.Get("Request-Id")
	}
	return e
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// RetryAfter returns how long the API asked you to wait, from the Retry-After
// header. ok is false when it sent none.
func (e *Error) RetryAfter() (d time.Duration, ok bool) {
	if e.Header == nil {
		return 0, false
	}
	secs, err := strconv.ParseFloat(e.Header.Get("Retry-After"), 64)
	if err != nil || secs < 0 {
		return 0, false
	}
	return time.Duration(secs * float64(time.Second)), true
}

func asError(err error, match func(*Error) bool) bool {
	var e *Error
	return errors.As(err, &e) && match(e)
}

// IsAuthentication reports whether err is a 401: the key is missing, wrong or revoked.
func IsAuthentication(err error) bool {
	return asError(err, func(e *Error) bool { return e.Status == 401 })
}

// IsNotFound reports whether err is a 404: nothing with this id belongs to the account.
func IsNotFound(err error) bool { return asError(err, func(e *Error) bool { return e.Status == 404 }) }

// IsConflict reports whether err is a 409: the object's state doesn't allow this.
func IsConflict(err error) bool { return asError(err, func(e *Error) bool { return e.Status == 409 }) }

// IsInvalidRequest reports whether err is a 400, 411, 413 or 422: fix the request before sending it again.
func IsInvalidRequest(err error) bool {
	return asError(err, func(e *Error) bool {
		return e.Status == 400 || e.Status == 411 || e.Status == 413 || e.Status == 422
	})
}

// IsRateLimit reports whether err is a 429: too many requests, or a limit such as dubs running at once.
func IsRateLimit(err error) bool { return asError(err, func(e *Error) bool { return e.Status == 429 }) }

// IsServer reports whether err is a 5xx: something went wrong on Reedapt's side.
func IsServer(err error) bool { return asError(err, func(e *Error) bool { return e.Status >= 500 }) }

// ConnectionError means the request never got an answer: the network failed
// or it timed out. If the caller's context ended, the context's error is
// returned instead.
type ConnectionError struct {
	// Timeout is true when the request took longer than the client's timeout.
	Timeout bool
	Message string
	Err     error
}

func (e *ConnectionError) Error() string { return e.Message }
func (e *ConnectionError) Unwrap() error { return e.Err }

// UploadError means a part could not be sent to storage, or the file could not be read.
type UploadError struct{ Message string }

func (e *UploadError) Error() string { return e.Message }

// WaitError means a WaitUntil... helper ran out of time, or the thing it waited on failed.
type WaitError struct{ Message string }

func (e *WaitError) Error() string { return e.Message }

// ErrNoAPIKey is returned by every request of a client made without an API key.
var ErrNoAPIKey = errors.New("reedapt: no API key. Pass one to NewClient or set the REEDAPT_API_KEY environment variable")
