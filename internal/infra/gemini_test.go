package infra

import "testing"

func TestResolveModelDoesNotDowngradePro(t *testing.T) {
	// 2.5-pro should NOT be silently changed to flash
	available := []string{"gemini-2.5-pro", "gemini-2.5-flash"}
	result := resolveModel("gemini-2.5-pro", available)
	if result != "gemini-2.5-pro" {
		t.Errorf("resolveModel downgraded 2.5-pro to %q, want %q", result, "gemini-2.5-pro")
	}
}
