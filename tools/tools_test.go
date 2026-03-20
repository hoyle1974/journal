package tools

import (
	"context"
	"reflect"
	"testing"

	"google.golang.org/genai"
)

func TestArgsString(t *testing.T) {
	args := NewArgs(map[string]interface{}{
		"name":  "test",
		"empty": "",
	})

	tests := []struct {
		name     string
		key      string
		def      string
		expected string
	}{
		{"existing key", "name", "default", "test"},
		{"missing key", "missing", "default", "default"},
		{"empty value", "empty", "default", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := args.String(tt.key, tt.def)
			if result != tt.expected {
				t.Errorf("String(%q, %q) = %q, want %q", tt.key, tt.def, result, tt.expected)
			}
		})
	}
}

func TestArgsRequiredString(t *testing.T) {
	args := NewArgs(map[string]interface{}{
		"name": "test",
	})

	// Existing key
	val, ok := args.RequiredString("name")
	if !ok || val != "test" {
		t.Errorf("RequiredString(name) = %q, %v; want test, true", val, ok)
	}

	// Missing key
	val, ok = args.RequiredString("missing")
	if ok || val != "" {
		t.Errorf("RequiredString(missing) = %q, %v; want '', false", val, ok)
	}
}

func TestArgsIntBounded(t *testing.T) {
	args := NewArgs(map[string]interface{}{
		"count": float64(25), // JSON unmarshals numbers as float64
		"low":   float64(-5),
		"high":  float64(100),
	})

	tests := []struct {
		name     string
		key      string
		def      int
		min      int
		max      int
		expected int
	}{
		{"existing within bounds", "count", 10, 1, 50, 25},
		{"missing uses default", "missing", 10, 1, 50, 10},
		{"below minimum", "low", 10, 1, 50, 1},
		{"above maximum", "high", 10, 1, 50, 50},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := args.IntBounded(tt.key, tt.def, tt.min, tt.max)
			if result != tt.expected {
				t.Errorf("IntBounded(%q, %d, %d, %d) = %d, want %d",
					tt.key, tt.def, tt.min, tt.max, result, tt.expected)
			}
		})
	}
}

func TestResultHelpers(t *testing.T) {
	// Test OK
	okResult := OK("Found %d items", 5)
	if !okResult.Success {
		t.Error("OK() should set Success to true")
	}
	if okResult.Result != "Found 5 items" {
		t.Errorf("OK() Result = %q, want 'Found 5 items'", okResult.Result)
	}

	// Test Fail
	failResult := Fail("Error: %s", "something went wrong")
	if failResult.Success {
		t.Error("Fail() should set Success to false")
	}
	if failResult.Result != "Error: something went wrong" {
		t.Errorf("Fail() Result = %q, want 'Error: something went wrong'", failResult.Result)
	}

	// Test MissingParam
	missingResult := MissingParam("query")
	if missingResult.Success {
		t.Error("MissingParam() should set Success to false")
	}
	if missingResult.Result != "Missing required parameter: query" {
		t.Errorf("MissingParam() Result = %q", missingResult.Result)
	}
}

func TestStructToGenaiSchema(t *testing.T) {
	type ExampleArgs struct {
		Query   string `json:"query" description:"Search query" required:"true"`
		Limit   int    `json:"limit" description:"Max results" default:"10"`
		Action  string `json:"action" description:"Action" required:"true" enum:"a,b,c"`
	}
	schema := StructToGenaiSchema(&ExampleArgs{})
	if schema.Type != genai.TypeObject {
		t.Errorf("schema.Type = %v, want TypeObject", schema.Type)
	}
	if len(schema.Properties) != 3 {
		t.Errorf("schema.Properties has %d keys, want 3", len(schema.Properties))
	}
	if len(schema.Required) != 2 {
		t.Errorf("schema.Required has %d items, want 2 (query, action)", len(schema.Required))
	}
}

