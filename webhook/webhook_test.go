package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

const secret = "whsec_test"

const body = `{"id":"evt_1","type":"dub.completed","created_at":"x","data":{"dub":{"id":"job_1"}}}`

func sign(payload string, t int64, key string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(fmt.Sprintf("%d.%s", t, payload)))
	return fmt.Sprintf("t=%d,v1=%s", t, hex.EncodeToString(mac.Sum(nil)))
}

func nowSigned(payload string) string { return sign(payload, time.Now().Unix(), secret) }

func mustFail(t *testing.T, err error, contains string) {
	t.Helper()
	var v *VerificationError
	if !errors.As(err, &v) || !strings.Contains(v.Message, contains) {
		t.Fatalf("want a verification error containing %q, got %v", contains, err)
	}
}

// The same vector the Node and Python libraries compute: the signature below
// was produced by Node's crypto for this body, secret and timestamp.
func TestKnownVectorFromTheNodeLibrary(t *testing.T) {
	const header = "t=1700000000,v1=a377632fa861b8e4922db6be6fef6a87f8976810c896f2352ce4d3a3ac307e16"
	old := now
	defer func() { now = old }()
	now = func() time.Time { return time.Unix(1700000100, 0) }
	if err := Verify([]byte(body), header, secret); err != nil {
		t.Fatal(err)
	}
	if got := sign(body, 1700000000, secret); got != header {
		t.Fatalf("our signing differs from Node's: %s", got)
	}
}

func TestACorrectlySignedDeliveryVerifiesAndParses(t *testing.T) {
	event, err := ConstructEvent([]byte(body), nowSigned(body), secret)
	if err != nil {
		t.Fatal(err)
	}
	if event.Type != DubCompleted || event.ID != "evt_1" {
		t.Fatalf("%+v", event)
	}
	dub, err := event.Dub()
	if err != nil || dub.ID != "job_1" {
		t.Fatalf("%+v %v", dub, err)
	}
	if _, err := event.Quality(); err == nil {
		t.Fatal("a dub.completed event has no quality")
	}
}

func TestAChangedBodyIsRefused(t *testing.T) {
	mustFail(t, Verify([]byte(strings.Replace(body, "job_1", "job_2", 1)), nowSigned(body), secret), "doesn't match")
}

func TestReEncodedJSONIsRefused(t *testing.T) {
	reencoded := strings.Replace(body, `{"id"`, "{\n  \"id\"", 1)
	mustFail(t, Verify([]byte(reencoded), nowSigned(body), secret), "doesn't match")
}

func TestTheWrongSecretIsRefused(t *testing.T) {
	mustFail(t, Verify([]byte(body), sign(body, time.Now().Unix(), "whsec_other"), secret), "doesn't match")
}

func TestAnOldSignatureIsRefusedWithinAConfigurableTolerance(t *testing.T) {
	old := sign(body, time.Now().Unix()-600, secret)
	mustFail(t, Verify([]byte(body), old, secret), "too old")
	if err := Verify([]byte(body), old, secret, WithTolerance(900*time.Second)); err != nil {
		t.Fatal(err)
	}
}

func TestAMissingOrMalformedHeaderIsRefused(t *testing.T) {
	mustFail(t, Verify([]byte(body), "", secret), "missing")
	mustFail(t, Verify([]byte(body), "nonsense", secret), "malformed")
	mustFail(t, Verify([]byte(body), "t=abc,v1=00", secret), "malformed")
	mustFail(t, Verify([]byte(body), "t=1700000000", secret), "malformed")
}

func TestAnyOneOfSeveralV1SignaturesMayMatch(t *testing.T) {
	good := nowSigned(body)
	parts := strings.Split(good, ",")
	header := parts[0] + ",v1=" + strings.Repeat("0", 64) + "," + parts[1]
	if err := Verify([]byte(body), header, secret); err != nil {
		t.Fatal(err)
	}
	// Non-hex and wrong-length signatures are just non-matching.
	mustFail(t, Verify([]byte(body), parts[0]+",v1=zz,v1=ab", secret), "doesn't match")
}

func TestADubCheckedDeliveryParsesToTheDubAndItsQualityReport(t *testing.T) {
	payload := `{"id":"evt_2","type":"dub.checked","created_at":"x","data":{"dub":{"id":"job_1"},"quality":{"dub_id":"job_1","checked":true,"listened":true,"lines":10,"fixed":1,"to_review":0,"flagged":[]}}}`
	event, err := ConstructEvent([]byte(payload), nowSigned(payload), secret)
	if err != nil || event.Type != DubChecked {
		t.Fatalf("%+v %v", event, err)
	}
	q, err := event.Quality()
	if err != nil || !q.Checked || q.Lines != 10 || q.Fixed != 1 {
		t.Fatalf("%+v %v", q, err)
	}
}

func TestABatchCompletedDeliveryParsesToItsSummary(t *testing.T) {
	payload := `{"id":"evt_3","type":"batch.completed","created_at":"x","data":{"batch":{"batch_id":"b1","status":"done","video_count":2,"jobs_total":4,"jobs_done":3,"jobs_failed":1,"dubs":[]}}}`
	event, err := ConstructEvent([]byte(payload), nowSigned(payload), secret)
	if err != nil {
		t.Fatal(err)
	}
	b, err := event.Batch()
	if err != nil || b.JobsFailed != 1 || b.BatchID != "b1" {
		t.Fatalf("%+v %v", b, err)
	}
}

func TestConstructEventRefusesWhatVerifyRefuses(t *testing.T) {
	_, err := ConstructEvent([]byte(body), "", secret)
	mustFail(t, err, "missing")
}
