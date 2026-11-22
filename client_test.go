package reedapt

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var ctx = context.Background()

func TestNoKeyFailsEveryRequest(t *testing.T) {
	t.Setenv("REEDAPT_API_KEY", "")
	_, err := NewClient("").Languages.List(ctx)
	if !errors.Is(err, ErrNoAPIKey) {
		t.Fatalf("got %v", err)
	}
}

func TestReadsKeyAndBaseURLFromEnvironment(t *testing.T) {
	c, rec := server(t, func(_ call, w http.ResponseWriter) {
		writeJSON(w, 200, map[string]any{"target_languages": []string{"fr"}})
	})
	t.Setenv("REEDAPT_API_KEY", "rk_test_env")
	t.Setenv("REEDAPT_BASE_URL", c.baseURL+"/")
	if _, err := NewClient("").Languages.List(ctx); err != nil {
		t.Fatal(err)
	}
	if got := rec.all()[0]; got.Path != "/v1/languages" || got.Header.Get("Authorization") != "Bearer rk_test_env" {
		t.Fatalf("got %+v", got)
	}
}

func TestSendsKeyUserAgentAndJSON(t *testing.T) {
	c, rec := server(t, func(_ call, w http.ResponseWriter) { writeJSON(w, 201, map[string]any{"dubs": []any{dubJSON}}) })
	created, err := c.Dubs.Create(ctx, &CreateDubsParams{UploadID: "job_1", SourceLanguage: "en", TargetLanguages: []string{"fr"}})
	if err != nil {
		t.Fatal(err)
	}
	if created.Dubs[0].ID != "job_1" {
		t.Fatalf("got %+v", created)
	}
	got := rec.all()[0]
	if got.Method != "POST" || got.Path != "/v1/dubs" {
		t.Fatalf("got %+v", got)
	}
	if got.Header.Get("Authorization") != "Bearer rk_test_abc" || got.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("headers %v", got.Header)
	}
	if ua := got.Header.Get("User-Agent"); ua != "reedapt-go/"+Version {
		t.Fatalf("user agent %q", ua)
	}
	want := map[string]any{"upload_id": "job_1", "source_language": "en", "target_languages": []any{"fr"}}
	if !reflect.DeepEqual(got.Body, want) {
		t.Fatalf("body %v", got.Body)
	}
}

func TestCreatingDubsCarriesOneIdempotencyKeyAcrossRetries(t *testing.T) {
	var n int32
	c, rec := server(t, func(_ call, w http.ResponseWriter) {
		if atomic.AddInt32(&n, 1) == 1 {
			writeError(w, 500, "INTERNAL")
			return
		}
		writeJSON(w, 201, map[string]any{"dubs": []any{}})
	})
	if _, err := c.Dubs.Create(ctx, &CreateDubsParams{UploadID: "u", SourceLanguage: "en", TargetLanguages: []string{"fr"}}); err != nil {
		t.Fatal(err)
	}
	calls := rec.all()
	key := calls[0].Header.Get("Idempotency-Key")
	if len(calls) != 2 || len(key) < 10 || calls[1].Header.Get("Idempotency-Key") != key {
		t.Fatalf("calls %+v", calls)
	}
}

func TestCallersIdempotencyKeyIsUsedAsGiven(t *testing.T) {
	c, rec := server(t, func(_ call, w http.ResponseWriter) { writeJSON(w, 201, map[string]any{"dubs": []any{}}) })
	_, _ = c.Dubs.Create(ctx, &CreateDubsParams{UploadID: "u"}, WithIdempotencyKey("order-1042"))
	if got := rec.all()[0].Header.Get("Idempotency-Key"); got != "order-1042" {
		t.Fatalf("got %q", got)
	}
}

