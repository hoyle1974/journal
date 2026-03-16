package agent

import (
	"reflect"
	"testing"
)

func TestExtractMissingInfoAndAnswer(t *testing.T) {
	tests := []struct {
		name           string
		raw            string
		wantAnswer     string
		wantMissing    []string
	}{
		{
			name:        "no missing info",
			raw:         "Here is the answer.",
			wantAnswer:  "Here is the answer.",
			wantMissing: nil,
		},
		{
			name:        "missing info at end",
			raw:         "Here is the answer.\nMISSING_INFO: exact date; full name",
			wantAnswer:  "Here is the answer.",
			wantMissing: []string{"exact date", "full name"},
		},
		{
			name:        "missing info lowercase",
			raw:         "Answer text.\nmissing_info: something missing",
			wantAnswer:  "Answer text.",
			wantMissing: []string{"something missing"},
		},
		{
			name:        "only missing info line",
			raw:         "MISSING_INFO: no data found",
			wantAnswer:  "MISSING_INFO: no data found", // fallback: strip left nothing
			wantMissing: []string{"no data found"},
		},
		{
			name:        "multi line answer then missing",
			raw:         "Line one.\nLine two.\nMISSING_INFO: gap1; gap2",
			wantAnswer:  "Line one.\nLine two.",
			wantMissing: []string{"gap1", "gap2"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAnswer, gotMissing := extractMissingInfoAndAnswer(tt.raw)
			if gotAnswer != tt.wantAnswer {
				t.Errorf("extractMissingInfoAndAnswer() answer = %q, want %q", gotAnswer, tt.wantAnswer)
			}
			if !reflect.DeepEqual(gotMissing, tt.wantMissing) {
				t.Errorf("extractMissingInfoAndAnswer() missing = %v, want %v", gotMissing, tt.wantMissing)
			}
		})
	}
}
