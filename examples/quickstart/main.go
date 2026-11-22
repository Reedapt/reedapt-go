// Dubs a file into French and Spanish and prints where the results are.
//
//	REEDAPT_API_KEY=rk_test_... go run ./examples/quickstart ./interview.mp4
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	reedapt "github.com/Reedapt/reedapt-go"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: quickstart <file>")
	}
	ctx := context.Background()
	client := reedapt.NewClient(os.Getenv("REEDAPT_API_KEY"))

	created, err := client.DubPath(ctx, os.Args[1], &reedapt.DubParams{
		SourceLanguage:  "en",
		TargetLanguages: []string{"fr", "es"},
	})
	if err != nil {
		var apiErr *reedapt.Error
		if errors.As(err, &apiErr) {
			log.Fatalf("Reedapt said %s (status %d, request %s)", apiErr.Code, apiErr.Status, apiErr.RequestID)
		}
		log.Fatal(err)
	}
	for _, r := range created.Refused {
		fmt.Printf("%s could not start: %s\n", r.TargetLanguage, r.Message)
	}

	// In production, use webhooks (see the webhook_server example) instead of waiting.
	for _, started := range created.Dubs {
		dub, err := client.Dubs.WaitUntilDone(ctx, started.ID, nil)
		if err != nil {
			log.Fatal(err)
		}
		if dub.Status != reedapt.DubStatusCompleted {
			fmt.Println(*dub.TargetLanguage, "failed:", dub.Error.Message)
			continue
		}
		files, err := client.Dubs.Files(ctx, dub.ID)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(*dub.TargetLanguage, files.Files[0].URL)
	}
}
