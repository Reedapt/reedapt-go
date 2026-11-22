package reedapt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

const (
	signBatch    = 100
	partAttempts = 4
)

// UploadsService sends files to Reedapt.
type UploadsService struct{ c *Client }

// Create starts an upload. Most code wants UploadFile, UploadPath or FromURL instead.
func (s *UploadsService) Create(ctx context.Context, params *CreateUploadParams, opts ...RequestOption) (*Upload, error) {
	var out Upload
	err := s.c.do(ctx, &request{method: "POST", path: "/uploads", body: params, retry: retrySafe, idempotent: true}, opts, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// SignParts signs links for up to 100 more parts.
func (s *UploadsService) SignParts(ctx context.Context, uploadID string, partNumbers []int, opts ...RequestOption) ([]PartLink, error) {
	var out struct {
		Parts []PartLink `json:"parts"`
	}
	body := map[string]any{"part_numbers": partNumbers}
	err := s.c.do(ctx, &request{method: "POST", path: "/uploads/" + escape(uploadID) + "/parts", body: body, retry: retrySafe}, opts, &out)
	if err != nil {
		return nil, err
	}
	return out.Parts, nil
}

// Complete finishes an upload once every part is in storage.
func (s *UploadsService) Complete(ctx context.Context, uploadID string, parts []UploadedPart, opts ...RequestOption) (*Upload, error) {
	var out Upload
	body := map[string]any{"parts": parts}
	err := s.c.do(ctx, &request{method: "POST", path: "/uploads/" + escape(uploadID) + "/complete", body: body, retry: retrySafe}, opts, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Retrieve gets one upload.
func (s *UploadsService) Retrieve(ctx context.Context, uploadID string, opts ...RequestOption) (*Upload, error) {
	var out Upload
	err := s.c.do(ctx, &request{method: "GET", path: "/uploads/" + escape(uploadID), retry: retrySafe}, opts, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// UploadProgress is how much of a file has reached storage.
type UploadProgress struct {
	UploadedBytes int64
	TotalBytes    int64
}

// UploadFileParams describes a file to upload.
type UploadFileParams struct {
	// Filename is what the file is called, with its extension.
	Filename string
	// Size is the exact number of bytes Body will deliver.
	Size int64
	// Body is the file. If it is also an io.ReaderAt (such as an *os.File or
	// *bytes.Reader), parts are read straight from it without being held in
	// memory; otherwise it is read once, in order, and Concurrency parts are
	// held in memory at a time.
	Body io.Reader
	// Concurrency is how many parts are sent at once. Defaults to 4.
	Concurrency int
	// OnProgress is called, from several goroutines one at a time, after each part.
	OnProgress func(UploadProgress)
}

// UploadFile sends a file to storage in parts, in parallel, and completes the
// upload. Expired links are signed again and failed parts are retried. Pass
// WithIdempotencyKey to make a repeat safe.
func (s *UploadsService) UploadFile(ctx context.Context, params *UploadFileParams, opts ...RequestOption) (*Upload, error) {
	if params.Filename == "" {
		return nil, &UploadError{"Pass a Filename, with its extension, when uploading from a reader."}
	}
	if params.Body == nil || params.Size < 0 {
		return nil, &UploadError{"Pass a Body and its Size."}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	follow := newConfig(opts).without()

	upload, err := s.Create(ctx, &CreateUploadParams{Filename: params.Filename, Size: params.Size}, opts...)
	if err != nil {
		return nil, err
	}
	if upload.PartCount < 1 || upload.PartSize < 1 {
		return nil, &UploadError{fmt.Sprintf("Upload %s came back without parts to send.", upload.ID)}
	}

	var mu sync.Mutex // guards links
	links := map[int]string{}
	for _, p := range upload.Parts {
		links[p.PartNumber] = p.URL
	}
	linkFor := func(n int, refresh bool) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if refresh {
			delete(links, n)
		}
		if _, ok := links[n]; !ok {
			first, last := n, n
			if !refresh {
				first = ((n-1)/signBatch)*signBatch + 1
				last = first + signBatch - 1
				if last > upload.PartCount {
					last = upload.PartCount
				}
			}
			var wanted []int
			for p := first; p <= last; p++ {
				if _, ok := links[p]; !ok {
					wanted = append(wanted, p)
				}
			}
			signed, err := s.SignParts(ctx, upload.ID, wanted, follow...)
			if err != nil {
				return "", err
			}
			for _, p := range signed {
				links[p.PartNumber] = p.URL
			}
		}
		u, ok := links[n]
		if !ok {
			return "", &UploadError{fmt.Sprintf("No link was signed for part %d of %s.", n, upload.ID)}
		}
		return u, nil
	}

	var base int64
	readerAt, seekable := params.Body.(io.ReaderAt)
	if seeker, ok := params.Body.(io.Seeker); ok && seekable {
		if base, err = seeker.Seek(0, io.SeekCurrent); err != nil {
			return nil, &UploadError{"Reading the file failed: " + err.Error()}
		}
	}

	workers := params.Concurrency
	if workers < 1 {
		workers = 4
	}
	if workers > upload.PartCount {
		workers = upload.PartCount
	}
	sem := make(chan struct{}, workers)
	var (
		wg       sync.WaitGroup
		resMu    sync.Mutex // guards done, uploaded, firstErr
		done     []UploadedPart
		uploaded int64
		firstErr error
	)
	fail := func(err error) {
		resMu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		resMu.Unlock()
		cancel() // one failed part stops the others from starting new parts
	}

loop:
	for n := 1; n <= upload.PartCount; n++ {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break loop
		}
		start := int64(n-1) * upload.PartSize
		end := start + upload.PartSize
		if end > params.Size {
			end = params.Size
		}
		var open func() io.Reader
		if seekable {
			open = func() io.Reader { return io.NewSectionReader(readerAt, base+start, end-start) }
		} else {
			buf := make([]byte, end-start)
			if _, err := io.ReadFull(params.Body, buf); err != nil {
				<-sem
				fail(&UploadError{fmt.Sprintf("The file ended before its %d bytes were read: %v", params.Size, err)})
				break loop
			}
			open = func() io.Reader { return bytesReader(buf) }
		}
		wg.Add(1)
		go func(n int, size int64) {
			defer wg.Done()
			defer func() { <-sem }()
			etag, err := s.putPart(ctx, upload.ID, n, size, open, linkFor)
			if err != nil {
				fail(err)
				return
			}
			resMu.Lock()
			done = append(done, UploadedPart{PartNumber: n, ETag: etag})
			uploaded += size
			progress := UploadProgress{UploadedBytes: uploaded, TotalBytes: params.Size}
			if params.OnProgress != nil {
				params.OnProgress(progress)
			}
			resMu.Unlock()
		}(n, end-start)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	sortParts(done)
	out, err := s.Complete(ctx, upload.ID, done, follow...)
	if err != nil {
		// A retried complete that had already gone through.
		var apiErr *Error
		if errors.As(err, &apiErr) && apiErr.Status == 409 && apiErr.Code == "UPLOAD_COMPLETE" {
			return s.Retrieve(ctx, upload.ID, follow...)
		}
		return nil, err
	}
	return out, nil
}

// UploadPath uploads the file at path, named after it unless filename is not empty.
func (s *UploadsService) UploadPath(ctx context.Context, path string, filename string, opts ...RequestOption) (*Upload, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if filename == "" {
		filename = filepath.Base(path)
	}
	return s.UploadFile(ctx, &UploadFileParams{Filename: filename, Size: info.Size(), Body: f}, opts...)
}

func (s *UploadsService) putPart(ctx context.Context, uploadID string, n int, size int64, open func() io.Reader, linkFor func(int, bool) (string, error)) (string, error) {
	refresh := false
	lastProblem := ""
	for attempt := 0; attempt < partAttempts; attempt++ {
		if attempt > 0 {
			if err := sleep(ctx, s.c.backoff(attempt)); err != nil {
				return "", err
			}
		}
		link, err := linkFor(n, refresh)
		if err != nil {
			return "", err
		}
		refresh = false
		// No API key here: the signed link carries its own permission.
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, link, io.NopCloser(open()))
		if err != nil {
			return "", &UploadError{fmt.Sprintf("Part %d of upload %s failed: %v.", n, uploadID, err)}
		}
		req.ContentLength = size
		res, err := s.c.httpClient.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			lastProblem = err.Error()
			continue
		}
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if res.StatusCode >= 200 && res.StatusCode < 300 {
			etag := res.Header.Get("ETag")
			if etag == "" {
				return "", &UploadError{fmt.Sprintf("Storage accepted part %d but sent no ETag.", n)}
			}
			return etag, nil
		}
		lastProblem = "storage answered " + strconv.Itoa(res.StatusCode)
		switch {
		case res.StatusCode == 403 || res.StatusCode == 400:
			// An expired or refused link: sign this part again.
			refresh = true
		case res.StatusCode < 500 && res.StatusCode != 408 && res.StatusCode != 429:
			return "", &UploadError{fmt.Sprintf("Part %d of upload %s failed: %s.", n, uploadID, lastProblem)}
		}
	}
	return "", &UploadError{fmt.Sprintf("Part %d of upload %s failed: %s.", n, uploadID, lastProblem)}
}

// FromURLParams describes a public link for Reedapt to download.
type FromURLParams struct {
	// Filename is only needed when the link doesn't end in a file name. Keep the extension.
	Filename string
	// NoWait returns as soon as the download starts instead of when it is done.
	NoWait bool
	// Wait tunes the wait. Only PollInterval and Timeout are used.
	Wait *WaitOptions
}

// FromURL has Reedapt download a public link. It waits until the upload is
// ready unless params.NoWait is set. params may be nil.
func (s *UploadsService) FromURL(ctx context.Context, sourceURL string, params *FromURLParams, opts ...RequestOption) (*Upload, error) {
	if params == nil {
		params = &FromURLParams{}
	}
	upload, err := s.Create(ctx, &CreateUploadParams{SourceURL: sourceURL, Filename: params.Filename}, opts...)
	if err != nil {
		return nil, err
	}
	if params.NoWait {
		return upload, nil
	}
	return s.WaitUntilReady(ctx, upload.ID, params.Wait)
}

// WaitUntilReady polls an upload until it can be dubbed. It returns a *WaitError if it failed.
func (s *UploadsService) WaitUntilReady(ctx context.Context, uploadID string, opts *WaitOptions) (*Upload, error) {
	var o WaitOptions
	if opts != nil {
		o = *opts
	}
	if o.PollInterval <= 0 {
		o.PollInterval = 10 * time.Second
	}
	if o.Timeout <= 0 {
		o.Timeout = 3 * time.Hour
	}
	deadline := time.Now().Add(o.Timeout)
	for {
		upload, err := s.Retrieve(ctx, uploadID)
		if err != nil {
			return nil, err
		}
		switch upload.Status {
		case UploadStatusUploaded:
			return upload, nil
		case UploadStatusFailed:
			reason := "no reason given"
			if upload.Error != nil {
				reason = *upload.Error
			}
			return nil, &WaitError{fmt.Sprintf("Upload %s failed: %s.", uploadID, reason)}
		case UploadStatusUsed:
			return nil, &WaitError{fmt.Sprintf("Upload %s was already dubbed. Upload the file again.", uploadID)}
		}
		if time.Now().Add(o.PollInterval).After(deadline) {
			return nil, &WaitError{fmt.Sprintf("Upload %s was still %s when the wait ran out.", uploadID, upload.Status)}
		}
		if err := sleep(ctx, o.PollInterval); err != nil {
			return nil, err
		}
	}
}

// DubParams is CreateDubsParams without the upload, for the Dub... helpers.
type DubParams struct {
	SourceLanguage  string
	TargetLanguages []string
	VoiceCloning    *bool
	NumSpeakers     *int
	MediaCategory   *string
	Instructions    *string
	Metadata        map[string]string
}

func (p *DubParams) create(uploadID string) *CreateDubsParams {
	return &CreateDubsParams{
		UploadID: uploadID, SourceLanguage: p.SourceLanguage, TargetLanguages: p.TargetLanguages,
		VoiceCloning: p.VoiceCloning, NumSpeakers: p.NumSpeakers, MediaCategory: p.MediaCategory,
		Instructions: p.Instructions, Metadata: p.Metadata,
	}
}

// suffixed derives one idempotency key for each of a helper's two calls from the caller's.
func suffixed(opts []RequestOption, suffix string) []RequestOption {
	cfg := newConfig(opts)
	if cfg.idempotencyKey == "" {
		return opts
	}
	key := cfg.idempotencyKey + ":" + suffix
	return append(append([]RequestOption{}, opts...), WithIdempotencyKey(key))
}

// DubPath uploads the file at path and starts a dub for each target language.
// It returns once the dubs have started, not when they finish. WithIdempotencyKey
// is used for both the upload and the dubs, with a suffix for each.
func (c *Client) DubPath(ctx context.Context, path string, params *DubParams, opts ...RequestOption) (*DubsCreated, error) {
	upload, err := c.Uploads.UploadPath(ctx, path, "", suffixed(opts, "upload")...)
	if err != nil {
		return nil, err
	}
	return c.Dubs.Create(ctx, params.create(upload.ID), suffixed(opts, "dubs")...)
}

// DubReader uploads a reader's bytes and starts a dub for each target language.
// See UploadFileParams for how Body is read.
func (c *Client) DubReader(ctx context.Context, file *UploadFileParams, params *DubParams, opts ...RequestOption) (*DubsCreated, error) {
	upload, err := c.Uploads.UploadFile(ctx, file, suffixed(opts, "upload")...)
	if err != nil {
		return nil, err
	}
	return c.Dubs.Create(ctx, params.create(upload.ID), suffixed(opts, "dubs")...)
}

// DubURL has Reedapt download a public link, then starts a dub for each target language.
func (c *Client) DubURL(ctx context.Context, sourceURL string, params *DubParams, opts ...RequestOption) (*DubsCreated, error) {
	upload, err := c.Uploads.FromURL(ctx, sourceURL, nil, suffixed(opts, "upload")...)
	if err != nil {
		return nil, err
	}
	return c.Dubs.Create(ctx, params.create(upload.ID), suffixed(opts, "dubs")...)
}
