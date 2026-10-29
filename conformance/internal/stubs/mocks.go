package stubs

import (
	"encoding/json"
	"net/http"
	"strings"

	"example.com/tests-studio/conformance/internal/proto"
)

// Setting ranges of the one mock the stub declares, mockName.
const (
	maxDelayMS  = 5000
	minErrorHTT = 100
	maxErrorHTT = 599
)

var (
	allowedSettings = []string{"delay_ms", "error_code"}
	zeroTraceID     = strings.Repeat("0", 32)
)

func (s *Stack) mocksMux() *http.ServeMux {
	m := http.NewServeMux()
	m.HandleFunc("POST /v1/runs", s.mocksOpenRun)
	m.HandleFunc("GET /v1/runs/{run_id}", s.mocksGetRun)
	m.HandleFunc("POST /v1/runs/{run_id}/reset", s.mocksReset)
	m.HandleFunc("GET /v1/runs/{run_id}/mocks/{mock}/settings", s.mocksGetSettings)
	m.HandleFunc("PUT /v1/runs/{run_id}/mocks/{mock}/settings", s.mocksSetScenario)
	m.HandleFunc("POST /v1/runs/{run_id}/correlations", s.mocksAnnounce)
	return m
}

// mocksAuth checks the run credential for the run named in the path.
func (s *Stack) mocksAuth(w http.ResponseWriter, r *http.Request) bool {
	want := ""
	if s.runID != "" && r.PathValue("run_id") == s.runID {
		want = s.runCred
	}
	return credentialOK(w, r, want)
}

// mocksOpenRun refuses: a runner adapter never opens a run on the Mocks Studio core itself.
func (s *Stack) mocksOpenRun(w http.ResponseWriter, _ *http.Request) {
	refuse(w, http.StatusForbidden, proto.ReasonOpenNotAllowed,
		"A call to openRun on the Mocks Studio core was measured from a runner adapter, and it was refused: a runner adapter opens its run through the Tests Studio core, or attaches to its leg's run. NEXT — call openTestRun or attachTestRun on the Tests Studio core.", nil)
}

func (s *Stack) mocksGetRun(w http.ResponseWriter, r *http.Request) {
	if !s.mocksAuth(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run_id": s.runID, "opened_at": now(), "endpoints": s.endpoints([]string{mockName})})
}

func (s *Stack) mocksReset(w http.ResponseWriter, r *http.Request) {
	if !s.mocksAuth(w, r) {
		return
	}
	s.emitMock("scope.reset")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Stack) mocksGetSettings(w http.ResponseWriter, r *http.Request) {
	if !s.mocksAuth(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"delay_ms": 0, "error_code": 0})
}

func (s *Stack) mocksSetScenario(w http.ResponseWriter, r *http.Request) {
	if !s.mocksAuth(w, r) {
		return
	}
	if r.PathValue("mock") != mockName {
		refuse(w, http.StatusUnprocessableEntity, "mock-unknown",
			"The mock was measured as not part of this run, so the scenario was refused. NEXT — name a mock the run declared.", []string{mockName})
		return
	}
	var settings map[string]json.Number
	raw, _ := readBody(r)
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&settings); err != nil {
		refuse(w, http.StatusUnprocessableEntity, "setting-type-invalid",
			"A setting value was measured as not a number, so the scenario was refused. NEXT — send integers for delay_ms and error_code.", allowedSettings)
		return
	}
	for name, v := range settings {
		n, err := v.Int64()
		switch {
		case !contains(allowedSettings, name):
			refuse(w, http.StatusUnprocessableEntity, "setting-unknown",
				"The setting "+name+" was measured as not declared by the mock, so the scenario was refused. NEXT — set one of the allowed settings.", allowedSettings)
			return
		case err != nil:
			refuse(w, http.StatusUnprocessableEntity, "setting-type-invalid",
				"The setting "+name+" was measured as not an integer, so the scenario was refused. NEXT — send an integer.", allowedSettings)
			return
		case (name == "delay_ms" && (n < 0 || n > maxDelayMS)) ||
			(name == "error_code" && n != 0 && (n < minErrorHTT || n > maxErrorHTT)):
			refuse(w, http.StatusUnprocessableEntity, "setting-out-of-range",
				"The setting "+name+" was measured as outside its allowed range, so the scenario was refused. NEXT — send a value inside the range the mock declares.", allowedSettings)
			return
		}
	}
	s.emitMock("scenario.set")
	writeJSON(w, http.StatusOK, map[string]any{"applied": settings})
}

func (s *Stack) mocksAnnounce(w http.ResponseWriter, r *http.Request) {
	if !s.mocksAuth(w, r) {
		return
	}
	var req struct {
		CorrelationID string            `json:"correlation_id"`
		Keys          map[string]string `json:"keys"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len(req.CorrelationID) != 32 || req.CorrelationID == zeroTraceID || len(req.Keys) == 0 {
		refuse(w, http.StatusUnprocessableEntity, proto.ReasonRequestInvalid,
			"The correlation announcement was measured as carrying no 32-hex non-zero correlation_id or no keys, so it was refused. NEXT — send both.", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// worldMux serves the stand-in system under test and every mock's own URL. A runner adapter
// calls neither on its own account: the first is its test's job, the second is forbidden.
func (s *Stack) worldMux() *http.ServeMux {
	m := http.NewServeMux()
	m.HandleFunc("/sut/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"ok": "sut"})
	})
	m.HandleFunc("/mock/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"ok": "mock"})
	})
	return m
}
