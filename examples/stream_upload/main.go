// Uploads from an io.Reader with a known length, with progress, then starts a dub.
// The reader here is a file, but it could be an HTTP download or an object store stream.
//
//	REEDAPT_API_KEY=rk_test_... go run ./examples/stream_upload ./film.mov
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	reedapt "github.com/Reedapt/reedapt-go"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: stream_upload <file>")
	}
	f, err := os.Open(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	client := reedapt.NewClient("") // reads REEDAPT_API_KEY

	upload, err := client.Uploads.UploadFile(ctx, &reedapt.UploadFileParams{
		Filename:    filepath.Base(os.Args[1]),
		Size:        info.Size(),
		Body:        f,
		Concurrency: 4,
		OnProgress: func(p reedapt.UploadProgress) {
			fmt.Printf("\r%d%%", 100*p.UploadedBytes/p.TotalBytes)
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	created, err := client.Dubs.Create(ctx, &reedapt.CreateDubsParams{
		UploadID:        upload.ID,
		SourceLanguage:  "en",
		TargetLanguages: []string{"yo"},
		Metadata:        map[string]string{"order_id": "1042"},
		Instructions:    reedapt.String("Keep the speaker's name, Adaeze, as it is. Warm, informal tone."), // paid plans
	}, reedapt.WithIdempotencyKey("order-1042"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("\nstarted", created.Dubs[0].ID)
}
