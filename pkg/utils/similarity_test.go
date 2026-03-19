package utils

import (
	"math"
	"testing"
)

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name string
		a, b []float32
		want float64
		tol  float64
	}{
		{
			name: "identical vectors",
			a:    []float32{1, 0, 0},
			b:    []float32{1, 0, 0},
			want: 1.0,
			tol:  1e-9,
		},
		{
			name: "orthogonal vectors",
			a:    []float32{1, 0},
			b:    []float32{0, 1},
			want: 0.0,
			tol:  1e-9,
		},
		{
			name: "opposite vectors",
			a:    []float32{1, 0},
			b:    []float32{-1, 0},
			want: -1.0,
			tol:  1e-9,
		},
		{
			name: "mismatched lengths",
			a:    []float32{1, 2},
			b:    []float32{1},
			want: 0.0,
			tol:  1e-9,
		},
		{
			name: "empty vectors",
			a:    []float32{},
			b:    []float32{},
			want: 0.0,
			tol:  1e-9,
		},
		{
			name: "zero vector",
			a:    []float32{0, 0},
			b:    []float32{1, 0},
			want: 0.0,
			tol:  1e-9,
		},
		{
			name: "45 degree angle",
			a:    []float32{1, 0},
			b:    []float32{1, 1},
			want: 1.0 / math.Sqrt2,
			tol:  1e-6,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CosineSimilarity(tc.a, tc.b)
			if math.Abs(got-tc.want) > tc.tol {
				t.Errorf("CosineSimilarity(%v, %v) = %v, want %v (±%v)", tc.a, tc.b, got, tc.want, tc.tol)
			}
		})
	}
}
