package memory

import (
	"strings"
	"testing"
)

func TestParseSPOTriple(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		input         string
		wantNil       bool
		wantSubject   string
		wantPredicate string
		wantObject    string
	}{
		{
			name:          "valid triple",
			input:         "Sarah | prefers | Oat Milk",
			wantNil:       false,
			wantSubject:   "Sarah",
			wantPredicate: "prefers",
			wantObject:    "Oat Milk",
		},
		{
			name:    "flat fact, no pipes",
			input:   "Sarah's birthday is March 5",
			wantNil: true,
		},
		{
			name:    "one pipe only",
			input:   "a | b",
			wantNil: true,
		},
		{
			// SplitN with n=3 means a fourth "|" ends up inside the Object field.
			// The implementation does NOT reject this — the object becomes "c | d".
			// This test documents the actual behaviour.
			name:          "four segments: extra pipe absorbed into Object",
			input:         "a | b | c | d",
			wantNil:       false,
			wantSubject:   "a",
			wantPredicate: "b",
			wantObject:    "c | d",
		},
		{
			name:          "extra whitespace is trimmed",
			input:         "  Alice  |  works at  |  ACME Corp  ",
			wantNil:       false,
			wantSubject:   "Alice",
			wantPredicate: "works at",
			wantObject:    "ACME Corp",
		},
		{
			name:    "empty subject",
			input:   " | prefers | Oat Milk",
			wantNil: true,
		},
		{
			name:    "empty predicate",
			input:   "Sarah |  | Oat Milk",
			wantNil: true,
		},
		{
			name:    "empty object",
			input:   "Sarah | prefers | ",
			wantNil: true,
		},
		{
			name:    "empty string",
			input:   "",
			wantNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ParseSPOTriple(tc.input)
			if tc.wantNil {
				if got != nil {
					t.Errorf("ParseSPOTriple(%q) = %+v, want nil", tc.input, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("ParseSPOTriple(%q) = nil, want non-nil triple", tc.input)
			}
			if got.Subject != tc.wantSubject {
				t.Errorf("Subject: got %q, want %q", got.Subject, tc.wantSubject)
			}
			if got.Predicate != tc.wantPredicate {
				t.Errorf("Predicate: got %q, want %q", got.Predicate, tc.wantPredicate)
			}
			if got.Object != tc.wantObject {
				t.Errorf("Object: got %q, want %q", got.Object, tc.wantObject)
			}
		})
	}
}

