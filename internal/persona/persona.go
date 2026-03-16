// Package persona provides a response-rewriting layer so user-facing text is
// formatted in a consistent, friendly assistant persona. Used for CLI, Twilio,
// Telegram, dream narrative, and pending questions before delivery.
package persona

import (
	_ "embed"
	"context"
	"strings"

	"github.com/jackstrohm/jot/internal/infra"
	"github.com/jackstrohm/jot/pkg/utils"
)

//go:embed persona.txt
var defaultPersonaPrompt string

// DefaultPersonaPrompt is the system instruction for the default persona:
// friendly personal assistant, professional and competent, transparent when
// unable to answer or perform a task. Loaded from embedded persona.txt.
func DefaultPersonaPrompt() string { return defaultPersonaPrompt }

// Apply rewrites rawText using the default persona. optionalContext can be the
// user's question or other context to guide the rewrite; it may be empty.
// env must implement infra.ToolEnv (Config, Dispatch). On failure, returns
// rawText unchanged so delivery is never blocked.
func Apply(ctx context.Context, env infra.ToolEnv, rawText, optionalContext string) string {
	rawText = strings.TrimSpace(rawText)
	if rawText == "" {
		return rawText
	}
	if env == nil || env.Config() == nil {
		return rawText
	}
	userPrompt := "Text to rewrite:\n" + utils.WrapAsUserData(rawText)
	if optionalContext != "" {
		userPrompt += "\n\nContext (user question or source):\n" + utils.WrapAsUserData(optionalContext)
	}
	rewritten, err := infra.GenerateContentSimple(ctx, env, DefaultPersonaPrompt(), userPrompt, env.Config(), &infra.GenConfig{
		MaxOutputTokens: 4096,
	})
	if err != nil {
		infra.LoggerFrom(ctx).Warn("persona rewrite failed, using original", "error", err)
		return rawText
	}
	if rewritten == "" {
		return rawText
	}
	return rewritten
}
