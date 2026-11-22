package reedapt

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// onlyReader hides every method but Read, so uploads can't use ReadAt.
type onlyReader struct{ r io.Reader }

func (o onlyReader) Read(p []byte) (int, error) { return o.r.Read(p) }

type fakeUploads struct {
	client  *Client
	rec     *recorder
	storage *httptest.Server
	mu      sync.Mutex
	puts    map[int][]byte
	// storageHook may answer a part's PUT itself; return true if it did.
	storageHook func(n int, w http.ResponseWriter) bool
}

func newFakeUploads(t *testing.T, size, partSize int, firstLinks int) *fakeUploads {
	t.Helper()
	f := &fakeUploads{puts: map[int][]byte{}}
	f.storage = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:])
		if f.storageHook != nil && f.storageHook(n, w) {
			return
		}
		if r.Header.Get("Authorization") != "" {
			t.Errorf("API key sent to storage")
		}
		raw, _ := io.ReadAll(r.Body)
		if r.ContentLength != int64(len(raw)) {
			t.Errorf("content length %d for %d bytes", r.ContentLength, len(raw))
		}
		f.mu.Lock()
		f.puts[n] = raw
		f.mu.Unlock()
		w.Header().Set("ETag", `"etag-`+strconv.Itoa(n)+`"`)
	}))
	t.Cleanup(f.storage.Close)
	partCount := (size + partSize - 1) / partSize
	link := func(n int) map[string]any {
		return map[string]any{"part_number": n, "url": f.storage.URL + "/part/" + strconv.Itoa(n)}
	}
	upload := map[string]any{"id": "job_up", "filename": "talk.mp4", "size": size, "part_size": partSize, "part_count": partCount}
	with := func(status string, extra map[string]any) map[string]any {
		m := map[string]any{"status": status}
		for k, v := range upload {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	f.client, f.rec = server(t, func(c call, w http.ResponseWriter) {
		switch strings.TrimPrefix(c.Path, "/v1") {
		case "/uploads":
			if firstLinks > partCount {
				firstLinks = partCount
			}
			var parts []any
			for i := 1; i <= firstLinks; i++ {
				parts = append(parts, link(i))
			}
			writeJSON(w, 201, with("uploading", map[string]any{"parts": parts}))
		case "/uploads/job_up/parts":
			var parts []any
			for _, n := range c.Body["part_numbers"].([]any) {
				parts = append(parts, link(int(n.(float64))))
			}
			writeJSON(w, 200, map[string]any{"parts": parts})
		case "/uploads/job_up/complete", "/uploads/job_up":
			writeJSON(w, 200, with("uploaded", nil))
		case "/dubs":
			writeJSON(w, 201, map[string]any{"dubs": []any{map[string]any{"id": "job_d"}}})
		default:
			writeError(w, 404, "NOT_FOUND")
		}
	})
	return f
}

func (f *fakeUploads) callsTo(suffix string) []call {
	var out []call
	for _, c := range f.rec.all() {
		if strings.HasSuffix(c.Path, suffix) {
			out = append(out, c)
		}
	}
	return out
}

func data(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + i%26)
	}
	return b
}

func TestUploadsInPartsAndCompletesWithEveryETagInOrder(t *testing.T) {
	for name, wrap := range map[string]func([]byte) io.Reader{
		"ReaderAt":     func(b []byte) io.Reader { return bytes.NewReader(b) },
		"plain reader": func(b []byte) io.Reader { return onlyReader{bytes.NewReader(b)} },
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeUploads(t, 10, 4, 100)
			body := data(10)
			var progress []int64
			var pmu sync.Mutex
			upload, err := f.client.Uploads.UploadFile(ctx, &UploadFileParams{
				Filename: "talk.mp4", Size: 10, Body: wrap(body),
				OnProgress: func(p UploadProgress) { pmu.Lock(); progress = append(progress, p.UploadedBytes); pmu.Unlock() },
			})
			if err != nil || upload.Status != UploadStatusUploaded {
				t.Fatalf("%+v %v", upload, err)
			}
			create := f.rec.all()[0]
			if create.Body["filename"] != "talk.mp4" || create.Body["size"] != 10.0 || create.Header.Get("Idempotency-Key") == "" {
				t.Fatalf("%+v", create)
			}
			var joined []byte
			for n := 1; n <= 3; n++ {
				joined = append(joined, f.puts[n]...)
			}
			if !bytes.Equal(joined, body) || len(f.puts[3]) != 2 {
				t.Fatalf("parts %q", f.puts)
			}
			complete := f.callsTo("/complete")[0]
			want := []any{
				map[string]any{"part_number": 1.0, "etag": `"etag-1"`},
				map[string]any{"part_number": 2.0, "etag": `"etag-2"`},
				map[string]any{"part_number": 3.0, "etag": `"etag-3"`},
			}
			if !reflect.DeepEqual(complete.Body["parts"], want) {
				t.Fatalf("%v", complete.Body)
			}
			if len(progress) != 3 || progress[2] != 10 {
				t.Fatalf("progress %v", progress)
			}
		})
	}
}

