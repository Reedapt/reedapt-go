// Package reedapt is the official Go library for the Reedapt API.
//
// Upload video or audio, dub it into other languages in each speaker's own
// voice, and get the files back.
//
//	client := reedapt.NewClient(os.Getenv("REEDAPT_API_KEY"))
//	languages, err := client.Languages.List(ctx)
//
// Every method takes a context.Context first and returns an error. Failed
// requests return a *reedapt.Error; use errors.As to read its Code, Status and
// RequestID. Webhook signatures are checked by the webhook package.
package reedapt

// Version is the version of this library. It is sent in the User-Agent header.
const Version = "0.1.0"

// DefaultBaseURL is where requests go unless WithBaseURL says otherwise.
const DefaultBaseURL = "https://api.reedapt.com/v1"

// String returns a pointer to s, for optional fields.
func String(s string) *string { return &s }

// Bool returns a pointer to b, for optional fields.
func Bool(b bool) *bool { return &b }

// Int returns a pointer to n, for optional fields.
func Int(n int) *int { return &n }
