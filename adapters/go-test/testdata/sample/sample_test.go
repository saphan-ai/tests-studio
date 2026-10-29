// Package sample is the sample project the conformance suite runs the adapter over: a passing
// test, a failing one, a skipped one, and one that calls the stand-in system under test with the
// trace context of its step. It lives under testdata, so `go test ./...` of the module never
// runs it; the suite names it explicitly.
package sample

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/saphan-ai/tests-studio/adapters/go-test/tracectx"
)

func TestPasses(t *testing.T) {
	if 1+1 != 2 {
		t.Fatal("arithmetic is broken")
	}
}

func TestFails(t *testing.T) {
	t.Fatal("this test fails on purpose, so the run reports a failure with text")
}

func TestSkipped(t *testing.T) {
	t.Skip("this test is skipped on purpose")
}

func TestCallsSystemUnderTest(t *testing.T) {
	base := os.Getenv("TESTS_CONFORMANCE_SUT_URL")
	if base == "" {
		t.Skip("TESTS_CONFORMANCE_SUT_URL is not set; the conformance suite sets it")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/ping", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !tracectx.Apply(req) {
		t.Fatal("no trace context in the environment; run this package with tests-go-test")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		t.Fatalf("the system under test answered %d", resp.StatusCode)
	}
}