func TestUploadPathNamesTheFileAfterThePath(t *testing.T) {
	f := newFakeUploads(t, 6, 4, 100)
	path := filepath.Join(t.TempDir(), "interview.mov")
	if err := os.WriteFile(path, data(6), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.client.Uploads.UploadPath(ctx, path, ""); err != nil {
		t.Fatal(err)
	}
	if got := f.rec.all()[0].Body; got["filename"] != "interview.mov" || got["size"] != 6.0 {
		t.Fatalf("%v", got)
	}
	if string(f.puts[1])+string(f.puts[2]) != string(data(6)) {
		t.Fatal("bytes differ")
	}
}

func TestUploadStartsFromTheReadersCurrentPosition(t *testing.T) {
	f := newFakeUploads(t, 4, 4, 100)
	r := bytes.NewReader(append([]byte("XXXX"), data(4)...))
	_, _ = r.Seek(4, io.SeekStart)
	if _, err := f.client.Uploads.UploadFile(ctx, &UploadFileParams{Filename: "a.mp4", Size: 4, Body: r}); err != nil {
		t.Fatal(err)
	}
	if string(f.puts[1]) != string(data(4)) {
		t.Fatalf("%q", f.puts[1])
	}
}

func TestSignsThePartsTheFirstAnswerDidntIncludeABatchAtATime(t *testing.T) {
	f := newFakeUploads(t, 250, 1, 1)
	if _, err := f.client.Uploads.UploadFile(ctx, &UploadFileParams{Filename: "a.mp4", Size: 250, Body: bytes.NewReader(data(250)), Concurrency: 1}); err != nil {
		t.Fatal(err)
	}
	signs := f.callsTo("/parts")
	if len(signs) != 3 {
		t.Fatalf("%d sign calls", len(signs))
	}
	first := signs[0].Body["part_numbers"].([]any)
	if len(first) != 99 || first[0] != 2.0 || len(signs[1].Body["part_numbers"].([]any)) != 100 || len(signs[2].Body["part_numbers"].([]any)) != 50 {
		t.Fatalf("%v", signs)
	}
}

func TestAnExpiredLinkIsSignedAgainAndThePartResent(t *testing.T) {
	f := newFakeUploads(t, 8, 4, 100)
	var refused int32
	f.storageHook = func(n int, w http.ResponseWriter) bool {
		if n == 2 && atomic.AddInt32(&refused, 1) == 1 {
			w.WriteHeader(403)
			return true
		}
		return false
	}
	if _, err := f.client.Uploads.UploadFile(ctx, &UploadFileParams{Filename: "a.mp4", Size: 8, Body: onlyReader{bytes.NewReader(data(8))}}); err != nil {
		t.Fatal(err)
	}
	resign := f.callsTo("/parts")
	if len(resign) != 1 || !reflect.DeepEqual(resign[0].Body["part_numbers"], []any{2.0}) {
		t.Fatalf("%+v", resign)
	}
	if len(f.puts[2]) != 4 {
		t.Fatal("part 2 never arrived whole")
	}
}

func TestAPartThatKeepsFailingStopsTheUpload(t *testing.T) {
	f := newFakeUploads(t, 8, 4, 100)
	f.storageHook = func(n int, w http.ResponseWriter) bool {
		if n == 1 {
			w.WriteHeader(500)
			return true
		}
		return false
	}
	_, err := f.client.Uploads.UploadFile(ctx, &UploadFileParams{Filename: "a.mp4", Size: 8, Body: bytes.NewReader(data(8)), Concurrency: 1})
	var up *UploadError
	if !errors.As(err, &up) {
		t.Fatalf("got %v", err)
	}
	if len(f.callsTo("/complete")) != 0 {
		t.Fatal("completed anyway")
	}
}

func TestAShortReaderIsAnError(t *testing.T) {
	f := newFakeUploads(t, 8, 4, 100)
	_, err := f.client.Uploads.UploadFile(ctx, &UploadFileParams{Filename: "a.mp4", Size: 8, Body: onlyReader{bytes.NewReader(data(5))}})
	var up *UploadError
	if !errors.As(err, &up) || len(f.callsTo("/complete")) != 0 {
		t.Fatalf("got %v", err)
	}
}

func TestStorageWithoutAnETagIsAnError(t *testing.T) {
	f := newFakeUploads(t, 4, 4, 100)
	f.storageHook = func(n int, w http.ResponseWriter) bool { w.WriteHeader(200); return true }
	_, err := f.client.Uploads.UploadFile(ctx, &UploadFileParams{Filename: "a.mp4", Size: 4, Body: bytes.NewReader(data(4))})
	var up *UploadError
	if !errors.As(err, &up) {
		t.Fatalf("got %v", err)
	}
}

func TestAReaderNeedsAFilename(t *testing.T) {
	f := newFakeUploads(t, 1, 1, 100)
	_, err := f.client.Uploads.UploadFile(ctx, &UploadFileParams{Size: 1, Body: bytes.NewReader(data(1))})
	var up *UploadError
	if !errors.As(err, &up) {
		t.Fatalf("got %v", err)
	}
}

func TestACompleteThatAlreadyWentThroughIsRecovered(t *testing.T) {
	f := newFakeUploads(t, 4, 4, 100)
	inner := f.client
	var completed int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/complete") {
			atomic.AddInt32(&completed, 1)
			writeError(w, 409, "UPLOAD_COMPLETE")
			return
		}
		// Hand everything else to the fake.
		req, _ := http.NewRequest(r.Method, inner.baseURL+strings.TrimPrefix(r.URL.Path, "/v1"), r.Body)
		req.Header = r.Header
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			w.WriteHeader(502)
			return
		}
		defer res.Body.Close()
		w.WriteHeader(res.StatusCode)
		_, _ = io.Copy(w, res.Body)
	}))
	defer ts.Close()
	c := NewClient("k", WithBaseURL(ts.URL+"/v1"))
	upload, err := c.Uploads.UploadFile(ctx, &UploadFileParams{Filename: "a.mp4", Size: 4, Body: bytes.NewReader(data(4))})
	if err != nil || upload.Status != UploadStatusUploaded || atomic.LoadInt32(&completed) != 1 {
		t.Fatalf("%+v %v", upload, err)
	}
}

