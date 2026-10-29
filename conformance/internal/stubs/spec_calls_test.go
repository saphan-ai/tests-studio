package stubs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSpecCallsTableHasOneRowPerOperation reads the table of section 6 of spec/TEST_RUNNER_V1.md and
// checks that every operationId of spec/runner-v1.openapi.yaml appears on a row of its own with exactly
// three cells. A Markdown table drops cells past the header's three, so two rows fused on one line
// publish a table that silently omits an operation.
func TestSpecCallsTableHasOneRowPerOperation(t *testing.T) {
	doc, dir := loadYAML(t, "runner-v1.openapi.yaml")
	raw, err := os.ReadFile(filepath.Join(dir, "TEST_RUNNER_V1.md"))
	if err != nil {
		t.Fatal(err)
	}
	rows := callsTableRows(t, string(raw))

	ids := map[string]int{}
	for i, row := range rows {
		cells := strings.Split(strings.Trim(strings.TrimSpace(row), "|"), "|")
		if len(cells) != 3 {
			t.Errorf("section 6 row %d has %d cells, want 3: %s", i+1, len(cells), row)
			continue
		}
		ids[strings.Trim(strings.TrimSpace(cells[1]), "`")]++
	}
	paths, _ := doc["paths"].(map[string]any)
	for p, item := range paths {
		for _, m := range httpMethods {
			op, ok := item.(map[string]any)[m].(map[string]any)
			if !ok {
				continue
			}
			id, _ := op["operationId"].(string)
			if ids[id] != 1 {
				t.Errorf("operation %s (%s %s) is on %d rows of section 6, want exactly 1", id, strings.ToUpper(m), p, ids[id])
			}
		}
	}
}

// callsTableRows returns the body rows of the first table under the heading "## 6. The calls".
func callsTableRows(t *testing.T, md string) []string {
	t.Helper()
	_, after, ok := strings.Cut(md, "\n## 6. The calls")
	if !ok {
		t.Fatal(`spec/TEST_RUNNER_V1.md has no heading "## 6. The calls"`)
	}
	var rows []string
	inTable := false
	for _, line := range strings.Split(after, "\n") {
		if strings.HasPrefix(line, "## ") {
			break
		}
		if !strings.HasPrefix(line, "|") {
			if inTable {
				break
			}
			continue
		}
		inTable = true
		rows = append(rows, line)
	}
	if len(rows) < 3 {
		t.Fatalf("section 6 has no table with a header, a separator and at least one row")
	}
	return rows[2:]
}
