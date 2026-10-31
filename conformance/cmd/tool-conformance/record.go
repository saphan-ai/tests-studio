package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v5"
	"gopkg.in/yaml.v3"
)

// metaChars are the characters refused in any argv element of a record.
const metaChars = ";|&$`<>\n"

var (
	placeholderRe  = regexp.MustCompile(`\{\{([^{}]*)\}\}`)
	closedInputs   = []string{"repo_root", "output_path", "config_path"}
	allowedFormats = []string{"sarif-2.1.0", "junit-xml", "cyclonedx-1.5", "json"}

	topKeys = []string{"schema", "id", "ecosystem", "version", "pin", "licence_class", "command", "config",
		"extra_inputs", "inputs", "egress", "exit_map", "output", "fixture"}
	pinKeys     = []string{"image", "asset", "sha256"}
	outputKeys  = []string{"format", "path"}
	fixtureKeys = []string{"repo", "expect"}
	extraKeys   = []string{"type", "value"}
)

// record is the typed view of a record that has already passed check (a).
type record struct {
	ID      string   `json:"id"`
	Command []string `json:"command"`
	Config  string   `json:"config"`
	Pin     struct {
		Image  string `json:"image"`
		Asset  string `json:"asset"`
		SHA256 string `json:"sha256"`
	} `json:"pin"`
	ExtraInputs map[string]struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"extra_inputs"`
	ExitMap map[string]string `json:"exit_map"`
	Output  struct {
		Format string `json:"format"`
		Path   string `json:"path"`
	} `json:"output"`
	Fixture struct {
		Repo   string `json:"repo"`
		Expect string `json:"expect"`
	} `json:"fixture"`
}

// loadDocument reads a YAML or JSON record into a JSON-shaped value. A duplicate key is an error, because a
// parser that keeps the last one hides the first.
func loadDocument(path string) (any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var n yaml.Node
	if err := yaml.Unmarshal(raw, &n); err != nil {
		return nil, fmt.Errorf("not valid YAML or JSON: %w", err)
	}
	if n.Kind == 0 {
		return nil, errors.New("the file is empty")
	}
	v, err := nodeToAny(&n, "record")
	if err != nil {
		return nil, err
	}
	// Round-trip through JSON so numbers and keys are exactly what a JSON consumer sees.
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("record is not representable as JSON: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func nodeToAny(n *yaml.Node, path string) (any, error) {
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, errors.New("the file is empty")
		}
		return nodeToAny(n.Content[0], path)
	case yaml.AliasNode:
		return nodeToAny(n.Alias, path)
	case yaml.MappingNode:
		m := map[string]any{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode {
				return nil, fmt.Errorf("%s: a mapping key is not a scalar", path)
			}
			if _, dup := m[k.Value]; dup {
				return nil, fmt.Errorf("%s: duplicate key %q", path, k.Value)
			}
			v, err := nodeToAny(n.Content[i+1], path+"."+k.Value)
			if err != nil {
				return nil, err
			}
			m[k.Value] = v
		}
		return m, nil
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for i, c := range n.Content {
			v, err := nodeToAny(c, fmt.Sprintf("%s[%d]", path, i))
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	default:
		var v any
		if err := n.Decode(&v); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		return v, nil
	}
}

// compileSchemaFile compiles the schema at path; references that are relative resolve beside the file.
func compileSchemaFile(path string) (*jsonschema.Schema, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	c.Draft = jsonschema.Draft2020
	return c.Compile(abs)
}

// schemaProblems flattens a validation error to one line per leaf cause.
func schemaProblems(err error) []string {
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return []string{"schema: " + err.Error()}
	}
	var out []string
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			loc := strings.ReplaceAll(strings.TrimPrefix(e.InstanceLocation, "/"), "/", ".")
			if loc == "" {
				loc = "(record)"
			}
			out = append(out, fmt.Sprintf("schema: %s: %s", loc, e.Message))
			return
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(ve)
	return out
}

func obj(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func sortedKeys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// extraRules holds the rules a JSON Schema cannot state: they name the element and the character, list what
// is allowed, or look across fields. Every line it returns begins with "rule: ".
func extraRules(doc any) []string {
	m, ok := obj(doc)
	if !ok {
		return []string{"rule: (record): the record must be a mapping"}
	}
	var p []string
	add := func(format string, a ...any) { p = append(p, "rule: "+fmt.Sprintf(format, a...)) }

	checkKeys := func(where string, sub map[string]any, allowed []string) {
		for _, k := range sortedKeys(sub) {
			if !contains(allowed, k) {
				add("%s: unknown key %q; allowed keys: %s", where, k, strings.Join(allowed, ", "))
			}
		}
	}
	checkKeys("(record)", m, topKeys)
	if pin, ok := obj(m["pin"]); ok {
		checkKeys("pin", pin, pinKeys)
	}
	if o, ok := obj(m["output"]); ok {
		checkKeys("output", o, outputKeys)
		if f, ok := o["format"].(string); ok && !contains(allowedFormats, f) {
			add("output.format: %q is not one of: %s", f, strings.Join(allowedFormats, ", "))
		}
		if s, ok := o["path"].(string); ok {
			checkRel(add, "output.path", s, false)
		}
	}
	if f, ok := obj(m["fixture"]); ok {
		checkKeys("fixture", f, fixtureKeys)
		if s, ok := f["repo"].(string); ok {
			checkRel(add, "fixture.repo", s, false)
		}
	}
	if s, ok := m["config"].(string); ok {
		checkRel(add, "config", s, false)
	}
	if in, ok := m["inputs"].([]any); ok {
		for i, v := range in {
			if s, ok := v.(string); ok {
				checkRel(add, fmt.Sprintf("inputs[%d]", i), s, true)
			}
		}
	}

	declared := map[string]bool{}
	if ex, ok := obj(m["extra_inputs"]); ok {
		for _, name := range sortedKeys(ex) {
			declared[name] = true
			e, ok := obj(ex[name])
			if !ok {
				continue
			}
			where := "extra_inputs." + name
			checkKeys(where, e, extraKeys)
			val, _ := e["value"].(string)
			switch e["type"] {
			case "int":
				if _, err := strconv.Atoi(val); err != nil {
					add("%s.value: %q is not an integer", where, val)
				}
			case "path":
				checkRel(add, where+".value", val, false)
			case "string":
				if r, bad := firstMeta(val); bad {
					add("%s.value: contains the shell metacharacter %s", where, strconv.QuoteRune(r))
				}
			}
		}
	}

	_, hasConfig := m["config"]
	if cmd, ok := m["command"].([]any); ok {
		for i, v := range cmd {
			el, ok := v.(string)
			if !ok {
				continue
			}
			if r, bad := firstMeta(el); bad {
				add("command[%d]: element %q contains the shell metacharacter %s; the argv is exec'd directly, so shell syntax is refused", i, el, strconv.QuoteRune(r))
			}
			names := placeholderRe.FindAllStringSubmatch(el, -1)
			for _, nm := range names {
				name := nm[1]
				switch {
				case i == 0:
					add("command[0]: the program must be a literal, not the placeholder {{%s}}", name)
				case !contains(closedInputs, name) && !declared[name]:
					add("command[%d]: placeholder {{%s}} names no input; the closed set is %s, plus the names declared in extra_inputs", i, name, strings.Join(closedInputs, ", "))
				case name == "config_path" && !hasConfig:
					add("command[%d]: {{config_path}} is used but the record declares no \"config\"", i)
				}
			}
			rest := placeholderRe.ReplaceAllString(el, "")
			if strings.Contains(rest, "{{") || strings.Contains(rest, "}}") {
				add("command[%d]: element %q has an unbalanced or empty placeholder", i, el)
			}
		}
	}
	return p
}

func firstMeta(s string) (rune, bool) {
	for _, r := range s {
		if strings.ContainsRune(metaChars, r) {
			return r, true
		}
	}
	return 0, false
}

// checkRel refuses a path that climbs out of its root; a glob star is allowed only where the field reads files.
func checkRel(add func(string, ...any), field, s string, allowStar bool) {
	for _, seg := range strings.Split(s, "/") {
		if seg == ".." {
			add("%s: %q has a '..' segment; the path must stay under its root", field, s)
			return
		}
	}
	if !allowStar && strings.Contains(s, "*") {
		add("%s: %q has a '*'; only inputs may name a glob", field, s)
	}
}
