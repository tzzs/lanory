package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Helpers for inspecting raw JSON in route tests.

// jsonFieldType returns the JSON kind ("array", "object", "string", ...) of a
// top level field, or "missing".
func jsonFieldType(t *testing.T, body []byte, field string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("body is not a JSON object: %v\n%s", err, body)
	}
	raw, ok := m[field]
	if !ok {
		return "missing"
	}
	s := strings.TrimSpace(string(raw))
	switch {
	case s == "null":
		return "null"
	case s == "":
		return "empty"
	case s[0] == '[':
		return "array"
	case s[0] == '{':
		return "object"
	case s[0] == '"':
		return "string"
	case s == "true", s == "false":
		return "boolean"
	default:
		return "number"
	}
}

// jsonFieldOf returns the raw JSON of a nested object inside a list, matched by
// its "id" field. It returns "" when no element matches.
func jsonFieldOf(t *testing.T, body []byte, listField, id string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("body is not a JSON object: %v", err)
	}
	raw, ok := m[listField]
	if !ok {
		return ""
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatalf("%s is not a JSON array of objects: %v", listField, err)
	}
	for _, item := range items {
		if got, ok := item["id"]; ok && strings.Trim(string(got), `"`) == id {
			b, err := json.Marshal(item)
			if err != nil {
				return ""
			}
			return string(b)
		}
	}
	return ""
}

// writeFile writes content to path, creating it.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := writeTestFile(path, content); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// writeTestFile is the filesystem primitive behind writeFile. It creates the
// parent directory so a test can write into assets/ without an extra step.
func writeTestFile(path, content string) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// jsonUnmarshal is a thin wrapper so tests do not import encoding/json twice.
func jsonUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// contextWithTimeout returns a context with a deadline, used by shutdown tests.
func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}
