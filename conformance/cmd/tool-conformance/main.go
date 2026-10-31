// Command tool-conformance checks one tool-invocation record the way the engine's gate does, minus the egress
// wall: it validates the record, checks the pin it can check, runs the argv against a fresh copy of the
// record's fixture, maps the exit code through exit_map, and checks the output the tool wrote. It stops at the
// first failure and prints one line per check, "(<letter>) <STATUS>: <text>".
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

const egressRemark = "remark: egress was NOT observed: this runner does not confine the tool and cannot see its network traffic, so no line above says anything about it; the engine's gate does the confining and the observing."

type report struct {
	w                  io.Writer
	pass, fail, na, un int
}

func (r *report) line(letter, status, text string) {
	switch status {
	case "PASS":
		r.pass++
	case "FAIL":
		r.fail++
	case "NOT APPLICABLE":
		r.na++
	case "UNMEASURED":
		r.un++
	}
	fmt.Fprintf(r.w, "(%s) %s: %s\n", letter, status, text)
}

func (r *report) detail(lines ...string) {
	for _, l := range lines {
		fmt.Fprintf(r.w, "    %s\n", l)
	}
}

func main() { os.Exit(run(os.Args[1:], os.Stdout)) }

// run returns 0 when no check failed, 1 when one did, and 2 for a usage error. The egress remark is the last
// line on every path, the exit-2 ones included.
func run(args []string, out io.Writer) int {
	code := runChecks(args, out)
	fmt.Fprintln(out, egressRemark)
	return code
}

func runChecks(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("tool-conformance", flag.ContinueOnError)
	fs.SetOutput(out)
	recordPath := fs.String("record", "", "the tool-invocation record to check (YAML or JSON)")
	fixturesRoot := fs.String("fixtures-root", "", "the directory that holds the fixture repositories")
	outputSchema := fs.String("output-schema", "", "a JSON Schema file for a sarif, cyclonedx or json output")
	assetFile := fs.String("asset", "", "the file an asset pin names; its sha256 is compared with the pin")
	recordSchema := fs.String("record-schema", "", "override the tool-invocation/v1 schema the runner carries with this file; its sha256 is printed")
	timeout := fs.Duration("timeout", 60*time.Second, "how long the tool may run")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *recordPath == "" || *fixturesRoot == "" || fs.NArg() > 0 {
		fmt.Fprintln(out, "usage: tool-conformance --record <file> --fixtures-root <dir> [--output-schema <file>] [--asset <file>] [--record-schema <file>] [--timeout <duration>]")
		return 2
	}
	var (
		schema   *jsonschema.Schema
		err      error
		schemaID string // what check (a) prints to say which schema bytes judged the record
	)
	if *recordSchema == "" {
		schema, err = compileEmbeddedRecordSchema()
		sum := sha256.Sum256(embeddedRecordSchema)
		schemaID = fmt.Sprintf("the embedded schema (sha256 %s)", hex.EncodeToString(sum[:]))
	} else {
		var sum string
		if sum, err = fileSHA256(*recordSchema); err == nil {
			schema, err = compileSchemaFile(*recordSchema)
		}
		schemaID = fmt.Sprintf("the schema file %s (sha256 %s)", *recordSchema, sum)
	}
	if err != nil {
		fmt.Fprintf(out, "cannot load the record schema: %v\n", err)
		return 2
	}

	r := &report{w: out}
	check(r, schema, schemaID, *recordPath, *fixturesRoot, *outputSchema, *assetFile, *timeout)
	summary := fmt.Sprintf("summary: %d PASS, %d FAIL, %d NOT APPLICABLE, %d UNMEASURED", r.pass, r.fail, r.na, r.un)
	if r.fail > 0 {
		summary += "; the checks after the first FAIL were not run"
	}
	fmt.Fprintln(out, summary)
	if r.fail > 0 {
		return 1
	}
	return 0
}

