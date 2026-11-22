// Lists every dub on the account, newest first, then last month's usage.
//
//	REEDAPT_API_KEY=rk_test_... go run ./examples/list_dubs
package main

import (
	"context"
	"fmt"
	"log"

	reedapt "github.com/Reedapt/reedapt-go"
)

func main() {
	ctx := context.Background()
	client := reedapt.NewClient("")

	it := client.Dubs.ListAll(ctx, &reedapt.ListDubsParams{Limit: 50})
	for it.Next() {
		dub := it.Dub()
		fmt.Println(dub.ID, dub.Status)
	}
	if err := it.Err(); err != nil {
		log.Fatal(err)
	}

	report, err := client.Usage.Report(ctx, nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s: %.1f minutes, %d dubs completed\n", report.Month, report.Minutes, report.Dubs.Completed)
}
