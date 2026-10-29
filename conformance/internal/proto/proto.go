// Package proto holds the names the test-runner specification v1 fixes: environment variables,
// HTTP headers, reason codes and wire documents. The stubs, the checks and the sample adapter
// all import it, so a name is spelled once.
package proto

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// SpecVersion is the version of the test-runner specification this suite checks.
const SpecVersion = "v1"

// Environment variables an adapter reads. The first group is what the conformance runner (and, in
// production, the core or a Saphan leg) puts into the adapter's environment.
const (
	EnvCoreURL    = "TESTS_CORE_URL"            // base URL of the Tests Studio core
	EnvRunToken   = "TESTS_RUN_TOKEN"           // optional: a test-run credential handed on by a parent process
	EnvResultFile = "TESTS_RESULT_FILE"         // path the adapter writes the run result to
	EnvLegRunID   = "MOCKS_RUN_ID"              // inside a leg: the leg's run id
	EnvLegToken   = "MOCKS_RUN_TOKEN"           // inside a leg: the leg's run credential
	EnvMocksURL   = "MOCKS_CORE_URL"            // inside a leg: base URL of the Mocks Studio core
	EnvSutURL     = "TESTS_CONFORMANCE_SUT_URL" // conformance only: the stand-in system under test
)

// HTTP headers. No header outside this list and the W3C ones is defined.
const (
	HeaderSpecVersion  = "Tests-Spec-Version"
	HeaderEvidenceKind = "Tests-Evidence-Kind"
	HeaderStepID       = "Tests-Step-Id"
	HeaderTraceparent  = "traceparent"
	HeaderBaggage      = "baggage"
)

// Reason codes of the Tests Studio core's refusals (the Mocks Studio core's are in Appendix C).
const (
	ReasonSpecUnsupported  = "spec-version-unsupported"
	ReasonCredentialAbsent = "run-credential-missing"
	ReasonCredentialBad    = "run-credential-invalid"
	ReasonOpenNotAllowed   = "run-open-not-allowed"
	ReasonRunAttached      = "run-attached"
	ReasonRunAlreadyOpen   = "run-already-open"
	ReasonRunIDInvalid     = "run-id-invalid"
	ReasonRequestInvalid   = "request-invalid"
	ReasonStepIDMissing    = "step-id-missing"
	ReasonStepUnknown      = "step-unknown"
	ReasonStepStarted      = "step-already-started"
	ReasonStepFinished     = "step-already-finished"
	ReasonResultInvalid    = "result-invalid"
	ReasonFailureMissing   = "failure-missing"
	ReasonEvidenceDigest   = "evidence-digest-mismatch"
	ReasonEvidenceKind     = "evidence-kind-unknown"
	ReasonEventInvalid     = "event-invalid"
	ReasonEventSchema      = "event-schema-unknown"
)

// Step results, and evidence kinds.
var (
	Results       = []string{"pass", "fail", "error", "skip"}
	EvidenceKinds = []string{"screenshot", "log", "artifact"}
)

// Refusal is the refusal document of Appendix C of the ecosystem contract.
type Refusal struct {
	Message    string   `json:"message"`
	ReasonCode string   `json:"reason_code"`
	Allowed    []string `json:"allowed,omitempty"`
}

// SpecDocument is the answer to GET /v1/spec.
type SpecDocument struct {
	Versions []string `json:"versions"`
	Core     Identity `json:"core"`
}

// Identity names a piece of software: the core, or an adapter.
type Identity struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Spec    string `json:"spec,omitempty"`
}

// Endpoint is one mock's endpoint for the run.
type Endpoint struct {
	Protocol string `json:"protocol"`
	URL      string `json:"url"`
}

// OpenRequest is the body of openTestRun and attachTestRun.
type OpenRequest struct {
	TestRunID string   `json:"test_run_id,omitempty"`
	Project   string   `json:"project"`
	Mocks     []string `json:"mocks,omitempty"`
	Reset     *bool    `json:"reset,omitempty"`
}

