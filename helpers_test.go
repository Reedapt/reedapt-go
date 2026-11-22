package reedapt

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type call struct {
	Method string
	Path   string // path and query, without the /v1 prefix
	Header http.Header
	Body   map[string]any
	Bytes  int
}

type recorder struct {
	mu    sync.Mutex
	calls []call
}

func (r *recorder) all() []call {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]call(nil), r.calls...)
}

// server answers every request from handler and records it.
func server(t *testing.T, handler func(c call, w http.ResponseWriter)) (*Client, *recorder) {
	t.Helper()
	rec := &recorder{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		c := call{Method: r.Method, Path: r.URL.RequestURI(), Header: r.Header, Bytes: len(raw)}
		if len(raw) > 0 && r.Header.Get("Content-Type") == "application/json" {
			_ = json.Unmarshal(raw, &c.Body)
		}
		rec.mu.Lock()
		rec.calls = append(rec.calls, c)
		rec.mu.Unlock()
		handler(c, w)
	}))
	t.Cleanup(ts.Close)
	client := NewClient("rk_test_abc", WithBaseURL(ts.URL+"/v1"), WithMaxRetries(2))
	client.backoff = func(int) time.Duration { return time.Millisecond }
	return client, rec
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code string, headers ...string) {
	for i := 0; i+1 < len(headers); i += 2 {
		w.Header().Set(headers[i], headers[i+1])
	}
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": code, "message": code + " happened", "request_id": "req_test"}})
}

var dubJSON = map[string]any{"id": "job_1", "status": "queued", "progress": 0, "audio": false, "test": true, "created_at": "x"}
