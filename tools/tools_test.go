package tools

import (
	"context"
	"testing"

	"github.com/google/generative-ai-go/genai"
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

func TestParamHelpers(t *testing.T) {
	// Test CountParam
	countParam := CountParam()
	if countParam.Name != "count" {
		t.Errorf("CountParam().Name = %q, want 'count'", countParam.Name)
	}
	if countParam.Type != genai.TypeInteger {
		t.Error("CountParam().Type should be TypeInteger")
	}
	if countParam.Required {
		t.Error("CountParam() should not be required")
	}

	// Test RequiredStringParam
	reqParam := RequiredStringParam("query", "Search query")
	if reqParam.Name != "query" {
		t.Errorf("RequiredStringParam().Name = %q, want 'query'", reqParam.Name)
	}
	if !reqParam.Required {
		t.Error("RequiredStringParam() should be required")
	}
	if reqParam.Type != genai.TypeString {
		t.Error("RequiredStringParam().Type should be TypeString")
	}

	// Test EnumParam
	enumParam := EnumParam("action", "Action to perform", true, []string{"save", "delete"})
	if len(enumParam.Enum) != 2 {
		t.Errorf("EnumParam().Enum has %d values, want 2", len(enumParam.Enum))
	}
}

func TestRegistryExecuteUnknownTool(t *testing.T) {
	result := Execute(context.Background(), "nonexistent_tool", nil)
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
