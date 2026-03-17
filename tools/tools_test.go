package tools

import (
	"context"
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
