// Receives Reedapt webhooks and checks their signatures.
//
//	REEDAPT_WEBHOOK_SECRET=whsec_... go run ./examples/webhook_server
package main

import (
	"errors"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/Reedapt/reedapt-go/webhook"
)

func main() {
	secret := os.Getenv("REEDAPT_WEBHOOK_SECRET")

	http.HandleFunc("/reedapt/webhooks", func(w http.ResponseWriter, r *http.Request) {
		// The signature covers the raw body, so read it before decoding anything.
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		event, err := webhook.ConstructEvent(body, r.Header.Get(webhook.SignatureHeader), secret)
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
			if dub, err := event.Dub(); err == nil {
				log.Println("dub finished:", dub.ID) // queue the work, then answer quickly
			}
		case webhook.DubChecked:
			if q, err := event.Quality(); err == nil {
				log.Printf("dub %s checked: %d fixed, %d to review", q.DubID, q.Fixed, q.ToReview)
			}
		} // Ignore types you don't know; every endpoint gets every event.

		// An event can arrive more than once: skip any event.ID you've handled.
		w.WriteHeader(http.StatusOK)
	})

	log.Fatal(http.ListenAndServe(":8080", nil))
}
