package sampleadapter

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
)

// junitFileName is the result file the import profile writes and then reads.
const junitFileName = "junit.xml"

// The JUnit XML shape both the writer and the reader know: a testsuite of testcase elements, each
// with a time in seconds and, when it did not pass, one of failure, error or skipped. The reader
// takes a testsuites or a testsuite root and every testcase beneath it, in document order.
type junitSuite struct {
	XMLName  xml.Name    `xml:"testsuite"`
	Name     string      `xml:"name,attr"`
	Tests    int         `xml:"tests,attr"`
	Failures int         `xml:"failures,attr"`
	Errors   int         `xml:"errors,attr"`
	Skipped  int         `xml:"skipped,attr"`
	Cases    []junitCase `xml:"testcase"`
}

type junitCase struct {
	Name    string        `xml:"name,attr"`
	Time    string        `xml:"time,attr"`
	Failure *junitProblem `xml:"failure"`
	Error   *junitProblem `xml:"error"`
	Skipped *junitProblem `xml:"skipped"`
}

type junitProblem struct {
	Message string `xml:"message,attr,omitempty"`
	Text    string `xml:",chardata"`
}

// runAndImport runs the project's items, writes a JUnit XML file of their outcomes and durations,
// and reads the items back from that file. What comes back is all an import run knows of the
// items: the report that follows is made from the file, never from the project.
func (a *adapter) runAndImport(proj Project) ([]Item, error) {
	dir, err := os.MkdirTemp("", "sample-adapter-junit-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir) // the scratch directory of this run only
	path := filepath.Join(dir, junitFileName)
	ran := make([]Item, len(proj.Tests))
	for i, it := range proj.Tests {
		if a.mutant == MutantSampleNoSkip && it.Outcome == "skip" {
			it.Outcome = "pass" // breaks one rule: a sample project with no skipped test
		}
		ran[i] = it // the item's body is a stand-in: its duration is the project's DurationMS
	}
	if err := writeJUnit(path, proj.Project, ran); err != nil {
		return nil, fmt.Errorf("cannot write the JUnit result file: %w", err)
	}
	items, raw, err := readJUnit(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read the JUnit result file: %w", err)
	}
	for i := range items { // the mock scenario belongs to the project, not to the result file
		if i < len(ran) {
			items[i].Scenario = ran[i].Scenario
		}
	}
	if len(items) > 0 {
		items[0].Evidence = string(raw) // the result file itself is the evidence, on the first step
	}
	a.evidenceKind = "artifact"
	return items, nil
}

func writeJUnit(path, project string, items []Item) error {
	s := junitSuite{Name: project, Tests: len(items)}
	for _, it := range items {
		c := junitCase{Name: it.Name, Time: strconv.FormatFloat(float64(it.DurationMS)/1000, 'f', 3, 64)}
		switch it.Outcome {
		case "fail":
			s.Failures++
			c.Failure = &junitProblem{Message: it.Failure, Text: it.Failure}
		case "error":
			s.Errors++
			c.Error = &junitProblem{Message: it.Failure, Text: it.Failure}
		case "skip":
			s.Skipped++
			c.Skipped = &junitProblem{}
		}
		s.Cases = append(s.Cases, c)
	}
	doc, err := xml.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append([]byte(xml.Header), append(doc, '\n')...), 0o600)
}

// readJUnit reads every testcase of a JUnit XML file, in order, and returns the items with the
// raw bytes it read. An element named failure or error carries the failure text in its message
// attribute or, when that is empty, its text.
func readJUnit(path string) ([]Item, []byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	dec := xml.NewDecoder(bytes.NewReader(raw))
	var items []Item
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		start, isStart := tok.(xml.StartElement)
		if !isStart || start.Name.Local != "testcase" {
			continue
		}
		var c junitCase
		if err := dec.DecodeElement(&c, &start); err != nil {
			return nil, nil, err
		}
		it := Item{Name: c.Name, Outcome: "pass"}
		switch {
		case c.Failure != nil:
			it.Outcome, it.Failure = "fail", problemText(c.Failure)
		case c.Error != nil:
			it.Outcome, it.Failure = "error", problemText(c.Error)
		case c.Skipped != nil:
			it.Outcome = "skip"
		}
		if c.Time != "" {
			secs, err := strconv.ParseFloat(c.Time, 64)
			if err != nil || secs < 0 {
				return nil, nil, fmt.Errorf("testcase %q has time %q; it must be a number of seconds, 0 or more", c.Name, c.Time)
			}
			it.DurationMS = int64(math.Round(secs * 1000))
		}
		items = append(items, it)
	}
	if len(items) == 0 {
		return nil, nil, errors.New("the file holds no testcase")
	}
	return items, raw, nil
}

func problemText(p *junitProblem) string {
	if p.Message != "" {
		return p.Message
	}
	if t := bytes.TrimSpace([]byte(p.Text)); len(t) > 0 {
		return string(t)
	}
	return "the test failed without a message"
}
