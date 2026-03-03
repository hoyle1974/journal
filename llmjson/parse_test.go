package llmjson

import (
	"encoding/json"
	"testing"
)

func TestRepairAndUnmarshal_ValidJSON(t *testing.T) {
	// Valid JSON unchanged
	text := `{"a": 1, "b": ["x", "y"]}`
	var v map[string]interface{}
	if err := RepairAndUnmarshal(text, &v); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v["a"].(float64) != 1 {
		t.Errorf("a: got %v", v["a"])
	}
	arr := v["b"].([]interface{})
	if len(arr) != 2 || arr[0].(string) != "x" {
		t.Errorf("b: got %v", arr)
	}
}

func TestRepairAndUnmarshal_TrailingComma(t *testing.T) {
	text := `{"relationship": ["a", "b"], "work": [],}`
	var v struct {
		Relationship []string `json:"relationship"`
		Work         []string `json:"work"`
	}
	if err := RepairAndUnmarshal(text, &v); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(v.Relationship) != 2 || v.Relationship[0] != "a" {
		t.Errorf("relationship: got %v", v.Relationship)
	}
}

func TestRepairAndUnmarshal_TruncatedMissingClose(t *testing.T) {
	// Truncated: missing closing }
	text := `{"summary": "ok", "facts": ["one", "two"]`
	var v struct {
		Summary string   `json:"summary"`
		Facts   []string `json:"facts"`
	}
	if err := RepairAndUnmarshal(text, &v); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Summary != "ok" || len(v.Facts) != 2 {
		t.Errorf("got summary=%q facts=%v", v.Summary, v.Facts)
	}
}

func TestRepairAndUnmarshal_TruncatedArray(t *testing.T) {
	text := `{"phases": [{"title": "A", "description": "B"}`
	var v struct {
		Phases []struct {
			Title       string `json:"title"`
			Description string `json:"description"`
		} `json:"phases"`
	}
	if err := RepairAndUnmarshal(text, &v); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(v.Phases) != 1 || v.Phases[0].Title != "A" {
		t.Errorf("phases: got %+v", v.Phases)
	}
}

func TestPartialUnmarshalObject_FullJSON(t *testing.T) {
	text := `{"relationship": ["a"], "work": ["b"], "task": []}`
	keys := []string{"relationship", "work", "task"}
	m, err := PartialUnmarshalObject(text, keys)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m) != 3 {
		t.Errorf("got %d keys: %v", len(m), m)
	}
	var rel []string
	if err := json.Unmarshal(m["relationship"], &rel); err != nil || len(rel) != 1 || rel[0] != "a" {
		t.Errorf("relationship: err=%v got %v", err, rel)
	}
}

func TestPartialUnmarshalObject_TruncatedAfterFirstKey(t *testing.T) {
	// Truncated after first key's value
	text := `{"relationship": ["alice", "bob"], "work"`
	m, _ := PartialUnmarshalObject(text, []string{"relationship", "work", "task"})
	if m == nil {
		t.Fatal("expected non-nil map")
	}
	if _, ok := m["relationship"]; !ok {
		t.Errorf("expected relationship key: %v", m)
	}
	var rel []string
	if err := json.Unmarshal(m["relationship"], &rel); err != nil || len(rel) != 2 || rel[0] != "alice" {
		t.Errorf("relationship: err=%v got %v", err, rel)
	}
	// work and task may or may not be present depending on extractor
	_ = m
}

func TestPartialUnmarshalObject_MalformedExtraComma(t *testing.T) {
	// Malformed: extra comma but one key valid
	text := `{"summary": "hello",, "facts": ["a"]}`
	m, _ := PartialUnmarshalObject(text, []string{"summary", "facts"})
	if m == nil {
		t.Fatal("expected non-nil map")
	}
	// Strict and repaired parse may fail; best-effort should still get at least one key
	if len(m) == 0 {
		t.Errorf("expected at least one key from best-effort: %v", m)
	}
}

func TestPartialUnmarshalObject_TruncatedMidKey(t *testing.T) {
	// Only "relationship" is complete
	text := `{"relationship": ["x"], "work": [`
	m, _ := PartialUnmarshalObject(text, []string{"relationship", "work"})
	if m == nil {
		t.Fatal("expected non-nil map")
	}
	if raw, ok := m["relationship"]; !ok || len(raw) == 0 {
		t.Errorf("expected relationship: %v", m)
	}
}

func TestRepair_UnchangedValid(t *testing.T) {
	valid := `{"a": 1}`
	repaired := Repair(valid)
	if repaired != valid {
		t.Errorf("valid JSON should be unchanged: got %q", repaired)
	}
}

func TestRepair_TrailingCommaRemoved(t *testing.T) {
	text := `{"x": 1,}`
	repaired := Repair(text)
	var v map[string]interface{}
	if err := json.Unmarshal([]byte(repaired), &v); err != nil {
		t.Errorf("repaired should parse: %v", err)
	}
	if v["x"].(float64) != 1 {
		t.Errorf("got %v", v)
	}
}
