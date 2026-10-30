package gotest_test

import (
	"strings"
	"testing"

	"github.com/saphan-ai/tests-studio/adapters/go-test/gotest"
)

func TestCheckArgsRefusesEveryFlagThatMisreports(t *testing.T) {
	for _, f := range []string{"run", "skip", "count", "list", "json"} {
		for _, spelling := range []string{"-" + f, "--" + f, "-test." + f, "--test." + f} {
			for _, args := range [][]string{{spelling}, {spelling + "=x"}, {"-race", spelling, "x"}} {
				err := gotest.CheckArgs(args)
				if err == nil || !strings.Contains(err.Error(), "-"+f) || !strings.Contains(err.Error(), "NEXT") {
					t.Errorf("CheckArgs(%v) = %v, want a refusal that names -%s and the next act", args, err, f)
				}
			}
		}
	}
	for _, args := range [][]string{nil, {"-race"}, {"-tags", "integration"}, {"-timeout", "30s"}, {"-tags=count", "-v"}, {"-args", "-run", "x"}} {
		if err := gotest.CheckArgs(args); err != nil {
			t.Errorf("CheckArgs(%v) refused: %v", args, err)
		}
	}
}