func TestNormalizedPredicate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"Is Part Of", "is_part_of"},
		{"works-at", "works_at"},
		{"prefers", "prefers"},
		{"Works At", "works_at"},
		{"LOVES", "loves"},
		{"has-sibling", "has_sibling"},
		{"  leading space  ", "leading_space"},
		{"mixed-spaces and-hyphens", "mixed_spaces_and_hyphens"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got := NormalizedPredicate(tc.input)
			if got != tc.want {
				t.Errorf("NormalizedPredicate(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestIsSPOTriple(t *testing.T) {
	t.Parallel()

	if !IsSPOTriple("Sarah | prefers | Oat Milk") {
		t.Error("IsSPOTriple: expected true for valid triple")
	}
	if IsSPOTriple("Sarah's birthday is March 5") {
		t.Error("IsSPOTriple: expected false for flat fact")
	}
}

func TestIsRegistered(t *testing.T) {
	t.Parallel()

	tests := []struct {
		nodeType string
		want     bool
	}{
		{"person", true},
		{"project", true},
		{"goal", true},
		{"preference", true},
		{"event", true},
		{"milestone", true},
		{"place", true},
		{"asset", true},
		{"tool", true},
		{"generic", true},
		{"user_identity", true},
		// Unregistered types.
		{"identity_anchor", false},
		{"weekly_summary", false},
		{"monthly_summary", false},
		{"unknown", false},
		{"", false},
	}

	for _, tc := range tests {
		t.Run(tc.nodeType, func(t *testing.T) {
			t.Parallel()
			got := IsRegistered(tc.nodeType)
			if got != tc.want {
				t.Errorf("IsRegistered(%q) = %v, want %v", tc.nodeType, got, tc.want)
			}
		})
	}
}

func TestValidateMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		nodeType  string
		meta      map[string]any
		wantErr   bool
		errSubstr string
	}{
		// nil map always errors regardless of type.
		{name: "nil map", nodeType: "person", meta: nil, wantErr: true, errSubstr: "metadata map is nil"},

		// Unregistered types always return nil.
		{name: "unregistered identity_anchor", nodeType: "identity_anchor", meta: map[string]any{}, wantErr: false},
		{name: "unregistered weekly_summary", nodeType: "weekly_summary", meta: map[string]any{}, wantErr: false},
		{name: "unregistered unknown", nodeType: "unknown", meta: map[string]any{}, wantErr: false},

		// person — no validation.
		{name: "person empty meta", nodeType: "person", meta: map[string]any{}, wantErr: false},
		{name: "person with fields", nodeType: "person", meta: map[string]any{"occupation": "chef"}, wantErr: false},

		// generic — no validation.
		{name: "generic empty", nodeType: "generic", meta: map[string]any{}, wantErr: false},

		// preference: invalid category.
		{name: "preference invalid category", nodeType: "preference", meta: map[string]any{"category": "music"}, wantErr: true, errSubstr: "invalid category"},
		// preference: invalid sentiment.
		{name: "preference invalid sentiment", nodeType: "preference", meta: map[string]any{"sentiment": "neutral"}, wantErr: true, errSubstr: "invalid sentiment"},
		// preference: valid category and sentiment.
		{name: "preference valid", nodeType: "preference", meta: map[string]any{"category": "food", "sentiment": "like"}, wantErr: false},
		// preference: case-insensitive.
		{name: "preference valid uppercase", nodeType: "preference", meta: map[string]any{"category": "FOOD", "sentiment": "LIKE"}, wantErr: false},

		// project: invalid status.
		{name: "project invalid status", nodeType: "project", meta: map[string]any{"status": "wip"}, wantErr: true, errSubstr: "invalid status"},
		// project: valid status.
		{name: "project valid status", nodeType: "project", meta: map[string]any{"status": "active"}, wantErr: false},
		// project: case-insensitive status.
		{name: "project uppercase status", nodeType: "project", meta: map[string]any{"status": "DONE"}, wantErr: false},

		// goal: same validators as project.
		{name: "goal invalid status", nodeType: "goal", meta: map[string]any{"status": "unknown"}, wantErr: true, errSubstr: "invalid status"},
		{name: "goal valid status", nodeType: "goal", meta: map[string]any{"status": "planning"}, wantErr: false},

		// event: invalid type.
		{name: "event invalid type", nodeType: "event", meta: map[string]any{"type": "party"}, wantErr: true, errSubstr: "invalid type"},
		// event: valid type.
		{name: "event valid type", nodeType: "event", meta: map[string]any{"type": "celebration"}, wantErr: false},
		// event: case-insensitive.
		{name: "event uppercase type", nodeType: "event", meta: map[string]any{"type": "WORK"}, wantErr: false},

		// milestone: same validators as event.
		{name: "milestone invalid type", nodeType: "milestone", meta: map[string]any{"type": "foo"}, wantErr: true, errSubstr: "invalid type"},
		{name: "milestone valid type", nodeType: "milestone", meta: map[string]any{"type": "health"}, wantErr: false},

		// place: invalid category.
		{name: "place invalid category", nodeType: "place", meta: map[string]any{"category": "museum"}, wantErr: true, errSubstr: "invalid category"},
		// place: valid category.
		{name: "place valid category", nodeType: "place", meta: map[string]any{"category": "home"}, wantErr: false},

		// asset: invalid type.
		{name: "asset invalid type", nodeType: "asset", meta: map[string]any{"type": "furniture"}, wantErr: true, errSubstr: "invalid type"},
		// asset: valid type.
		{name: "asset valid type", nodeType: "asset", meta: map[string]any{"type": "software"}, wantErr: false},

		// tool: same validators as asset.
		{name: "tool invalid type", nodeType: "tool", meta: map[string]any{"type": "organic"}, wantErr: true, errSubstr: "invalid type"},
		{name: "tool valid type", nodeType: "tool", meta: map[string]any{"type": "hardware"}, wantErr: false},

		// user_identity: invalid category.
		{name: "user_identity invalid category", nodeType: "user_identity", meta: map[string]any{"category": "hobby"}, wantErr: true, errSubstr: "invalid category"},
		// user_identity: valid category.
		{name: "user_identity valid category name", nodeType: "user_identity", meta: map[string]any{"category": "name"}, wantErr: false},
		{name: "user_identity valid category role", nodeType: "user_identity", meta: map[string]any{"category": "role"}, wantErr: false},
		// user_identity: empty category is allowed.
		{name: "user_identity empty category", nodeType: "user_identity", meta: map[string]any{}, wantErr: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateMetadata(tc.nodeType, tc.meta)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ValidateMetadata(%q, %v) = nil, want error containing %q", tc.nodeType, tc.meta, tc.errSubstr)
				}
				if tc.errSubstr != "" && !strings.Contains(err.Error(), tc.errSubstr) {
					t.Errorf("ValidateMetadata error = %q, want it to contain %q", err.Error(), tc.errSubstr)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateMetadata(%q, %v) = %v, want nil", tc.nodeType, tc.meta, err)
				}
			}
		})
	}
}

func TestNormalizeMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		nodeType string
		meta     map[string]any
		// check is a function that inspects the normalized output.
		check func(t *testing.T, got map[string]any)
	}{
		{
			name:     "nil map returns empty map",
			nodeType: "person",
			meta:     nil,
			check: func(t *testing.T, got map[string]any) {
				t.Helper()
				if got == nil || len(got) != 0 {
					t.Errorf("got %v, want non-nil empty map", got)
				}
			},
		},
		{
			name:     "unregistered type returns map unchanged",
			nodeType: "identity_anchor",
			meta:     map[string]any{"foo": "bar"},
			check: func(t *testing.T, got map[string]any) {
				t.Helper()
				if got["foo"] != "bar" {
					t.Errorf("got %v, want original map unchanged", got)
				}
			},
		},
		{
			name:     "person copies string fields and interests, omits empty",
			nodeType: "person",
			meta: map[string]any{
				"relationship_strength": "close",
				"occupation":            "engineer",
				"birthdate":             "1990-01-01",
				"last_interaction":      "2026-01-01",
				"interests":             []string{"coding", "hiking"},
				"ignored_field":         "should not appear",
			},
			check: func(t *testing.T, got map[string]any) {
				t.Helper()
				assertEqual(t, "relationship_strength", got["relationship_strength"], "close")
				assertEqual(t, "occupation", got["occupation"], "engineer")
				assertEqual(t, "birthdate", got["birthdate"], "1990-01-01")
				assertEqual(t, "last_interaction", got["last_interaction"], "2026-01-01")
				interests, _ := got["interests"].([]string)
				if len(interests) != 2 || interests[0] != "coding" {
					t.Errorf("interests: got %v, want [coding hiking]", interests)
				}
				if _, present := got["ignored_field"]; present {
					t.Error("ignored_field should not appear in normalized output")
				}
			},
		},
		{
			name:     "person omits empty strings",
			nodeType: "person",
			meta:     map[string]any{"occupation": "", "birthdate": "2000-05-10"},
			check: func(t *testing.T, got map[string]any) {
				t.Helper()
				if _, present := got["occupation"]; present {
					t.Error("empty occupation should be omitted")
				}
				assertEqual(t, "birthdate", got["birthdate"], "2000-05-10")
			},
		},
		{
			name:     "project lowercases status and resolves parent_goal alias",
			nodeType: "project",
			meta: map[string]any{
				"status":     "ACTIVE",
				"parent_goal": "goal-abc",
				"deadline":   "2026-12-31",
			},
			check: func(t *testing.T, got map[string]any) {
				t.Helper()
				assertEqual(t, "status", got["status"], "active")
				assertEqual(t, "parent_goal", got["parent_goal"], "goal-abc")
				assertEqual(t, "project_id", got["project_id"], "goal-abc")
				assertEqual(t, "deadline", got["deadline"], "2026-12-31")
			},
		},
		{
			name:     "project resolves parent_goal_id alias",
			nodeType: "project",
			meta:     map[string]any{"parent_goal_id": "goal-xyz"},
			check: func(t *testing.T, got map[string]any) {
				t.Helper()
				assertEqual(t, "parent_goal", got["parent_goal"], "goal-xyz")
				assertEqual(t, "project_id", got["project_id"], "goal-xyz")
			},
		},
		{
			name:     "project resolves project_id alias",
			nodeType: "project",
			meta:     map[string]any{"project_id": "proj-123"},
			check: func(t *testing.T, got map[string]any) {
				t.Helper()
				assertEqual(t, "parent_goal", got["parent_goal"], "proj-123")
				assertEqual(t, "project_id", got["project_id"], "proj-123")
			},
		},
		{
			name:     "project passes through step_number and dependencies",
			nodeType: "project",
			meta:     map[string]any{"step_number": 3, "dependencies": []string{"task-1"}},
			check: func(t *testing.T, got map[string]any) {
				t.Helper()
				if got["step_number"] != 3 {
					t.Errorf("step_number: got %v, want 3", got["step_number"])
				}
				if got["dependencies"] == nil {
					t.Error("dependencies should be passed through")
				}
			},
		},
		{
			name:     "goal uses same normalizer as project",
			nodeType: "goal",
			meta:     map[string]any{"status": "Done"},
			check: func(t *testing.T, got map[string]any) {
				t.Helper()
				assertEqual(t, "status", got["status"], "done")
			},
		},
		{
			name:     "preference lowercases category and sentiment, copies subject",
			nodeType: "preference",
			meta:     map[string]any{"category": "FOOD", "sentiment": "LIKE", "subject": "coffee"},
			check: func(t *testing.T, got map[string]any) {
				t.Helper()
				assertEqual(t, "category", got["category"], "food")
				assertEqual(t, "sentiment", got["sentiment"], "like")
				assertEqual(t, "subject", got["subject"], "coffee")
			},
		},
		{
			name:     "event lowercases type and copies date and attendees",
			nodeType: "event",
			meta: map[string]any{
				"type":      "CELEBRATION",
				"date":      "2026-06-01",
				"attendees": []string{"Alice", "Bob"},
			},
			check: func(t *testing.T, got map[string]any) {
				t.Helper()
				assertEqual(t, "type", got["type"], "celebration")
				assertEqual(t, "date", got["date"], "2026-06-01")
				att, _ := got["attendees"].([]string)
				if len(att) != 2 {
					t.Errorf("attendees: got %v, want 2 entries", att)
				}
			},
		},
		{
			name:     "milestone uses same normalizer as event",
			nodeType: "milestone",
			meta:     map[string]any{"type": "WORK"},
			check: func(t *testing.T, got map[string]any) {
				t.Helper()
				assertEqual(t, "type", got["type"], "work")
			},
		},
		{
			name:     "place lowercases category and copies address and notes",
			nodeType: "place",
			meta:     map[string]any{"category": "HOME", "address": "123 Main St", "notes": "cozy"},
			check: func(t *testing.T, got map[string]any) {
				t.Helper()
				assertEqual(t, "category", got["category"], "home")
				assertEqual(t, "address", got["address"], "123 Main St")
				assertEqual(t, "notes", got["notes"], "cozy")
			},
		},
		{
			name:     "asset lowercases type and passes through configuration and preferences",
			nodeType: "asset",
			meta: map[string]any{
				"type":          "SOFTWARE",
				"configuration": map[string]any{"key": "val"},
				"preferences":   map[string]any{"theme": "dark"},
			},
			check: func(t *testing.T, got map[string]any) {
				t.Helper()
				assertEqual(t, "type", got["type"], "software")
				if got["configuration"] == nil {
					t.Error("configuration should be passed through")
				}
				if got["preferences"] == nil {
					t.Error("preferences should be passed through")
				}
			},
		},
		{
			name:     "tool uses same normalizer as asset",
			nodeType: "tool",
			meta:     map[string]any{"type": "HARDWARE"},
			check: func(t *testing.T, got map[string]any) {
				t.Helper()
				assertEqual(t, "type", got["type"], "hardware")
			},
		},
		{
			name:     "generic copies source_excerpt and extracted_facts",
			nodeType: "generic",
			meta: map[string]any{
				"source_excerpt":  "some text",
				"extracted_facts": []string{"fact1", "fact2"},
				"tags":            []string{"tag1"},
				"confidence_score": 0.9,
			},
			check: func(t *testing.T, got map[string]any) {
				t.Helper()
				assertEqual(t, "source_excerpt", got["source_excerpt"], "some text")
				facts, _ := got["extracted_facts"].([]string)
				if len(facts) != 2 {
					t.Errorf("extracted_facts: got %v, want 2 entries", facts)
				}
				if got["confidence_score"] != 0.9 {
					t.Errorf("confidence_score: got %v, want 0.9", got["confidence_score"])
				}
				tags, _ := got["tags"].([]string)
				if len(tags) != 1 || tags[0] != "tag1" {
					t.Errorf("tags: got %v, want [tag1]", tags)
				}
			},
		},
		{
			name:     "generic coerces int confidence_score to float64",
			nodeType: "generic",
			meta:     map[string]any{"confidence_score": int(1)},
			check: func(t *testing.T, got map[string]any) {
				t.Helper()
				v, ok := got["confidence_score"].(float64)
				if !ok || v != 1.0 {
					t.Errorf("confidence_score: got %v (%T), want 1.0 float64", got["confidence_score"], got["confidence_score"])
				}
			},
		},
		{
			name:     "generic defaults confidence_score to 0.0 when missing",
			nodeType: "generic",
			meta:     map[string]any{},
			check: func(t *testing.T, got map[string]any) {
				t.Helper()
				v, ok := got["confidence_score"].(float64)
				if !ok || v != 0.0 {
					t.Errorf("confidence_score: got %v (%T), want 0.0 float64", got["confidence_score"], got["confidence_score"])
				}
			},
		},
		{
			name:     "generic coerces invalid confidence_score to 0.0",
			nodeType: "generic",
			meta:     map[string]any{"confidence_score": "high"},
			check: func(t *testing.T, got map[string]any) {
				t.Helper()
				v, ok := got["confidence_score"].(float64)
				if !ok || v != 0.0 {
					t.Errorf("confidence_score: got %v (%T), want 0.0 float64", got["confidence_score"], got["confidence_score"])
				}
			},
		},
		{
			name:     "user_identity lowercases category",
			nodeType: "user_identity",
			meta:     map[string]any{"category": "ROLE"},
			check: func(t *testing.T, got map[string]any) {
				t.Helper()
				assertEqual(t, "category", got["category"], "role")
			},
		},
		{
			name:     "user_identity omits empty category",
			nodeType: "user_identity",
			meta:     map[string]any{"category": ""},
			check: func(t *testing.T, got map[string]any) {
				t.Helper()
				if _, present := got["category"]; present {
					t.Error("empty category should be omitted")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := NormalizeMetadata(tc.nodeType, tc.meta)
			if err != nil {
				t.Fatalf("NormalizeMetadata(%q, ...) unexpected error: %v", tc.nodeType, err)
			}
			tc.check(t, got)
		})
	}
}

func TestMetadataToJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		meta     map[string]any
		wantJSON string
	}{
		{name: "nil map", meta: nil, wantJSON: "{}"},
		{name: "empty map", meta: map[string]any{}, wantJSON: "{}"},
		{
			name:     "single key",
			meta:     map[string]any{"foo": "bar"},
			wantJSON: `{"foo":"bar"}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := MetadataToJSON(tc.meta)
			if err != nil {
				t.Fatalf("MetadataToJSON(%v) unexpected error: %v", tc.meta, err)
			}
			if got != tc.wantJSON {
				t.Errorf("MetadataToJSON = %q, want %q", got, tc.wantJSON)
			}
		})
	}
}

// assertEqual is a helper to compare map values with expected strings.
func assertEqual(t *testing.T, field string, got any, want string) {
	t.Helper()
	s, _ := got.(string)
	if s != want {
		t.Errorf("%s: got %q, want %q", field, s, want)
	}
}

func TestNewNodeTypeConstants(t *testing.T) {
	if NodeTypeTask != "task" {
		t.Errorf("expected NodeTypeTask == 'task', got %q", NodeTypeTask)
	}
	if NodeTypeQuery != "query" {
		t.Errorf("expected NodeTypeQuery == 'query', got %q", NodeTypeQuery)
	}
	if NodeTypePendingQuestion != "pending_question" {
		t.Errorf("expected NodeTypePendingQuestion == 'pending_question', got %q", NodeTypePendingQuestion)
	}
	if NodeTypeLog != "log" {
		t.Errorf("expected NodeTypeLog == 'log', got %q", NodeTypeLog)
	}
}

func TestTaskStatusConstants(t *testing.T) {
	if TaskStatusPending != "pending" {
		t.Errorf("got %q", TaskStatusPending)
	}
	if TaskStatusActive != "active" {
		t.Errorf("got %q", TaskStatusActive)
	}
	if TaskStatusCompleted != "completed" {
		t.Errorf("got %q", TaskStatusCompleted)
	}
	if TaskStatusAbandoned != "abandoned" {
		t.Errorf("got %q", TaskStatusAbandoned)
	}
}
