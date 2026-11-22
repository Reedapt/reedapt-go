package reedapt

import "encoding/json"

// Objects may gain fields over time, so unknown fields are normal. Fields the
// API can leave out or send as null are pointers.

// UploadStatus is where an upload is: uploading, fetching, uploaded, used or failed.
type UploadStatus string

const (
	UploadStatusUploading UploadStatus = "uploading"
	UploadStatusFetching  UploadStatus = "fetching"
	UploadStatusUploaded  UploadStatus = "uploaded"
	UploadStatusUsed      UploadStatus = "used"
	UploadStatusFailed    UploadStatus = "failed"
)

// PartLink is a signed storage link for one part of an upload.
type PartLink struct {
	PartNumber int    `json:"part_number"`
	URL        string `json:"url"`
}

// Upload is a file sent to Reedapt, or being fetched from a link.
type Upload struct {
	ID            string       `json:"id"`
	Object        string       `json:"object,omitempty"`
	Filename      string       `json:"filename"`
	Size          int64        `json:"size"`
	Status        UploadStatus `json:"status"`
	Error         *string      `json:"error,omitempty"`
	PartSize      int64        `json:"part_size"`
	PartCount     int          `json:"part_count"`
	Parts         []PartLink   `json:"parts,omitempty"`
	PartsExpireAt *string      `json:"parts_expire_at,omitempty"`
}

// UploadedPart is a part sent to storage and the ETag storage answered with.
type UploadedPart struct {
	PartNumber int    `json:"part_number"`
	ETag       string `json:"etag"`
}

// CreateUploadParams starts an upload from a file's name and size, or from a public link.
type CreateUploadParams struct {
	Filename  string `json:"filename,omitempty"`
	Size      int64  `json:"size,omitempty"`
	SourceURL string `json:"source_url,omitempty"`
}

// DubStatus is where a dub is: queued, processing, completed or failed.
type DubStatus string

const (
	DubStatusQueued     DubStatus = "queued"
	DubStatusProcessing DubStatus = "processing"
	DubStatusCompleted  DubStatus = "completed"
	DubStatusFailed     DubStatus = "failed"
)

