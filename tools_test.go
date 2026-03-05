package jot

import (
	"strings"
	"testing"

	"github.com/jackstrohm/jot/pkg/utils"
)

func TestFormatEntriesForContext(t *testing.T) {
	tests := []struct {
		name     string
		entries  []Entry
		maxChars int
		contains []string
	}{
		{
			name:     "empty entries",
			entries:  []Entry{},
			maxChars: 1000,
			contains: []string{"No entries found"},
		},
		{
			name: "single entry",
			entries: []Entry{
				{Timestamp: "2024-01-15T10:00:00Z", Source: "cli", Content: "Test entry"},
			},
			maxChars: 1000,
			contains: []string{"2024-01-15T10:00:00", "cli", "Test entry"},
		},
		{
			name: "truncation",
			entries: []Entry{
				{Timestamp: "2024-01-15T10:00:00Z", Source: "cli", Content: "First entry"},
				{Timestamp: "2024-01-15T11:00:00Z", Source: "cli", Content: "Second entry"},
			},
			maxChars: 50,
			contains: []string{"truncated"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatEntriesForContext(tt.entries, tt.maxChars)
			for _, s := range tt.contains {
				if !strings.Contains(result, s) {
					t.Errorf("FormatEntriesForContext() result should contain %q, got %q", s, result)
				}
			}
		})
	}
}

func TestCalculateTool(t *testing.T) {
	tests := []struct {
		expr     string
		contains string
	}{
		{"2+2", "4"},
		{"10*5", "50"},
		{"100/4", "25"},
		{"2^10", "1024"},
		{"15% of 200", "30"},
		{"sqrt(144)", "12"},
		{"(2+3)*4", "20"},
		{" 10 - 2 * 3 ", "4"},
	}

	for _, tc := range tests {
		result, err := utils.EvaluateMathExpression(tc.expr)
		if err != nil {
			t.Errorf("EvaluateMathExpression(%q) error: %v", tc.expr, err)
			continue
		}
		if result != tc.contains {
			t.Errorf("EvaluateMathExpression(%q) = %q, want %q", tc.expr, result, tc.contains)
		}
	}
}

func TestDateCalcTool(t *testing.T) {
	// Test day_of_week
	result, err := utils.PerformDateCalculation("day_of_week", "2024-01-01", "", 0)
	if err != nil {
		t.Errorf("day_of_week error: %v", err)
	}
	if !strings.Contains(result, "Monday") {
		t.Errorf("day_of_week expected Monday, got: %s", result)
	}

	// Test add_days
	result, err = utils.PerformDateCalculation("add_days", "2024-01-01", "", 7)
	if err != nil {
		t.Errorf("add_days error: %v", err)
	}
	if !strings.Contains(result, "2024-01-08") {
		t.Errorf("add_days expected 2024-01-08, got: %s", result)
	}

	// Test days_between
	result, err = utils.PerformDateCalculation("days_between", "2024-01-01", "2024-01-31", 0)
	if err != nil {
		t.Errorf("days_between error: %v", err)
	}
	if !strings.Contains(result, "30") {
		t.Errorf("days_between expected 30 days, got: %s", result)
	}
}

func TestConvertUnitsTool(t *testing.T) {
	tests := []struct {
		value    float64
		from     string
		to       string
		contains string
	}{
		{100, "c", "f", "212"},
		{32, "f", "c", "0"},
		{1, "km", "m", "1000"},
		{1, "kg", "lb", "2.205"},
		{1024, "mb", "gb", "1"},
	}

	for _, tc := range tests {
		result, err := utils.ConvertUnits(tc.value, tc.from, tc.to)
		if err != nil {
			t.Errorf("ConvertUnits(%v, %q, %q) error: %v", tc.value, tc.from, tc.to, err)
			continue
		}
		if !strings.Contains(result, tc.contains) {
			t.Errorf("ConvertUnits(%v, %q, %q) = %q, want to contain %q", tc.value, tc.from, tc.to, result, tc.contains)
		}
	}
}

func TestTextStatsTool(t *testing.T) {
	text := "Hello world. This is a test. Three sentences here."
	result := utils.AnalyzeText(text)

	if !strings.Contains(result, "Words: 9") {
		t.Errorf("AnalyzeText expected 9 words, got: %s", result)
	}
	if !strings.Contains(result, "Sentences: 3") {
		t.Errorf("AnalyzeText expected 3 sentences, got: %s", result)
	}
}

func TestRandomTool(t *testing.T) {
	// Test coin flip
	result := utils.GenerateRandom("coin", 0, 0, "")
	if !strings.Contains(result, "Heads") && !strings.Contains(result, "Tails") {
		t.Errorf("coin flip expected Heads or Tails, got: %s", result)
	}

	// Test dice
	result = utils.GenerateRandom("dice", 0, 0, "")
	if !strings.Contains(result, "Dice roll:") {
		t.Errorf("dice roll expected 'Dice roll:', got: %s", result)
	}

	// Test UUID
	result = utils.GenerateRandom("uuid", 0, 0, "")
	if !strings.Contains(result, "Random UUID:") {
		t.Errorf("uuid expected 'Random UUID:', got: %s", result)
	}

	// Test pick
	result = utils.GenerateRandom("pick", 0, 0, "red, green, blue")
	if !strings.Contains(result, "Picked:") {
		t.Errorf("pick expected 'Picked:', got: %s", result)
	}
}

func TestTimezoneConvert(t *testing.T) {
	// Test PST to EST
	result, err := utils.ConvertTimezone("3:00 PM", "PST", "EST")
	if err != nil {
		t.Errorf("ConvertTimezone error: %v", err)
	}
	if !strings.Contains(result, "6:00 PM") {
		t.Errorf("expected 6:00 PM EST, got: %s", result)
	}

	// Test UTC to JST
	result, err = utils.ConvertTimezone("12:00 PM", "UTC", "JST")
	if err != nil {
		t.Errorf("ConvertTimezone error: %v", err)
	}
	if !strings.Contains(result, "9:00 PM") {
		t.Errorf("expected 9:00 PM JST, got: %s", result)
	}
}

func TestEncodeDecode(t *testing.T) {
	// Test base64 encode
	result, err := utils.EncodeDecodeText("base64_encode", "hello world")
	if err != nil {
		t.Errorf("base64_encode error: %v", err)
	}
	if !strings.Contains(result, "aGVsbG8gd29ybGQ=") {
		t.Errorf("base64_encode expected aGVsbG8gd29ybGQ=, got: %s", result)
	}

	// Test base64 decode
	result, err = utils.EncodeDecodeText("base64_decode", "aGVsbG8gd29ybGQ=")
	if err != nil {
		t.Errorf("base64_decode error: %v", err)
	}
	if !strings.Contains(result, "hello world") {
		t.Errorf("base64_decode expected 'hello world', got: %s", result)
	}

	// Test URL encode
	result, err = utils.EncodeDecodeText("url_encode", "hello world")
	if err != nil {
		t.Errorf("url_encode error: %v", err)
	}
	if !strings.Contains(result, "hello+world") {
		t.Errorf("url_encode expected 'hello+world', got: %s", result)
	}

	// Test JSON format
	result, err = utils.EncodeDecodeText("json_format", `{"a":1,"b":2}`)
	if err != nil {
		t.Errorf("json_format error: %v", err)
	}
	if !strings.Contains(result, "  ") { // Check for indentation
		t.Errorf("json_format expected indented JSON, got: %s", result)
	}
}
