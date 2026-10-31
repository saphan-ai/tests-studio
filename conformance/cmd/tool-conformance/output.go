package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

type outcome int

const (
	outPass outcome = iota
	outFail
	outUnmeasured
)

type junitSuite struct {
	Name   string       `xml:"name,attr"`
	Cases  []junitCase  `xml:"testcase"`
	Suites []junitSuite `xml:"testsuite"`
}

type junitCase struct {
	Classname string `xml:"classname,attr"`
	Name      string `xml:"name,attr"`
}

// checkJUnit measures the structure only: the root, the testcases, and the uniqueness of (classname, name)
// inside each suite. It does not read pass or fail out of the cases.
func checkJUnit(raw []byte) (outcome, string) {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	var root string
	for {
		tok, err := dec.Token()
		if err != nil {
			return outFail, fmt.Sprintf("output is not well-formed XML: %v", err)
		}
		if se, ok := tok.(xml.StartElement); ok {
			root = se.Name.Local
			break
		}
	}
	if root != "testsuites" && root != "testsuite" {
		return outFail, fmt.Sprintf("root element is <%s>; JUnit XML has <testsuites> or <testsuite>", root)
	}
	var top junitSuite
	if err := xml.Unmarshal(raw, &top); err != nil {
		return outFail, fmt.Sprintf("output is not well-formed XML: %v", err)
	}
	cases := 0
	var walk func(s junitSuite, where string) string
	walk = func(s junitSuite, where string) string {
		seen := map[[2]string]bool{}
		for _, c := range s.Cases {
			cases++
			if c.Name == "" {
				return fmt.Sprintf("%s: a <testcase> has no name attribute", where)
			}
			k := [2]string{c.Classname, c.Name}
			if seen[k] {
				return fmt.Sprintf("%s: the (classname, name) pair (%q, %q) appears twice in one suite", where, c.Classname, c.Name)
			}
			seen[k] = true
		}
		for i, sub := range s.Suites {
			if msg := walk(sub, fmt.Sprintf("%s/testsuite[%d]", where, i)); msg != "" {
				return msg
			}
		}
		return ""
	}
	if msg := walk(top, root); msg != "" {
		return outFail, msg
	}
	if cases == 0 {
		return outFail, "the output holds no <testcase>"
	}
	return outPass, fmt.Sprintf("JUnit XML is structurally valid (%d testcases)", cases)
}

// checkJSONOutput parses a JSON-family output, runs the cheap identity checks for its format, and validates
// against the user's schema file when one is given. Without a schema the shape is UNMEASURED, never a pass.
func checkJSONOutput(format, outPath, schemaPath string) (outcome, []string) {
	raw, err := os.ReadFile(outPath)
	if err != nil {
		return outFail, []string{err.Error()}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return outFail, []string{fmt.Sprintf("output is not well-formed JSON: %v", err)}
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return outFail, []string{"output.path is not well-formed JSON: data follows the first JSON value; the file must hold exactly one"}
	}
	if bad := identityProblem(format, doc); bad != "" {
		return outFail, []string{bad}
	}
	if schemaPath == "" {
		return outUnmeasured, []string{"no schema given (pass --output-schema <file>); the output parses as JSON and nothing more was measured"}
	}
	sum, err := fileSHA256(schemaPath)
	if err != nil {
		return outFail, []string{fmt.Sprintf("cannot read --output-schema: %v", err)}
	}
	sch, err := compileSchemaFile(schemaPath)
	if err != nil {
		return outUnmeasured, []string{fmt.Sprintf("the schema file (sha256 %s) could not be compiled, so nothing was validated: %v", sum, err)}
	}
	if err := sch.Validate(doc); err != nil {
		return outFail, append([]string{fmt.Sprintf("output does not validate against the schema file (sha256 %s)", sum)}, schemaProblems(err)...)
	}
	return outPass, []string{fmt.Sprintf("output validates against the schema file you passed (sha256 %s)", sum)}
}

func identityProblem(format string, doc any) string {
	m, _ := obj(doc)
	switch format {
	case "sarif-2.1.0":
		if v, _ := m["version"].(string); v != "2.1.0" {
			return fmt.Sprintf("SARIF \"version\" is %v; it must be \"2.1.0\"", m["version"])
		}
		if runs, ok := m["runs"].([]any); !ok || len(runs) == 0 {
			return "SARIF \"runs\" must be a non-empty array"
		}
	case "cyclonedx-1.5":
		if v, _ := m["bomFormat"].(string); v != "CycloneDX" {
			return fmt.Sprintf("CycloneDX \"bomFormat\" is %v; it must be \"CycloneDX\"", m["bomFormat"])
		}
		if v, _ := m["specVersion"].(string); v != "1.5" {
			return fmt.Sprintf("CycloneDX \"specVersion\" is %v; it must be \"1.5\"", m["specVersion"])
		}
	case "json":
		if strings.TrimSpace(string(mustJSON(doc))) == "null" {
			return "output is the JSON value null"
		}
	}
	return ""
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
