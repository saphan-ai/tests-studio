package reporter

// The wire documents of specification section 6, written from spec/runner-v1.openapi.yaml and
// spec/schemas/. The headers the adapter sets besides the standard ones:
const (
	headerSpecVersion  = "Tests-Spec-Version"
	headerEvidenceKind = "Tests-Evidence-Kind"
	headerStepID       = "Tests-Step-Id"
)

// eventPayload is the redacted shape of a payload: the digest and size of what was stored, never the content.
type eventPayload struct {
	Redacted bool   `json:"redacted"`
	Digest   string `json:"digest"`
	Size     int    `json:"size"`
}

type refusal struct {
	Message    string   `json:"message"`
	ReasonCode string   `json:"reason_code"`
	Allowed    []string `json:"allowed,omitempty"`
}

type specDocument struct {
	Versions []string `json:"versions"`
}

type identity struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Spec    string `json:"spec"`
}

type openRequest struct {
	Project string   `json:"project"`
	Mocks   []string `json:"mocks,omitempty"`
}

type openedRun struct {
	TestRunID     string `json:"test_run_id"`
	RunID         string `json:"run_id"`
	Credential    string `json:"credential"`
	RunCredential string `json:"run_credential,omitempty"`
}

type startStepRequest struct {
	StepID string `json:"step_id"`
	Name   string `json:"name"`
}

type stepStarted struct {
	StepID        string `json:"step_id"`
	CorrelationID string `json:"correlation_id"`
	Traceparent   string `json:"traceparent"`
	Baggage       string `json:"baggage"`
}

type stepResult struct {
	StepID     string `json:"step_id"`
	Result     string `json:"result"`
	DurationMS int64  `json:"duration_ms"`
	Failure    string `json:"failure,omitempty"`
}

// Counts is the tally of a run: total is the number of steps and the four others sum to it.
type Counts struct {
	Total   int `json:"total"`
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Errored int `json:"errored"`
	Skipped int `json:"skipped"`
}

func (c *Counts) add(result string) {
	c.Total++
	switch result {
	case ResultPass:
		c.Passed++
	case ResultFail:
		c.Failed++
	case ResultError:
		c.Errored++
	case ResultSkip:
		c.Skipped++
	}
}

// verdict follows specification section 7: error outranks fail, and a skip never fails a run.
func (c Counts) verdict() string {
	switch {
	case c.Errored > 0:
		return ResultError
	case c.Failed > 0:
		return ResultFail
	default:
		return ResultPass
	}
}

type closeRequest struct {
	Verdict   string   `json:"verdict"`
	Counts    Counts   `json:"counts"`
	StartedAt string   `json:"started_at"`
	EndedAt   string   `json:"ended_at"`
	Adapter   identity `json:"adapter"`
}

type runResult struct {
	EvidenceLine string `json:"evidence_line"`
}

type event struct {
	Schema        string        `json:"schema"`
	EventID       string        `json:"event_id"`
	At            string        `json:"at"`
	RunID         string        `json:"run_id"`
	TestRunID     string        `json:"test_run_id"`
	StepID        string        `json:"step_id,omitempty"`
	CorrelationID string        `json:"correlation_id,omitempty"`
	Kind          string        `json:"kind"`
	Emitter       identity      `json:"emitter"`
	Summary       string        `json:"summary"`
	Outcome       string        `json:"outcome,omitempty"`
	Payload       *eventPayload `json:"payload,omitempty"`
}
