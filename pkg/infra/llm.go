package infra

import (
	"fmt"
	"strings"
)

var (
	llmQuotaBillingKeywords = []string{
		"429", "resource_exhausted", "RESOURCE_EXHAUSTED", "quota", "rate limit", "rate_limit",
		"billing", "exceeded", "limit exceeded", "daily limit", "per minute",
	}
	llmPermissionKeywords = []string{
		"403", "permission_denied", "PERMISSION_DENIED", "forbidden", "invalid api key",
		"api key not valid", "billing has not been enabled", "FAILED_PRECONDITION",
	}
)

// IsLLMQuotaOrBillingError returns true if err indicates rate limit, quota, or billing.
func IsLLMQuotaOrBillingError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, k := range llmQuotaBillingKeywords {
		if strings.Contains(s, strings.ToLower(k)) {
			return true
		}
	}
	return false
}

// IsLLMPermissionOrBillingDenied returns true if err indicates permission denied or billing not enabled.
func IsLLMPermissionOrBillingDenied(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, k := range llmPermissionKeywords {
		if strings.Contains(s, strings.ToLower(k)) {
			return true
		}
	}
	return false
}

// WrapLLMError wraps Gemini/LLM API errors with a user-facing message when applicable.
func WrapLLMError(err error) error {
	if err == nil {
		return nil
	}
	msg := userFacingMessageForLLMError(err)
	if msg == "" {
		return err
	}
	return fmt.Errorf("%s — %w", msg, err)
}

func userFacingMessageForLLMError(err error) string {
	if err == nil {
		return ""
	}
	if IsLLMPermissionOrBillingDenied(err) {
		return "Permission denied or billing not enabled. Verify your API key and that billing is enabled for the Gemini API."
	}
	if IsLLMQuotaOrBillingError(err) {
		return "Rate limit or billing limit exceeded. Check your quota at Google AI Studio (aistudio.google.com) or Cloud Console, and try again later."
	}
	return ""
}
