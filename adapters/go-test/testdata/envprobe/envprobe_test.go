// Package envprobe is a fixture for the adapter's own tests: its tests write what they find in
// their environment to the file named by ENVPROBE_OUT, so a test of the adapter can read what a
// started test item was handed without any socket.
package envprobe

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/saphan-ai/tests-studio/adapters/go-test/tracectx"
)

func TestReportsItsEnvironment(t *testing.T) {
	tp, bg, ok := tracectx.Get()
	var leaked []string
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasSuffix(k, "_TOKEN") {
			leaked = append(leaked, k)
		}
	}
	doc, err := json.Marshal(map[string]any{"traceparent": tp, "baggage": bg, "ok": ok, "token_variables": leaked})
	if err != nil {
		t.Fatal(err)
	}
	if out := os.Getenv("ENVPROBE_OUT"); out != "" {
		if err := os.WriteFile(out, doc, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPanics(t *testing.T) {
	panic("deliberate panic of the fixture")
}

func TestAlsoPasses(t *testing.T) {}

func TestSkipsToo(t *testing.T) { t.Skip("fixture skip") }

func TestFailsInSubtest(t *testing.T) {
	t.Run("inner", func(t *testing.T) { t.Error("inner failure text") })
}