func TestFromURLWaitsUntilTheDownloadIsDone(t *testing.T) {
	statuses := []string{"fetching", "uploaded"}
	var n int32
	c, rec := server(t, func(c call, w http.ResponseWriter) {
		if c.Method == "POST" {
			writeJSON(w, 202, map[string]any{"id": "job_u", "status": "fetching"})
			return
		}
		writeJSON(w, 200, map[string]any{"id": "job_u", "status": statuses[atomic.AddInt32(&n, 1)-1], "filename": "a.mp4", "size": 5})
	})
	upload, err := c.Uploads.FromURL(ctx, "https://example.com/a.mp4", &FromURLParams{Wait: &WaitOptions{PollInterval: time.Millisecond}})
	if err != nil || upload.Status != UploadStatusUploaded {
		t.Fatalf("%+v %v", upload, err)
	}
	calls := rec.all()
	if !reflect.DeepEqual(calls[0].Body, map[string]any{"source_url": "https://example.com/a.mp4"}) || len(calls) != 3 {
		t.Fatalf("%+v", calls)
	}
}

func TestFromURLNoWaitReturnsImmediately(t *testing.T) {
	c, rec := server(t, func(_ call, w http.ResponseWriter) {
		writeJSON(w, 202, map[string]any{"id": "job_u", "status": "fetching"})
	})
	upload, err := c.Uploads.FromURL(ctx, "https://example.com/a.mp4", &FromURLParams{NoWait: true, Filename: "talk.mp4"})
	if err != nil || upload.Status != UploadStatusFetching || len(rec.all()) != 1 || rec.all()[0].Body["filename"] != "talk.mp4" {
		t.Fatalf("%+v %v", upload, err)
	}
}

