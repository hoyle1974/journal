package utils

import (
	"reflect"
	"testing"
)

func TestParseKeyValueMap(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		wantSimple map[string]string
		wantSections map[string][]string
	}{
		{
			name: "simple keys only",
			text: "summary: A short summary\nmood: 7\ncategory: work",
			wantSimple: map[string]string{
				"summary":  "A short summary",
				"mood":     "7",
				"category": "work",
			},
			wantSections: map[string][]string{},
		},
		{
			name: "section with items",
			text: "summary: One line\nentities:\nAlice | person | In-Progress\nBob | project | Planned",
			wantSimple: map[string]string{"summary": "One line"},
			wantSections: map[string][]string{
				"entities": {"Alice | person | In-Progress", "Bob | project | Planned"},
			},
		},
		{
			name:        "empty section (no items)",
			text:        "mood: 5\nopen_loops:\n",
			wantSimple:  map[string]string{"mood": "5"},
			wantSections: map[string][]string{}, // section with no lines never gets a key
		},
		{
			name:      "empty input",
			text:      "",
			wantSimple: map[string]string{},
			wantSections: map[string][]string{},
		},
		{
			name: "keys lowercased",
			text: "Summary: Big picture\nTAGS: a, b, c",
			wantSimple: map[string]string{
				"summary": "Big picture",
				"tags":    "a, b, c",
			},
			wantSections: map[string][]string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			simple, sections := ParseKeyValueMap(tt.text)
			if !reflect.DeepEqual(simple, tt.wantSimple) {
				t.Errorf("ParseKeyValueMap() simple = %v, want %v", simple, tt.wantSimple)
			}
			if !reflect.DeepEqual(sections, tt.wantSections) {
				t.Errorf("ParseKeyValueMap() sections = %v, want %v", sections, tt.wantSections)
			}
		})
	}
}