// check runs (a) to (e) in order and returns at the first FAIL.
func check(r *report, schema interface{ Validate(any) error }, schemaID, recordPath, fixturesRoot, outputSchema, assetFile string, timeout time.Duration) {
	// (a) the record
	doc, err := loadDocument(recordPath)
	if err != nil {
		r.line("a", "FAIL", fmt.Sprintf("cannot read the record %s: %v", recordPath, err))
		return
	}
	problems := extraRules(doc)
	if err := schema.Validate(doc); err != nil {
		problems = append(problems, schemaProblems(err)...)
	}
	if len(problems) > 0 {
		r.line("a", "FAIL", fmt.Sprintf("the record is refused, %d problem(s); judged against %s and the runner's own rules", len(problems), schemaID))
		r.detail(problems...)
		return
	}
	var rec record
	if err := json.Unmarshal(mustJSON(doc), &rec); err != nil {
		r.line("a", "FAIL", fmt.Sprintf("the record validates but cannot be read: %v", err))
		return
	}
	r.line("a", "PASS", fmt.Sprintf("record %q validates against %s and the runner's own rules (schema key, required keys, params)", rec.ID, schemaID))

	// (b) the pin
	if !checkPin(r, &rec, assetFile) {
		return
	}

	// (c) the run, in a fresh copy of the fixture
	src := filepath.Join(fixturesRoot, filepath.FromSlash(rec.Fixture.Repo))
	if st, err := os.Stat(src); err != nil || !st.IsDir() {
		r.line("c", "FAIL", fmt.Sprintf("fixture.repo %q is not a directory under --fixtures-root %s", rec.Fixture.Repo, fixturesRoot))
		return
	}
	work, err := os.MkdirTemp("", "tool-conformance-*")
	if err != nil {
		r.line("c", "FAIL", fmt.Sprintf("cannot make a scratch directory: %v", err))
		return
	}
	defer os.RemoveAll(work)
	root := filepath.Join(work, "repo")
	if err := copyTree(src, root); err != nil {
		r.line("c", "FAIL", fmt.Sprintf("cannot copy fixture.repo %q: %v", rec.Fixture.Repo, err))
		return
	}
	outPath := filepath.Join(root, filepath.FromSlash(rec.Output.Path))
	if _, err := os.Lstat(outPath); err == nil {
		r.line("c", "FAIL", fmt.Sprintf("output.path %q already exists in the fixture, so a file there would prove nothing about the tool", rec.Output.Path))
		return
	}
	res, err := runTool(&rec, root, timeout)
	if err != nil {
		r.line("c", "FAIL", err.Error())
		if res.tail != "" {
			r.detail("tool output (tail): " + res.tail)
		}
		return
	}
	r.line("c", "PASS", fmt.Sprintf("the argv ran in a fresh copy of fixture %q and exited with code %d", rec.Fixture.Repo, res.code))

	// (d) the exit code
	if !checkExit(r, &rec, res) {
		return
	}

	// (e) the output
	checkOutput(r, &rec, outPath, outputSchema)
}

func checkPin(r *report, rec *record, assetFile string) bool {
	if rec.Pin.Image != "" {
		msg := "this runner does not pull images; the image pin was not checked"
		if assetFile != "" {
			msg += fmt.Sprintf("; --asset %s was given and ignored, because an image pin names no file", assetFile)
		}
		r.line("b", "NOT APPLICABLE", msg)
		return true
	}
	if assetFile == "" {
		r.line("b", "UNMEASURED", fmt.Sprintf("an asset pin (%s) needs --asset <file> to be checked; none was given", rec.Pin.Asset))
		return true
	}
	got, err := fileSHA256(assetFile)
	if err != nil {
		r.line("b", "FAIL", fmt.Sprintf("cannot read --asset: %v", err))
		return false
	}
	if got != rec.Pin.SHA256 {
		r.line("b", "FAIL", fmt.Sprintf("pin.sha256 is %s but %s has sha256 %s", rec.Pin.SHA256, assetFile, got))
		return false
	}
	r.line("b", "PASS", fmt.Sprintf("pin.sha256 matches the sha256 of %s", assetFile))
	r.detail("remark: the runner hashes the file you name; it does not check that argv[0] resolves to that file.")
	return true
}

func checkExit(r *report, rec *record, res runResult) bool {
	class, key := classOf(rec, res.code)
	field := "exit_map." + key
	if class != rec.Fixture.Expect {
		r.line("d", "FAIL", fmt.Sprintf("exit code %d maps to %q through %s, but fixture.expect is %q", res.code, class, field, rec.Fixture.Expect))
		return false
	}
	r.line("d", "PASS", fmt.Sprintf("exit code %d maps to %q through %s, which is fixture.expect", res.code, class, field))
	return true
}

func checkOutput(r *report, rec *record, outPath, schemaPath string) {
	raw, err := os.ReadFile(outPath)
	if err != nil {
		r.line("e", "FAIL", fmt.Sprintf("output.path %q was not written by the tool", rec.Output.Path))
		return
	}
	if rec.Output.Format == "junit-xml" {
		o, msg := checkJUnit(raw)
		if o == outFail {
			r.line("e", "FAIL", msg)
			return
		}
		r.line("e", "PASS", msg)
		return
	}
	o, msgs := checkJSONOutput(rec.Output.Format, outPath, schemaPath)
	status := map[outcome]string{outPass: "PASS", outFail: "FAIL", outUnmeasured: "UNMEASURED"}[o]
	r.line("e", status, strings.TrimSpace(msgs[0]))
	r.detail(msgs[1:]...)
}