// DubError says why a dub failed.
type DubError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Dub is one video or audio file dubbed into one language.
type Dub struct {
	ID              string            `json:"id"`
	Object          string            `json:"object,omitempty"`
	Status          DubStatus         `json:"status"`
	Stage           *string           `json:"stage,omitempty"`
	Progress        float64           `json:"progress"`
	SourceLanguage  *string           `json:"source_language"`
	TargetLanguage  *string           `json:"target_language"`
	Audio           bool              `json:"audio"`
	Test            bool              `json:"test"`
	PreviewSeconds  *float64          `json:"preview_seconds,omitempty"`
	DurationMinutes *float64          `json:"duration_minutes,omitempty"`
	CreatedAt       string            `json:"created_at"`
	StartedAt       *string           `json:"started_at,omitempty"`
	CompletedAt     *string           `json:"completed_at,omitempty"`
	Error           *DubError         `json:"error,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

// RefusedLanguage is a target language that could not start.
type RefusedLanguage struct {
	TargetLanguage string `json:"target_language"`
	Code           string `json:"code"`
	Message        string `json:"message"`
}

// DubsCreated is the dubs that started, one per target language, and the languages that couldn't.
type DubsCreated struct {
	Dubs    []Dub             `json:"dubs"`
	Refused []RefusedLanguage `json:"refused,omitempty"`
}

// DubList is one page of dubs.
type DubList struct {
	Data    []Dub `json:"data"`
	HasMore bool  `json:"has_more"`
}

// CreateDubsParams starts one dub per target language of an uploaded file.
type CreateDubsParams struct {
	UploadID        string   `json:"upload_id"`
	SourceLanguage  string   `json:"source_language"`
	TargetLanguages []string `json:"target_languages"`
	VoiceCloning    *bool    `json:"voice_cloning,omitempty"`
	NumSpeakers     *int     `json:"num_speakers,omitempty"`
	MediaCategory   *string  `json:"media_category,omitempty"`
	// Instructions are notes for the dub, up to 2000 characters: names to
	// keep, tone, how to address the audience. Followed on paid plans.
	Instructions *string           `json:"instructions,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// ListDubsParams pages through dubs, newest first. Zero values use the API's defaults.
type ListDubsParams struct {
	Limit  int
	Offset int
}

// FileKind is what a downloadable file is: dub, dubbed_audio_wav, voice or background.
type FileKind string

// FileLink is a download link, good for an hour.
type FileLink struct {
	Kind     FileKind `json:"kind"`
	Filename string   `json:"filename"`
	URL      string   `json:"url"`
}

// DubFiles is a completed dub's download links.
type DubFiles struct {
	Files     []FileLink `json:"files"`
	ExpiresAt string     `json:"expires_at"`
}

// TranscriptLine is one line of a completed dub.
type TranscriptLine struct {
	StartMS int `json:"start_ms"`
	EndMS   int `json:"end_ms"`
	// Speaker is the same label throughout a dub, such as SPEAKER_00.
	Speaker      *string `json:"speaker,omitempty"`
	OriginalText string  `json:"original_text"`
	// Text is the line in the dub's language.
	Text string `json:"text"`
}

// Transcript is a completed dub's lines in time order, edits made in the editor included.
type Transcript struct {
	DubID          string           `json:"dub_id"`
	SourceLanguage *string          `json:"source_language,omitempty"`
	TargetLanguage *string          `json:"target_language,omitempty"`
	Lines          []TranscriptLine `json:"lines"`
}

// SubtitlesParams picks a subtitle file. Zero values use the defaults.
type SubtitlesParams struct {
	// Format is "srt" (the default) or "vtt".
	Format string
	// Language is "target" (the default) for the dub's language, "source" for the original words.
	Language string
}

// FlaggedLine is a line Reedapt's check flagged.
type FlaggedLine struct {
	Line    int `json:"line"`
	StartMS int `json:"start_ms"`
	// Checks says what was found, such as "fit" (the line doesn't fit its time).
	Checks []string `json:"checks"`
	// Fixed is "shortened" or "revoiced" if Reedapt dealt with it, nil if it's left for you.
	Fixed *string `json:"fixed"`
}

// Quality is Reedapt's check of a finished dub. Checked is false until the measurement checks have run.
type Quality struct {
	DubID    string        `json:"dub_id"`
	Checked  bool          `json:"checked"`
	Listened bool          `json:"listened"`
	Lines    int           `json:"lines"`
	Fixed    int           `json:"fixed"`
	ToReview int           `json:"to_review"`
	Flagged  []FlaggedLine `json:"flagged"`
}

// RunStatus is done, needs_input, applied, declined or failed.
type RunStatus string

const (
	RunStatusDone       RunStatus = "done"
	RunStatusNeedsInput RunStatus = "needs_input"
	RunStatusApplied    RunStatus = "applied"
	RunStatusDeclined   RunStatus = "declined"
	RunStatusFailed     RunStatus = "failed"
)

// RunChoice answers a run that needs input.
type RunChoice string

const (
	RunChoiceApply   RunChoice = "apply"
	RunChoiceDecline RunChoice = "decline"
)

// Autonomy is "ask", which waits for your answer before changing anything, or
// "decide", which lets Reedapt apply changes itself.
type Autonomy string

const (
	AutonomyAsk    Autonomy = "ask"
	AutonomyDecide Autonomy = "decide"
)

// RunProposal is a change Reedapt proposes.
type RunProposal struct {
	Kind    string `json:"kind"`
	Summary string `json:"summary"`
	// Lines is how many lines it would change.
	Lines int `json:"lines"`
}

// RunOption is a way to answer a run.
type RunOption struct {
	ID    RunChoice `json:"id"`
	Label string    `json:"label"`
}

// Run is a request for changes to a dub, and what Reedapt did or proposes to do about it.
type Run struct {
	ID      string    `json:"id"`
	Object  string    `json:"object,omitempty"`
	DubID   string    `json:"dub_id"`
	Status  RunStatus `json:"status"`
	Request string    `json:"request"`
	Reply   string    `json:"reply"`
	// Proposal is present while Status is needs_input.
	Proposal *RunProposal `json:"proposal,omitempty"`
	Options  []RunOption  `json:"options,omitempty"`
	// AppliedLines is how many lines were changed, once the run is applied.
	AppliedLines *int   `json:"applied_lines,omitempty"`
	CreatedAt    string `json:"created_at"`
}

// CreateRunParams is a request for changes, in plain words.
type CreateRunParams struct {
	Text string `json:"text"`
	// Autonomy defaults to ask.
	Autonomy Autonomy `json:"autonomy,omitempty"`
}

// AnswerRunParams answers a run that needs input.
type AnswerRunParams struct {
	Choice RunChoice `json:"choice"`
}

// DubLine is one line of a dub, as it can be edited.
type DubLine struct {
	// Line is the line's number in time order, starting at 1.
	Line         int     `json:"line"`
	StartMS      int     `json:"start_ms"`
	EndMS        int     `json:"end_ms"`
	Speaker      *string `json:"speaker,omitempty"`
	OriginalText string  `json:"original_text"`
	// Text is the line in the dub's language.
	Text              string  `json:"text"`
	DeliveryStyle     *string `json:"delivery_style,omitempty"`
	VoiceInstructions *string `json:"voice_instructions,omitempty"`
}

// DubLines is every line of a dub.
type DubLines struct {
	DubID string    `json:"dub_id"`
	Lines []DubLine `json:"lines"`
}

// LineEdit changes one line. Only the fields you set are sent.
type LineEdit struct {
	Line int
	// Text is the new wording, in the dub's language.
	Text *string
	// DeliveryStyle is for example "angry" or "whisper".
	DeliveryStyle      *string
	ClearDeliveryStyle bool
	// VoiceInstructions say how to say it, in plain words. Up to 300 characters.
	VoiceInstructions      *string
	ClearVoiceInstructions bool
}

// MarshalJSON leaves out unset fields and sends null for the Clear... ones.
func (l LineEdit) MarshalJSON() ([]byte, error) {
	m := map[string]any{"line": l.Line}
	if l.Text != nil {
		m["text"] = *l.Text
	}
	if l.DeliveryStyle != nil {
		m["delivery_style"] = *l.DeliveryStyle
	} else if l.ClearDeliveryStyle {
		m["delivery_style"] = nil
	}
	if l.VoiceInstructions != nil {
		m["voice_instructions"] = *l.VoiceInstructions
	} else if l.ClearVoiceInstructions {
		m["voice_instructions"] = nil
	}
	return json.Marshal(m)
}

// EditLinesParams changes lines.
type EditLinesParams struct {
	// Lines holds at most 100 edits.
	Lines []LineEdit `json:"lines"`
}

// LinesEdited says which lines are being voiced again.
type LinesEdited struct {
	DubID  string `json:"dub_id"`
	Lines  []int  `json:"lines"`
	Status string `json:"status"`
}

// Languages lists the languages dubs can be made in.
type Languages struct {
	TargetLanguages []string `json:"target_languages"`
}

// Usage is the plan and minutes left behind the API key.
type Usage struct {
	Plan             *string  `json:"plan,omitempty"`
	KeyMode          string   `json:"key_mode"` // "live" or "test"
	MinutesRemaining *float64 `json:"minutes_remaining,omitempty"`
	Unlimited        bool     `json:"unlimited"`
	TestPreviewSecs  float64  `json:"test_preview_seconds"`
}

// DubCounts counts dubs by outcome.
type DubCounts struct {
	Completed  int `json:"completed"`
	InProgress int `json:"in_progress"`
	// Failed counts failed or cancelled dubs. Their minutes were returned.
	Failed int `json:"failed"`
}

// UsageCounts is minutes and dubs.
type UsageCounts struct {
	// Minutes used by completed dubs. Test dubs are free.
	Minutes float64 `json:"minutes"`
	// MinutesInProgress is reserved by dubs that haven't finished.
	MinutesInProgress float64   `json:"minutes_in_progress"`
	Dubs              DubCounts `json:"dubs"`
}

// KeyUsage is the usage of one API key.
type KeyUsage struct {
	UsageCounts
	KeyID *string `json:"key_id"`
	Mode  string  `json:"mode"`
}

// LanguageUsage is the usage of one target language.
type LanguageUsage struct {
	UsageCounts
	TargetLanguage *string `json:"target_language"`
}

// UsageReport is one month of the account's API usage. A dub counts in the month it started (UTC).
type UsageReport struct {
	UsageCounts
	Month       string          `json:"month"`
	PeriodStart string          `json:"period_start"`
	PeriodEnd   string          `json:"period_end"`
	Keys        []KeyUsage      `json:"keys"`
	Languages   []LanguageUsage `json:"languages"`
}

// UsageReportParams picks the month.
type UsageReportParams struct {
	// Month is YYYY-MM, UTC. It defaults to this month.
	Month string
}

// WebhookEndpoint is a URL Reedapt sends events to.
type WebhookEndpoint struct {
	ID          string   `json:"id"`
	Object      string   `json:"object,omitempty"`
	URL         string   `json:"url"`
	Description *string  `json:"description,omitempty"`
	Events      []string `json:"events"`
	CreatedAt   string   `json:"created_at"`
	// Secret is only on the endpoint returned by Create. Keep it to check signatures.
	Secret *string `json:"secret,omitempty"`
}

// WebhookEndpointList is an account's endpoints.
type WebhookEndpointList struct {
	Data []WebhookEndpoint `json:"data"`
}

// CreateWebhookEndpointParams adds an endpoint.
type CreateWebhookEndpointParams struct {
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
}

// WebhookDelivery is one attempt series to deliver an event to an endpoint.
type WebhookDelivery struct {
	ID             string  `json:"id"`
	EventID        string  `json:"event_id"`
	EventType      string  `json:"event_type"`
	Status         string  `json:"status"` // pending, delivered or failed
	Attempts       int     `json:"attempts"`
	LastStatusCode *int    `json:"last_status_code,omitempty"`
	LastError      *string `json:"last_error,omitempty"`
	NextAttemptAt  *string `json:"next_attempt_at,omitempty"`
	CreatedAt      string  `json:"created_at"`
	DeliveredAt    *string `json:"delivered_at,omitempty"`
}

// WebhookDeliveryList is recent deliveries to an endpoint.
type WebhookDeliveryList struct {
	Data []WebhookDelivery `json:"data"`
}

// BatchSummary is what the batch.completed event carries.
type BatchSummary struct {
	BatchID    string `json:"batch_id"`
	Status     string `json:"status"`
	VideoCount int    `json:"video_count"`
	JobsTotal  int    `json:"jobs_total"`
	JobsDone   int    `json:"jobs_done"`
	JobsFailed int    `json:"jobs_failed"`
	Dubs       []Dub  `json:"dubs"`
}
