// Package schemas loads the JSON Schema 2020-12 documents of the specification and validates
// documents against them. It uses an RE2 regular-expression engine, the dialect the schemas'
// patterns are written for (spec/TEST_RUNNER_V1.md, section "Schemas").
package schemas

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// File names of the schemas, relative to the specification directory.
const (
	TestsEventFile    = "schemas/tests-event-v1.schema.json"
	RunResultFile     = "schemas/tests-run-result-v1.schema.json"
	MocksEventFile    = "ecosystem-v1/mocks-event-v1.schema.json"
	specMarkerFile    = "TEST_RUNNER_V1.md"
	maxSpecDirClimbUp = 8
)

// Compile compiles one schema document read from path with the 2020-12 draft.
func Compile(path string) (*jsonschema.Schema, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read schema %s: %w", path, err)
	}
	return CompileBytes(filepath.Base(path), raw)
}

// CompileBytes compiles one schema document held in memory under the given resource name.
func CompileBytes(name string, raw []byte) (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	c.Draft = jsonschema.Draft2020
	if err := c.AddResource(name, bytes.NewReader(raw)); err != nil {
		return nil, fmt.Errorf("add schema %s: %w", name, err)
	}
	s, err := c.Compile(name)
	if err != nil {
		return nil, fmt.Errorf("compile schema %s: %w", name, err)
	}
	return s, nil
}

// Validator validates documents against the schemas of the Tests Studio specification.
type Validator struct {
	Event     *jsonschema.Schema
	RunResult *jsonschema.Schema
}

// Load compiles the Tests Studio schemas found under specDir.
func Load(specDir string) (*Validator, error) {
	ev, err := Compile(filepath.Join(specDir, TestsEventFile))
	if err != nil {
		return nil, err
	}
	rr, err := Compile(filepath.Join(specDir, RunResultFile))
	if err != nil {
		return nil, err
	}
	return &Validator{Event: ev, RunResult: rr}, nil
}

// ValidateEvent reports why raw is not a valid tests.event/v1 document, or nil.
func (v *Validator) ValidateEvent(raw []byte) error { return validate(v.Event, raw) }

// ValidateRunResult reports why raw is not a valid tests.run-result/v1 document, or nil.
func (v *Validator) ValidateRunResult(raw []byte) error { return validate(v.RunResult, raw) }

func validate(s *jsonschema.Schema, raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // the validator reads json.Number, so integers keep their exact value
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return fmt.Errorf("not JSON: %w", err)
	}
	if dec.More() {
		return errors.New("not JSON: trailing data after the document")
	}
	return s.Validate(doc)
}

// FindSpecDir returns the specification directory: the given one when non-empty, else the first
// directory named "spec" that holds TEST_RUNNER_V1.md, searched from start upwards.
func FindSpecDir(given, start string) (string, error) {
	if given != "" {
		return given, nil
	}
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for i := 0; i < maxSpecDirClimbUp; i++ {
		cand := filepath.Join(dir, "spec")
		if _, err := os.Stat(filepath.Join(cand, specMarkerFile)); err == nil {
			return cand, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", errors.New("no specification directory found: pass --spec-dir <path to the spec/ directory>")
}
