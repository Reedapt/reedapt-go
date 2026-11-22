package reedapt

import (
	"context"
	"net/url"
	"strconv"
	"time"
)

// DubsService starts, reads and manages dubs.
type DubsService struct {
	c *Client
	// Requests asks Reedapt for changes to a finished dub. Early access.
	Requests *DubRequestsService
	// Lines reads and edits a finished dub's lines. Early access.
	Lines *DubLinesService
}

func newDubsService(c *Client) *DubsService {
	return &DubsService{c: c, Requests: &DubRequestsService{c}, Lines: &DubLinesService{c}}
}

// Create starts one dub per target language. Languages that can't start come
// back in Refused. An Idempotency-Key is sent automatically, so a retry never
// makes a second dub.
func (s *DubsService) Create(ctx context.Context, params *CreateDubsParams, opts ...RequestOption) (*DubsCreated, error) {
	var out DubsCreated
	err := s.c.do(ctx, &request{method: "POST", path: "/dubs", body: params, retry: retrySafe, idempotent: true}, opts, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Retrieve gets one dub.
func (s *DubsService) Retrieve(ctx context.Context, dubID string, opts ...RequestOption) (*Dub, error) {
	var out Dub
	err := s.c.do(ctx, &request{method: "GET", path: "/dubs/" + escape(dubID), retry: retrySafe}, opts, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// List gets one page of the account's dubs, newest first. params may be nil.
func (s *DubsService) List(ctx context.Context, params *ListDubsParams, opts ...RequestOption) (*DubList, error) {
	q := url.Values{}
	if params != nil {
		if params.Limit > 0 {
			q.Set("limit", strconv.Itoa(params.Limit))
		}
		if params.Offset > 0 {
			q.Set("offset", strconv.Itoa(params.Offset))
		}
	}
	var out DubList
	err := s.c.do(ctx, &request{method: "GET", path: "/dubs", query: q, retry: retrySafe}, opts, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// DubIterator walks every dub, fetching pages as it goes.
//
//	it := client.Dubs.ListAll(ctx, nil)
//	for it.Next() {
//		fmt.Println(it.Dub().ID)
//	}
//	if err := it.Err(); err != nil { ... }
type DubIterator struct {
	s      *DubsService
	ctx    context.Context
	opts   []RequestOption
	limit  int
	offset int
	page   []Dub
	index  int
	more   bool
	seen   map[string]bool
	cur    *Dub
	err    error
}

// ListAll returns an iterator over every dub, newest first. Only params.Limit
// (the page size, default 100) is used.
func (s *DubsService) ListAll(ctx context.Context, params *ListDubsParams, opts ...RequestOption) *DubIterator {
	limit := 100
	if params != nil && params.Limit > 0 {
		limit = params.Limit
	}
	return &DubIterator{s: s, ctx: ctx, opts: opts, limit: limit, more: true, seen: map[string]bool{}}
}

// Next moves to the next dub. It returns false at the end or after an error; check Err.
func (it *DubIterator) Next() bool {
	for {
		if it.err != nil {
			return false
		}
		for it.index < len(it.page) {
			dub := it.page[it.index]
			it.index++
			// Dubs started while paging shift later pages by one; skip the repeats.
			if it.seen[dub.ID] {
				continue
			}
			it.seen[dub.ID] = true
			it.cur = &dub
			return true
		}
		if !it.more {
			return false
		}
		page, err := it.s.List(it.ctx, &ListDubsParams{Limit: it.limit, Offset: it.offset}, it.opts...)
		if err != nil {
			it.err = err
			return false
		}
		it.offset += it.limit
		it.page, it.index = page.Data, 0
		it.more = page.HasMore && len(page.Data) > 0
	}
}

// Dub returns the current dub. Call it after Next returned true.
func (it *DubIterator) Dub() *Dub { return it.cur }

// Err returns the error that stopped the iteration, if any.
func (it *DubIterator) Err() error { return it.err }

// Cancel stops an unfinished dub and deletes it. Its minutes are returned.
func (s *DubsService) Cancel(ctx context.Context, dubID string, opts ...RequestOption) error {
	return s.c.do(ctx, &request{method: "POST", path: "/dubs/" + escape(dubID) + "/cancel", retry: retrySafe}, opts, nil)
}

// Files gets download links for a completed dub's files. Each link lasts an hour.
func (s *DubsService) Files(ctx context.Context, dubID string, opts ...RequestOption) (*DubFiles, error) {
	var out DubFiles
	err := s.c.do(ctx, &request{method: "GET", path: "/dubs/" + escape(dubID) + "/files", retry: retrySafe}, opts, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Transcript gets every line of a completed dub with its timing, speaker, original words and translation.
func (s *DubsService) Transcript(ctx context.Context, dubID string, opts ...RequestOption) (*Transcript, error) {
	var out Transcript
	err := s.c.do(ctx, &request{method: "GET", path: "/dubs/" + escape(dubID) + "/transcript", retry: retrySafe}, opts, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Quality gets Reedapt's check of a finished dub: what it listened to, what it
// fixed and the lines left for you to review. Checked is false until the check
// has run; the dub.checked webhook announces it.
func (s *DubsService) Quality(ctx context.Context, dubID string, opts ...RequestOption) (*Quality, error) {
	var out Quality
	err := s.c.do(ctx, &request{method: "GET", path: "/dubs/" + escape(dubID) + "/quality", retry: retrySafe}, opts, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Subtitles gets a completed dub's subtitles as SRT or WebVTT text. params may be nil.
func (s *DubsService) Subtitles(ctx context.Context, dubID string, params *SubtitlesParams, opts ...RequestOption) (string, error) {
	q := url.Values{}
	if params != nil {
		if params.Format != "" {
			q.Set("format", params.Format)
		}
		if params.Language != "" {
			q.Set("language", params.Language)
		}
	}
	var out string
	err := s.c.do(ctx, &request{method: "GET", path: "/dubs/" + escape(dubID) + "/subtitles", query: q, retry: retrySafe, text: true}, opts, &out)
	return out, err
}

// WaitOptions tunes the WaitUntil... helpers. Zero values use the defaults.
type WaitOptions struct {
	// PollInterval is the time between checks. Defaults to 30 seconds for dubs and 10 for uploads.
	PollInterval time.Duration
	// Timeout is how long to wait before giving up. Defaults to 12 hours for dubs and 3 for uploads.
	Timeout time.Duration
	// OnUpdate is called after each check of a dub, for example to show progress.
	OnUpdate func(*Dub)
}

// WaitUntilDone polls a dub until it is completed or failed and returns it.
// Check Status on the result. Webhooks are better for anything long-running.
func (s *DubsService) WaitUntilDone(ctx context.Context, dubID string, opts *WaitOptions) (*Dub, error) {
	var o WaitOptions
	if opts != nil {
		o = *opts
	}
	if o.PollInterval <= 0 {
		o.PollInterval = 30 * time.Second
	}
	if o.Timeout <= 0 {
		o.Timeout = 12 * time.Hour
	}
	deadline := time.Now().Add(o.Timeout)
	for {
		dub, err := s.Retrieve(ctx, dubID)
		if err != nil {
			return nil, err
		}
		if o.OnUpdate != nil {
			o.OnUpdate(dub)
		}
		if dub.Status == DubStatusCompleted || dub.Status == DubStatusFailed {
			return dub, nil
		}
		if time.Now().Add(o.PollInterval).After(deadline) {
			return nil, &WaitError{"Dub " + dubID + " was still " + string(dub.Status) + " when the wait ran out."}
		}
		if err := sleep(ctx, o.PollInterval); err != nil {
			return nil, err
		}
	}
}

// DubRequestsService asks Reedapt for changes to a finished dub. Early access.
type DubRequestsService struct{ c *Client }

// Create hands Reedapt a request in plain words, such as "Make line 3 sound
// calmer". With Autonomy ask (the default) nothing changes until you answer
// the run with Runs.Answer. With decide, Reedapt applies what it judges right.
func (s *DubRequestsService) Create(ctx context.Context, dubID string, params *CreateRunParams, opts ...RequestOption) (*Run, error) {
	body := *params
	if body.Autonomy == "" {
		body.Autonomy = AutonomyAsk
	}
	var out Run
	// Not retried after a network failure: it could start the request twice.
	err := s.c.do(ctx, &request{method: "POST", path: "/dubs/" + escape(dubID) + "/requests", body: body, retry: retryUnsafe}, opts, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// DubLinesService reads and edits a finished dub's lines. Early access.
type DubLinesService struct{ c *Client }

// List gets every line with its timing, speaker, original and dubbed words, and delivery notes.
func (s *DubLinesService) List(ctx context.Context, dubID string, opts ...RequestOption) (*DubLines, error) {
	var out DubLines
	err := s.c.do(ctx, &request{method: "GET", path: "/dubs/" + escape(dubID) + "/lines", retry: retrySafe}, opts, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Edit changes up to 100 lines. They are voiced again in the background; the dub's files update when that finishes.
func (s *DubLinesService) Edit(ctx context.Context, dubID string, params *EditLinesParams, opts ...RequestOption) (*LinesEdited, error) {
	var out LinesEdited
	err := s.c.do(ctx, &request{method: "POST", path: "/dubs/" + escape(dubID) + "/lines/edit", body: params, retry: retryUnsafe}, opts, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// RunsService answers requests for changes to dubs. Early access.
type RunsService struct{ c *Client }

// Retrieve gets one run.
func (s *RunsService) Retrieve(ctx context.Context, runID string, opts ...RequestOption) (*Run, error) {
	var out Run
	err := s.c.do(ctx, &request{method: "GET", path: "/runs/" + escape(runID), retry: retrySafe}, opts, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Answer answers a run that is needs_input: apply the proposal or decline it.
func (s *RunsService) Answer(ctx context.Context, runID string, params *AnswerRunParams, opts ...RequestOption) (*Run, error) {
	var out Run
	err := s.c.do(ctx, &request{method: "POST", path: "/runs/" + escape(runID) + "/answer", body: params, retry: retryUnsafe}, opts, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// LanguagesService lists the languages dubs can be made in.
type LanguagesService struct{ c *Client }

// List gets the target languages.
func (s *LanguagesService) List(ctx context.Context, opts ...RequestOption) (*Languages, error) {
	var out Languages
	err := s.c.do(ctx, &request{method: "GET", path: "/languages", retry: retrySafe}, opts, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// UsageService reads minutes used and left.
type UsageService struct{ c *Client }

// Retrieve gets the plan, key mode and minutes left.
func (s *UsageService) Retrieve(ctx context.Context, opts ...RequestOption) (*Usage, error) {
	var out Usage
	err := s.c.do(ctx, &request{method: "GET", path: "/usage", retry: retrySafe}, opts, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Report gets a month of minutes and dubs, per key and per language. params may be nil.
func (s *UsageService) Report(ctx context.Context, params *UsageReportParams, opts ...RequestOption) (*UsageReport, error) {
	q := url.Values{}
	if params != nil && params.Month != "" {
		q.Set("month", params.Month)
	}
	var out UsageReport
	err := s.c.do(ctx, &request{method: "GET", path: "/usage/report", query: q, retry: retrySafe}, opts, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}