func TestRegistryExecuteUnknownTool(t *testing.T) {
	result := Execute(context.Background(), nil, "nonexistent_tool", nil)
	if result.Success {
		t.Error("Execute for unknown tool should return failure")
	}
	if result.Result != "Unknown tool: nonexistent_tool" {
		t.Errorf("Execute result = %q", result.Result)
	}
}

func TestGetDefinitionsReturnsSlice(t *testing.T) {
	// Tool registration happens when a main binary imports impl (e.g. cmd/local).
	// In isolation this package has no tools registered.
	defs := GetDefinitions()
	if defs == nil {
		t.Error("GetDefinitions() returned nil")
	}
}

// ---------------------------------------------------------------------------
// mapGoTypeToGenai
// ---------------------------------------------------------------------------

func TestMapGoTypeToGenai(t *testing.T) {
	tests := []struct {
		name  string
		input reflect.Type
		want  genai.Type
	}{
		{"string", reflect.TypeOf(""), genai.TypeString},
		{"bool", reflect.TypeOf(true), genai.TypeBoolean},
		{"int", reflect.TypeOf(0), genai.TypeInteger},
		{"int64", reflect.TypeOf(int64(0)), genai.TypeInteger},
		{"uint", reflect.TypeOf(uint(0)), genai.TypeInteger},
		{"float32", reflect.TypeOf(float32(0)), genai.TypeNumber},
		{"float64", reflect.TypeOf(float64(0)), genai.TypeNumber},
		{"slice", reflect.TypeOf([]string{}), genai.TypeArray},
		{"array", reflect.TypeOf([3]int{}), genai.TypeArray},
		{"map", reflect.TypeOf(map[string]int{}), genai.TypeObject},
		{"struct", reflect.TypeOf(struct{ X int }{}), genai.TypeObject},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapGoTypeToGenai(tt.input)
			if got != tt.want {
				t.Errorf("mapGoTypeToGenai(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// MapToTypedArgs
// ---------------------------------------------------------------------------

type testMTAArgs struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
	Flag  bool   `json:"flag"`
}

func TestMapToTypedArgs(t *testing.T) {
	t.Run("nil args returns nil nil", func(t *testing.T) {
		tool := &Tool{Args: nil}
		got, err := MapToTypedArgs(tool, map[string]interface{}{"x": 1})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("non-pointer args returns error", func(t *testing.T) {
		tool := &Tool{Args: testMTAArgs{}}
		_, err := MapToTypedArgs(tool, map[string]interface{}{})
		if err == nil {
			t.Fatal("expected error for non-pointer args, got nil")
		}
	})

	t.Run("valid args map populates struct", func(t *testing.T) {
		tool := &Tool{Args: &testMTAArgs{}}
		got, err := MapToTypedArgs(tool, map[string]interface{}{
			"name":  "hello",
			"count": float64(5),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := got.(*testMTAArgs)
		if !ok {
			t.Fatalf("expected *testMTAArgs, got %T", got)
		}
		if result.Name != "hello" {
			t.Errorf("Name = %q, want %q", result.Name, "hello")
		}
		if result.Count != 5 {
			t.Errorf("Count = %d, want 5", result.Count)
		}
	})

	t.Run("empty map returns zero-value struct", func(t *testing.T) {
		tool := &Tool{Args: &testMTAArgs{}}
		got, err := MapToTypedArgs(tool, map[string]interface{}{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result := got.(*testMTAArgs)
		if result.Name != "" || result.Count != 0 || result.Flag {
			t.Errorf("expected zero struct, got %+v", result)
		}
	})

	t.Run("extra fields in map are ignored", func(t *testing.T) {
		tool := &Tool{Args: &testMTAArgs{}}
		got, err := MapToTypedArgs(tool, map[string]interface{}{
			"name":    "hi",
			"unknown": "ignored",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result := got.(*testMTAArgs)
		if result.Name != "hi" {
			t.Errorf("Name = %q, want %q", result.Name, "hi")
		}
	})
}

// ---------------------------------------------------------------------------
// ParamInfosFromArgs
// ---------------------------------------------------------------------------

type testPIArgs struct {
	Query  string `json:"query" required:"true" description:"Search query"`
	Limit  int    `json:"limit" description:"Max results"`
	Hidden string `json:"-"`
	NoTag  string
}

func TestParamInfosFromArgs(t *testing.T) {
	t.Run("returns only json-tagged non-dash fields", func(t *testing.T) {
		infos := ParamInfosFromArgs(&testPIArgs{})
		if len(infos) != 2 {
			t.Fatalf("expected 2 params, got %d: %+v", len(infos), infos)
		}
		if infos[0].Name != "query" || !infos[0].Required || infos[0].Description != "Search query" {
			t.Errorf("first param unexpected: %+v", infos[0])
		}
		if infos[1].Name != "limit" || infos[1].Required || infos[1].Description != "Max results" {
			t.Errorf("second param unexpected: %+v", infos[1])
		}
	})

	t.Run("non-struct input returns nil", func(t *testing.T) {
		if got := ParamInfosFromArgs("not a struct"); got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})
}

// ---------------------------------------------------------------------------
// ParamNamesFromArgs
// ---------------------------------------------------------------------------

func TestParamNamesFromArgs(t *testing.T) {
	t.Run("required field has no suffix, optional has question mark", func(t *testing.T) {
		names := ParamNamesFromArgs(&testPIArgs{})
		if len(names) != 2 {
			t.Fatalf("expected 2 names, got %d: %v", len(names), names)
		}
		if names[0] != "query" {
			t.Errorf("names[0] = %q, want %q", names[0], "query")
		}
		if names[1] != "limit?" {
			t.Errorf("names[1] = %q, want %q", names[1], "limit?")
		}
	})
}

// ---------------------------------------------------------------------------
// ApplyDefaults
// ---------------------------------------------------------------------------

type testDefaultArgs struct {
	Name      string `json:"name" default:"world"`
	Count     int    `json:"count" default:"10"`
	Enabled   bool   `json:"enabled" default:"true"`
	NoDefault string `json:"no_default"`
}

func TestApplyDefaults(t *testing.T) {
	t.Run("nil ptr is no-op no panic", func(t *testing.T) {
		var p *testDefaultArgs
		ApplyDefaults(p) // must not panic
	})

	t.Run("non-ptr is no-op no panic", func(t *testing.T) {
		ApplyDefaults(testDefaultArgs{}) // must not panic
	})

	t.Run("empty string gets default", func(t *testing.T) {
		s := &testDefaultArgs{}
		ApplyDefaults(s)
		if s.Name != "world" {
			t.Errorf("Name = %q, want %q", s.Name, "world")
		}
	})

	t.Run("non-empty string is unchanged", func(t *testing.T) {
		s := &testDefaultArgs{Name: "existing"}
		ApplyDefaults(s)
		if s.Name != "existing" {
			t.Errorf("Name = %q, want %q", s.Name, "existing")
		}
	})

	t.Run("zero int gets default", func(t *testing.T) {
		s := &testDefaultArgs{}
		ApplyDefaults(s)
		if s.Count != 10 {
			t.Errorf("Count = %d, want 10", s.Count)
		}
	})

	t.Run("non-zero int is unchanged", func(t *testing.T) {
		s := &testDefaultArgs{Count: 5}
		ApplyDefaults(s)
		if s.Count != 5 {
			t.Errorf("Count = %d, want 5", s.Count)
		}
	})

	t.Run("bool default always applied", func(t *testing.T) {
		s := &testDefaultArgs{Enabled: false}
		ApplyDefaults(s)
		if !s.Enabled {
			t.Error("Enabled should be true after ApplyDefaults")
		}
	})

	t.Run("field with no default tag is untouched", func(t *testing.T) {
		s := &testDefaultArgs{}
		ApplyDefaults(s)
		if s.NoDefault != "" {
			t.Errorf("NoDefault = %q, want empty", s.NoDefault)
		}
	})
}
