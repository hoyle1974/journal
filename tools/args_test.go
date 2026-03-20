package tools

import "testing"

func TestOptionalString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		raw     map[string]interface{}
		key     string
		wantNil bool
		wantVal string
	}{
		{
			name:    "key present with non-empty value",
			raw:     map[string]interface{}{"k": "hello"},
			key:     "k",
			wantNil: false,
			wantVal: "hello",
		},
		{
			name:    "key present with empty string",
			raw:     map[string]interface{}{"k": ""},
			key:     "k",
			wantNil: false,
			wantVal: "",
		},
		{
			name:    "key absent",
			raw:     map[string]interface{}{},
			key:     "k",
			wantNil: true,
		},
		{
			name:    "key present but wrong type (int)",
			raw:     map[string]interface{}{"k": 42},
			key:     "k",
			wantNil: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := NewArgs(tt.raw).OptionalString(tt.key)
			if tt.wantNil {
				if got != nil {
					t.Errorf("OptionalString(%q) = %q, want nil", tt.key, *got)
				}
				return
			}
			if got == nil {
				t.Fatalf("OptionalString(%q) = nil, want %q", tt.key, tt.wantVal)
			}
			if *got != tt.wantVal {
				t.Errorf("OptionalString(%q) = %q, want %q", tt.key, *got, tt.wantVal)
			}
		})
	}
}

func TestOptionalStringNonEmpty(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		raw     map[string]interface{}
		key     string
		wantNil bool
		wantVal string
	}{
		{
			name:    "key present with non-empty value",
			raw:     map[string]interface{}{"k": "hello"},
			key:     "k",
			wantNil: false,
			wantVal: "hello",
		},
		{
			name:    "key present with empty string",
			raw:     map[string]interface{}{"k": ""},
			key:     "k",
			wantNil: true,
		},
		{
			name:    "key absent",
			raw:     map[string]interface{}{},
			key:     "k",
			wantNil: true,
		},
		{
			name:    "key present with whitespace only",
			raw:     map[string]interface{}{"k": " "},
			key:     "k",
			wantNil: false,
			wantVal: " ",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := NewArgs(tt.raw).OptionalStringNonEmpty(tt.key)
			if tt.wantNil {
				if got != nil {
					t.Errorf("OptionalStringNonEmpty(%q) = %q, want nil", tt.key, *got)
				}
				return
			}
			if got == nil {
				t.Fatalf("OptionalStringNonEmpty(%q) = nil, want %q", tt.key, tt.wantVal)
			}
			if *got != tt.wantVal {
				t.Errorf("OptionalStringNonEmpty(%q) = %q, want %q", tt.key, *got, tt.wantVal)
			}
		})
	}
}

func TestArgsInt(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  map[string]interface{}
		key  string
		def  int
		want int
	}{
		{
			name: "key present as float64",
			raw:  map[string]interface{}{"n": float64(7)},
			key:  "n",
			def:  99,
			want: 7,
		},
		{
			name: "key present as float64 zero",
			raw:  map[string]interface{}{"n": float64(0)},
			key:  "n",
			def:  99,
			want: 0,
		},
		{
			name: "key absent returns default",
			raw:  map[string]interface{}{},
			key:  "n",
			def:  99,
			want: 99,
		},
		{
			name: "key present as string (wrong type) returns default",
			raw:  map[string]interface{}{"n": "seven"},
			key:  "n",
			def:  99,
			want: 99,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := NewArgs(tt.raw).Int(tt.key, tt.def)
			if got != tt.want {
				t.Errorf("Int(%q, %d) = %d, want %d", tt.key, tt.def, got, tt.want)
			}
		})
	}
}

func TestArgsFloat(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  map[string]interface{}
		key  string
		def  float64
		want float64
	}{
		{
			name: "key present as float64",
			raw:  map[string]interface{}{"f": float64(3.14)},
			key:  "f",
			def:  1.5,
			want: 3.14,
		},
		{
			name: "key absent returns default",
			raw:  map[string]interface{}{},
			key:  "f",
			def:  1.5,
			want: 1.5,
		},
		{
			name: "key present as string returns default",
			raw:  map[string]interface{}{"f": "3.14"},
			key:  "f",
			def:  1.5,
			want: 1.5,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := NewArgs(tt.raw).Float(tt.key, tt.def)
			if got != tt.want {
				t.Errorf("Float(%q, %v) = %v, want %v", tt.key, tt.def, got, tt.want)
			}
		})
	}
}

func TestArgsBool(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  map[string]interface{}
		key  string
		def  bool
		want bool
	}{
		{
			name: "key present true",
			raw:  map[string]interface{}{"b": true},
			key:  "b",
			def:  false,
			want: true,
		},
		{
			name: "key present false (not the default)",
			raw:  map[string]interface{}{"b": false},
			key:  "b",
			def:  true,
			want: false,
		},
		{
			name: "key absent returns default",
			raw:  map[string]interface{}{},
			key:  "b",
			def:  true,
			want: true,
		},
		{
			name: "key present as string returns default",
			raw:  map[string]interface{}{"b": "true"},
			key:  "b",
			def:  true,
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := NewArgs(tt.raw).Bool(tt.key, tt.def)
			if got != tt.want {
				t.Errorf("Bool(%q, %v) = %v, want %v", tt.key, tt.def, got, tt.want)
			}
		})
	}
}

func TestArgsRaw(t *testing.T) {
	t.Parallel()

	t.Run("returns same map reference", func(t *testing.T) {
		t.Parallel()
		m := map[string]interface{}{"x": "y"}
		a := NewArgs(m)
		got := a.Raw()
		// Verify same reference: mutate and check via Raw again.
		got["added"] = "val"
		if a.Raw()["added"] != "val" {
			t.Error("Raw() did not return the same map reference")
		}
	})

	t.Run("mutation of returned map affects future calls", func(t *testing.T) {
		t.Parallel()
		m := map[string]interface{}{"x": "original"}
		a := NewArgs(m)
		a.Raw()["x"] = "mutated"
		got := a.String("x", "default")
		if got != "mutated" {
			t.Errorf("expected mutated value, got %q", got)
		}
	})

	t.Run("NewArgs nil returns empty non-nil map", func(t *testing.T) {
		t.Parallel()
		a := NewArgs(nil)
		got := a.Raw()
		if got == nil {
			t.Error("Raw() returned nil for NewArgs(nil)")
		}
		if len(got) != 0 {
			t.Errorf("Raw() len = %d, want 0", len(got))
		}
	})
}
