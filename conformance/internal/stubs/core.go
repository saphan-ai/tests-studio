package stubs

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/saphan-ai/tests-studio/conformance/internal/proto"
	"github.com/saphan-ai/tests-studio/conformance/internal/schemas"
)

// Mode says whether the adapter runs on its own or inside a Saphan leg's run.
type Mode string

// The two modes (specification section "Opening or attaching").
const (
	ModeStandalone Mode = "standalone"
	ModeLeg        Mode = "leg"
)

const (
	maxBody         = 8 << 20
	timeLayout      = "2006-01-02T15:04:05.000Z"
	stubCoreName    = "stub-tests-core"
	stubCoreVersion = "1.0.0"
	mockName        = "sample-http"
)

var (
	runIDPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,63}$`)
	stepIDPattern = regexp.MustCompile(`^.{1,128}$`)
	sha256Hex     = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type step struct {
	started, finished bool
	result            string // the accepted result, when finished
}

// tally counts the test run the way ruling 1 of v1.1 says the core does: total is the steps
// started, the four others are the results accepted. A refused result is not counted.
func (tr *testRun) tally() (c proto.Counts, unfinished int) {
	for _, st := range tr.steps {
		c.Total++
		switch {
		case !st.finished:
			unfinished++
		case st.result == "pass":
			c.Passed++
		case st.result == "fail":
			c.Failed++
		case st.result == "error":
			c.Errored++
		case st.result == "skip":
			c.Skipped++
		}
	}
	return c, unfinished
}

type testRun struct {
	id, cred, project string
	closed            bool
	steps             map[string]*step
	eventIDs          map[string]bool
}

type mockEvent struct{ id, testRunID string }

// Stack is the pair of stub cores and the world around them, with the state they share.
type Stack struct {
	// Mode is the mode the stack was built for.
	Mode Mode
	// Rec holds every call any of the three stubs served.
	Rec *Recorder

	val      *schemas.Validator
	served   []string
	coreURL  string
	mocksURL string
	worldURL string

	runID, runCred string
	tr             *testRun
	mockEvents     []mockEvent
	counter        int
}

// New builds a stack. In leg mode the stack starts with the leg's run already open.
func New(mode Mode, val *schemas.Validator) *Stack {
	s := &Stack{Mode: mode, Rec: &Recorder{}, val: val, served: []string{proto.SpecVersion}}
	if mode == ModeLeg {
		s.runID = "leg-conformance-" + randHex(4)
		s.runCred = "leg_" + randHex(16)
	}
	return s
}

// SetURLs tells the stack where each stub is served; it must be called before the first request.
func (s *Stack) SetURLs(core, mocks, world string) {
	s.Rec.mu.Lock()
	defer s.Rec.mu.Unlock()
	s.coreURL, s.mocksURL, s.worldURL = core, mocks, world
}

// Served returns the spec versions the stub core serves.
func (s *Stack) Served() []string { return append([]string(nil), s.served...) }

// LegEnv returns the run id and credential a leg's environment would carry (leg mode only).
func (s *Stack) LegEnv() (runID, token string) {
	s.Rec.mu.Lock()
	defer s.Rec.mu.Unlock()
	return s.runID, s.runCred
}

// Handlers returns the three recorded stubs.
func (s *Stack) Handlers() (core, mocks, world http.Handler) {
	return s.Rec.Wrap(ServiceCore, s.coreMux()), s.Rec.Wrap(ServiceMocks, s.mocksMux()), s.Rec.Wrap(ServiceWorld, s.worldMux())
}

func randHex(nBytes int) string {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error()) // a stub cannot mint credentials without it
	}
	return hex.EncodeToString(b)
}

func now() string { return time.Now().UTC().Format(timeLayout) }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) // the client is gone if this fails; nothing to recover
}

func refuse(w http.ResponseWriter, status int, code, message string, allowed []string) {
	writeJSON(w, status, proto.Refusal{Message: message, ReasonCode: code, Allowed: allowed})
}

func readBody(r *http.Request) ([]byte, error) { return io.ReadAll(io.LimitReader(r.Body, maxBody)) }

func decode(w http.ResponseWriter, r *http.Request, into any) bool {
	raw, err := readBody(r)
	if err == nil {
		err = json.Unmarshal(raw, into)
	}
	if err != nil {
		refuse(w, http.StatusUnprocessableEntity, proto.ReasonRequestInvalid,
			"The request body was measured as not a JSON document of the shape this operation takes, so it was refused. NEXT — send the body the OpenAPI document describes. Detail: "+err.Error(), nil)
		return false
	}
	return true
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// credentialOK checks the bearer against the credential want; a missing one is 401, a wrong one
// 403, and neither says whether the other run exists.
func credentialOK(w http.ResponseWriter, r *http.Request, want string) bool {
	got := bearer(r)
	switch {
	case got == "":
		refuse(w, http.StatusUnauthorized, proto.ReasonCredentialAbsent,
			"The request carried no Authorization bearer header, so it was refused. NEXT — present the run credential as 'Authorization: Bearer <credential>' on this call.", nil)
		return false
	case want == "" || got != want:
		refuse(w, http.StatusForbidden, proto.ReasonCredentialBad,
			"The bearer credential was measured as not valid for this run, so the call was refused. NEXT — present the credential this run issued, and no other.", nil)
		return false
	}
	return true
}

func (s *Stack) specVersionOK(w http.ResponseWriter, r *http.Request) bool {
	got := r.Header.Get(proto.HeaderSpecVersion)
	for _, v := range s.served {
		if got == v {
			return true
		}
	}
	refuse(w, http.StatusUnprocessableEntity, proto.ReasonSpecUnsupported,
		fmt.Sprintf("The adapter declared spec version %q and this core serves %s, so the call was refused. NEXT — speak one of the served versions and send it in the %s header.",
			got, strings.Join(s.served, ", "), proto.HeaderSpecVersion), s.served)
	return false
}

// coreRoutes is the Tests Studio core API the stub serves, keyed "METHOD /path". A test compares
// it with spec/runner-v1.openapi.yaml, so the stub and the specification cannot drift apart.
func (s *Stack) coreRoutes() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"GET /v1/spec":                                      s.getSpec,
		"POST /v1/test-runs":                                s.openTestRun,
		"POST /v1/runs/{run_id}/test-runs":                  s.attachTestRun,
		"POST /v1/test-runs/{test_run_id}/steps":            s.startStep,
		"POST /v1/test-runs/{test_run_id}/step-results":     s.finishStep,
		"PUT /v1/test-runs/{test_run_id}/evidence/{digest}": s.attachEvidence,
		"POST /v1/test-runs/{test_run_id}/events":           s.emitEvent,
		"POST /v1/test-runs/{test_run_id}/close":            s.closeTestRun,
	}
}

func (s *Stack) coreMux() *http.ServeMux { return mux(s.coreRoutes()) }

func mux(routes map[string]http.HandlerFunc) *http.ServeMux {
	m := http.NewServeMux()
	for pattern, h := range routes {
		m.HandleFunc(pattern, h)
	}
	return m
}

// CoreRoutes lists the stub core's operations as "METHOD /path".
func CoreRoutes() []string { return keys((&Stack{}).coreRoutes()) }

// MocksRoutes lists the stub Mocks Studio core's operations as "METHOD /path".
func MocksRoutes() []string { return keys((&Stack{}).mocksRoutes()) }

func keys(m map[string]http.HandlerFunc) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (s *Stack) getSpec(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, proto.SpecDocument{Versions: s.served, Core: proto.Identity{Name: stubCoreName, Version: stubCoreVersion}})
}

func (s *Stack) emitMock(kind string) {
	s.counter++
	trID := ""
	if s.tr != nil {
		trID = s.tr.id
	}
	s.mockEvents = append(s.mockEvents, mockEvent{id: fmt.Sprintf("mock-evt-%04d-%s", s.counter, kind), testRunID: trID})
}

func (s *Stack) newTestRun(w http.ResponseWriter, req proto.OpenRequest) *testRun {
	id := req.TestRunID
	if id == "" {
		id = "tr-" + randHex(4)
	}
	if !runIDPattern.MatchString(id) {
		refuse(w, http.StatusUnprocessableEntity, proto.ReasonRunIDInvalid,
			"The test_run_id was measured as not matching ^[a-z0-9][a-z0-9._-]{2,63}$, so it was refused. NEXT — send an id of that form, or omit it and let the core generate one.", nil)
		return nil
	}
	if req.Project == "" {
		refuse(w, http.StatusUnprocessableEntity, proto.ReasonRequestInvalid,
			"The request was measured as carrying no project, so it was refused. NEXT — send a non-empty project name.", nil)
		return nil
	}
	return &testRun{id: id, cred: "ttr_" + randHex(16), project: req.Project, steps: map[string]*step{}, eventIDs: map[string]bool{}}
}

func (s *Stack) endpoints(mocks []string) map[string]proto.Endpoint {
	eps := map[string]proto.Endpoint{}
	for _, m := range mocks {
		eps[m] = proto.Endpoint{Protocol: "http", URL: s.worldURL + "/mock/" + m}
	}
	return eps
}

func (s *Stack) opened(tr *testRun, mocks []string, withRunCred bool) proto.TestRunOpened {
	o := proto.TestRunOpened{
		TestRunID: tr.id, RunID: s.runID, StartedAt: now(), Credential: tr.cred,
		MocksCoreURL: s.mocksURL, Endpoints: s.endpoints(mocks),
	}
	if withRunCred {
		o.RunCredential = s.runCred
	}
	return o
}

func (s *Stack) openTestRun(w http.ResponseWriter, r *http.Request) {
	if !s.specVersionOK(w, r) {
		return
	}
	if s.Mode == ModeLeg {
		refuse(w, http.StatusForbidden, proto.ReasonOpenNotAllowed,
			"A test run was asked to open a second run inside a leg whose run is already open, so it was refused. NEXT — attach to the leg's run with POST /v1/runs/{run_id}/test-runs, using MOCKS_RUN_ID and MOCKS_RUN_TOKEN.", nil)
		return
	}
	var req proto.OpenRequest
	if !decode(w, r, &req) {
		return
	}
	if s.tr != nil {
		refuse(w, http.StatusConflict, proto.ReasonRunAlreadyOpen,
			"A test run is already open on this connection, so a second was refused. NEXT — close the open test run first.", nil)
		return
	}
	tr := s.newTestRun(w, req)
	if tr == nil {
		return
	}
	s.runID, s.runCred, s.tr = "conf-run-"+randHex(3), "run_"+randHex(16), tr
	s.emitMock("scope.opened")
	writeJSON(w, http.StatusCreated, s.opened(tr, req.Mocks, true))
}

func (s *Stack) attachTestRun(w http.ResponseWriter, r *http.Request) {
	if !s.specVersionOK(w, r) {
		return
	}
	want := ""
	if s.Mode == ModeLeg && r.PathValue("run_id") == s.runID {
		want = s.runCred
	}
	if !credentialOK(w, r, want) {
		return
	}
	var req proto.OpenRequest
	if !decode(w, r, &req) {
		return
	}
	if s.tr != nil && !s.tr.closed {
		refuse(w, http.StatusConflict, proto.ReasonRunAttached,
			"A test run is already attached to this run, so a second attach was refused. NEXT — close the attached test run, then attach again.", nil)
		return
	}
	tr := s.newTestRun(w, req)
	if tr == nil {
		return
	}
	s.tr = tr
	if req.Reset == nil || *req.Reset {
		s.emitMock("scope.reset")
	}
	writeJSON(w, http.StatusCreated, s.opened(tr, []string{mockName}, false))
}

// authTestRun answers whether the call may act on test run id; it writes the refusal when not.
func (s *Stack) authTestRun(w http.ResponseWriter, r *http.Request) *testRun {
	tr := s.tr
	want := ""
	if tr != nil && !tr.closed && tr.id == r.PathValue("test_run_id") {
		want = tr.cred
	}
	if !credentialOK(w, r, want) {
		return nil
	}
	return tr
}

func (s *Stack) startStep(w http.ResponseWriter, r *http.Request) {
	tr := s.authTestRun(w, r)
	if tr == nil {
		return
	}
	var req proto.StartStepRequest
	if !decode(w, r, &req) {
		return
	}
	if !stepIDPattern.MatchString(req.StepID) || req.Name == "" {
		refuse(w, http.StatusUnprocessableEntity, proto.ReasonStepIDMissing,
			"The step start was measured as carrying no step_id of 1 to 128 characters or no name, so it was refused. NEXT — send both a step_id and a name.", nil)
		return
	}
	if tr.steps[req.StepID] != nil {
		refuse(w, http.StatusConflict, proto.ReasonStepStarted,
			"A step with this step_id was already started in this test run, so a second start was refused. NEXT — use a new step_id for every test item.", nil)
		return
	}
	tr.steps[req.StepID] = &step{started: true}
	trace := randHex(16)
	if trace == strings.Repeat("0", 32) {
		trace = strings.Repeat("a", 32)
	}
	writeJSON(w, http.StatusCreated, proto.StepStarted{
		StepID: req.StepID, CorrelationID: trace,
		Traceparent: "00-" + trace + "-" + randHex(8) + "-01",
		Baggage:     "mocks.run=" + s.runID + ",mocks.step=" + req.StepID,
	})
}

func (s *Stack) finishStep(w http.ResponseWriter, r *http.Request) {
	tr := s.authTestRun(w, r)
	if tr == nil {
		return
	}
	var req proto.StepResult
	if !decode(w, r, &req) {
		return
	}
	if req.StepID == nil || *req.StepID == "" {
		refuse(w, http.StatusUnprocessableEntity, proto.ReasonStepIDMissing,
			"The step result was measured as carrying no step_id, so it was refused. NEXT — send the step_id the core returned when the step started.", nil)
		return
	}
	st := tr.steps[*req.StepID]
	switch {
	case st == nil:
		refuse(w, http.StatusUnprocessableEntity, proto.ReasonStepUnknown,
			"The step_id was measured as not started in this test run, so the result was refused. NEXT — start the step first.", nil)
	case st.finished:
		refuse(w, http.StatusConflict, proto.ReasonStepFinished,
			"The step already has a result, so a second was refused. NEXT — send exactly one result per step.", nil)
	case !contains(proto.Results, req.Result) || req.DurationMS == nil || *req.DurationMS < 0:
		refuse(w, http.StatusUnprocessableEntity, proto.ReasonResultInvalid,
			"The step result was measured as outside pass, fail, error, skip or with an absent or negative duration, so it was refused. NEXT — send one of the four results and a duration_ms of 0 or more.", proto.Results)
	case (req.Result == "fail" || req.Result == "error") && req.Failure == "":
		refuse(w, http.StatusUnprocessableEntity, proto.ReasonFailureMissing,
			"A failing step result was measured as carrying no failure text, so it was refused. NEXT — send the failure text with a fail or error result.", nil)
	default:
		st.finished, st.result = true, req.Result
		w.WriteHeader(http.StatusNoContent)
	}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (s *Stack) attachEvidence(w http.ResponseWriter, r *http.Request) {
	tr := s.authTestRun(w, r)
	if tr == nil {
		return
	}
	declared := r.PathValue("digest")
	body, _ := readBody(r) // a short read shows up as a digest mismatch below
	kind := r.Header.Get(proto.HeaderEvidenceKind)
	switch {
	case !contains(proto.EvidenceKinds, kind):
		refuse(w, http.StatusUnprocessableEntity, proto.ReasonEvidenceKind,
			"The evidence kind was measured as outside screenshot, log, artifact, so it was refused. NEXT — send one of the three kinds in the "+proto.HeaderEvidenceKind+" header.", proto.EvidenceKinds)
	case tr.steps[r.Header.Get(proto.HeaderStepID)] == nil:
		refuse(w, http.StatusUnprocessableEntity, proto.ReasonStepUnknown,
			"The evidence named a step that was not started in this test run, so it was refused. NEXT — send the step's id in the "+proto.HeaderStepID+" header.", nil)
	case !sha256Hex.MatchString(declared) || proto.SHA256Digest(body) != "sha256:"+declared:
		refuse(w, http.StatusUnprocessableEntity, proto.ReasonEvidenceDigest,
			"The SHA-256 of the evidence bytes was measured as different from the digest in the path, so the bytes were refused. NEXT — compute the SHA-256 over the exact bytes you send and put its lowercase hex in the path.", nil)
	default:
		writeJSON(w, http.StatusCreated, map[string]any{"digest": "sha256:" + declared, "size": len(body)})
	}
}

func (s *Stack) emitEvent(w http.ResponseWriter, r *http.Request) {
	tr := s.authTestRun(w, r)
	if tr == nil {
		return
	}
	raw, _ := readBody(r)
	var head struct {
		Schema    string `json:"schema"`
		EventID   string `json:"event_id"`
		RunID     string `json:"run_id"`
		TestRunID string `json:"test_run_id"`
	}
	_ = json.Unmarshal(raw, &head) // a body that is not JSON fails validation below
	if strings.HasPrefix(head.Schema, "tests.event/") && head.Schema != "tests.event/v1" {
		refuse(w, http.StatusUnprocessableEntity, proto.ReasonEventSchema,
			fmt.Sprintf("The event declared schema %q and this core ingests tests.event/v1, so it was refused. NEXT — emit tests.event/v1.", head.Schema), []string{"tests.event/v1"})
		return
	}
	if err := s.val.ValidateEvent(raw); err != nil || head.RunID != s.runID || head.TestRunID != tr.id {
		detail := "it names a run_id or test_run_id other than this test run's"
		if err != nil {
			detail = err.Error()
		}
		refuse(w, http.StatusUnprocessableEntity, proto.ReasonEventInvalid,
			"The event was measured as not a valid tests.event/v1 document of this test run, so it was refused. NEXT — fix the field the detail names. Detail: "+detail, nil)
		return
	}
	tr.eventIDs[head.EventID] = true
	w.WriteHeader(http.StatusAccepted)
}

func (s *Stack) closeTestRun(w http.ResponseWriter, r *http.Request) {
	tr := s.authTestRun(w, r)
	if tr == nil {
		return
	}
	var req proto.CloseRequest
	if !decode(w, r, &req) {
		return
	}
	// Ruling 1 of v1.1: the core is the record, so it re-tallies the accepted results and refuses
	// a verdict or counts that disagree. The test run stays open, so the adapter can still close.
	tally, unfinished := tr.tally()
	if unfinished > 0 || req.Counts != tally || req.Verdict != proto.VerdictOf(tally) {
		refuse(w, http.StatusUnprocessableEntity, proto.ReasonResultMismatch,
			fmt.Sprintf("The close carried verdict %q and counts %+v, while the core's tally of the results it accepted is verdict %q and counts %+v with %d started steps still lacking a result, so the close was refused. NEXT — close again with the core's tally once every started step has an accepted result; the test run is still open.",
				req.Verdict, req.Counts, proto.VerdictOf(tally), tally, unfinished), nil)
		return
	}
	s.emitMock("scope.closed")
	var ids []string
	for _, e := range s.mockEvents {
		if e.testRunID == tr.id {
			ids = append(ids, e.id)
		}
	}
	digest := proto.EventsDigest(ids)
	tr.closed = true // closing revokes the credential
	writeJSON(w, http.StatusOK, proto.RunResult{
		Schema: "tests.run-result/v1", RunID: s.runID, TestRunID: tr.id, Project: tr.project,
		Verdict: req.Verdict, Counts: req.Counts, StartedAt: req.StartedAt, EndedAt: req.EndedAt,
		EventsDigest: digest, Adapter: req.Adapter, EvidenceLine: proto.EvidenceLine(s.runID, tr.id, req.Verdict, digest),
	})
}