func TestOptionalParamsAreOmittedWhenUnset(t *testing.T) {
	c, rec := server(t, func(_ call, w http.ResponseWriter) { writeJSON(w, 201, map[string]any{"dubs": []any{}}) })
	_, _ = c.Dubs.Create(ctx, &CreateDubsParams{
		UploadID: "u", SourceLanguage: "en", TargetLanguages: []string{"fr"},
		Instructions: String("Keep the name Adaeze."), VoiceCloning: Bool(false), NumSpeakers: Int(2),
		Metadata: map[string]string{"order_id": "1042"},
	})
	body := rec.all()[0].Body
	if body["instructions"] != "Keep the name Adaeze." || body["voice_cloning"] != false || body["num_speakers"] != 2.0 {
		t.Fatalf("body %v", body)
	}
	if _, ok := body["media_category"]; ok {
		t.Fatalf("media_category was sent: %v", body)
	}
}

func TestErrorsCarryStatusCodeAndRequestID(t *testing.T) {
	c, _ := server(t, func(_ call, w http.ResponseWriter) { writeError(w, 401, "UNAUTHORIZED") })
	_, err := c.Usage.Retrieve(ctx)
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("got %T %v", err, err)
	}
	if apiErr.Status != 401 || apiErr.Code != "UNAUTHORIZED" || apiErr.RequestID != "req_test" || !IsAuthentication(err) {
		t.Fatalf("got %+v", apiErr)
	}
	if !strings.HasPrefix(err.Error(), "UNAUTHORIZED: ") {
		t.Fatal(err.Error())
	}
}

func TestErrorWithoutABodyFallsBackToStatus(t *testing.T) {
	c, _ := server(t, func(_ call, w http.ResponseWriter) {
		w.Header().Set("Request-Id", "req_hdr")
		w.WriteHeader(404)
	})
	_, err := c.Dubs.Retrieve(ctx, "nope")
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Code != "HTTP_404" || apiErr.RequestID != "req_hdr" || !IsNotFound(err) {
		t.Fatalf("got %+v", err)
	}
}

func TestValidationErrorsListTheirFields(t *testing.T) {
	c, rec := server(t, func(_ call, w http.ResponseWriter) {
		writeJSON(w, 422, map[string]any{"error": map[string]any{
			"code": "INVALID_REQUEST", "message": "bad", "request_id": "req_1",
			"fields": []any{map[string]any{"field": "target_languages", "message": "Field required"}},
		}})
	})
	_, err := c.Dubs.Create(ctx, &CreateDubsParams{UploadID: "u", SourceLanguage: "en", TargetLanguages: []string{}})
	var apiErr *Error
	if !errors.As(err, &apiErr) || !IsInvalidRequest(err) || apiErr.Fields[0].Field != "target_languages" {
		t.Fatalf("got %+v", err)
	}
	if len(rec.all()) != 1 {
		t.Fatal("a 422 was retried")
	}
}

func TestErrorKinds(t *testing.T) {
	for status, check := range map[int]func(error) bool{409: IsConflict, 429: IsRateLimit, 500: IsServer, 400: IsInvalidRequest, 413: IsInvalidRequest} {
		c, _ := server(t, func(_ call, w http.ResponseWriter) { writeError(w, status, "SOMETHING") })
		c.maxRetries = 0
		_, err := c.Languages.List(ctx)
		if !check(err) {
			t.Errorf("status %d: wrong kind for %v", status, err)
		}
	}
}

func TestRateLimitWaitsForRetryAfterThenRetries(t *testing.T) {
	var n int32
	c, rec := server(t, func(_ call, w http.ResponseWriter) {
		if atomic.AddInt32(&n, 1) == 1 {
			writeError(w, 429, "RATE_LIMITED", "Retry-After", "0")
			return
		}
		writeJSON(w, 200, map[string]any{"target_languages": []string{}})
	})
	if _, err := c.Languages.List(ctx); err != nil {
		t.Fatal(err)
	}
	if len(rec.all()) != 2 {
		t.Fatal("not retried")
	}
}

func TestRetryAfterHeaderIsHonoured(t *testing.T) {
	var n int32
	c, _ := server(t, func(_ call, w http.ResponseWriter) {
		if atomic.AddInt32(&n, 1) == 1 {
			writeError(w, 429, "RATE_LIMITED", "Retry-After", "1")
			return
		}
		writeJSON(w, 200, map[string]any{"target_languages": []string{}})
	})
	start := time.Now()
	if _, err := c.Languages.List(ctx); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < 900*time.Millisecond {
		t.Fatalf("only waited %s", time.Since(start))
	}
}