// TestRunOpened is the answer to openTestRun and attachTestRun.
type TestRunOpened struct {
	TestRunID     string              `json:"test_run_id"`
	RunID         string              `json:"run_id"`
	StartedAt     string              `json:"started_at"`
	Credential    string              `json:"credential"`
	RunCredential string              `json:"run_credential,omitempty"`
	MocksCoreURL  string              `json:"mocks_core_url"`
	Endpoints     map[string]Endpoint `json:"endpoints"`
}

// StartStepRequest and StepStarted are the body and answer of startStep.
type StartStepRequest struct {
	StepID string `json:"step_id"`
	Name   string `json:"name"`
}

// StepStarted carries what the adapter hands to the system under test.
type StepStarted struct {
	StepID        string `json:"step_id"`
	CorrelationID string `json:"correlation_id"`
	Traceparent   string `json:"traceparent"`
	Baggage       string `json:"baggage"`
}

// StepResult is the body of finishStep. StepID and DurationMS are pointers so that an absent
// field reads differently from a zero one.
type StepResult struct {
	StepID     *string `json:"step_id,omitempty"`
	Result     string  `json:"result"`
	DurationMS *int64  `json:"duration_ms"`
	Failure    string  `json:"failure,omitempty"`
}

// Counts is the tally of a run.
type Counts struct {
	Total   int `json:"total"`
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Errored int `json:"errored"`
	Skipped int `json:"skipped"`
}

// CloseRequest is the body of closeTestRun.
type CloseRequest struct {
	Verdict   string   `json:"verdict"`
	Counts    Counts   `json:"counts"`
	StartedAt string   `json:"started_at"`
	EndedAt   string   `json:"ended_at"`
	Adapter   Identity `json:"adapter"`
}

// RunResult is the tests.run-result/v1 document.
type RunResult struct {
	Schema       string   `json:"schema"`
	RunID        string   `json:"run_id"`
	TestRunID    string   `json:"test_run_id"`
	Project      string   `json:"project"`
	Verdict      string   `json:"verdict"`
	Counts       Counts   `json:"counts"`
	StartedAt    string   `json:"started_at"`
	EndedAt      string   `json:"ended_at"`
	EventsDigest string   `json:"events_digest"`
	Adapter      Identity `json:"adapter"`
	EvidenceLine string   `json:"evidence_line"`
}

// EvidenceLine is the line a Saphan leg cites, verbatim from section 6 of the ecosystem contract.
func EvidenceLine(runID, testRunID, verdict, eventsDigest string) string {
	return "- evidence: tests-studio-run " + runID + "/" + testRunID + " verdict=" + verdict + " events=" + eventsDigest
}

// EventsDigest is SHA-256 over the sorted event ids, each followed by one "\n" byte, printed as
// "sha256:" and 64 lowercase hex characters. An empty set digests the empty input.
func EventsDigest(eventIDs []string) string {
	ids := append([]string(nil), eventIDs...)
	sort.Strings(ids)
	h := sha256.New()
	for _, id := range ids {
		h.Write([]byte(id))
		h.Write([]byte{'\n'})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// VerdictOf derives the run verdict from counts: error outranks fail, fail outranks pass, and
// skipped steps never fail a run.
func VerdictOf(c Counts) string {
	switch {
	case c.Errored > 0:
		return "error"
	case c.Failed > 0:
		return "fail"
	default:
		return "pass"
	}
}

// SHA256Digest returns "sha256:" and the lowercase hex SHA-256 of b.
func SHA256Digest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// TraceID returns the trace-id field of a W3C traceparent value, or "" when it is not one.
func TraceID(traceparent string) string {
	parts := strings.Split(traceparent, "-")
	if len(parts) != 4 || len(parts[1]) != 32 {
		return ""
	}
	return parts[1]
}
