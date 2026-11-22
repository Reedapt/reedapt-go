# Reedapt Go library

The official Go library for the [Reedapt API](https://docs.reedapt.com). Upload video or audio, dub it into other languages in each speaker's own voice, and get the files back.

## Install

```bash
go get github.com/Reedapt/reedapt-go
```

Go 1.21 or later. It uses only the standard library.

## Quickstart

Make a key under **Developers** in [Reedapt](https://dub.reedapt.com/developers) and keep it in `REEDAPT_API_KEY`. Test keys are free and dub the first 60 seconds with a watermark.

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	reedapt "github.com/Reedapt/reedapt-go"
)

func main() {
	ctx := context.Background()
	client := reedapt.NewClient(os.Getenv("REEDAPT_API_KEY")) // "" also reads REEDAPT_API_KEY

	created, err := client.DubPath(ctx, "./interview.mp4", &reedapt.DubParams{
		SourceLanguage:  "en",
		TargetLanguages: []string{"fr", "es"},
	})
	if err != nil {
		log.Fatal(err)
	}

	for _, started := range created.Dubs {
		dub, err := client.Dubs.WaitUntilDone(ctx, started.ID, nil)
		if err != nil {
			log.Fatal(err)
		}
		if dub.Status == reedapt.DubStatusCompleted {
			files, _ := client.Dubs.Files(ctx, dub.ID)
			fmt.Println(*dub.TargetLanguage, files.Files[0].URL)
		} else {
			fmt.Println(*dub.TargetLanguage, "failed:", dub.Error.Message)
		}
	}
}
```

`DubPath` uploads a file and starts the dubs. `DubReader` does the same from an `io.Reader`, and `DubURL` from a public `https://` link that Reedapt downloads itself. In production, use [webhooks](#webhooks) instead of `WaitUntilDone`.

More programs are in [`examples/`](examples): `quickstart`, `stream_upload`, `webhook_server` and `list_dubs`.

Every method takes a `context.Context` first, and the last arguments of every method are optional [per-request options](#configuration).

Fields the API can leave out or send as `null` are pointers (`*string`, `*float64`). `reedapt.String`, `reedapt.Bool` and `reedapt.Int` make a pointer from a value.

## Uploads

`Uploads.UploadFile` sends a file straight to storage in parts, four at a time, signs more links as it needs them, and retries parts that fail. It streams from an `io.Reader` and needs the length up front:

```go
upload, err := client.Uploads.UploadFile(ctx, &reedapt.UploadFileParams{
	Filename:    "film.mov",
	Size:        size,
	Body:        reader,
	Concurrency: 4,
	OnProgress: func(p reedapt.UploadProgress) {
		fmt.Printf("%d%%\n", 100*p.UploadedBytes/p.TotalBytes)
	},
})

created, err := client.Dubs.Create(ctx, &reedapt.CreateDubsParams{
	UploadID:        upload.ID,
	SourceLanguage:  "en",
	TargetLanguages: []string{"yo"},
	Metadata:        map[string]string{"order_id": "1042"},
	Instructions:    reedapt.String("Keep the speaker's name, Adaeze, as it is. Warm, informal tone."), // paid plans
})
```

If `Body` is also an `io.ReaderAt` (an `*os.File` or `*bytes.Reader`), parts are read straight from it and nothing is held in memory. Otherwise the reader is read once, in order, and `Concurrency` parts (each 16 MiB or more) are held in memory at a time. If it ends before `Size` bytes, you get an `*UploadError`.

`Uploads.UploadPath(ctx, path, "")` uploads a file from disk, named after the path. For a file that's already online:

```go
upload, err := client.Uploads.FromURL(ctx, "https://example.com/media/talk.mp4", nil)
```

## Dubs

```go
client.Dubs.Retrieve(ctx, "job_3f9a2c7e1b4d")
client.Dubs.List(ctx, &reedapt.ListDubsParams{Limit: 20})
client.Dubs.Files(ctx, "job_3f9a2c7e1b4d")      // links last an hour
client.Dubs.Cancel(ctx, "job_3f9a2c7e1b4d")     // minutes are returned
client.Dubs.Transcript(ctx, "job_3f9a2c7e1b4d") // every line, with timing, speaker and translation
client.Dubs.Quality(ctx, "job_3f9a2c7e1b4d")    // Reedapt's check: lines fixed and lines left to review
srt, err := client.Dubs.Subtitles(ctx, "job_3f9a2c7e1b4d", nil) // SRT text
vtt, err := client.Dubs.Subtitles(ctx, "job_3f9a2c7e1b4d", &reedapt.SubtitlesParams{Format: "vtt", Language: "source"})

client.Languages.List(ctx)                                        // {TargetLanguages: [...]}
client.Usage.Retrieve(ctx)                                        // plan, key mode and minutes left
client.Usage.Report(ctx, &reedapt.UsageReportParams{Month: "2026-10"}) // minutes and dubs per key and per language
```

### Pagination

`Dubs.List` returns one page (`HasMore` says whether there is another). `Dubs.ListAll` walks every dub, newest first, fetching pages as you go:

```go
it := client.Dubs.ListAll(ctx, &reedapt.ListDubsParams{Limit: 50})
for it.Next() {
	dub := it.Dub()
	fmt.Println(dub.ID, dub.Status)
}
if err := it.Err(); err != nil {
	log.Fatal(err)
}
```

## Ask Reedapt for changes

Early access: this is released to some paid accounts first, and other accounts get a `403` with the code `NOT_AVAILABLE`. Describe a change in plain words and Reedapt either answers, proposes a change, or makes it. With `Autonomy: reedapt.AutonomyAsk` (the default) nothing changes until you say so:

```go
run, err := client.Dubs.Requests.Create(ctx, "job_3f9a2c7e1b4d", &reedapt.CreateRunParams{
	Text: "Is line 3 natural? If not, make it sound calmer.",
})
fmt.Println(run.Reply)

if run.Status == reedapt.RunStatusNeedsInput {
	fmt.Println(run.Proposal.Summary, run.Proposal.Lines) // Lines = how many it would change
	run, err = client.Runs.Answer(ctx, run.ID, &reedapt.AnswerRunParams{Choice: reedapt.RunChoiceApply}) // or RunChoiceDecline
}
// run.Status is done, applied, declined or failed; run.AppliedLines counts the lines changed.
client.Runs.Retrieve(ctx, run.ID)
```

You can also read and edit lines yourself. Edited lines are voiced again in the background:

```go
lines, err := client.Dubs.Lines.List(ctx, "job_3f9a2c7e1b4d")
edited, err := client.Dubs.Lines.Edit(ctx, "job_3f9a2c7e1b4d", &reedapt.EditLinesParams{
	Lines: []reedapt.LineEdit{
		{Line: 3, Text: reedapt.String("Bienvenue chez nous."), DeliveryStyle: reedapt.String("warm")},
		{Line: 4, ClearDeliveryStyle: true}, // sends null
	},
}) // edited.Status == "revoicing"
```

A request counts against your plan's allowance for these requests; when it's used up you get `AGENT_ALLOWANCE_USED` (429) until it renews.

## Webhooks

Add an endpoint under **Developers**, or with `client.WebhookEndpoints.Create`, and keep its `whsec_` secret (it is shown once, in the answer's `Secret`):

```go
endpoint, err := client.WebhookEndpoints.Create(ctx, &reedapt.CreateWebhookEndpointParams{URL: "https://example.com/reedapt/webhooks"})
client.WebhookEndpoints.List(ctx)
client.WebhookEndpoints.SendTest(ctx, endpoint.ID)                // sends a webhook.test event
client.WebhookEndpoints.Deliveries(ctx, endpoint.ID, 20)          // recent deliveries and what the endpoint answered
client.WebhookEndpoints.ResendDelivery(ctx, endpoint.ID, "whd_1") // sends a past delivery again
client.WebhookEndpoints.Delete(ctx, endpoint.ID)
```

Check every delivery against the raw body, with the `webhook` package. It signs `"<t>.<body>"` with HMAC-SHA256 and compares in constant time, as the Node.js and Python libraries do, and refuses signatures older than 5 minutes (`webhook.WithTolerance` changes that):

```go
import "github.com/Reedapt/reedapt-go/webhook"

func handler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body) // the raw bytes, not re-encoded JSON
	event, err := webhook.ConstructEvent(body, r.Header.Get("Reedapt-Signature"), os.Getenv("REEDAPT_WEBHOOK_SECRET"))
	if err != nil {
		var bad *webhook.VerificationError
		if errors.As(err, &bad) {
			http.Error(w, "bad signature", http.StatusBadRequest)
			return
		}
		http.Error(w, "bad event", http.StatusBadRequest)
		return
	}
	switch event.Type {
	case webhook.DubCompleted:
		dub, _ := event.Dub() // the finished dub. Queue the work, then answer quickly.
		_ = dub
	case webhook.DubChecked:
		quality, _ := event.Quality() // the lines Reedapt fixed and the ones left to review
		_ = quality
	} // Ignore types you don't know; every endpoint gets every event.
	w.WriteHeader(http.StatusOK)
}
```

`webhook.Verify(payload, header, secret)` only checks the signature. `event.Dub()`, `event.Quality()` and `event.Batch()` decode the event's data. An event can arrive more than once, so skip any `event.ID` you've already handled.

## Errors

Failed requests return a `*reedapt.Error` with the HTTP `Status`, a stable `Code`, a plain-English `Message` and the `RequestID` to quote to support. Use `errors.As`:

```go
_, err := client.Dubs.Create(ctx, params)
var apiErr *reedapt.Error
if errors.As(err, &apiErr) {
	if apiErr.Code == "TOO_MANY_DUBS_RUNNING" {
		// Start more once some finish.
	}
	log.Println(apiErr.Status, apiErr.Code, apiErr.Message, apiErr.RequestID)
}
```

| Error | When |
| --- | --- |
| `*reedapt.Error` | The API refused the request. Helpers tell the kinds apart: `IsAuthentication` (401), `IsNotFound` (404), `IsConflict` (409), `IsInvalidRequest` (400, 411, 413, 422; `Fields` lists invalid fields), `IsRateLimit` (429; `RetryAfter()`), `IsServer` (5xx). |
| `*reedapt.ConnectionError` | No answer arrived. `Timeout` is true if the request took longer than the timeout. |
| `*reedapt.UploadError` | A part couldn't be sent to storage, or the file couldn't be read. |
| `*reedapt.WaitError` | A `WaitUntil...` helper ran out of time, or the upload failed. |
| `*webhook.VerificationError` | A delivery's signature didn't check out. |
| `reedapt.ErrNoAPIKey` | The client was made without a key. |

If your context is cancelled or its deadline passes, the context's error (`context.Canceled`, `context.DeadlineExceeded`) is returned and the request isn't retried.

The [error codes](https://docs.reedapt.com/errors) are listed in the docs.

## Retries and repeated requests

Network failures, `500`, `502`, `503`, `504` and per-minute rate limits are retried twice, with backoff, honouring `Retry-After`. Creating uploads and dubs always sends an `Idempotency-Key`, so a retry never makes a second dub. Requests that could do the work twice (adding a webhook endpoint, asking for changes, editing lines) aren't retried after a network failure. Pass your own key to make a repeat safe across restarts:

```go
client.Dubs.Create(ctx, params, reedapt.WithIdempotencyKey("order-1042"))
```

## Configuration

```go
client := reedapt.NewClient(os.Getenv("REEDAPT_API_KEY"),
	reedapt.WithBaseURL("https://api.reedapt.com/v1"), // or REEDAPT_BASE_URL
	reedapt.WithHTTPClient(&http.Client{Transport: myTransport}),
	reedapt.WithTimeout(60*time.Second), // per request, response body included
	reedapt.WithMaxRetries(2),
)

client.Dubs.Retrieve(ctx, id,
	reedapt.WithRequestTimeout(10*time.Second),
	reedapt.WithRequestMaxRetries(0),
)
```

Test keys (`rk_test_...`) and live keys use the same client. Requests are sent with the header `User-Agent: reedapt-go/<version>`. A `*reedapt.Client` is safe for concurrent use.

Keep keys on your server. Never put one in a web page or an app people can download.

## Links

- [Documentation](https://docs.reedapt.com)
- [API reference](https://docs.reedapt.com/api-reference/introduction)
- [Status](https://status.reedapt.com)
- Support: support@reedapt.com