func TestLongLastingLimitsAreNotRetried(t *testing.T) {
	c, rec := server(t, func(_ call, w http.ResponseWriter) { writeError(w, 429, "TOO_MANY_DUBS_RUNNING", "Retry-After", "7") })
	_, err := c.Dubs.Create(ctx, &CreateDubsParams{UploadID: "u"})
	var apiErr *Error
	if !errors.As(err, &apiErr) || !IsRateLimit(err) {
		t.Fatal(err)
	}
	if d, ok := apiErr.RetryAfter(); !ok || d != 7*time.Second {
		t.Fatalf("retry after %v %v", d, ok)
	}
	if len(rec.all()) != 1 {
		t.Fatal("retried")
	}
}

func TestServerErrorsAreRetriedUpToMaxRetries(t *testing.T) {
	c, rec := server(t, func(_ call, w http.ResponseWriter) { writeError(w, 502, "BAD_GATEWAY") })
	c.maxRetries = 1
	_, err := c.Languages.List(ctx)
	if !IsServer(err) || len(rec.all()) != 2 {
		t.Fatalf("%v, %d calls", err, len(rec.all()))
	}
}

func TestPerRequestMaxRetries(t *testing.T) {
	c, rec := server(t, func(_ call, w http.ResponseWriter) { writeError(w, 500, "INTERNAL") })
	_, _ = c.Languages.List(ctx, WithRequestMaxRetries(0))
	if len(rec.all()) != 1 {
		t.Fatal("retried")
	}
}

func TestUnsafeRequestsRetryOnlyWhenRefusedBeforeActing(t *testing.T) {
	c, rec := server(t, func(_ call, w http.ResponseWriter) { writeError(w, 500, "INTERNAL") })
	_, _ = c.WebhookEndpoints.Create(ctx, &CreateWebhookEndpointParams{URL: "https://example.com/hook"})
	if len(rec.all()) != 1 {
		t.Fatal("a 500 on an unsafe request was retried")
	}
	var n int32
	c, rec = server(t, func(_ call, w http.ResponseWriter) {
		if atomic.AddInt32(&n, 1) == 1 {
			writeError(w, 503, "UNAVAILABLE")
			return
		}
		writeJSON(w, 201, map[string]any{"id": "we_1"})
	})
	if _, err := c.WebhookEndpoints.Create(ctx, &CreateWebhookEndpointParams{URL: "https://example.com/hook"}); err != nil || len(rec.all()) != 2 {
		t.Fatalf("503 should retry: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func failingClient(calls *int32) *Client {
	c := NewClient("rk_test_abc", WithBaseURL("https://api.test/v1"), WithHTTPClient(&http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			atomic.AddInt32(calls, 1)
			return nil, errors.New("connection reset")
		}),
	}))
	c.backoff = func(int) time.Duration { return time.Millisecond }
	return c
}

func TestNetworkFailureIsRetriedForSafeRequests(t *testing.T) {
	var calls int32
	_, err := failingClient(&calls).Languages.List(ctx)
	var conn *ConnectionError
	if !errors.As(err, &conn) || conn.Timeout || atomic.LoadInt32(&calls) != 3 {
		t.Fatalf("%v after %d calls", err, calls)
	}
}

func TestNetworkFailureIsNotRetriedForUnsafeRequests(t *testing.T) {
	var calls int32
	c := failingClient(&calls)
	checks := map[string]func() error{
		"webhook create": func() error {
			_, err := c.WebhookEndpoints.Create(ctx, &CreateWebhookEndpointParams{URL: "https://x.test"})
			return err
		},
		"resend": func() error { _, err := c.WebhookEndpoints.ResendDelivery(ctx, "we_1", "whd_1"); return err },
		"request": func() error {
			_, err := c.Dubs.Requests.Create(ctx, "job_1", &CreateRunParams{Text: "x"})
			return err
		},
		"lines edit": func() error { _, err := c.Dubs.Lines.Edit(ctx, "job_1", &EditLinesParams{}); return err },
		"answer": func() error {
			_, err := c.Runs.Answer(ctx, "run_1", &AnswerRunParams{Choice: RunChoiceApply})
			return err
		},
	}
	for name, run := range checks {
		atomic.StoreInt32(&calls, 0)
		var conn *ConnectionError
		if err := run(); !errors.As(err, &conn) || atomic.LoadInt32(&calls) != 1 {
			t.Errorf("%s: %v after %d calls", name, err, calls)
		}
	}
}

