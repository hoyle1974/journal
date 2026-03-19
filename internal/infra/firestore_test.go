package infra_test

import (
	"testing"

	"github.com/jackstrohm/jot/internal/infra"
)

func TestGetFloat32SliceField(t *testing.T) {
	tests := []struct {
		name  string
		data  map[string]interface{}
		field string
		want  []float32
	}{
		{
			name:  "happy path",
			data:  map[string]interface{}{"v": []interface{}{float64(1.0), float64(0.5), float64(-0.25)}},
			field: "v",
			want:  []float32{1.0, 0.5, -0.25},
		},
		{
			name:  "missing field",
			data:  map[string]interface{}{},
			field: "v",
			want:  nil,
		},
		{
			name:  "wrong type",
			data:  map[string]interface{}{"v": "notanarray"},
			field: "v",
			want:  nil,
		},
		{
			name:  "empty slice",
			data:  map[string]interface{}{"v": []interface{}{}},
			field: "v",
			want:  []float32{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := infra.GetFloat32SliceField(tc.data, tc.field)
			if len(got) != len(tc.want) {
				t.Fatalf("got len=%d want len=%d; got=%v", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("index %d: got %v want %v", i, got[i], tc.want[i])
				}
			}
		})
	}
}
