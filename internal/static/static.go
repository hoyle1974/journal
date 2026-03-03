// Package static provides embedded static assets (HTML, etc.) loaded via go:embed.
package static

import (
	_ "embed"
)

//go:embed privacy_policy.html
var PrivacyPolicyHTML string

//go:embed terms_and_conditions.html
var TermsAndConditionsHTML string