func TestSlowRequestsTimeOut(t *testing.T) {
	c, _ := server(t, func(_ call, w http.ResponseWriter) { time.Sleep(300 * time.Millisecond) })
	c.timeout = 30 * time.Millisecond
	c.maxRetries = 0
	_, err := c.Languages.List(ctx)
	var conn *ConnectionError
	if !errors.As(err, &conn) || !conn.Timeout {
		t.Fatalf("got %v", err)
	}
}

func TestWithTimeoutOptionAndPerRequestTimeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(200 * time.Millisecond) }))
	defer ts.Close()
	c := NewClient("k", WithBaseURL(ts.URL), WithTimeout(10*time.Second), WithMaxRetries(0))
	_, err := c.Languages.List(ctx, WithRequestTimeout(20*time.Millisecond))
	var conn *ConnectionError
	if !errors.As(err, &conn) || !conn.Timeout {
		t.Fatalf("got %v", err)
	}
}

func TestCancelledContextStopsWithoutRetrying(t *testing.T) {
	c, rec := server(t, func(_ call, w http.ResponseWriter) { time.Sleep(300 * time.Millisecond) })
	cctx, cancel := context.WithCancel(ctx)
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	_, err := c.Languages.List(cctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if len(rec.all()) != 1 {
		t.Fatal("retried")
	}
}

func TestCancelAndDeleteAnswerNoContent(t *testing.T) {
	c, rec := server(t, func(_ call, w http.ResponseWriter) { w.WriteHeader(204) })
	if err := c.Dubs.Cancel(ctx, "job_1"); err != nil {
		t.Fatal(err)
	}
	if err := c.WebhookEndpoints.Delete(ctx, "we_1"); err != nil {
		t.Fatal(err)
	}
	if err := c.WebhookEndpoints.SendTest(ctx, "we_1"); err != nil {
		t.Fatal(err)
	}
	calls := rec.all()
	if calls[0].Path != "/v1/dubs/job_1/cancel" || calls[1].Method != "DELETE" || calls[2].Path != "/v1/webhook_endpoints/we_1/test" {
		t.Fatalf("%+v", calls)
	}
}

func TestIDsArePathEscaped(t *testing.T) {
	c, rec := server(t, func(_ call, w http.ResponseWriter) { writeJSON(w, 200, dubJSON) })
	_, _ = c.Dubs.Retrieve(ctx, "a/b c")
	if got := rec.all()[0].Path; got != "/v1/dubs/a%2Fb%20c" {
		t.Fatal(got)
	}
}

func TestListAllWalksEveryPageAndSkipsRepeats(t *testing.T) {
	pages := map[string]any{
		"":  nil,
		"0": map[string]any{"data": []any{map[string]any{"id": "a"}, map[string]any{"id": "b"}}, "has_more": true},
		"2": map[string]any{"data": []any{map[string]any{"id": "b"}, map[string]any{"id": "c"}}, "has_more": false},
	}
	c, rec := server(t, func(c call, w http.ResponseWriter) {
		offset := "0"
		if i := strings.Index(c.Path, "offset="); i >= 0 {
			offset = c.Path[i+7:]
		}
		writeJSON(w, 200, pages[offset])
	})
	var ids []string
	it := c.Dubs.ListAll(ctx, &ListDubsParams{Limit: 2})
	for it.Next() {
		ids = append(ids, it.Dub().ID)
	}
	if it.Err() != nil || !reflect.DeepEqual(ids, []string{"a", "b", "c"}) {
		t.Fatalf("%v %v", ids, it.Err())
	}
	if got := rec.all()[0].Path; got != "/v1/dubs?limit=2" {
		t.Fatal(got)
	}
}

func TestListAllStopsOnError(t *testing.T) {
	c, _ := server(t, func(_ call, w http.ResponseWriter) { writeError(w, 401, "UNAUTHORIZED") })
	it := c.Dubs.ListAll(ctx, nil)
	if it.Next() || !IsAuthentication(it.Err()) {
		t.Fatal("expected an authentication error")
	}
}

func TestListSendsLimitAndOffset(t *testing.T) {
	c, rec := server(t, func(_ call, w http.ResponseWriter) {
		writeJSON(w, 200, map[string]any{"data": []any{}, "has_more": false})
	})
	_, _ = c.Dubs.List(ctx, &ListDubsParams{Limit: 20, Offset: 40})
	_, _ = c.Dubs.List(ctx, nil)
	calls := rec.all()
	if calls[0].Path != "/v1/dubs?limit=20&offset=40" || calls[1].Path != "/v1/dubs" {
		t.Fatalf("%+v", calls)
	}
}

func TestWaitUntilDonePollsUntilFinal(t *testing.T) {
	statuses := []string{"queued", "processing", "completed"}
	var n int32
	c, _ := server(t, func(_ call, w http.ResponseWriter) {
		d := map[string]any{"id": "job_1", "status": statuses[atomic.AddInt32(&n, 1)-1]}
		writeJSON(w, 200, d)
	})
	var seen []DubStatus
	dub, err := c.Dubs.WaitUntilDone(ctx, "job_1", &WaitOptions{PollInterval: time.Millisecond, OnUpdate: func(d *Dub) { seen = append(seen, d.Status) }})
	if err != nil || dub.Status != DubStatusCompleted || !reflect.DeepEqual(seen, []DubStatus{"queued", "processing", "completed"}) {
		t.Fatalf("%v %v %v", dub, seen, err)
	}
}

func TestWaitUntilDoneGivesUp(t *testing.T) {
	c, _ := server(t, func(_ call, w http.ResponseWriter) { writeJSON(w, 200, dubJSON) })
	_, err := c.Dubs.WaitUntilDone(ctx, "job_1", &WaitOptions{PollInterval: 5 * time.Millisecond, Timeout: 20 * time.Millisecond})
	var wait *WaitError
	if !errors.As(err, &wait) {
		t.Fatalf("got %v", err)
	}
}

func TestUsageReportAsksForTheMonthGiven(t *testing.T) {
	c, rec := server(t, func(_ call, w http.ResponseWriter) {
		writeJSON(w, 200, map[string]any{
			"month": "2026-09", "minutes": 1.5, "minutes_in_progress": 0, "dubs": map[string]any{"completed": 2},
			"keys":      []any{map[string]any{"key_id": "k1", "mode": "test", "minutes": 1, "dubs": map[string]any{}}},
			"languages": []any{map[string]any{"target_language": "fr", "minutes": 1, "dubs": map[string]any{"completed": 1}}},
		})
	})
	report, err := c.Usage.Report(ctx, &UsageReportParams{Month: "2026-09"})
	if err != nil || report.Month != "2026-09" || report.Minutes != 1.5 || report.Dubs.Completed != 2 ||
		*report.Keys[0].KeyID != "k1" || report.Keys[0].Mode != "test" || *report.Languages[0].TargetLanguage != "fr" {
		t.Fatalf("%+v %v", report, err)
	}
	_, _ = c.Usage.Report(ctx, nil)
	calls := rec.all()
	if calls[0].Path != "/v1/usage/report?month=2026-09" || calls[1].Path != "/v1/usage/report" {
		t.Fatalf("%+v", calls)
	}
}

func TestUsageAndLanguages(t *testing.T) {
	c, _ := server(t, func(c call, w http.ResponseWriter) {
		if strings.HasSuffix(c.Path, "/languages") {
			writeJSON(w, 200, map[string]any{"target_languages": []string{"fr", "es"}})
			return
		}
		writeJSON(w, 200, map[string]any{"plan": nil, "key_mode": "test", "unlimited": false, "test_preview_seconds": 60})
	})
	langs, err := c.Languages.List(ctx)
	if err != nil || len(langs.TargetLanguages) != 2 {
		t.Fatal(err)
	}
	u, err := c.Usage.Retrieve(ctx)
	if err != nil || u.KeyMode != "test" || u.Plan != nil || u.TestPreviewSecs != 60 {
		t.Fatalf("%+v %v", u, err)
	}
}

func TestTranscript(t *testing.T) {
	c, rec := server(t, func(_ call, w http.ResponseWriter) {
		writeJSON(w, 200, map[string]any{"dub_id": "job_1", "lines": []any{map[string]any{"start_ms": 1200, "end_ms": 3850, "speaker": "SPEAKER_00", "original_text": "Hi.", "text": "Salut."}}})
	})
	tr, err := c.Dubs.Transcript(ctx, "job_1")
	if err != nil || tr.Lines[0].Text != "Salut." || *tr.Lines[0].Speaker != "SPEAKER_00" || tr.Lines[0].EndMS != 3850 {
		t.Fatalf("%+v %v", tr, err)
	}
	if rec.all()[0].Path != "/v1/dubs/job_1/transcript" {
		t.Fatal(rec.all()[0].Path)
	}
}

func TestSubtitlesReturnTheFilesText(t *testing.T) {
	vtt := "WEBVTT\n\n00:00:01.200 --> 00:00:03.850\nSalut.\n"
	c, rec := server(t, func(_ call, w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
		_, _ = w.Write([]byte(vtt))
	})
	got, err := c.Dubs.Subtitles(ctx, "job_1", &SubtitlesParams{Format: "vtt", Language: "source"})
	if err != nil || got != vtt {
		t.Fatalf("%q %v", got, err)
	}
	_, _ = c.Dubs.Subtitles(ctx, "job_1", nil)
	calls := rec.all()
	if calls[0].Path != "/v1/dubs/job_1/subtitles?format=vtt&language=source" || calls[1].Path != "/v1/dubs/job_1/subtitles" {
		t.Fatalf("%+v", calls)
	}
}

func TestSubtitlesOfAnUnfinishedDubAreAConflict(t *testing.T) {
	c, _ := server(t, func(_ call, w http.ResponseWriter) { writeError(w, 409, "DUB_NOT_READY") })
	_, err := c.Dubs.Subtitles(ctx, "job_1", nil)
	var apiErr *Error
	if !IsConflict(err) || !errors.As(err, &apiErr) || apiErr.Code != "DUB_NOT_READY" {
		t.Fatal(err)
	}
}

func TestIdempotencyInProgressIsRetried(t *testing.T) {
	var n int32
	c, rec := server(t, func(_ call, w http.ResponseWriter) {
		if atomic.AddInt32(&n, 1) == 1 {
			writeError(w, 409, "IDEMPOTENCY_IN_PROGRESS")
			return
		}
		writeJSON(w, 201, map[string]any{"dubs": []any{}})
	})
	if _, err := c.Dubs.Create(ctx, &CreateDubsParams{UploadID: "u"}); err != nil || len(rec.all()) != 2 {
		t.Fatal(err)
	}
}

func TestFilesAndQuality(t *testing.T) {
	quality := map[string]any{"dub_id": "job_1", "checked": true, "listened": true, "lines": 40, "fixed": 2, "to_review": 1,
		"flagged": []any{map[string]any{"line": 3, "start_ms": 9000, "checks": []string{"fit"}, "fixed": "shortened"}, map[string]any{"line": 4, "start_ms": 9500, "checks": []string{}, "fixed": nil}}}
	c, rec := server(t, func(c call, w http.ResponseWriter) {
		if strings.HasSuffix(c.Path, "/files") {
			writeJSON(w, 200, map[string]any{"files": []any{map[string]any{"kind": "dub", "filename": "a.mp4", "url": "https://x/a.mp4"}}, "expires_at": "later"})
			return
		}
		writeJSON(w, 200, quality)
	})
	files, err := c.Dubs.Files(ctx, "job_1")
	if err != nil || files.Files[0].URL != "https://x/a.mp4" || files.Files[0].Kind != "dub" {
		t.Fatalf("%+v %v", files, err)
	}
	q, err := c.Dubs.Quality(ctx, "job_1")
	if err != nil || !q.Checked || q.ToReview != 1 || *q.Flagged[0].Fixed != "shortened" || q.Flagged[1].Fixed != nil {
		t.Fatalf("%+v %v", q, err)
	}
	if rec.all()[1].Path != "/v1/dubs/job_1/quality" {
		t.Fatal(rec.all()[1].Path)
	}
}

func TestRequestsDefaultToAsk(t *testing.T) {
	run := map[string]any{"id": "run_1", "dub_id": "job_1", "status": "done", "request": "Is line 3 natural?", "reply": "Yes.", "created_at": "x"}
	c, rec := server(t, func(_ call, w http.ResponseWriter) { writeJSON(w, 200, run) })
	got, err := c.Dubs.Requests.Create(ctx, "job_1", &CreateRunParams{Text: "Is line 3 natural?"})
	if err != nil || got.Status != RunStatusDone || got.Reply != "Yes." {
		t.Fatalf("%+v %v", got, err)
	}
	_, _ = c.Dubs.Requests.Create(ctx, "job_1", &CreateRunParams{Text: "Fix it.", Autonomy: AutonomyDecide})
	calls := rec.all()
	if calls[0].Method != "POST" || calls[0].Path != "/v1/dubs/job_1/requests" ||
		!reflect.DeepEqual(calls[0].Body, map[string]any{"autonomy": "ask", "text": "Is line 3 natural?"}) ||
		calls[0].Header.Get("Idempotency-Key") != "" || calls[1].Body["autonomy"] != "decide" {
		t.Fatalf("%+v", calls)
	}
}

func TestNotReleasedYetIs403NotAvailable(t *testing.T) {
	c, _ := server(t, func(_ call, w http.ResponseWriter) { writeError(w, 403, "NOT_AVAILABLE") })
	_, err := c.Dubs.Requests.Create(ctx, "job_1", &CreateRunParams{Text: "x"})
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Status != 403 || apiErr.Code != "NOT_AVAILABLE" {
		t.Fatal(err)
	}
}

func TestRunsRetrieveAndAnswer(t *testing.T) {
	run := map[string]any{"id": "run_1", "dub_id": "job_1", "status": "needs_input", "request": "Calmer.", "reply": "I can soften line 3.",
		"proposal": map[string]any{"kind": "edit_lines", "summary": "Soften line 3", "lines": 1},
		"options":  []any{map[string]any{"id": "apply", "label": "Apply"}, map[string]any{"id": "decline", "label": "Decline"}}, "created_at": "x"}
	c, rec := server(t, func(c call, w http.ResponseWriter) {
		if c.Method == "GET" {
			writeJSON(w, 200, run)
			return
		}
		writeJSON(w, 200, map[string]any{"id": "run_1", "dub_id": "job_1", "status": "declined", "proposal": nil, "options": nil})
	})
	got, err := c.Runs.Retrieve(ctx, "run_1")
	if err != nil || got.Proposal.Lines != 1 || got.Options[1].ID != RunChoiceDecline {
		t.Fatalf("%+v %v", got, err)
	}
	answered, err := c.Runs.Answer(ctx, "run_1", &AnswerRunParams{Choice: RunChoiceDecline})
	if err != nil || answered.Status != RunStatusDeclined {
		t.Fatalf("%+v %v", answered, err)
	}
	calls := rec.all()
	if calls[0].Path != "/v1/runs/run_1" || calls[1].Path != "/v1/runs/run_1/answer" || !reflect.DeepEqual(calls[1].Body, map[string]any{"choice": "decline"}) {
		t.Fatalf("%+v", calls)
	}
}

func TestLinesListAndEdit(t *testing.T) {
	c, rec := server(t, func(c call, w http.ResponseWriter) {
		if c.Method == "GET" {
			writeJSON(w, 200, map[string]any{"dub_id": "job_1", "lines": []any{map[string]any{"line": 3, "start_ms": 1, "end_ms": 2, "speaker": "SPEAKER_00", "original_text": "Hi.", "text": "Salut.", "delivery_style": nil, "voice_instructions": nil}}})
			return
		}
		writeJSON(w, 202, map[string]any{"dub_id": "job_1", "lines": []int{3}, "status": "revoicing"})
	})
	lines, err := c.Dubs.Lines.List(ctx, "job_1")
	if err != nil || lines.Lines[0].Line != 3 || lines.Lines[0].DeliveryStyle != nil {
		t.Fatalf("%+v %v", lines, err)
	}
	edited, err := c.Dubs.Lines.Edit(ctx, "job_1", &EditLinesParams{Lines: []LineEdit{
		{Line: 3, Text: String("Bonjour.")},
		{Line: 4, DeliveryStyle: String("warm"), ClearVoiceInstructions: true},
	}})
	if err != nil || edited.Status != "revoicing" || edited.Lines[0] != 3 {
		t.Fatalf("%+v %v", edited, err)
	}
	calls := rec.all()
	want := map[string]any{"lines": []any{
		map[string]any{"line": 3.0, "text": "Bonjour."},
		map[string]any{"line": 4.0, "delivery_style": "warm", "voice_instructions": nil},
	}}
	if calls[0].Path != "/v1/dubs/job_1/lines" || calls[1].Path != "/v1/dubs/job_1/lines/edit" || !reflect.DeepEqual(calls[1].Body, want) {
		t.Fatalf("%+v", calls[1].Body)
	}
}

func TestWebhookEndpoints(t *testing.T) {
	c, rec := server(t, func(c call, w http.ResponseWriter) {
		switch {
		case c.Method == "POST" && strings.HasSuffix(c.Path, "/resend"):
			writeJSON(w, 201, map[string]any{"id": "whd_1", "event_id": "evt_1", "event_type": "dub.completed", "status": "pending", "attempts": 0, "created_at": "x"})
		case strings.HasSuffix(c.Path, "/deliveries?limit=5"):
			writeJSON(w, 200, map[string]any{"data": []any{map[string]any{"id": "whd_1", "last_status_code": 500, "last_error": nil}}})
		case c.Method == "POST":
			writeJSON(w, 201, map[string]any{"id": "we_1", "url": "https://example.com/hook", "events": []string{"dub.completed"}, "secret": "whsec_abc", "created_at": "x"})
		default:
			writeJSON(w, 200, map[string]any{"data": []any{map[string]any{"id": "we_1", "url": "https://example.com/hook", "description": nil}}})
		}
	})
	created, err := c.WebhookEndpoints.Create(ctx, &CreateWebhookEndpointParams{URL: "https://example.com/hook", Description: "prod"})
	if err != nil || *created.Secret != "whsec_abc" {
		t.Fatalf("%+v %v", created, err)
	}
	list, err := c.WebhookEndpoints.List(ctx)
	if err != nil || list.Data[0].ID != "we_1" || list.Data[0].Description != nil {
		t.Fatalf("%+v %v", list, err)
	}
	deliveries, err := c.WebhookEndpoints.Deliveries(ctx, "we_1", 5)
	if err != nil || *deliveries.Data[0].LastStatusCode != 500 {
		t.Fatalf("%+v %v", deliveries, err)
	}
	d, err := c.WebhookEndpoints.ResendDelivery(ctx, "we_1", "whd_1")
	if err != nil || d.EventID != "evt_1" {
		t.Fatalf("%+v %v", d, err)
	}
	calls := rec.all()
	if !reflect.DeepEqual(calls[0].Body, map[string]any{"url": "https://example.com/hook", "description": "prod"}) ||
		calls[3].Path != "/v1/webhook_endpoints/we_1/deliveries/whd_1/resend" {
		t.Fatalf("%+v", calls)
	}
}

func TestDefaultHeadersAndPerRequestHeaders(t *testing.T) {
	c, rec := server(t, func(_ call, w http.ResponseWriter) { writeJSON(w, 200, map[string]any{}) })
	WithDefaultHeaders(map[string]string{"X-App": "a", "Authorization": "Bearer evil"})(c)
	_, _ = c.Languages.List(ctx, WithHeader("X-Trace", "t1"))
	h := rec.all()[0].Header
	if h.Get("X-App") != "a" || h.Get("X-Trace") != "t1" || h.Get("Authorization") != "Bearer rk_test_abc" {
		t.Fatalf("%v", h)
	}
}