func TestFromURLFailsWhenTheDownloadFailed(t *testing.T) {
	c, _ := server(t, func(c call, w http.ResponseWriter) {
		status := "fetching"
		if c.Method == "GET" {
			status = "failed"
		}
		writeJSON(w, 200, map[string]any{"id": "job_u", "status": status, "error": "SOURCE_URL_TOO_SLOW"})
	})
	_, err := c.Uploads.FromURL(ctx, "https://example.com/a.mp4", &FromURLParams{Wait: &WaitOptions{PollInterval: time.Millisecond}})
	var wait *WaitError
	if !errors.As(err, &wait) || !strings.Contains(err.Error(), "SOURCE_URL_TOO_SLOW") {
		t.Fatalf("got %v", err)
	}
}

func TestWaitUntilReadyRefusesAnUploadAlreadyUsed(t *testing.T) {
	c, _ := server(t, func(_ call, w http.ResponseWriter) {
		writeJSON(w, 200, map[string]any{"id": "job_u", "status": "used"})
	})
	_, err := c.Uploads.WaitUntilReady(ctx, "job_u", nil)
	var wait *WaitError
	if !errors.As(err, &wait) {
		t.Fatalf("got %v", err)
	}
}

func TestDubReaderUploadsAndStartsTheDubsInOneCall(t *testing.T) {
	f := newFakeUploads(t, 4, 4, 100)
	created, err := f.client.DubReader(ctx,
		&UploadFileParams{Filename: "talk.mp4", Size: 4, Body: bytes.NewReader(data(4))},
		&DubParams{SourceLanguage: "en", TargetLanguages: []string{"fr", "es"}, Instructions: String("Warm.")},
		WithIdempotencyKey("order-7"))
	if err != nil || created.Dubs[0].ID != "job_d" {
		t.Fatalf("%+v %v", created, err)
	}
	calls := f.rec.all()
	dubs := f.callsTo("/dubs")[0]
	if !reflect.DeepEqual(dubs.Body, map[string]any{"upload_id": "job_up", "source_language": "en", "target_languages": []any{"fr", "es"}, "instructions": "Warm."}) {
		t.Fatalf("%v", dubs.Body)
	}
	if dubs.Header.Get("Idempotency-Key") != "order-7:dubs" || calls[0].Header.Get("Idempotency-Key") != "order-7:upload" {
		t.Fatalf("keys %q %q", dubs.Header.Get("Idempotency-Key"), calls[0].Header.Get("Idempotency-Key"))
	}
}

func TestDubPathAndDubURL(t *testing.T) {
	f := newFakeUploads(t, 3, 4, 100)
	path := filepath.Join(t.TempDir(), "a.mp3")
	_ = os.WriteFile(path, data(3), 0o600)
	if _, err := f.client.DubPath(ctx, path, &DubParams{SourceLanguage: "en", TargetLanguages: []string{"fr"}}); err != nil {
		t.Fatal(err)
	}
	if len(f.callsTo("/dubs")) != 1 {
		t.Fatal("no dub started")
	}
	c, rec := server(t, func(c call, w http.ResponseWriter) {
		switch {
		case strings.HasSuffix(c.Path, "/dubs"):
			writeJSON(w, 201, map[string]any{"dubs": []any{map[string]any{"id": "job_d"}}})
		default:
			writeJSON(w, 200, map[string]any{"id": "job_u", "status": "uploaded"})
		}
	})
	created, err := c.DubURL(ctx, "https://example.com/a.mp4", &DubParams{SourceLanguage: "en", TargetLanguages: []string{"fr"}})
	if err != nil || created.Dubs[0].ID != "job_d" || rec.all()[2].Body["upload_id"] != "job_u" {
		t.Fatalf("%+v %v", created, err)
	}
}
