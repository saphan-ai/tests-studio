package sampleadapter

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestJUnitFileRoundTrips pins that what the writer puts in the result file is what the reader
// takes out: names, outcomes, failure text and durations to the millisecond, in order.
func TestJUnitFileRoundTrips(t *testing.T) {
	t.Parallel()
	in := []Item{
		{Name: "TestPasses", Outcome: "pass", DurationMS: 1250},
		{Name: "TestFails", Outcome: "fail", DurationMS: 37, Failure: `expected "3" & got <4>`},
		{Name: "TestSkipped", Outcome: "skip"},
		{Name: "TestFixtureFails", Outcome: "error", DurationMS: 4, Failure: "fixture setup failed"},
	}
	path := filepath.Join(t.TempDir(), "junit.xml")
	if err := writeJUnit(path, "p", in); err != nil {
		t.Fatal(err)
	}
	got, raw, err := readJUnit(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("read back %+v\nwrote     %+v", got, in)
	}
	if !strings.Contains(string(raw), `tests="4" failures="1" errors="1" skipped="1"`) {
		t.Errorf("the suite element does not carry the counts: %s", raw)
	}
}

// TestJUnitReaderTakesWhatRealToolsWrite reads a testsuites root with several suites, a failure
// whose text is in the element body instead of a message attribute, and a time with no decimals.
func TestJUnitReaderTakesWhatRealToolsWrite(t *testing.T) {
	t.Parallel()
	doc := `<?xml version="1.0" encoding="UTF-8"?>
<testsuites>
  <testsuite name="a" tests="2"><testcase name="one" classname="x" time="2"/>
    <testcase name="two" time="0.0005"><failure type="AssertionError">boom
  at line 3</failure></testcase></testsuite>
  <testsuite name="b" tests="1"><testcase name="three" time="0.001"><skipped message="not today"/></testcase></testsuite>
</testsuites>`
	path := filepath.Join(t.TempDir(), "junit.xml")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	got, _, err := readJUnit(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []Item{
		{Name: "one", Outcome: "pass", DurationMS: 2000},
		{Name: "two", Outcome: "fail", DurationMS: 1, Failure: "boom\n  at line 3"},
		{Name: "three", Outcome: "skip", DurationMS: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestJUnitReaderRefusesWhatItCannotRead(t *testing.T) {
	t.Parallel()
	for name, doc := range map[string]string{
		"no testcase":   `<testsuite name="a"/>`,
		"not xml":       `{"json": true}`,
		"negative time": `<testsuite><testcase name="x" time="-1"/></testsuite>`,
		"text time":     `<testsuite><testcase name="x" time="soon"/></testsuite>`,
	} {
		path := filepath.Join(t.TempDir(), "junit.xml")
		if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := readJUnit(path); err == nil {
			t.Errorf("%s: the reader accepted %q", name, doc)
		}
	}
}

func TestAdapterRefusesAnUnknownProfile(t *testing.T) {
	t.Parallel()
	var stderr strings.Builder
	if code := Main([]string{"--project", "none", "--profile", "batch"}, nil, nil, &strings.Builder{}, &stderr); code != 2 || !strings.Contains(stderr.String(), "must be live or import") {
		t.Fatalf("exit %d, stderr %q; want exit 2 naming the two profiles", code, stderr.String())
	}
}
